package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/day2"
	"github.com/damhau/rvav/internal/ids"
	"github.com/damhau/rvav/internal/provision"
	"github.com/damhau/rvav/internal/store"
)

// Day-2 actions (ADR-0013): each run is a request of kind day2 on the
// deployment, with its own steps, events and audit entries.

// visibleDeployment loads a deployment the principal may see.
func (s *Service) visibleDeployment(ctx context.Context, p *auth.Principal, id string) (store.DeploymentRow, error) {
	d, err := s.Store.GetDeployment(ctx, id)
	if errors.Is(err, store.ErrNotFound) || (err == nil && !p.CanSeeProject(d.ProjectID) && d.OwnerID != p.User.ID) {
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
	dep := day2.Target{ResourceName: d.Name, Type: day2.DeploymentType, OwnerID: d.OwnerID,
		Lease: day2.Lease{ExpiresAt: d.LeaseExpiresAt, ExtensionsUsed: d.LeaseExtensionsUsed, ExpiredAt: d.LeaseExpiredAt}}
	out := []day2.Target{}
	for _, r := range resources {
		t := day2.Target{ResourceID: r.ID, ResourceName: r.Name, Type: r.Type, OwnerID: d.OwnerID}
		if r.Type == "vsphere.vm" {
			t.VM = day2.VM(r.Properties)
		}
		dep.Resources = append(dep.Resources, day2.Resource{ID: r.ID, Type: r.Type, Name: r.Name, VM: t.VM})
		out = append(out, t)
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
		lease, err := s.Store.LeasePolicy(ctx, d.ProjectID, d.Environment)
		if err != nil {
			return c, err
		}
		c.LeaseDefaultDays, c.LeaseMaxDays, c.LeaseMaxExtensions = lease.DefaultDays, lease.MaxDays, lease.MaxExtensions
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
	return c, nil
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
				if why := a.Approval(t, c, nil); why != "" {
					dec.Chain = append(dec.Chain, ChainLevel{Name: "Project admins", Reasons: []string{why}})
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
		out.Form = a.Form(t, c)
	}
	return out
}

// RunAction creates the day-2 request and starts it.
func (s *Service) RunAction(ctx context.Context, p *auth.Principal, deploymentID string, draft api.Day2Draft) (store.RequestRow, error) {
	d, err := s.visibleDeployment(ctx, p, deploymentID)
	if err != nil {
		return store.RequestRow{}, err
	}
	a, err := s.day2Action(ctx, draft.Action)
	if err != nil {
		return store.RequestRow{}, err
	}
	if a == nil {
		return store.RequestRow{}, &ValidationError{Errors: []api.FieldError{fieldErr("/action", "unknown_action",
			fmt.Sprintf("%q is not a day-2 action.", draft.Action))}}
	}
	if a.Direct {
		return store.RequestRow{}, &ValidationError{Errors: []api.FieldError{fieldErr("/action", "direct_action",
			a.Name+" is not a request: use its own endpoint.")}}
	}
	if a.Hidden && p.User.ID != SystemUserID {
		return store.RequestRow{}, &ValidationError{Errors: []api.FieldError{fieldErr("/action", "unknown_action",
			fmt.Sprintf("%q is not a day-2 action.", draft.Action))}}
	}
	resources, err := s.Store.ListResources(ctx, d.ID)
	if err != nil {
		return store.RequestRow{}, err
	}
	var target *day2.Target
	for _, t := range targets(d, resources) {
		if t.ResourceID == deref(draft.ResourceId) && a.AppliesTo(t.Type) {
			target = &t
			break
		}
	}
	if target == nil {
		msg := a.Name + " needs one of this deployment's resources it applies to."
		if a.AppliesTo(day2.DeploymentType) {
			msg = a.Name + " acts on the whole deployment: leave out resource_id."
		}
		return store.RequestRow{}, &ValidationError{Errors: []api.FieldError{fieldErr("/resource_id", "unknown_resource", msg)}}
	}
	j, err := s.judge(ctx, p, d)
	if err != nil {
		return store.RequestRow{}, err
	}
	if !j.offered(a, *target) {
		return store.RequestRow{}, &ValidationError{Errors: []api.FieldError{fieldErr("/action", "not_offered",
			fmt.Sprintf("%s is not offered for %s.", a.Name, target.ResourceName))}}
	}
	if dec := j.decide(a, *target, nil); dec.Denied != "" {
		return store.RequestRow{}, fmt.Errorf("%w: %s", ErrForbidden, dec.Denied)
	}
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
		form := a.Form(*target, c)
		schema, _ := json.Marshal(form.Schema)
		sum := sha256.Sum256(schema)
		errs, err := validateInputs("day2:"+hex.EncodeToString(sum[:8]), schema, inputs)
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
	if errs, err := s.checkAction(ctx, a, *target, inputs); err != nil || len(errs) > 0 {
		if err != nil {
			return store.RequestRow{}, err
		}
		return store.RequestRow{}, &ValidationError{Errors: errs}
	}
	overSoft, err := s.day2Quota(ctx, a, d, *target, inputs)
	if err != nil {
		return store.RequestRow{}, err
	}
	// The inputs may matter to a policy's condition: decide again with them.
	dec := j.decideQuota(a, *target, inputs, overSoft)
	if dec.Denied != "" {
		return store.RequestRow{}, fmt.Errorf("%w: %s", ErrForbidden, dec.Denied)
	}
	if a.Approval != nil && p.User.ID != SystemUserID {
		if why := a.Approval(*target, c, inputs); why != "" {
			level, err := s.projectAdminsLevel(ctx, d, p.User.ID, why)
			if err != nil {
				return store.RequestRow{}, err
			}
			dec.Chain = append(dec.Chain, level)
		}
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
	plan := provision.Plan{Kind: "day2", ApprovalRequired: req.ApprovalRequired, Quota: day2Add(a, *target, inputs), Day2: &provision.Day2{
		Action: a.Key, Name: a.Name, DeploymentID: d.ID, ResourceID: target.ResourceID, Moref: target.VM.Moref,
		Ops: ops, Before: d.Status, During: orDefault(a.During, "updating"), After: orDefault(a.After, "ready"),
	}}
	req.Plan, _ = json.Marshal(plan)
	req.Inputs, _ = json.Marshal(inputs)

	err = s.Store.Tx(ctx, func(tx pgx.Tx) error {
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
		if d, err = s.Store.GetDeployment(ctx, d.ID); err != nil {
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
		now := time.Now().UTC()
		var steps []store.Step
		for i, sd := range plan.Steps(c.Environment) {
			st := store.Step{ID: ids.New(ids.Step), RequestID: req.ID, Position: i, Key: sd.Key, Title: sd.Title,
				Detail: sd.Detail, System: &sd.System, Status: sd.Status}
			if sd.Status != "pending" {
				st.StartedAt = &now
			}
			if sd.Status == "done" {
				st.FinishedAt = &now
			}
			steps = append(steps, st)
		}
		if err := s.Store.InsertSteps(ctx, tx, steps); err != nil {
			return err
		}
		if err := s.Store.InsertApprovals(ctx, tx, chainRows(req.ID, dec.Chain)); err != nil {
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
	s.publishRequest(ctx, req.ID)
	return s.Store.GetRequest(ctx, req.ID)
}

// projectAdminsLevel is an approval level for the project's admins other
// than the requester, directly or through the groups mapped to the role.
// It is rejected after three days without a decision, so it never blocks
// the deployment for good.
func (s *Service) projectAdminsLevel(ctx context.Context, d store.DeploymentRow, requesterID, reason string) (ChainLevel, error) {
	ids, groups, err := s.Store.ProjectAdmins(ctx, d.ProjectID)
	if err != nil {
		return ChainLevel{}, err
	}
	members, err := s.Store.GroupMembers(ctx, groups)
	if err != nil {
		return ChainLevel{}, err
	}
	var approvers []string
	for _, id := range append(ids, members...) {
		if id != requesterID && !contains(approvers, id) {
			approvers = append(approvers, id)
		}
	}
	if len(approvers) == 0 {
		return ChainLevel{}, &ConflictError{Msg: d.ProjectName + " has no other project admin to approve this."}
	}
	return ChainLevel{Name: "Project admins", Approvers: approvers, Mode: "any", PolicyIDs: []string{"lease_policy"},
		Reasons: []string{reason}, Rules: []string{"lease policy"}, Timeout: 72 * time.Hour, OnTimeout: "reject"}, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
