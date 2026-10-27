package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/day2"
	"github.com/damhau/rvav/internal/provision"
	"github.com/damhau/rvav/internal/store"
)

// Retry and roll back failed requests (ADR-0013).

// failedRequest loads a failed request the principal may retry or roll back.
func (s *Service) failedRequest(ctx context.Context, p *auth.Principal, idOrNumber, verb string) (store.RequestRow, error) {
	r, err := s.Store.GetRequest(ctx, idOrNumber)
	if errors.Is(err, store.ErrNotFound) || (err == nil && !CanSeeRequest(p, r)) {
		return r, ErrNotFound
	}
	if err != nil {
		return r, err
	}
	owner, project := r.OwnerID, r.ProjectID
	if r.DeploymentID != nil {
		// A day-2 request: the deployment's owner and project now, not then.
		if d, err := s.Store.GetDeployment(ctx, *r.DeploymentID); err == nil {
			owner, project = d.OwnerID, d.ProjectID
		}
	}
	if r.RequesterID != p.User.ID && owner != p.User.ID && p.ProjectRole(project) != "project-admin" {
		return r, fmt.Errorf("%w: only the requester, the owner or a project admin can %s this request", ErrForbidden, verb)
	}
	if r.Status != "failed" {
		return r, &ConflictError{Msg: fmt.Sprintf("%s is %s; only failed requests can be %s.", r.Number, statusText(r.Status), verb+"ed")}
	}
	return r, nil
}

func statusText(status string) string {
	switch status {
	case "pending_approval":
		return "waiting for approval"
	}
	return status
}

// requestDeployments are the deployments a request created or acts on.
func (s *Service) requestDeployments(ctx context.Context, r store.RequestRow) ([]string, error) {
	if r.DeploymentID != nil {
		return []string{*r.DeploymentID}, nil
	}
	deps, err := s.Store.DeploymentsForRequest(ctx, r.ID)
	if err != nil {
		return nil, err
	}
	ids := make([]string, len(deps))
	for i, d := range deps {
		ids[i] = d.ID
	}
	return ids, nil
}

// lockForRetry locks the request's deployments and checks that nothing
// else changed them under it: no other request runs on them, none is being
// deleted or rolled back, and none was deleted since, unless by a rollback
// of this request (rolledBack).
func (s *Service) lockForRetry(ctx context.Context, tx pgx.Tx, r store.RequestRow, rolledBack bool) error {
	ids, err := s.requestDeployments(ctx, r)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := s.Store.LockDeployment(ctx, tx, id); err != nil {
			return err
		}
		active, err := s.Store.ActiveDeploymentRequest(ctx, tx, id, deref(r.ResourceID), r.ID)
		if err != nil {
			return err
		}
		if active != "" {
			return &ConflictError{Msg: active + " is running on this deployment; try again when it is done."}
		}
		var status string
		if err := tx.QueryRow(ctx, `select status from deployments where id = $1`, id).Scan(&status); err != nil {
			return err
		}
		switch {
		case status == "deleting":
			return &ConflictError{Msg: "The deployment is being deleted or rolled back."}
		case status == "deleted" && !rolledBack:
			return &ConflictError{Msg: "The deployment was deleted since. Submit a new request instead."}
		}
	}
	return nil
}

// checkDay2Retry applies to a day-2 retry what running the action checks
// now: the principal still sees the deployment, the day-2 policies still
// let them run it without approval, and its VM is still there.
func (s *Service) checkDay2Retry(ctx context.Context, p *auth.Principal, r store.RequestRow) error {
	d, err := s.visibleDeployment(ctx, p, deref(r.DeploymentID))
	if err != nil {
		return err
	}
	a := day2.Get(deref(r.Action))
	if a == nil {
		return &ConflictError{Msg: "This action no longer exists."}
	}
	resources, err := s.Store.ListResources(ctx, d.ID)
	if err != nil {
		return err
	}
	var target *day2.Target
	for _, t := range targets(d, resources) {
		if t.ResourceID == deref(r.ResourceID) && a.AppliesTo(t.Type) {
			target = &t
			break
		}
	}
	if target == nil {
		return &ConflictError{Msg: deref(r.ResourceName) + " is no longer part of the deployment."}
	}
	j, err := s.judge(ctx, p, d)
	if err != nil {
		return err
	}
	inputs := map[string]any{}
	_ = json.Unmarshal(r.Inputs, &inputs)
	dec := j.decide(a, *target, inputs)
	switch {
	case dec.Denied != "":
		return fmt.Errorf("%w: %s", ErrForbidden, dec.Denied)
	case len(dec.Chain) > 0:
		return &ConflictError{Msg: a.Name + " needs approval now: run it again to ask for it."}
	}
	return nil
}

// Retry runs a failed request again from its failed step: the steps that
// were done stay done. A request whose resources were rolled back starts
// again after its approval.
func (s *Service) Retry(ctx context.Context, p *auth.Principal, idOrNumber string) (store.RequestRow, error) {
	r, err := s.failedRequest(ctx, p, idOrNumber, "retry")
	if err != nil {
		return r, err
	}
	steps, err := s.Store.ListSteps(ctx, r.ID)
	if err != nil {
		return r, err
	}
	// Only a rollback that finished freed everything; after one that
	// failed, what is left is reused.
	rolledBack := false
	for _, st := range steps {
		if st.Key == "rollback" && st.Status == "done" {
			rolledBack = true
		}
	}
	if r.Kind == "day2" {
		if err := s.checkDay2Retry(ctx, p, r); err != nil {
			return r, err
		}
	}
	if rolledBack {
		// Its VM names were freed: someone else may have one now.
		var plan provision.Plan
		_ = json.Unmarshal(r.Plan, &plan)
		for _, vm := range plan.VMs {
			taken, err := s.Store.InventoryVMNames(ctx, vm.Name)
			if err != nil {
				return r, err
			}
			if taken[vm.Name] {
				return r, &ConflictError{Msg: vm.Name + " was taken since the rollback. Submit a new request instead."}
			}
		}
	}
	err = s.Store.Tx(ctx, func(tx pgx.Tx) error {
		if err := s.lockForRetry(ctx, tx, r, rolledBack); err != nil {
			return err
		}
		if err := s.Store.ResetForRetry(ctx, tx, r.ID, rolledBack); err != nil {
			return err
		}
		if r.Kind != "day2" {
			if err := s.Store.ReviveDeploymentsForRequest(ctx, tx, r.ID); err != nil {
				return err
			}
		}
		return s.Store.Audit(ctx, tx, store.AuditEvent{ActorID: &p.User.ID, Action: "request.retry", TargetType: "request",
			TargetID: r.ID, ProjectID: &r.ProjectID, Detail: map[string]any{"request": r.Number, "error": deref(r.ErrorMessage)}})
	})
	if err != nil {
		return r, err
	}
	if err := s.Workflows.Start(ctx, r.ID); err != nil {
		s.Log.Error("restart workflow", "request", r.Number, "err", err)
		_ = s.Store.SetRequestStatus(ctx, s.Store.Pool, r.ID, "failed")
		return r, &UpstreamError{System: "Temporal", Err: err}
	}
	s.publishRequest(ctx, r.ID)
	return s.Store.GetRequest(ctx, r.ID)
}

// RollbackRequest deletes what a failed provisioning request created: its
// VMs, DNS records and addresses. Its deployments end deleted.
func (s *Service) RollbackRequest(ctx context.Context, p *auth.Principal, idOrNumber string) (store.RequestRow, error) {
	r, err := s.failedRequest(ctx, p, idOrNumber, "roll back")
	if err != nil {
		return r, err
	}
	if r.Kind == "day2" {
		return r, &ConflictError{Msg: "A day-2 action has nothing to roll back. Retry it, or run another action."}
	}
	resources, err := s.Store.ResourcesForRequest(ctx, r.ID)
	if err != nil {
		return r, err
	}
	if len(resources) == 0 {
		return r, &ConflictError{Msg: r.Number + " has nothing to roll back: it created nothing, or that was deleted already."}
	}
	err = s.Store.Tx(ctx, func(tx pgx.Tx) error {
		if err := s.lockForRetry(ctx, tx, r, false); err != nil {
			return err
		}
		if err := s.Store.SetDeploymentsStatusForRequest(ctx, tx, r.ID, "deleting"); err != nil {
			return err
		}
		return s.Store.Audit(ctx, tx, store.AuditEvent{ActorID: &p.User.ID, Action: "request.rollback", TargetType: "request",
			TargetID: r.ID, ProjectID: &r.ProjectID, Detail: map[string]any{"request": r.Number, "resources": len(resources)}})
	})
	if err != nil {
		return r, err
	}
	if err := s.Workflows.Rollback(ctx, r.ID); err != nil {
		s.Log.Error("start rollback", "request", r.Number, "err", err)
		_ = s.Store.SetDeploymentsStatusForRequest(ctx, s.Store.Pool, r.ID, "failed")
		return r, &UpstreamError{System: "Temporal", Err: err}
	}
	s.publishRequest(ctx, r.ID)
	return s.Store.GetRequest(ctx, r.ID)
}
