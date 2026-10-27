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

// permit says whether the principal may run the action on the deployment.
// Day-2 policies replace it in S4.7.
func permit(p *auth.Principal, d store.DeploymentRow, _ *day2.Action) string {
	if p.CanRequest(d.ProjectID) || d.OwnerID == p.User.ID {
		return ""
	}
	return "Only the owner and the members of " + d.ProjectName + " can run actions on it."
}

// deploymentBusy says why the action can't start on the deployment now ("" when it can).
func deploymentBusy(d store.DeploymentRow, active string, a *day2.Action) string {
	switch {
	case active != "":
		return active + " is still running on this deployment."
	case d.Status == "ready", d.Status == "failed" && a.OnFailed:
		return ""
	case d.Status == "deleted":
		return "The deployment was deleted."
	}
	return "The deployment is " + d.Status + "."
}

// targets are the deployment itself, then each of its resources.
func targets(d store.DeploymentRow, resources []store.Resource) []day2.Target {
	dep := day2.Target{ResourceName: d.Name, Type: day2.DeploymentType, OwnerID: d.OwnerID}
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
	active, err := s.Store.ActiveDeploymentRequest(ctx, s.Store.Pool, d.ID)
	if err != nil {
		return nil, err
	}
	out := []api.Day2Action{}
	for _, t := range targets(d, resources) {
		c, err := s.choices(ctx, p, d, t)
		if err != nil {
			return nil, err
		}
		for _, a := range day2.All() {
			if !a.AppliesTo(t.Type) {
				continue
			}
			reason := deploymentBusy(d, active, a)
			if reason == "" {
				reason = permit(p, d, a)
			}
			if reason == "" && a.Unavailable != nil {
				reason = a.Unavailable(t, c)
			}
			out = append(out, presentAction(a, t, c, reason))
		}
	}
	return out, nil
}

func presentAction(a *day2.Action, t day2.Target, c day2.Choices, reason string) api.Day2Action {
	id, name := t.ResourceID, t.ResourceName
	out := api.Day2Action{
		Key: a.Key, Name: a.Name, Group: api.Day2ActionGroup(a.Group), ResourceName: &name,
		Enabled: reason == "", Destructive: a.Destructive,
		Approval: struct {
			Reasons  []string `json:"reasons"`
			Required bool     `json:"required"`
		}{Reasons: []string{}},
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
	a := day2.Get(draft.Action)
	if a == nil {
		return store.RequestRow{}, &ValidationError{Errors: []api.FieldError{fieldErr("/action", "unknown_action",
			fmt.Sprintf("%q is not a day-2 action.", draft.Action))}}
	}
	if reason := permit(p, d, a); reason != "" {
		return store.RequestRow{}, fmt.Errorf("%w: %s", ErrForbidden, reason)
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
	req := store.Request{
		ID: ids.New(ids.Request), ItemID: d.ItemID, VersionID: d.VersionID, ProjectID: d.ProjectID,
		Environment: d.Environment, Title: a.Name + " · " + target.ResourceName, Status: "queued",
		RequesterID: p.User.ID, OwnerID: d.OwnerID, Count: 1, SubmittedAt: time.Now().UTC(),
		Kind: "day2", Action: &a.Key, DeploymentID: &d.ID,
	}
	if target.Type != day2.DeploymentType {
		req.ResourceID, req.ResourceName = &target.ResourceID, &target.ResourceName
	}
	if draft.Justification != nil {
		req.Justification = strings.TrimSpace(*draft.Justification)
	}
	wf := provision.WorkflowID(req.ID)
	req.WorkflowID = &wf
	plan := provision.Plan{Kind: "day2", Day2: &provision.Day2{
		Action: a.Key, DeploymentID: d.ID, ResourceID: target.ResourceID, Moref: target.VM.Moref,
		Ops: a.Ops(*target, inputs), Before: d.Status, During: orDefault(a.During, "updating"), After: orDefault(a.After, "ready"),
	}}
	req.Plan, _ = json.Marshal(plan)
	req.Inputs, _ = json.Marshal(inputs)

	err = s.Store.Tx(ctx, func(tx pgx.Tx) error {
		if err := s.Store.LockDeployment(ctx, tx, d.ID); err != nil {
			return err
		}
		active, err := s.Store.ActiveDeploymentRequest(ctx, tx, d.ID)
		if err != nil {
			return err
		}
		if d, err = s.Store.GetDeployment(ctx, d.ID); err != nil {
			return err
		}
		if reason := deploymentBusy(d, active, a); reason != "" {
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

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
