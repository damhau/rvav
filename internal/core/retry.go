package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/damhau/rvav/internal/auth"
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
	if r.RequesterID != p.User.ID && r.OwnerID != p.User.ID && p.ProjectRole(r.ProjectID) != "project-admin" {
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
	rolledBack := false
	for _, st := range steps {
		if st.Key == "rollback" {
			rolledBack = true
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
		if r.DeploymentID != nil {
			if err := s.Store.LockDeployment(ctx, tx, *r.DeploymentID); err != nil {
				return err
			}
			active, err := s.Store.ActiveDeploymentRequest(ctx, tx, *r.DeploymentID, deref(r.ResourceID), r.ID)
			if err != nil {
				return err
			}
			if active != "" {
				return &ConflictError{Msg: active + " is running on this deployment; retry when it is done."}
			}
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
