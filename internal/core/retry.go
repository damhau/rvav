package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

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
	if r.RequesterID != p.User.ID && owner != p.User.ID && !p.In(auth.RequestManage, project) {
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
func (s *Service) requestDeployments(ctx context.Context, r store.RequestRow) ([]store.DeploymentRow, error) {
	if r.DeploymentID != nil {
		return []store.DeploymentRow{{ID: *r.DeploymentID}}, nil
	}
	return s.Store.DeploymentsForRequest(ctx, r.ID)
}

func deploymentIDs(deps []store.DeploymentRow) []string {
	out := make([]string, len(deps))
	for i, d := range deps {
		out[i] = d.ID
	}
	return out
}

// rolledBackChange is what a request whose rollback freed everything
// takes again when retried: its quota, and the place in the deployment
// limits of the deleted deployments it brings back (ADR-0047).
func (s *Service) rolledBackChange(ctx context.Context, p *auth.Principal, r store.RequestRow, plan provision.Plan,
	deleted int) (*quotaChange, error) {
	proj, err := s.Store.GetProject(ctx, r.ProjectID)
	if err != nil {
		return nil, err
	}
	env, err := s.Store.GetEnvironment(ctx, r.Environment)
	if err != nil {
		return nil, err
	}
	owner, err := s.Store.GetUser(ctx, r.OwnerID)
	if err != nil {
		return nil, err
	}
	sc := quotaScope{Project: proj, Env: env.Key, EnvName: env.Name, OwnerID: owner.ID, OwnerName: owner.Name, You: owner.ID == p.User.ID}
	ch := &quotaChange{scope: sc, what: "This request", add: plan.Quota}
	vms := plan.VMs
	for _, c := range plan.Composite {
		vms = append(vms, c.VMs...)
	}
	sizes := map[string][2]int{}
	for _, vm := range vms {
		sz := sizes[vm.DeploymentID]
		sizes[vm.DeploymentID] = [2]int{sz[0] + vm.VCPU, sz[1] + vm.MemoryGB}
	}
	if len(sizes) > 0 {
		ask := &limitAsk{quotaScope: sc, ToOwner: deleted, ToProject: deleted, Many: len(sizes) > 1}
		for _, sz := range sizes {
			ask.VCPU, ask.MemoryGB = max(ask.VCPU, sz[0]), max(ask.MemoryGB, sz[1])
		}
		ask.AddVCPU, ask.AddMemory = ask.VCPU, ask.MemoryGB
		ch.limits = ask
	}
	return ch, nil
}

// reclaimNames holds again the names of the deployments a rollback
// deleted: another deployment may have taken one since.
func (s *Service) reclaimNames(ctx context.Context, tx pgx.Tx, r store.RequestRow, names []string) error {
	if len(names) == 0 {
		return nil
	}
	if err := s.Store.LockDeploymentNames(ctx, tx, r.ProjectID, names); err != nil {
		return err
	}
	taken, err := s.Store.TakenDeploymentNames(ctx, tx, r.ProjectID, "", names)
	if err != nil || len(taken) == 0 {
		return err
	}
	return &ConflictError{Msg: fmt.Sprintf("Another deployment of %s is named %s since the rollback. Submit a new request instead.",
		r.ProjectName, taken[0])}
}

// asConflict makes a check's errors a conflict: a retry has no inputs to
// point them at.
func asConflict(err error) error {
	var v *ValidationError
	if !errors.As(err, &v) {
		return err
	}
	msgs := make([]string, len(v.Errors))
	for i, e := range v.Errors {
		msgs[i] = e.Message
	}
	return &ConflictError{Msg: strings.Join(msgs, " ")}
}

// lockForRetry locks the request's deployments and checks that nothing
// else changed them under it: no other request runs on them, none is being
// deleted or rolled back, and none was deleted since, unless by a rollback
// of this request (rolledBack).
func (s *Service) lockForRetry(ctx context.Context, tx pgx.Tx, r store.RequestRow, ids []string, rolledBack bool) error {
	for _, id := range ids {
		if err := s.Store.LockDeployment(ctx, tx, id); err != nil {
			return err
		}
		active, err := s.Store.ActiveDeploymentRequest(ctx, tx, id, deref(r.ResourceID), r.ID, true)
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
// let them run it without approval, its VM is still there, and it still
// fits the quotas and the deployment limits. It returns what the action
// counts against them, and the resources over a soft quota.
func (s *Service) checkDay2Retry(ctx context.Context, p *auth.Principal, r store.RequestRow) (*quotaChange, []string, error) {
	d, err := s.visibleDeployment(ctx, p, deref(r.DeploymentID))
	if err != nil {
		return nil, nil, err
	}
	a, err := s.day2Action(ctx, deref(r.Action))
	if err != nil {
		return nil, nil, err
	}
	if a == nil {
		return nil, nil, &ConflictError{Msg: "This action is no longer offered."}
	}
	if a.Hidden || r.RequesterID == SystemUserID {
		return nil, nil, &ConflictError{Msg: "rvav runs this itself and tries again when it is due."}
	}
	resources, err := s.Store.ListResources(ctx, d.ID)
	if err != nil {
		return nil, nil, err
	}
	var target *day2.Target
	for _, t := range targets(d, resources) {
		if t.ResourceID == deref(r.ResourceID) && a.AppliesTo(t.Type) {
			target = &t
			break
		}
	}
	if target == nil {
		return nil, nil, &ConflictError{Msg: deref(r.ResourceName) + " is no longer part of the deployment."}
	}
	j, err := s.judge(ctx, p, d)
	if err != nil {
		return nil, nil, err
	}
	if !j.offered(a, *target) {
		return nil, nil, &ConflictError{Msg: a.Name + " is no longer offered for " + target.ResourceName + "."}
	}
	// An AWX action needs its VMs as when it was asked for (powered on). A
	// native one is not checked: it may have half done its work, and its
	// retry must finish it (a resize that powered the VM off, for instance).
	if a.Unavailable != nil && a.Group == day2.GroupAutomation {
		c, err := s.choices(ctx, p, d, *target)
		if err != nil {
			return nil, nil, err
		}
		if reason := a.Unavailable(*target, c); reason != "" {
			return nil, nil, &ConflictError{Msg: reason}
		}
	}
	inputs := map[string]any{}
	_ = json.Unmarshal(r.Inputs, &inputs)
	change, err := s.day2Change(ctx, p, a, d, *target, inputs)
	if err != nil {
		return nil, nil, err
	}
	// Its requester accepted the soft limits when they asked.
	overSoft, _, err := s.checkChange(ctx, s.Store.Pool, change)
	if err != nil {
		return nil, nil, asConflict(err)
	}
	dec := j.decideQuota(a, *target, inputs, overSoft)
	needs := len(dec.Chain) > 0
	if a.Approval != nil && dec.Denied == "" {
		c, err := s.choices(ctx, p, d, *target)
		if err != nil {
			return nil, nil, err
		}
		needs = needs || a.Approval(*target, c, inputs) != ""
	}
	switch {
	case dec.Denied != "":
		return nil, nil, fmt.Errorf("%w: %s", ErrForbidden, dec.Denied)
	case needs:
		return nil, nil, &ConflictError{Msg: a.Name + " needs approval now: run it again to ask for it."}
	}
	return change, overSoft, nil
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
	var change *quotaChange
	var overSoft []string
	if r.Kind == "day2" || r.Kind == "update" {
		if change, overSoft, err = s.checkDay2Retry(ctx, p, r); err != nil {
			return r, err
		}
	}
	var plan provision.Plan
	_ = json.Unmarshal(r.Plan, &plan)
	deps, err := s.requestDeployments(ctx, r)
	if err != nil {
		return r, err
	}
	var names []string
	if rolledBack {
		for _, d := range deps {
			if d.CreatedByRequestID != nil && *d.CreatedByRequestID == r.ID && d.Status == "deleted" {
				names = append(names, d.Name)
			}
		}
		if change, err = s.rolledBackChange(ctx, p, r, plan, len(names)); err != nil {
			return r, err
		}
		// Its approval stands: soft quotas and limits don't ask again.
		overSoft = quotaOrder
		// Its VM names were freed: someone else may have one now.
		vms := plan.VMs
		for _, c := range plan.Composite {
			vms = append(vms, c.VMs...)
		}
		for _, vm := range vms {
			taken, err := s.Store.InventoryVMNames(ctx, s.Store.Pool, vm.Name)
			if err != nil {
				return r, err
			}
			if taken[vm.Name] {
				return r, &ConflictError{Msg: vm.Name + " was taken since the rollback. Submit a new request instead."}
			}
		}
	}
	// A composite VM step done, whose VMs a rollback that stopped halfway
	// deleted, runs again (with its NSX segments and groups).
	var lost []string
	if !rolledBack && len(plan.Composite) > 0 {
		resources, err := s.Store.ResourcesForRequest(ctx, r.ID)
		if err != nil {
			return r, err
		}
		for _, c := range plan.Composite {
			gone := slices.ContainsFunc(c.VMs, func(vm provision.VMUnit) bool {
				return !slices.ContainsFunc(resources, func(x store.Resource) bool { return x.Type == "vsphere.vm" && x.Name == vm.Name })
			})
			if gone && slices.ContainsFunc(steps, func(st store.Step) bool { return st.Key == c.StepKey() && st.Status == "done" }) {
				lost = append(lost, c.StepKey())
			}
		}
	}
	yes := true
	err = s.Store.Tx(ctx, func(tx pgx.Tx) error {
		if err := asConflict(s.holdChange(ctx, tx, change, overSoft, &yes, nil)); err != nil {
			return err
		}
		if err := s.reclaimNames(ctx, tx, r, names); err != nil {
			return err
		}
		if err := s.lockForRetry(ctx, tx, r, deploymentIDs(deps), rolledBack); err != nil {
			return err
		}
		if err := s.Store.FailSteps(ctx, tx, r.ID, lost); err != nil {
			return err
		}
		// A composite VM step runs again with its own steps: those done stay
		// done, the others are planned again.
		var children []store.ChildStep
		var keep []string
		positions := map[string]int{}
		for _, d := range plan.Steps(r.Environment) {
			if d.Parent != "" {
				children = append(children, store.ChildStep{ParentKey: d.Parent, Key: d.Key, Title: d.Title, Detail: d.Detail,
					System: d.System, Position: positions[d.Parent]})
				keep = append(keep, d.Key)
				positions[d.Parent]++
			}
		}
		if err := s.Store.ResetForRetry(ctx, tx, r.ID, rolledBack, keep); err != nil {
			return err
		}
		// Everything runs again and may go another way: no step keeps saying
		// what happened in the run before, as a branch's sides do.
		if rolledBack {
			for _, d := range plan.Steps(r.Environment) {
				if err := s.Store.SetPlannedDetail(ctx, tx, r.ID, d.Key, d.Detail); err != nil {
					return err
				}
			}
		}
		// A step that waits for a person and runs again asks again (ADR-0033).
		if err := s.Store.RetireStepApprovals(ctx, tx, r.ID); err != nil {
			return err
		}
		if err := s.Store.RestoreChildSteps(ctx, tx, r.ID, children); err != nil {
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
	switch r.Kind {
	case "day2":
		return r, &ConflictError{Msg: "A day-2 action has nothing to roll back. Retry it, or run another action."}
	case "update":
		return r, &ConflictError{Msg: "An update has nothing to roll back. Retry it, or update the deployment again."}
	}
	resources, err := s.Store.ResourcesForRequest(ctx, r.ID)
	if err != nil {
		return r, err
	}
	if len(resources) == 0 {
		return r, &ConflictError{Msg: r.Number + " has nothing to roll back: it created nothing, or that was deleted already."}
	}
	deps, err := s.requestDeployments(ctx, r)
	if err != nil {
		return r, err
	}
	err = s.Store.Tx(ctx, func(tx pgx.Tx) error {
		if err := s.lockForRetry(ctx, tx, r, deploymentIDs(deps), false); err != nil {
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
