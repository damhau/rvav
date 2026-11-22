package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/day2"
	"github.com/damhau/rvav/internal/present"
	"github.com/damhau/rvav/internal/store"
)

// Forms designed for custom day-2 actions (ADR-0050): an administrator
// lays out an AWX or workflow action's form in the catalog's designer, and
// while it is active the action asks for it instead of its survey's or
// input schema's, its rules and value sources worked out on the target.

// customAction is an AWX or workflow day-2 action as administrators see
// it, offered or not, with the form its survey or input schema gives (nil
// when it takes no inputs).
type customAction struct {
	key, name string
	kind      api.Day2FormDesignKind
	types     []string
	base      *api.ActionForm
	// closed: the input schema takes no inputs it doesn't name.
	closed bool
}

func (s *Service) customAction(ctx context.Context, key string) (customAction, error) {
	if wf, ok := strings.CutPrefix(key, "wf."); ok {
		w, err := s.Store.GetWorkflowRegistration(ctx, wf)
		if errors.Is(err, store.ErrNotFound) {
			return customAction{}, ErrNotFound
		}
		if err != nil {
			return customAction{}, err
		}
		def, ok := workflowDef(w)
		if !ok {
			return customAction{}, ErrNotFound
		}
		var schema map[string]any
		_ = json.Unmarshal(w.InputSchema, &schema)
		return customAction{key: key, name: def.Name, kind: api.Day2FormWorkflow, types: def.Types,
			base: formOf(day2.WorkflowAction(def.WorkflowDef)), closed: schema["additionalProperties"] == false}, nil
	}
	if !strings.HasPrefix(key, "awx.") {
		return customAction{}, ErrNotFound
	}
	a, err := s.Store.GetAWXAction(ctx, key)
	if errors.Is(err, store.ErrNotFound) {
		return customAction{}, ErrNotFound
	}
	if err != nil {
		return customAction{}, err
	}
	return customAction{key: key, name: a.Name, kind: api.Day2FormAwx, types: a.Types, base: formOf(awxAction(a))}, nil
}

func formOf(a *day2.Action) *api.ActionForm {
	if a.Form == nil {
		return nil
	}
	return a.Form(day2.Target{}, day2.Choices{})
}

// emptyForm is where a form starts for an action that takes no inputs.
func emptyForm() *api.ActionForm {
	return &api.ActionForm{Schema: map[string]any{"type": "object", "properties": map[string]any{}}, InitialValues: map[string]any{},
		UiSchema: api.UiSchema{Fields: map[string]api.UiField{}}}
}

// designedForm is the form saved for an action, as the action asks for it.
func designedForm(f store.Day2Form) *api.ActionForm {
	form := &api.ActionForm{Designed: present.Ptr(true)}
	_ = json.Unmarshal(f.FormSchema, &form.Schema)
	_ = json.Unmarshal(f.UISchema, &form.UiSchema)
	_ = json.Unmarshal(f.InitialValues, &form.InitialValues)
	if form.Schema == nil {
		form.Schema = map[string]any{}
	}
	if form.InitialValues == nil {
		form.InitialValues = map[string]any{}
	}
	return form
}

// designed is the action asking for its designed form.
func designed(a *day2.Action, f store.Day2Form) *day2.Action {
	form := designedForm(f)
	c := *a
	c.Form = func(day2.Target, day2.Choices) *api.ActionForm { return form }
	return &c
}

// withDesignedForms gives the custom actions with an active designed
// form that form.
func (s *Service) withDesignedForms(ctx context.Context, actions []*day2.Action) error {
	forms, err := s.Store.Day2Forms(ctx)
	if err != nil {
		return err
	}
	for _, f := range forms {
		if !f.Active {
			continue
		}
		for i, a := range actions {
			if a.Key == f.Action && custom(a.Key) {
				actions[i] = designed(a, f)
			}
		}
	}
	return nil
}

// withDesignedForm is withDesignedForms for one action.
func (s *Service) withDesignedForm(ctx context.Context, a *day2.Action) (*day2.Action, error) {
	if a == nil || !custom(a.Key) {
		return a, nil
	}
	f, err := s.Store.GetDay2Form(ctx, a.Key)
	if errors.Is(err, store.ErrNotFound) || err == nil && !f.Active {
		return a, nil
	}
	if err != nil {
		return nil, err
	}
	return designed(a, f), nil
}

func custom(key string) bool { return strings.HasPrefix(key, "awx.") || strings.HasPrefix(key, "wf.") }

func isDesigned(f *api.ActionForm) bool { return f.Designed != nil && *f.Designed }

// targetVars are what a designed form reads of its target, beside what a
// request form reads: the resource it runs on, and its deployment.
func targetVars(d store.DeploymentRow, t day2.Target) map[string]any {
	resource := map[string]any{"id": t.ResourceID, "type": t.Type, "name": t.ResourceName}
	switch t.Type {
	case "vsphere.vm":
		vm := t.VM
		disk := 0
		for _, dk := range vm.Disks {
			disk += dk.CapacityGb
		}
		props, tags := map[string]any{}, []string{}
		if vm.CustomProperties != nil {
			for k, v := range *vm.CustomProperties {
				props[k] = v
			}
		}
		if vm.Tags != nil {
			tags = *vm.Tags
		}
		resource["power_state"], resource["cpu_count"], resource["memory_gb"] = string(vm.PowerState), vm.CpuCount, vm.MemoryMb/1024
		resource["disk_gb"], resource["guest_os"], resource["os_family"] = disk, deref(vm.GuestOs), osFamily(deref(vm.GuestOs))
		resource["fqdn"], resource["ip"], resource["moref"] = deref(vm.Fqdn), firstIP(vm), vm.Moref
		resource["cluster"], resource["folder"] = deref(vm.Cluster), deref(vm.Folder)
		resource["tags"], resource["custom_properties"] = tags, props
	case day2.DeploymentType:
		resource["id"] = d.ID
		vms := []any{}
		for _, r := range t.Resources {
			if r.Type == "vsphere.vm" {
				vms = append(vms, map[string]any{"name": r.Name, "fqdn": deref(r.VM.Fqdn), "ip": firstIP(r.VM)})
			}
		}
		resource["os_family"], resource["vms"] = vmsFamily(t.Resources), vms
	}
	if day2.IsCustom(t.Type) {
		_, props := customTarget(t)
		resource["key"], resource["properties"] = props.Key, nonNilMap(props.Properties)
	}
	return map[string]any{
		"resource":   resource,
		"deployment": map[string]any{"id": d.ID, "name": d.Name, "description": d.Description, "owner_id": d.OwnerID, "item": d.ItemSlug},
	}
}

// actionVersion is a designed form as the form engine reads a catalog
// item version's, on the target. Its ID changes with the schema, which
// the engine compiles once per ID.
func actionVersion(key string, form *api.ActionForm, d store.DeploymentRow, t day2.Target) store.ItemVersion {
	v := store.ItemVersion{ItemID: d.ItemID, Target: targetVars(d, t)}
	v.FormSchema, _ = json.Marshal(form.Schema)
	v.UISchema, _ = json.Marshal(form.UiSchema)
	v.InitialValues, _ = json.Marshal(form.InitialValues)
	sum := sha256.Sum256(v.FormSchema)
	v.ID = "day2:" + key + ":" + hex.EncodeToString(sum[:8])
	return v
}

// formInputs checks inputs against an action's form, each input left out
// given its start value first (ADR-0055). A designed one is
// worked out as a request's form is (ADR-0019): its defaults and rules
// apply to inputs, as a run sends them, then its schema, the values picked
// from its sources and, on a run, its validations are checked. fields are
// its states (none for the other forms).
func (s *Service) formInputs(ctx context.Context, p *auth.Principal, d store.DeploymentRow, t day2.Target, key string,
	form *api.ActionForm, inputs map[string]any, run bool) (api.FieldStates, []api.FieldError, error) {
	start := StartValues(form.Schema, form.InitialValues)
	withStart(form.UiSchema, start, inputs)
	if !isDesigned(form) {
		schema, _ := json.Marshal(form.Schema)
		sum := sha256.Sum256(schema)
		errs, err := validateInputs("day2:"+hex.EncodeToString(sum[:8]), schema, inputs)
		return api.FieldStates{}, errs, err
	}
	proj, err := s.Store.GetProject(ctx, d.ProjectID)
	if err != nil {
		return nil, nil, err
	}
	env, err := s.Store.GetEnvironment(ctx, d.Environment)
	if err != nil {
		return nil, nil, err
	}
	ev := &Evaluation{Project: proj, Env: env, Item: store.CatalogItem{ID: d.ItemID}, Version: actionVersion(key, form, d, t), Inputs: inputs}
	ev.Fields = s.settledStates(ctx, p, proj, env.Key, ev.Version, form.UiSchema, start, inputs)
	ruleErrs := applyStates(ev.Fields, form.UiSchema, start, inputs)
	errs, err := validateInputs(ev.Version.ID, ev.Version.FormSchema, inputs)
	if err != nil {
		return nil, nil, err
	}
	errs = locateNested(form.UiSchema, hiddenErrors(ev.Fields, errs))
	for _, e := range ruleErrs {
		if !slices.ContainsFunc(errs, func(f api.FieldError) bool { return f.Field == e.Field }) {
			errs = append(errs, e)
		}
	}
	ev.Errors = errs
	if err := s.evaluatePickers(ctx, p, ev, run); err != nil {
		return nil, nil, err
	}
	if err := s.evaluateFound(ctx, p, ev, run); err != nil {
		return nil, nil, err
	}
	if run {
		s.evaluateValidations(ctx, p, ev, form.UiSchema)
	}
	return ev.Fields, ev.Errors, nil
}

// actionRun is an action on its target, for the principal filling its
// form in or running it.
type actionRun struct {
	d store.DeploymentRow
	a *day2.Action
	t day2.Target
	j *judge
}

// actionTarget finds an action and its target on a deployment: offered
// there, and not refused the principal.
func (s *Service) actionTarget(ctx context.Context, p *auth.Principal, deploymentID, key string, resourceID *string) (actionRun, error) {
	var r actionRun
	var err error
	if r.d, err = s.visibleDeployment(ctx, p, deploymentID); err != nil {
		return r, err
	}
	if r.a, err = s.day2Action(ctx, key); err != nil {
		return r, err
	}
	unknown := &ValidationError{Errors: []api.FieldError{fieldErr("/action", "unknown_action", fmt.Sprintf("%q is not a day-2 action.", key))}}
	if r.a == nil || r.a.Hidden && p.User.ID != SystemUserID {
		return r, unknown
	}
	if r.a.Direct {
		return r, &ValidationError{Errors: []api.FieldError{fieldErr("/action", "direct_action", r.a.Name+" is not a request: use its own endpoint.")}}
	}
	resources, err := s.Store.ListResources(ctx, r.d.ID)
	if err != nil {
		return r, err
	}
	found := false
	for _, t := range targets(r.d, resources) {
		if t.ResourceID == deref(resourceID) && r.a.AppliesTo(t.Type) {
			r.t, found = t, true
			break
		}
	}
	if !found {
		msg := r.a.Name + " needs one of this deployment's resources it applies to."
		if r.a.AppliesTo(day2.DeploymentType) {
			msg = r.a.Name + " acts on the whole deployment: leave out resource_id."
		}
		return r, &ValidationError{Errors: []api.FieldError{fieldErr("/resource_id", "unknown_resource", msg)}}
	}
	if err := s.tagCategories(ctx, r.d, &r.t); err != nil {
		return r, err
	}
	if r.j, err = s.judge(ctx, p, r.d); err != nil {
		return r, err
	}
	if !r.j.offered(r.a, r.t) {
		return r, &ValidationError{Errors: []api.FieldError{fieldErr("/action", "not_offered",
			fmt.Sprintf("%s is not offered for %s.", r.a.Name, r.t.ResourceName))}}
	}
	if dec := r.j.decide(r.a, r.t, nil); dec.Denied != "" {
		return r, fmt.Errorf("%w: %s", ErrForbidden, dec.Denied)
	}
	return r, nil
}

// actionForm is the form an action asks for on its target.
func (s *Service) actionForm(ctx context.Context, p *auth.Principal, r actionRun) (*api.ActionForm, error) {
	if r.a.Form == nil {
		return nil, &BadRequestError{Msg: r.a.Name + " asks for nothing."}
	}
	c, err := s.choices(ctx, p, r.d, r.t)
	if err != nil {
		return nil, err
	}
	return r.a.Form(r.t, c), nil
}

// PreviewAction works an action's form out for the inputs given, on its
// target, as a run would.
func (s *Service) PreviewAction(ctx context.Context, p *auth.Principal, deploymentID string, q api.Day2FormQuery) (api.Day2FormPreview, error) {
	r, err := s.actionTarget(ctx, p, deploymentID, q.Action, q.ResourceId)
	if err != nil {
		return api.Day2FormPreview{}, err
	}
	form, err := s.actionForm(ctx, p, r)
	if err != nil {
		return api.Day2FormPreview{}, err
	}
	return s.previewForm(ctx, p, r.d, r.t, r.a.Key, form, q.Inputs)
}

func (s *Service) previewForm(ctx context.Context, p *auth.Principal, d store.DeploymentRow, t day2.Target, key string,
	form *api.ActionForm, given map[string]any) (api.Day2FormPreview, error) {
	inputs := map[string]any{}
	for k, v := range given {
		inputs[k] = v
	}
	fields, errs, err := s.formInputs(ctx, p, d, t, key, form, inputs, false)
	if err != nil {
		return api.Day2FormPreview{}, err
	}
	return api.Day2FormPreview{Form: *startForm(form), Fields: fields, Errors: nonNilErrors(errs)}, nil
}

func nonNilErrors(errs []api.FieldError) []api.FieldError {
	if errs == nil {
		return []api.FieldError{}
	}
	return errs
}

// ActionFieldValues resolves a field's value source in an action's form,
// on its target, for whoever may run the action there.
func (s *Service) ActionFieldValues(ctx context.Context, p *auth.Principal, deploymentID string, q api.Day2FieldValuesQuery) (api.FieldValues, error) {
	r, err := s.actionTarget(ctx, p, deploymentID, q.Action, q.ResourceId)
	if err != nil {
		return api.FieldValues{Field: q.Field}, err
	}
	form, err := s.actionForm(ctx, p, r)
	if err != nil {
		return api.FieldValues{Field: q.Field}, err
	}
	return s.formValues(ctx, p, r.d, r.t, r.a.Key, form, q.Field, q.Inputs, deref(q.Search))
}

func (s *Service) formValues(ctx context.Context, p *auth.Principal, d store.DeploymentRow, t day2.Target, key string,
	form *api.ActionForm, name string, inputs map[string]any, search string) (api.FieldValues, error) {
	field, ok := form.UiSchema.Fields[name]
	if !ok {
		return api.FieldValues{Field: name}, &BadRequestError{Msg: fmt.Sprintf("The form has no field %q.", name)}
	}
	proj, err := s.Store.GetProject(ctx, d.ProjectID)
	if err != nil {
		return api.FieldValues{Field: name}, err
	}
	inputs = maps.Clone(inputs)
	if inputs == nil {
		inputs = map[string]any{}
	}
	withStart(form.UiSchema, StartValues(form.Schema, form.InitialValues), inputs)
	return s.sourceValues(ctx, p, proj, d.Environment, actionVersion(key, form, d, t), name, field, inputs, search)
}

// Day2Forms lists the designed forms, for administrators.
func (s *Service) Day2Forms(ctx context.Context, p *auth.Principal) (api.Day2FormList, error) {
	out := api.Day2FormList{Items: []api.Day2FormSummary{}}
	if !p.Everywhere(auth.ExtensibilityManage) {
		return out, ErrForbidden
	}
	list, err := s.Store.Day2Forms(ctx)
	for _, f := range list {
		out.Items = append(out.Items, api.Day2FormSummary{Action: f.Action, Active: f.Active, Revision: f.Revision, UpdatedAt: f.UpdatedAt})
	}
	return out, err
}

// Day2Form is the form designed for a custom action, or the one its
// survey or input schema gives, to start from.
func (s *Service) Day2Form(ctx context.Context, p *auth.Principal, key string) (api.Day2FormDesign, error) {
	if !p.Everywhere(auth.ExtensibilityManage) {
		return api.Day2FormDesign{}, ErrForbidden
	}
	ca, err := s.customAction(ctx, key)
	if err != nil {
		return api.Day2FormDesign{}, err
	}
	f, err := s.Store.GetDay2Form(ctx, key)
	if errors.Is(err, store.ErrNotFound) {
		return presentDay2Form(ca, nil), nil
	}
	if err != nil {
		return api.Day2FormDesign{}, err
	}
	return presentDay2Form(ca, &f), nil
}

func presentDay2Form(ca customAction, f *store.Day2Form) api.Day2FormDesign {
	out := api.Day2FormDesign{Action: ca.key, Name: ca.name, Kind: ca.kind, Types: []api.Day2FormDesignTypes{}, Warnings: []string{}}
	for _, t := range ca.types {
		out.Types = append(out.Types, api.Day2FormDesignTypes(t))
	}
	form := ca.base
	if f != nil {
		form = designedForm(*f)
		out.Revision, out.Active, out.UpdatedAt = f.Revision, f.Active, &f.UpdatedAt
		if f.UpdatedBy != nil {
			out.UpdatedBy = &api.UserRef{Id: *f.UpdatedBy, Name: deref(f.UpdatedByName)}
		}
	}
	if form == nil {
		form = emptyForm()
	}
	if form.UiSchema.Fields == nil {
		form.UiSchema.Fields = map[string]api.UiField{}
	}
	if form.UiSchema.Layout.Sections == nil {
		form.UiSchema.Layout.Sections = []api.UiSection{}
	}
	if form.UiSchema.Layout.Kind == "" {
		form.UiSchema.Layout.Kind = api.Page
	}
	out.Definition = api.Day2FormDefinition{FormSchema: form.Schema, UiSchema: form.UiSchema, InitialValues: form.InitialValues}
	if out.Definition.InitialValues == nil {
		out.Definition.InitialValues = map[string]any{}
	}
	if f != nil {
		out.Warnings = designWarnings(ca, form.Schema)
	}
	return out
}

// designWarnings are what the action's survey or input schema asks for
// that a designed form doesn't give, or gives as another type: AWX or the
// workflow may refuse such a run.
func designWarnings(ca customAction, schema map[string]any) []string {
	of, runner := "the survey", "AWX"
	if ca.kind == api.Day2FormWorkflow {
		of, runner = "the input schema", "the workflow"
	}
	var baseProps map[string]any
	var required []any
	if ca.base != nil {
		baseProps, _ = ca.base.Schema["properties"].(map[string]any)
		required, _ = ca.base.Schema["required"].([]any)
	}
	props, _ := schema["properties"].(map[string]any)
	out := []string{}
	for _, r := range required {
		if name, _ := r.(string); props[name] == nil {
			out = append(out, fmt.Sprintf("This form doesn't ask for %s, which %s requires: %s may refuse the run.", name, of, runner))
		}
	}
	for _, name := range sortedKeys(props) {
		mine, _ := props[name].(map[string]any)
		base, ok := baseProps[name].(map[string]any)
		switch {
		case !ok && ca.kind == api.Day2FormAwx:
			out = append(out, fmt.Sprintf("%s is not in the survey: AWX passes it to the job only if the template prompts for variables on launch.", name))
		case !ok && ca.closed:
			out = append(out, fmt.Sprintf("%s is not in the input schema, which takes no other inputs: the workflow may refuse the run.", name))
		case ok && base["type"] != nil && mine["type"] != nil && !reflect.DeepEqual(base["type"], mine["type"]):
			out = append(out, fmt.Sprintf("%s is of type %v here but %v in %s: %s may refuse the run.", name, mine["type"], base["type"], of, runner))
		}
	}
	return out
}

// checkDay2Form checks a designed form as a catalog item version's form
// is, a draft's way while inactive and a published version's while active,
// and refuses what an action's form can't do.
func checkDay2Form(d api.CatalogVersionDefinition, active bool) []api.FieldError {
	errs := checkFormDef(d, nil, "/definition", active)
	if d.UiSchema.Layout.Kind == api.Wizard {
		errs = append(errs, fieldErr("/definition/ui_schema/layout/kind", "not_allowed",
			"An action's form shows in a dialog: lay it out as a page or as tabs."))
	}
	for _, name := range sortedKeys(d.UiSchema.Fields) {
		f := d.UiSchema.Fields[name]
		at := "/definition/ui_schema/fields/" + name
		switch {
		case f.Widget == api.UiWidgetFile:
			errs = append(errs, fieldErr(at+"/widget", "not_allowed", "An action's form can't take files: ask for an address to fetch it from instead."))
		case f.Widget == api.UiWidgetSecret || f.Secret != nil && *f.Secret:
			// A run keeps its inputs with the request as they are.
			errs = append(errs, fieldErr(at+"/secret", "not_allowed", "An action's form can't take secrets: keep the secret in AWX or in rvav's secrets and name it instead."))
		}
	}
	return errs
}

// SaveDay2Form saves the form designed for a custom action.
func (s *Service) SaveDay2Form(ctx context.Context, p *auth.Principal, key string, w api.Day2FormWrite) (api.Day2FormDesign, error) {
	if !p.Everywhere(auth.ExtensibilityManage) {
		return api.Day2FormDesign{}, ErrForbidden
	}
	ca, err := s.customAction(ctx, key)
	if err != nil {
		return api.Day2FormDesign{}, err
	}
	current, err := s.Store.GetDay2Form(ctx, key)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return api.Day2FormDesign{}, err
	}
	active := err == nil && current.Active
	if w.Active != nil {
		active = *w.Active
	}
	def := api.CatalogVersionDefinition{FormSchema: w.Definition.FormSchema, UiSchema: w.Definition.UiSchema,
		InitialValues: w.Definition.InitialValues, Blueprint: map[string]any{}}
	errs := checkDay2Form(def, active)
	refs, err := s.checkFormRefs(ctx, def, "/definition", active)
	if err != nil {
		return api.Day2FormDesign{}, err
	}
	if errs = append(errs, refs...); len(errs) > 0 {
		return api.Day2FormDesign{}, &ValidationError{Errors: errs}
	}
	f := store.Day2Form{Action: ca.key}
	f.FormSchema, _ = json.Marshal(def.FormSchema)
	f.UISchema, _ = json.Marshal(def.UiSchema)
	f.InitialValues, _ = json.Marshal(def.InitialValues)
	revision, err := s.Store.SaveDay2Form(ctx, f, w.Active, w.BaseRevision, p.User.ID)
	if errors.Is(err, store.ErrFormChanged) {
		return api.Day2FormDesign{}, &ConflictError{Msg: "This form was saved elsewhere since you opened it: reload it to see those changes."}
	}
	if err != nil {
		return api.Day2FormDesign{}, err
	}
	s.audit(ctx, p, "day2_form.save", "day2_form", ca.key, nil, map[string]any{"revision": revision, "active": active})
	return s.Day2Form(ctx, p, key)
}

// DeleteDay2Form drops the form designed for a custom action: it asks for
// its survey's or schema's again.
func (s *Service) DeleteDay2Form(ctx context.Context, p *auth.Principal, key string) error {
	if !p.Everywhere(auth.ExtensibilityManage) {
		return ErrForbidden
	}
	if _, err := s.customAction(ctx, key); err != nil {
		return err
	}
	if err := s.Store.DeleteDay2Form(ctx, key); err != nil {
		return err
	}
	s.audit(ctx, p, "day2_form.delete", "day2_form", key, nil, nil)
	return nil
}

// designerTarget is the saved form of a custom action, active or not (the
// survey's or schema's before the first save), and the target the
// designer previews it on.
func (s *Service) designerTarget(ctx context.Context, p *auth.Principal, key, deploymentID string, resourceID *string) (
	*api.ActionForm, store.DeploymentRow, day2.Target, error) {
	design, err := s.Day2Form(ctx, p, key)
	if err != nil {
		return nil, store.DeploymentRow{}, day2.Target{}, err
	}
	form := &api.ActionForm{Designed: present.Ptr(true), Schema: design.Definition.FormSchema, UiSchema: design.Definition.UiSchema,
		InitialValues: design.Definition.InitialValues}
	d, err := s.visibleDeployment(ctx, p, deploymentID)
	if err != nil {
		return nil, d, day2.Target{}, err
	}
	resources, err := s.Store.ListResources(ctx, d.ID)
	if err != nil {
		return nil, d, day2.Target{}, err
	}
	for _, t := range targets(d, resources) {
		if t.ResourceID == deref(resourceID) && slices.Contains(design.Types, api.Day2FormDesignTypes(t.Type)) {
			return form, d, t, nil
		}
	}
	msg := design.Name + " runs on a VM: choose one of the deployment's."
	if slices.Contains(design.Types, api.Day2FormDesignTypesDeployment) {
		msg = design.Name + " runs on the whole deployment: leave out resource_id."
	}
	return nil, d, day2.Target{}, &ValidationError{Errors: []api.FieldError{fieldErr("/resource_id", "unknown_resource", msg)}}
}

// PreviewDay2Form is the designer's preview of a custom action's form, on
// a deployment or one of its VMs, as the administrator.
func (s *Service) PreviewDay2Form(ctx context.Context, p *auth.Principal, key string, q api.Day2FormDesignerQuery) (api.Day2FormPreview, error) {
	form, d, t, err := s.designerTarget(ctx, p, key, q.DeploymentId, q.ResourceId)
	if err != nil {
		return api.Day2FormPreview{}, err
	}
	return s.previewForm(ctx, p, d, t, key, form, q.Inputs)
}

// Day2FormValues resolves a field's value source in the designer's
// preview of a custom action's form.
func (s *Service) Day2FormValues(ctx context.Context, p *auth.Principal, key string, q api.Day2FormDesignerValuesQuery) (api.FieldValues, error) {
	form, d, t, err := s.designerTarget(ctx, p, key, q.DeploymentId, q.ResourceId)
	if err != nil {
		return api.FieldValues{Field: q.Field}, err
	}
	return s.formValues(ctx, p, d, t, key, form, q.Field, q.Inputs, deref(q.Search))
}
