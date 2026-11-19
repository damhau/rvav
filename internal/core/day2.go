package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/day2"
	"github.com/damhau/rvav/internal/ids"
	"github.com/damhau/rvav/internal/outbox"
	"github.com/damhau/rvav/internal/present"
	"github.com/damhau/rvav/internal/provision"
	"github.com/damhau/rvav/internal/store"
)

// Day-2 actions (ADR-0013): each run is a request of kind day2 on the
// deployment, with its own steps, events and audit entries.

// visibleDeployment loads a deployment the principal may see.
func (s *Service) visibleDeployment(ctx context.Context, p *auth.Principal, id string) (store.DeploymentRow, error) {
	d, err := s.Store.GetDeployment(ctx, id)
	if errors.Is(err, store.ErrNotFound) || (err == nil && !SeesDeployment(p, d.ProjectID, d.ProjectSharing, d.OwnerID)) {
		return d, ErrNotFound
	}
	return d, err
}

// deploymentBusy says why the action can't start on the deployment now ("" when it can).
// active is the request in its way, if any; actions on other VMs may run.
func deploymentBusy(d store.DeploymentRow, active string, a *day2.Action, onVM bool) string {
	switch {
	case active != "" && onVM:
		return active + " is still running on this VM."
	case active != "":
		return active + " is still running on this deployment."
	case d.Status == "ready", d.Status == "failed" && a.OnFailed, d.Status == "updating" && onVM:
		return ""
	case d.Status == "deleted":
		return "The deployment was deleted."
	}
	return "The deployment is " + d.Status + "."
}

// targets are the deployment itself, then each of its resources.
func targets(d store.DeploymentRow, resources []store.Resource) []day2.Target {
	dep := day2.Target{ResourceName: d.Name, Type: day2.DeploymentType, OwnerID: d.OwnerID, ItemType: d.ItemType, Description: d.Description,
		Lease: day2.Lease{ExpiresAt: d.LeaseExpiresAt, ExtensionsUsed: d.LeaseExtensionsUsed, ExpiredAt: d.LeaseExpiredAt}}
	out := []day2.Target{}
	for _, r := range resources {
		t := day2.Target{ResourceID: r.ID, ResourceName: r.Name, Type: r.Type, OwnerID: d.OwnerID}
		if r.Type == "vsphere.vm" {
			t.VM, t.Drift = day2.VM(r.Properties), day2.DriftOf(r.SyncState, r.Properties)
		}
		dep.Resources = append(dep.Resources, day2.Resource{ID: r.ID, Type: r.Type, Name: r.Name, VM: t.VM, Properties: r.Properties,
			PreventDelete: r.PreventDelete})
		out = append(out, t)
	}
	// A VM's actions see its deployment's other resources too: its NSX
	// groups (ADR-0028).
	for i := range out {
		out[i].Resources = dep.Resources
	}
	return append([]day2.Target{dep}, out...)
}

// choices are what the project's placement rules and the environment allow the target.
func (s *Service) choices(ctx context.Context, p *auth.Principal, d store.DeploymentRow, t day2.Target) (day2.Choices, error) {
	var c day2.Choices
	env, err := s.Store.GetEnvironment(ctx, d.Environment)
	if err != nil {
		return c, err
	}
	c.Environment, c.SnapshotMaxCount = env.Name, env.SnapshotMaxCount
	if env.SnapshotMaxAgeDays != nil {
		c.SnapshotMaxAgeDays = *env.SnapshotMaxAgeDays
	}
	if t.Type == day2.DeploymentType {
		c.LeaseDefaultDays, c.LeaseMaxDays, c.LeaseMaxExtensions = d.LeaseDefaultDays, d.LeaseMaxDays, derefInt(d.LeaseMaxExtensions)
		c.LeaseSoft = deref(d.LeaseEnforcement) == store.EnforcementSoft
		c.LeasePolicy = deref(d.LeasePolicy)
		if d.LeaseMaxLifetimeDays != nil {
			c.LeaseLifetimeDays = d.LeaseMaxLifetimeDays
			c.LeaseLifetimeLeft = present.Ptr(lifetimeLeft(d.CreatedAt, *d.LeaseMaxLifetimeDays, time.Now()))
		}
		if c.Members, err = s.memberChoices(ctx, d); err != nil {
			return c, err
		}
		c.Projects, err = s.projectChoices(ctx, p, d, t)
		return c, err
	}
	if t.VM.Cluster == nil {
		return c, nil
	}
	pl, err := s.placement(ctx, d.ProjectID, d.Environment)
	if err != nil {
		return c, err
	}
	for _, n := range pl.networksFor(*t.VM.Cluster) {
		c.Networks = append(c.Networks, n.Name)
	}
	return c, s.diskChoices(ctx, pl, t, &c)
}

// Actions lists, per resource, the actions that apply and whether the
// principal may run them now.
func (s *Service) Actions(ctx context.Context, p *auth.Principal, deploymentID string) ([]api.Day2Action, error) {
	d, err := s.visibleDeployment(ctx, p, deploymentID)
	if err != nil {
		return nil, err
	}
	resources, err := s.Store.ListResources(ctx, d.ID)
	if err != nil {
		return nil, err
	}
	j, err := s.judge(ctx, p, d)
	if err != nil {
		return nil, err
	}
	actions, err := s.day2Actions(ctx)
	if err != nil {
		return nil, err
	}
	out := []api.Day2Action{}
	active := map[string]string{}
	for _, t := range targets(d, resources) {
		if active[t.ResourceID], err = s.Store.ActiveDeploymentRequest(ctx, s.Store.Pool, d.ID, t.ResourceID, "", true); err != nil {
			return nil, err
		}
		if err := s.tagCategories(ctx, d, &t); err != nil {
			return nil, err
		}
		c, err := s.choices(ctx, p, d, t)
		if err != nil {
			return nil, err
		}
		for _, a := range actions {
			if !a.AppliesTo(t.Type) || a.Hidden || !j.offered(a, t) {
				continue
			}
			dec := j.decide(a, t, nil)
			if a.Approval != nil && dec.Denied == "" {
				if why := a.Approval(t, c, nil); why != "" && c.LeaseSoft {
					dec.Warnings = append(dec.Warnings, leaseWarning(c, why))
				} else if why != "" {
					dec.Chain = append(dec.Chain, ChainLevel{Name: "Project approvers", Reasons: []string{why}})
				}
			}
			reason := deploymentBusy(d, active[t.ResourceID], a, t.ResourceID != "")
			if reason == "" {
				reason = dec.Denied
			}
			if reason == "" && a.Unavailable != nil {
				reason = a.Unavailable(t, c)
			}
			out = append(out, presentAction(a, t, c, dec, reason))
		}
	}
	return out, nil
}

func presentAction(a *day2.Action, t day2.Target, c day2.Choices, dec decision, reason string) api.Day2Action {
	id, name := t.ResourceID, t.ResourceName
	out := api.Day2Action{
		Key: a.Key, Name: a.Name, Group: api.Day2ActionGroup(a.Group), ResourceName: &name,
		Enabled: reason == "", Destructive: a.Destructive,
		Approval: struct {
			Reasons  []string `json:"reasons"`
			Required bool     `json:"required"`
		}{Reasons: dec.reasons(), Required: len(dec.Chain) > 0},
	}
	if t.Type != day2.DeploymentType {
		out.ResourceId = &id
	}
	if reason != "" {
		out.DisabledReason = &reason
	}
	if a.Confirm != "" || a.TypeName {
		c := struct {
			Message       string  `json:"message"`
			TypeToConfirm *string `json:"type_to_confirm,omitempty"`
		}{Message: a.Confirm}
		if a.TypeName {
			c.TypeToConfirm = &name
		}
		out.Confirm = &c
	}
	if a.Form != nil && reason == "" {
		out.Form = startForm(a.Form(t, c))
	}
	if len(dec.Warnings) > 0 && reason == "" {
		out.Warnings = &dec.Warnings
	}
	return out
}

// lifetimeLeft is the whole days a deployment created at created may
// still live at now, with a lifetime of days (ADR-0048); 0 once past it.
func lifetimeLeft(created time.Time, days int, now time.Time) int {
	return max(int(created.AddDate(0, 0, days).Sub(now)/(24*time.Hour)), 0)
}

func leaseWarning(c day2.Choices, why string) api.PolicyWarning {
	return present.Warning("Lease in "+c.Environment, why)
}

// accepted refuses what soft policies warn about until the requester
// accepts the warnings (ADR-0045): those they list, or any with acceptAll.
func accepted(warnings []api.PolicyWarning, acceptAll *bool, listed *[]string) error {
	switch {
	case len(warnings) == 0:
		return nil
	case listed != nil:
		if !slices.ContainsFunc(warnings, func(w api.PolicyWarning) bool { return !slices.Contains(*listed, w.Id) }) {
			return nil
		}
	case acceptAll != nil && *acceptAll:
		return nil
	}
	errs := make([]api.FieldError, len(warnings))
	for i, w := range warnings {
		errs[i] = fieldErr("/accept_warnings", "warning", w.Message)
		errs[i].Warning = &w
	}
	return &ValidationError{Errors: errs}
}

// withWarnings keeps the warnings accepted in the request's approval record.
func withWarnings(approval json.RawMessage, warnings []api.PolicyWarning) json.RawMessage {
	if len(warnings) == 0 {
		return approval
	}
	m := map[string]any{}
	_ = json.Unmarshal(approval, &m)
	m["warnings"] = warnings
	raw, _ := json.Marshal(m)
	return raw
}

func (s *Service) auditWarnings(ctx context.Context, tx pgx.Tx, p *auth.Principal, req store.Request, warnings []api.PolicyWarning) error {
	if len(warnings) == 0 {
		return nil
	}
	return s.Store.Audit(ctx, tx, store.AuditEvent{ActorID: &p.User.ID, Action: "request.warnings.accept", TargetType: "request",
		TargetID: req.ID, ProjectID: &req.ProjectID, Detail: map[string]any{"request": req.Number, "warnings": warnings}})
}

// RunAction creates the day-2 request and starts it.
func (s *Service) RunAction(ctx context.Context, p *auth.Principal, deploymentID string, draft api.Day2Draft) (store.RequestRow, error) {
	r, err := s.actionTarget(ctx, p, deploymentID, draft.Action, draft.ResourceId)
	if err != nil {
		return store.RequestRow{}, err
	}
	d, a, target, j := r.d, r.a, &r.t, r.j
	c, err := s.choices(ctx, p, d, *target)
	if err != nil {
		return store.RequestRow{}, err
	}
	if a.Unavailable != nil {
		if reason := a.Unavailable(*target, c); reason != "" {
			return store.RequestRow{}, &ConflictError{Msg: reason}
		}
	}
	inputs := draft.Inputs
	if inputs == nil {
		inputs = map[string]any{}
	}
	if a.Form == nil && len(inputs) > 0 && a.Group == day2.GroupAutomation {
		// Nothing a user sends reaches AWX unchecked. (rvav's own hidden
		// actions, such as lease expiry, pass inputs without a form.)
		return store.RequestRow{}, &ValidationError{Errors: []api.FieldError{fieldErr("/inputs", "no_inputs",
			a.Name+" takes no inputs.")}}
	}
	if a.Form != nil {
		_, errs, err := s.formInputs(ctx, p, d, *target, a.Key, a.Form(*target, c), inputs, true)
		if err != nil {
			return store.RequestRow{}, err
		}
		if len(errs) > 0 {
			return store.RequestRow{}, &ValidationError{Errors: errs}
		}
	}
	if a.Validate != nil {
		if errs := a.Validate(*target, inputs); len(errs) > 0 {
			return store.RequestRow{}, &ValidationError{Errors: errs}
		}
	}
	errs, err := s.checkAction(ctx, a, d, *target, inputs)
	if err != nil {
		return store.RequestRow{}, err
	}
	if errs = append(errs, justificationErrs(draft.Justification)...); len(errs) > 0 {
		return store.RequestRow{}, &ValidationError{Errors: errs}
	}
	change, err := s.day2Change(ctx, p, a, d, *target, inputs)
	if err != nil {
		return store.RequestRow{}, err
	}
	overSoft, limited, err := s.checkChange(ctx, s.Store.Pool, change)
	if err != nil {
		return store.RequestRow{}, err
	}
	// The inputs may matter to a policy's condition: decide again with them.
	dec := j.decideQuota(a, *target, inputs, overSoft)
	if dec.Denied != "" {
		return store.RequestRow{}, fmt.Errorf("%w: %s", ErrForbidden, dec.Denied)
	}
	if a.Approval != nil && p.User.ID != SystemUserID {
		if why := a.Approval(*target, c, inputs); why != "" && c.LeaseSoft {
			dec.Warnings = append(dec.Warnings, leaseWarning(c, why))
		} else if why != "" {
			level, err := s.projectApproversLevel(ctx, d, p.User.ID, why)
			if err != nil {
				return store.RequestRow{}, err
			}
			dec.Chain = append(dec.Chain, level)
		}
	}
	dec.Warnings = append(dec.Warnings, limited...)
	if err := accepted(dec.Warnings, draft.AcceptWarnings, draft.AcceptedWarnings); err != nil {
		return store.RequestRow{}, err
	}
	req := store.Request{
		ID: ids.New(ids.Request), ItemID: d.ItemID, VersionID: d.VersionID, ProjectID: d.ProjectID,
		Environment: d.Environment, Title: a.Name + " · " + target.ResourceName, Status: "queued",
		RequesterID: p.User.ID, OwnerID: d.OwnerID, Count: 1, SubmittedAt: time.Now().UTC(),
		Kind: "day2", Action: &a.Key, DeploymentID: &d.ID,
	}
	if target.Type != day2.DeploymentType {
		req.ResourceID, req.ResourceName = &target.ResourceID, &target.ResourceName
	}
	if len(dec.Chain) > 0 {
		req.Status, req.ApprovalRequired = "pending_approval", true
	}
	req.Approval, _ = json.Marshal(map[string]any{"required": req.ApprovalRequired, "policy": dec.Policy, "reasons": dec.reasons()})
	req.Approval = withWarnings(req.Approval, dec.Warnings)
	if draft.Justification != nil {
		req.Justification = strings.TrimSpace(*draft.Justification)
	}
	wf := provision.WorkflowID(req.ID)
	req.WorkflowID = &wf
	// What it adds is reserved while it waits and runs, as for new requests.
	ops := a.Ops(*target, inputs)
	for _, op := range ops {
		if op.System == day2.SystemAWX && op.Op == day2.OpJob {
			op.Args["rvav"] = rvavContext(req.ID, p, d, *target)
		}
	}
	plan := provision.Plan{Kind: "day2", ApprovalRequired: req.ApprovalRequired, Day2: &provision.Day2{
		Action: a.Key, Name: a.Name, DeploymentID: d.ID, ResourceID: target.ResourceID, Moref: target.VM.Moref,
		Ops: ops, Before: d.Status, During: orDefault(a.During, "updating"), After: orDefault(a.After, "ready"),
	}}
	plan.Quota, plan.QuotaTo = day2Reserve(a, *target, inputs)
	req.Plan, _ = json.Marshal(plan)
	req.Inputs, _ = json.Marshal(inputs)

	err = s.Store.Tx(ctx, func(tx pgx.Tx) error {
		if err := s.holdChange(ctx, tx, change, overSoft, draft.AcceptWarnings, draft.AcceptedWarnings); err != nil {
			return err
		}
		if err := s.Store.LockDeployment(ctx, tx, d.ID); err != nil {
			return err
		}
		// rvav's own actions (lease expiry, retention) don't wait for
		// requests that are themselves waiting, such as an extension nobody
		// approves.
		active, err := s.Store.ActiveDeploymentRequest(ctx, tx, d.ID, target.ResourceID, "", p.User.ID != SystemUserID)
		if err != nil {
			return err
		}
		if d, err = s.Store.GetDeploymentIn(ctx, tx, d.ID); err != nil {
			return err
		}
		if reason := deploymentBusy(d, active, a, target.ResourceID != ""); reason != "" {
			return &ConflictError{Msg: reason}
		}
		if req.Number, err = s.Store.NextRequestNumber(ctx, tx); err != nil {
			return err
		}
		if err := s.Store.InsertRequest(ctx, tx, req); err != nil {
			return err
		}
		if err := s.Store.InsertSteps(ctx, tx, stepsOf(req.ID, plan.Steps(c.Environment))); err != nil {
			return err
		}
		if err := s.Store.InsertApprovals(ctx, tx, chainRows(req.ID, dec.Chain)); err != nil {
			return err
		}
		if err := outbox.Submitted(ctx, tx, req.ID); err != nil {
			return err
		}
		if err := s.auditWarnings(ctx, tx, p, req, dec.Warnings); err != nil {
			return err
		}
		return s.Store.Audit(ctx, tx, store.AuditEvent{ActorID: &p.User.ID, Action: "action.run", TargetType: "deployment",
			TargetID: d.ID, ProjectID: &d.ProjectID, Detail: map[string]any{
				"request": req.Number, "action": a.Key, "resource": target.ResourceName, "inputs": inputs}})
	})
	if err != nil {
		return store.RequestRow{}, err
	}
	if err := s.Workflows.Start(ctx, req.ID); err != nil {
		s.Log.Error("start workflow", "request", req.Number, "err", err)
		if derr := s.Store.DeleteUnstartedRequest(ctx, req.ID); derr != nil {
			s.Log.Error("remove unstarted request", "request", req.Number, "err", derr)
		}
		return store.RequestRow{}, &UpstreamError{System: "Temporal", Err: err}
	}
	// Not tied to the caller: the workflow has started. Its first activity
	// lets them go too, should this fail.
	if err := s.Store.ReleaseRequestEvents(context.WithoutCancel(ctx), req.ID); err != nil {
		s.Log.Error("release the request's events", "request", req.Number, "err", err)
	}
	s.publishRequest(ctx, req.ID)
	return s.Store.GetRequest(ctx, req.ID)
}

// projectApproversLevel asks the project's approvers: whoever holds a role
// with approval.project given in the project, themselves or through a
// group, when it opens (ADR-0041). It is refused now when nobody but the
// requester holds one, and rejected after three days without a decision,
// so it never blocks the deployment for good.
func (s *Service) projectApproversLevel(ctx context.Context, d store.DeploymentRow, requesterID, reason string) (ChainLevel, error) {
	roles, err := s.rolesWith(ctx, auth.ApprovalProject)
	if err != nil {
		return ChainLevel{}, err
	}
	holders, err := s.previewHolders(ctx, d.ProjectID, roles, requesterID)
	if err != nil {
		return ChainLevel{}, err
	}
	if len(holders) == 0 {
		return ChainLevel{}, &ConflictError{Msg: d.ProjectName + " has no other project approver to approve this."}
	}
	return ChainLevel{Name: "Project approvers", Roles: roles, Mode: "any", PolicyIDs: []string{"lease_policy"},
		Reasons: []string{reason}, Rules: []string{"lease policy"}, Timeout: 72 * time.Hour, OnTimeout: "reject"}, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
