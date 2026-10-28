package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/day2"
	"github.com/damhau/rvav/internal/policy"
	"github.com/damhau/rvav/internal/present"
	"github.com/damhau/rvav/internal/store"
)

// Day-2 actions backed by AWX templates (ADR-0018), next to the native ones.

func awxAction(a store.AWXAction) *day2.Action {
	return day2.AWXAction(day2.AWXDef{Key: a.Key, Name: a.Name, Template: a.Template, TemplateID: a.AWXID, Workflow: a.Workflow,
		Types: a.Types, Condition: a.Condition, Schema: a.FormSchema, UISchema: a.UISchema, Initial: a.Initial})
}

// day2Actions are the native actions and the AWX actions offered now
// (enabled, and their template still chosen), in menu order.
func (s *Service) day2Actions(ctx context.Context) ([]*day2.Action, error) {
	list, err := s.Store.AWXActions(ctx)
	if err != nil {
		return nil, err
	}
	out := day2.All()
	for _, a := range list {
		if a.Enabled && !a.Gone {
			out = append(out, awxAction(a))
		}
	}
	wfs, err := s.workflowActions(ctx)
	if err != nil {
		return nil, err
	}
	return day2.Sorted(append(out, wfs...)), nil
}

// workflowActions are the custom workflows offered as day-2 actions
// (ADR-0022): registered for it, enabled and offered.
func (s *Service) workflowActions(ctx context.Context) ([]*day2.Action, error) {
	regs, err := s.Store.WorkflowRegistrations(ctx)
	if err != nil {
		return nil, err
	}
	var out []*day2.Action
	for _, w := range regs {
		if a := workflowAction(w); a != nil {
			out = append(out, a)
		}
	}
	return out, nil
}

func workflowAction(w store.WorkflowRegistration) *day2.Action {
	if !w.Enabled || !slices.Contains(w.Usages, "day2_action") || len(w.Day2) == 0 {
		return nil
	}
	var d api.WorkflowDay2
	if json.Unmarshal(w.Day2, &d) != nil || d.Offered == nil || !*d.Offered {
		return nil
	}
	types := make([]string, len(d.Types))
	for i, t := range d.Types {
		types[i] = string(t)
	}
	return day2.WorkflowAction(day2.WorkflowDef{Key: w.Key, Name: d.Name, Type: w.WorkflowType, TaskQueue: w.TaskQueue,
		Timeout: w.TimeoutSeconds, Types: types, Condition: deref(d.Condition), Projects: w.ProjectIDs, Schema: w.InputSchema})
}

// day2Action is the action with this key, native or AWX (nil when none is
// offered).
func (s *Service) day2Action(ctx context.Context, key string) (*day2.Action, error) {
	if a := day2.Get(key); a != nil {
		return a, nil
	}
	if wf, ok := strings.CutPrefix(key, "wf."); ok {
		w, err := s.Store.GetWorkflowRegistration(ctx, wf)
		if errors.Is(err, store.ErrNotFound) {
			return nil, nil
		} else if err != nil {
			return nil, err
		}
		return workflowAction(w), nil
	}
	a, err := s.Store.GetAWXAction(ctx, key)
	if errors.Is(err, store.ErrNotFound) || err == nil && (!a.Enabled || a.Gone) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return awxAction(a), nil
}

// offered says whether an action's condition holds for the target. A
// condition that can't be evaluated offers nothing.
func (j *judge) offered(a *day2.Action, t day2.Target) bool {
	if len(a.Projects) > 0 && !slices.Contains(a.Projects, j.d.ProjectID) {
		return false
	}
	if a.Condition == "" {
		return true
	}
	ok, err := policy.Day2Bool(a.Condition, day2Vars(j.p, j.d, j.env, a, t, nil, j.roles))
	if err != nil {
		j.s.Log.Warn("day-2 action condition could not be evaluated", "action", a.Key, "err", err)
		return false
	}
	return ok
}

// rvavContext is what an AWX job of a day-2 action gets as extra_vars.rvav,
// as provisioning jobs do (integrations.md, launch contract).
func rvavContext(requestID string, p *auth.Principal, d store.DeploymentRow, t day2.Target) map[string]any {
	var resources []map[string]any
	add := func(name string, vm api.VmProperties) {
		r := map[string]any{"name": name, "fqdn": deref(vm.Fqdn), "moref": vm.Moref}
		for _, nic := range vm.Nics {
			if nic.IpAddresses != nil && len(*nic.IpAddresses) > 0 {
				r["ip"] = (*nic.IpAddresses)[0]
				break
			}
		}
		resources = append(resources, r)
	}
	if t.Type == day2.DeploymentType {
		for _, r := range t.Resources {
			if r.Type == "vsphere.vm" {
				add(r.Name, r.VM)
			}
		}
	} else {
		add(t.ResourceName, t.VM)
	}
	return map[string]any{"request_id": requestID, "deployment_id": d.ID, "deployment": d.Name, "project": d.ProjectCode,
		"environment": d.Environment, "requester": p.User.Email, "resources": resources}
}

func presentAWXAction(a store.AWXAction) api.AwxAction {
	out := api.AwxAction{Key: a.Key, Name: a.Name, Description: a.Description, TemplateId: a.AWXID, Template: a.Template,
		Workflow: a.Workflow, Condition: a.Condition, Enabled: a.Enabled, Warnings: append([]string{}, a.Warnings...),
		Gone: a.Gone, SyncedAt: a.SyncedAt, Types: []api.AwxActionTypes{}}
	for _, t := range a.Types {
		out.Types = append(out.Types, api.AwxActionTypes(t))
	}
	var ui api.UiSchema
	if json.Unmarshal(a.UISchema, &ui) == nil {
		fields := []string{}
		for _, sec := range ui.Layout.Sections {
			fields = append(fields, sec.Fields...)
		}
		out.Fields = &fields
	}
	return out
}

// checkCondition evaluates an action's condition on a sample of each type it
// runs on: it must give a boolean, so a misspelt variable (or one a
// deployment doesn't have) is caught when it is saved rather than hiding
// the action.
func checkCondition(condition string, types []string) error {
	if err := policy.CheckDay2(condition); err != nil {
		return err
	}
	p := &auth.Principal{User: store.User{ID: "usr_sample"}}
	d := store.DeploymentRow{}
	d.ID, d.Name, d.OwnerID, d.ProjectID, d.ProjectCode, d.ProjectName = "dep_sample", "sample", "usr_sample", "prj_sample", "sample", "Sample"
	a := &day2.Action{Key: "awx.sample", Group: day2.GroupAutomation}
	vm := api.VmProperties{Moref: "vm-1", PowerState: api.On, GuestOs: present.Ptr("rhel9_64Guest")}
	for _, typ := range types {
		t := day2.Target{ResourceID: "res_sample", ResourceName: "sample", Type: typ, VM: vm}
		if typ == day2.DeploymentType {
			t = day2.Target{Type: typ, ResourceName: "sample", Resources: []day2.Resource{{ID: "res_sample", Type: "vsphere.vm", Name: "sample", VM: vm}}}
		}
		if _, err := policy.Day2Bool(condition, day2Vars(p, d, store.Environment{Key: "dev", Name: "Development"}, a, t, nil, []string{"member"})); err != nil {
			return fmt.Errorf("on a %s: %w", map[string]string{"vsphere.vm": "VM", day2.DeploymentType: "deployment"}[typ], err)
		}
	}
	return nil
}

// AWXActions lists the AWX templates synced as day-2 actions, for
// administrators.
func (s *Service) AWXActions(ctx context.Context, p *auth.Principal) (api.AwxActionList, error) {
	out := api.AwxActionList{Items: []api.AwxAction{}}
	if !p.IsAdmin() {
		return out, ErrForbidden
	}
	list, err := s.Store.AWXActions(ctx)
	for _, a := range list {
		out.Items = append(out.Items, presentAWXAction(a))
	}
	return out, err
}

// UpdateAWXAction sets what administrators choose for an AWX action: its
// name, the resource types it applies to, its condition and whether it is
// offered.
func (s *Service) UpdateAWXAction(ctx context.Context, p *auth.Principal, key string, u api.AwxActionUpdate) (api.AwxAction, error) {
	if !p.IsAdmin() {
		return api.AwxAction{}, ErrForbidden
	}
	var errs []api.FieldError
	if u.Name != nil && (len(*u.Name) == 0 || len(*u.Name) > 80) {
		errs = append(errs, fieldErr("/name", "invalid", "Give the action a name of 1 to 80 characters."))
	}
	var types []string
	if u.Types != nil {
		if len(*u.Types) == 0 {
			errs = append(errs, fieldErr("/types", "required", "Choose what the action runs on."))
		}
		for _, t := range *u.Types {
			if !slices.Contains([]string{"vsphere.vm", day2.DeploymentType}, string(t)) {
				errs = append(errs, fieldErr("/types", "invalid", fmt.Sprintf("%q is not a VM or a deployment.", t)))
			}
			if !slices.Contains(types, string(t)) {
				types = append(types, string(t))
			}
		}
	}
	current, err := s.Store.GetAWXAction(ctx, key)
	if errors.Is(err, store.ErrNotFound) {
		return api.AwxAction{}, ErrNotFound
	}
	if err != nil {
		return api.AwxAction{}, err
	}
	condition := current.Condition
	if u.Condition != nil {
		condition = *u.Condition
	}
	if types == nil {
		types = current.Types
	}
	if condition != "" && len(errs) == 0 {
		if err := checkCondition(condition, types); err != nil {
			errs = append(errs, fieldErr("/condition", "invalid", "This condition is not valid: "+err.Error()))
		}
	}
	if len(errs) > 0 {
		return api.AwxAction{}, &ValidationError{Errors: errs}
	}
	b := store.AWXActionBinding{Name: u.Name, Types: types, Condition: u.Condition, Enabled: u.Enabled}
	a, err := s.Store.UpdateAWXAction(ctx, key, b)
	if errors.Is(err, store.ErrNotFound) {
		return api.AwxAction{}, ErrNotFound
	}
	if err != nil {
		return api.AwxAction{}, err
	}
	_ = s.Store.Audit(ctx, s.Store.Pool, store.AuditEvent{ActorID: &p.User.ID, Action: "awx_action.update", TargetType: "awx_action",
		TargetID: key, Detail: map[string]any{"name": a.Name, "types": a.Types, "condition": a.Condition, "enabled": a.Enabled}})
	return presentAWXAction(a), nil
}
