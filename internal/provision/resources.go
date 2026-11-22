package provision

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"time"

	"go.temporal.io/sdk/workflow"

	"github.com/damhau/rvav/internal/day2"
	"github.com/damhau/rvav/internal/ids"
	"github.com/damhau/rvav/internal/outbox"
	"github.com/damhau/rvav/internal/policy"
	"github.com/damhau/rvav/internal/store"
)

// Custom resources (ADR-0056): a resource type's custom workflow or AWX
// template makes, updates, reads and deletes them; rvav keeps what they
// give as the resource's properties.

// KeyResource prefixes the step making one of a plan's custom resources.
const KeyResource = "resource:"

// CustomResource is a custom resource's type in the resources table.
const CustomResource = day2.CustomType

// Phases of a custom resource's life.
const (
	PhaseCreate = "create"
	PhaseUpdate = "update"
	PhaseRead   = "read"
	PhaseDelete = "delete"
)

// versionResources gates what custom resources added to running workflows.
const versionResources = "custom-resources"

// ResourceUnit is a custom resource a plan makes, or an update changes.
type ResourceUnit struct {
	Key   string `json:"key"`
	Title string `json:"title"`
	// Type is the resource type's key; System what runs its step (rvav for
	// a custom workflow, awx for a job template), for the timeline.
	Type   string         `json:"type"`
	System string         `json:"system"`
	With   map[string]any `json:"with,omitempty"`
	// DeploymentID is the deployment it joins; a composite's resource step
	// joins that of the VM step Step.
	DeploymentID string `json:"deployment_id"`
	Step         string `json:"step,omitempty"`
	// ResourceID and Phase update a resource an update keeps.
	ResourceID string `json:"resource_id,omitempty"`
	Phase      string `json:"phase,omitempty"`
}

// StepKey is its step in the timeline.
func (u ResourceUnit) StepKey() string { return KeyResource + u.Key }

func resourceSteps(units []ResourceUnit) []StepDef {
	var steps []StepDef
	for _, u := range units {
		title := u.Title
		if u.Phase == PhaseUpdate {
			title = "Update " + u.Title
		}
		steps = append(steps, StepDef{Key: u.StepKey(), Title: title, Detail: "Custom resource · " + u.Type, System: u.System, Status: "pending"})
	}
	return steps
}

// makesResources says whether the plan may have made custom resources a
// rollback deletes.
func (p Plan) makesResources() bool {
	if len(p.Resources) > 0 {
		return true
	}
	var has func([]CompositeStep) bool
	has = func(steps []CompositeStep) bool {
		for _, c := range steps {
			if c.Resource != nil {
				return true
			}
			if c.Branch != nil {
				for _, side := range c.Branch.Sides() {
					if has(side) {
						return true
					}
				}
			}
		}
		return false
	}
	return has(p.Composite)
}

// ResourceRun is one step of a custom resource's life: Unit says which to
// make; the others name the resource by ID. Inputs, when Resolved, are a
// composite step's, worked out already; Components are what the
// deployment's components built, for a blueprint's bindings.
type ResourceRun struct {
	Phase      string         `json:"phase"`
	ResourceID string         `json:"resource_id,omitempty"`
	Unit       *ResourceUnit  `json:"unit,omitempty"`
	Source     string         `json:"source,omitempty"`
	Resolved   bool           `json:"resolved,omitempty"`
	Inputs     map[string]any `json:"inputs,omitempty"`
	Components map[string]any `json:"components,omitempty"`
	// Title names a step added as it runs (a rollback's).
	Title string `json:"title,omitempty"`
}

// ResourceInput is a run of one of its steps.
type ResourceInput struct {
	RequestID string      `json:"request_id"`
	ProjectID string      `json:"project_id"`
	StepKey   string      `json:"step_key"`
	Run       ResourceRun `json:"run"`
}

// ResourceCall is what runs the step: a custom workflow or an AWX job; Skip
// says why nothing runs.
type ResourceCall struct {
	Skip       string         `json:"skip,omitempty"`
	ResourceID string         `json:"resource_id"`
	Workflow   *WorkflowCall  `json:"workflow,omitempty"`
	Job        *Job           `json:"job,omitempty"`
	ExtraVars  map[string]any `json:"extra_vars,omitempty"`
	Inputs     map[string]any `json:"inputs,omitempty"`
	// Resource is the resource as the workflow's context and the job's
	// rvav.resource have it.
	Resource map[string]any `json:"resource"`
}

// FinishResourceInput records what the step gave.
type FinishResourceInput struct {
	ResourceInput
	Call   ResourceCall   `json:"call"`
	Result map[string]any `json:"result,omitempty"`
}

// makeResources makes a plan's custom resources, or updates those an
// update keeps, once its VMs are built: components are what they built.
func makeResources(ctx workflow.Context, info Info, units []ResourceUnit, components map[string]any) (string, error) {
	if len(units) == 0 || workflow.GetVersion(ctx, versionResources, workflow.DefaultVersion, 1) == workflow.DefaultVersion {
		return "", nil
	}
	for _, u := range units {
		if info.done(u.StepKey()) {
			continue
		}
		run := ResourceRun{Phase: cmp.Or(u.Phase, PhaseCreate), Unit: &u, ResourceID: u.ResourceID, Source: "blueprint", Components: components}
		if _, err := runResource(ctx, info, u.StepKey(), run, nil); err != nil {
			return u.StepKey(), err
		}
	}
	return "", nil
}

// runResource runs one step of a custom resource's life under stepKey and
// returns its properties after it; steps are a composite's outputs so far.
func runResource(ctx workflow.Context, info Info, stepKey string, run ResourceRun, steps map[string]any) (map[string]any, error) {
	var a *Activities
	in := ResourceInput{RequestID: info.RequestID, ProjectID: info.ProjectID, StepKey: stepKey, Run: run}
	fail := func(err error) (map[string]any, error) {
		_ = workflow.ExecuteActivity(ctx, a.MarkStep, info.RequestID, info.ProjectID, stepKey, "failed", errMessage(err)).Get(ctx, nil)
		return nil, err
	}
	var call ResourceCall
	if err := workflow.ExecuteActivity(ctx, a.PrepareResource, in).Get(ctx, &call); err != nil {
		return fail(err)
	}
	if call.Skip != "" {
		return nil, workflow.ExecuteActivity(ctx, a.MarkStep, info.RequestID, info.ProjectID, stepKey, "skipped", call.Skip).Get(ctx, nil)
	}
	var result map[string]any
	var err error
	switch {
	case call.Workflow != nil:
		result, err = runCustom(ctx, info, stepKey, *call.Workflow, steps)
	case call.Job != nil:
		job := JobInput{RequestID: info.RequestID, ProjectID: info.ProjectID, StepKey: stepKey, Job: *call.Job, ExtraVars: call.ExtraVars}
		err = workflow.ExecuteActivity(jobCtx(ctx), a.RunJob, job).Get(ctx, nil)
	}
	if err != nil {
		return nil, err
	}
	var props map[string]any
	if err := workflow.ExecuteActivity(ctx, a.FinishResource, FinishResourceInput{ResourceInput: in, Call: call, Result: result}).Get(ctx, &props); err != nil {
		return fail(err)
	}
	return props, nil
}

// rollbackResources deletes the custom resources a failed request made,
// the last made first, before the rest of the rollback. It says which it
// couldn't delete, and why.
func rollbackResources(ctx workflow.Context, info Info, force bool) []string {
	if !force && !info.Plan.makesResources() {
		return nil
	}
	if workflow.GetVersion(ctx, versionResources, workflow.DefaultVersion, 1) == workflow.DefaultVersion {
		return nil
	}
	var a *Activities
	var list []RolledBackResource
	if err := workflow.ExecuteActivity(ctx, a.RequestResources, info.RequestID).Get(ctx, &list); err != nil {
		workflow.GetLogger(ctx).Error("custom resources to roll back", "err", err)
		return []string{"its custom resources: " + errMessage(err)}
	}
	var failed []string
	for _, r := range list {
		run := ResourceRun{Phase: PhaseDelete, ResourceID: r.ID, Title: "Delete " + r.Name}
		if _, err := runResource(ctx, info, "rollback:"+KeyResource+r.ID, run, nil); err != nil {
			workflow.GetLogger(ctx).Error("roll back a custom resource", "resource", r.ID, "err", err)
			failed = append(failed, r.Name+": "+errMessage(err))
		}
	}
	return failed
}

// RolledBackResource is a custom resource a rollback deletes.
type RolledBackResource struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// RequestResources lists the custom resources a request made, the last
// made first.
func (a *Activities) RequestResources(ctx context.Context, requestID string) ([]RolledBackResource, error) {
	list, err := a.Store.ResourcesForRequest(ctx, requestID)
	if err != nil {
		return nil, err
	}
	out := []RolledBackResource{}
	for _, r := range slices.Backward(list) {
		if r.Type == CustomResource {
			out = append(out, RolledBackResource{ID: r.ID, Name: r.Name})
		}
	}
	return out, nil
}

// PrepareResource works out a step of a custom resource's life: its type's
// workflow or template for the step, the inputs it gets, and the resource
// as they see it. It writes the step's .pre event, and adds the step when
// the request has none yet (a rollback's).
func (a *Activities) PrepareResource(ctx context.Context, in ResourceInput) (ResourceCall, error) {
	run := in.Run
	req, err := a.Store.GetRequest(ctx, in.RequestID)
	if err != nil {
		return ResourceCall{}, err
	}
	var props store.CustomProps
	var res store.Resource
	unit := ResourceUnit{}
	if run.Unit != nil {
		unit = *run.Unit
	}
	if run.Phase != PhaseCreate {
		if res, err = a.Store.GetResource(ctx, run.ResourceID); errors.Is(err, store.ErrNotFound) {
			return ResourceCall{Skip: "The resource is gone already."}, a.ensureStep(ctx, in, "rvav")
		} else if err != nil {
			return ResourceCall{}, err
		}
		_ = json.Unmarshal(res.Properties, &props)
		unit.Type, unit.Key, unit.DeploymentID = props.ResourceType.Key, props.Key, res.DeploymentID
	}
	t, err := a.Store.GetResourceType(ctx, unit.Type)
	if errors.Is(err, store.ErrNotFound) {
		return ResourceCall{}, permanent("the resource type %s no longer exists", unit.Type)
	} else if err != nil {
		return ResourceCall{}, err
	}
	if run.Phase == PhaseCreate && !t.Enabled {
		return ResourceCall{}, permanent("the resource type %s is disabled", t.Title)
	}
	driver := t.Lifecycle.Driver(run.Phase)
	if err := a.ensureStep(ctx, in, DriverSystem(driver)); err != nil {
		return ResourceCall{}, err
	}
	if driver == nil {
		return ResourceCall{Skip: t.Title + " has no " + run.Phase + " step."}, nil
	}
	call := ResourceCall{ResourceID: run.ResourceID}
	if call.ResourceID == "" {
		if call.ResourceID, err = a.begunResource(ctx, in.RequestID, unit); err != nil {
			return ResourceCall{}, err
		}
	}
	dep, err := a.Store.GetDeployment(ctx, unit.DeploymentID)
	if err != nil {
		return ResourceCall{}, fmt.Errorf("the deployment of %s: %w", unit.Key, err)
	}
	switch run.Phase {
	case PhaseCreate, PhaseUpdate:
		if call.Inputs, err = a.resourceInputs(ctx, req, dep, run, unit); err != nil {
			return ResourceCall{}, err
		}
		if run.Phase == PhaseUpdate && reflect.DeepEqual(normalized(call.Inputs), normalized(props.Inputs)) {
			return ResourceCall{Skip: "Its inputs are the same."}, nil
		}
	default:
		call.Inputs = orEmpty(maps.Clone(props.Properties))
	}
	name := res.Name
	if name == "" {
		name = resourceName(nil, call.Inputs, dep.Name, unit.Key)
	}
	call.Resource = map[string]any{"id": call.ResourceID, "type": map[string]any{"key": t.Key, "title": t.Title}, "key": unit.Key,
		"name": name, "deployment_id": dep.ID, "phase": run.Phase, "inputs": orEmpty(props.Inputs),
		"properties": orEmpty(props.Properties)}
	if props.Creating {
		call.Resource["creating"] = true
	}
	if run.Phase == PhaseCreate || run.Phase == PhaseUpdate {
		call.Resource["inputs"] = call.Inputs
	}
	switch driver.Kind {
	case "workflow":
		reg, err := a.Store.GetWorkflowRegistration(ctx, driver.Name)
		if errors.Is(err, store.ErrNotFound) || err == nil && !reg.Enabled {
			return ResourceCall{}, permanent("the custom workflow %s is not registered or is disabled", driver.Name)
		} else if err != nil {
			return ResourceCall{}, err
		}
		// As at submit; later steps still run, so what was made can be deleted.
		if run.Phase == PhaseCreate && !slices.Contains(reg.Usages, "resource_type") {
			return ResourceCall{}, permanent("the custom workflow %s isn't registered for resource types", driver.Name)
		}
		if run.Phase == PhaseCreate && len(reg.ProjectIDs) > 0 && !slices.Contains(reg.ProjectIDs, dep.ProjectID) {
			return ResourceCall{}, permanent("the custom workflow %s isn't offered to this project", driver.Name)
		}
		call.Workflow = &WorkflowCall{Key: reg.Key, Title: reg.Title, Type: reg.WorkflowType, TaskQueue: reg.TaskQueue,
			Timeout: reg.TimeoutSeconds, Inputs: call.Inputs, Resource: call.Resource}
	case "awx":
		call.Job = &Job{Key: in.StepKey, Title: driver.Name, Template: driver.Name}
		call.ExtraVars = maps.Clone(call.Inputs)
		if call.ExtraVars == nil {
			call.ExtraVars = map[string]any{}
		}
		call.ExtraVars["rvav"] = map[string]any{"request_id": req.ID, "request_number": req.Number, "project": req.ProjectCode,
			"environment": req.Environment, "requester": req.RequesterEmail, "deployment_id": dep.ID, "deployment": dep.Name,
			"resource": call.Resource}
	default:
		return ResourceCall{}, permanent("the resource type %s runs its %s step with %q, which rvav doesn't know", t.Key, run.Phase, driver.Kind)
	}
	if topic := resourceTopic(run.Phase, true); topic != "" {
		if err := outbox.ForCustom(ctx, a.Store.Pool, topic, in.RequestID, in.RequestID+":"+topic+":"+in.StepKey, call.Resource); err != nil {
			return ResourceCall{}, err
		}
	}
	if run.Phase == PhaseCreate {
		// Kept from now on, so that a create failing part way is deleted
		// by the rollback with what it has (ADR-0056).
		props := store.CustomProps{ResourceType: store.CustomResourceType{Key: t.Key, Title: t.Title}, Key: unit.Key, Source: run.Source,
			System: DriverSystem(&t.Lifecycle.Delete), Inputs: call.Inputs, Properties: map[string]any{},
			Titles: propertyTitles(t.PropertySchema), Creating: true}
		raw, _ := json.Marshal(props)
		r := store.Resource{ID: call.ResourceID, DeploymentID: dep.ID, Type: CustomResource, Name: name, ExternalID: &call.ResourceID,
			Properties: raw, RequestID: &in.RequestID}
		if _, err := a.Store.UpsertResource(ctx, a.Store.Pool, r); err != nil {
			return ResourceCall{}, err
		}
	}
	return call, nil
}

// begunResource is the ID of the custom resource a request makes under the
// unit's key: the one an earlier try began, else a new one.
func (a *Activities) begunResource(ctx context.Context, requestID string, unit ResourceUnit) (string, error) {
	list, err := a.Store.ResourcesForRequest(ctx, requestID)
	if err != nil {
		return "", err
	}
	for _, r := range list {
		var p store.CustomProps
		if r.Type == CustomResource && r.DeploymentID == unit.DeploymentID && json.Unmarshal(r.Properties, &p) == nil && p.Key == unit.Key {
			return r.ID, nil
		}
	}
	return ids.New(ids.Resource), nil
}

// FinishResource records what the step gave: the resource made, its
// properties after an update or a read, or the resource gone. It writes the
// step's .post event and returns the properties.
func (a *Activities) FinishResource(ctx context.Context, in FinishResourceInput) (map[string]any, error) {
	result := in.Result
	if in.Call.Job != nil {
		var err error
		if result, err = a.jobArtifacts(ctx, in.RequestID, in.StepKey); err != nil {
			return nil, err
		}
	}
	resource := maps.Clone(in.Call.Resource)
	depID, _ := resource["deployment_id"].(string)
	detail := ""
	switch in.Run.Phase {
	case PhaseDelete:
		if err := a.Store.DeleteResource(ctx, a.Store.Pool, in.Call.ResourceID); err != nil {
			return nil, err
		}
		detail = fmt.Sprint(resource["name"])
	default:
		t, err := a.Store.GetResourceType(ctx, typeKey(resource))
		if err != nil {
			return nil, err
		}
		var props store.CustomProps
		var name string
		if in.Run.Phase == PhaseCreate {
			key, _ := resource["key"].(string)
			props = store.CustomProps{Key: key, Source: in.Run.Source}
			dep, err := a.Store.GetDeployment(ctx, depID)
			if err != nil {
				return nil, err
			}
			name = resourceName(result, in.Call.Inputs, dep.Name, key)
		} else {
			res, err := a.Store.GetResource(ctx, in.Call.ResourceID)
			if err != nil {
				return nil, err
			}
			_ = json.Unmarshal(res.Properties, &props)
			name = cmp.Or(stringOf(result, "name"), res.Name)
		}
		props.ResourceType = store.CustomResourceType{Key: t.Key, Title: t.Title}
		props.System = DriverSystem(&t.Lifecycle.Delete)
		props.Titles = propertyTitles(t.PropertySchema)
		if in.Run.Phase != PhaseRead {
			props.Inputs = in.Call.Inputs
		}
		if result != nil || props.Properties == nil {
			props.Properties = orEmpty(result)
		}
		raw, _ := json.Marshal(props)
		r := store.Resource{ID: in.Call.ResourceID, DeploymentID: depID, Type: CustomResource, Name: name, ExternalID: &in.Call.ResourceID,
			Properties: raw}
		if in.Run.Phase == PhaseCreate {
			r.RequestID = &in.RequestID
		}
		if _, err := a.Store.UpsertResource(ctx, a.Store.Pool, r); err != nil {
			return nil, err
		}
		resource["name"], resource["properties"], resource["inputs"] = name, props.Properties, orEmpty(props.Inputs)
		detail = name
		result = props.Properties
	}
	if topic := resourceTopic(in.Run.Phase, false); topic != "" {
		if err := outbox.ForCustom(ctx, a.Store.Pool, topic, in.RequestID, in.RequestID+":"+topic+":"+in.StepKey, resource); err != nil {
			return nil, err
		}
	}
	// The run data (ADR-0057): the properties after, or what a delete gave.
	if result != nil {
		raw, _ := json.Marshal(result)
		if err := a.Store.SetStepOutputs(ctx, in.RequestID, in.StepKey, raw); err != nil {
			return nil, err
		}
	}
	a.update(ctx, in.RequestID, in.ProjectID, in.StepKey, store.StepUpdate{Status: "done", Detail: &detail})
	return orEmpty(result), nil
}

// jobArtifacts are what the step's AWX job gave with set_stats.
func (a *Activities) jobArtifacts(ctx context.Context, requestID, stepKey string) (map[string]any, error) {
	st, err := a.Store.GetStepByKey(ctx, requestID, stepKey)
	if err != nil {
		return nil, err
	}
	if st.ExternalID == nil {
		return nil, permanent("the AWX job of %s is not recorded", stepKey)
	}
	id, err := strconv.Atoi(*st.ExternalID)
	if err != nil {
		return nil, permanent("the AWX job of %s is %q", stepKey, *st.ExternalID)
	}
	job, err := a.AWX.GetJob(ctx, id, false)
	if err != nil {
		return nil, fmt.Errorf("AWX: %w", err)
	}
	return job.Artifacts, nil
}

// ensureStep adds the step a rollback runs, at the end of the timeline.
func (a *Activities) ensureStep(ctx context.Context, in ResourceInput, system string) error {
	if _, err := a.Store.GetStepByKey(ctx, in.RequestID, in.StepKey); err == nil || !errors.Is(err, store.ErrNotFound) {
		return err
	}
	steps, err := a.Store.ListSteps(ctx, in.RequestID)
	if err != nil {
		return err
	}
	pos := 0
	for _, s := range steps {
		if s.ParentID == nil && s.Position >= pos {
			pos = s.Position + 1
		}
	}
	now := time.Now().UTC()
	st := store.Step{ID: ids.New(ids.Step), RequestID: in.RequestID, Position: pos, Key: in.StepKey,
		Title: cmp.Or(in.Run.Title, "Delete a custom resource"), System: &system, Status: "pending", StartedAt: &now}
	if err := a.Store.InsertSteps(ctx, a.Store.Pool, []store.Step{st}); err != nil && !store.IsUniqueViolation(err) {
		return err
	}
	a.Events.Step(ctx, in.ProjectID, in.RequestID, st)
	return nil
}

// resourceInputs works a blueprint resource's bindings out, or takes a
// composite step's.
func (a *Activities) resourceInputs(ctx context.Context, req store.RequestRow, dep store.DeploymentRow, run ResourceRun, unit ResourceUnit) (map[string]any, error) {
	if run.Resolved {
		return orEmpty(run.Inputs), nil
	}
	vars, err := a.stepVars(ctx, req.ID, nil)
	if err != nil {
		return nil, err
	}
	delete(vars, "steps")
	others, err := a.Store.ListResources(ctx, dep.ID)
	if err != nil {
		return nil, err
	}
	made := map[string]any{}
	for _, r := range others {
		if r.Type != CustomResource {
			continue
		}
		var p store.CustomProps
		if json.Unmarshal(r.Properties, &p) == nil && p.Key != "" {
			made[p.Key] = orEmpty(p.Properties)
		}
	}
	vars["resources"], vars["components"] = made, orEmpty(run.Components)
	vars["deployment"] = map[string]any{"id": dep.ID, "name": dep.Name}
	out := map[string]any{}
	for _, k := range slices.Sorted(maps.Keys(unit.With)) {
		expr, ok := unit.With[k].(string)
		if !ok {
			out[k] = unit.With[k]
			continue
		}
		v, err := policy.ResourceEval(expr, vars)
		if err != nil {
			return nil, permanent("%s: the input %s: %v", unit.Key, k, err)
		}
		out[k] = v
	}
	return out, nil
}

func resourceTopic(phase string, pre bool) string {
	switch {
	case phase == PhaseCreate && pre:
		return outbox.CustomCreatePre
	case phase == PhaseCreate:
		return outbox.CustomCreatePost
	case phase == PhaseDelete && pre:
		return outbox.CustomDeletePre
	case phase == PhaseDelete:
		return outbox.CustomDeletePost
	}
	return ""
}

// DriverSystem is what runs a step of a custom resource, for the timeline.
func DriverSystem(d *store.ResourceDriver) string {
	if d != nil && d.Kind == "awx" {
		return day2.SystemAWX
	}
	return "rvav"
}

// resourceName is the name create gave, else the name input, else the
// deployment's name and the resource's key.
func resourceName(result, inputs map[string]any, deployment, key string) string {
	return cmp.Or(stringOf(result, "name"), stringOf(inputs, "name"), deployment+"-"+key)
}

func stringOf(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return s
}

func typeKey(resource map[string]any) string {
	t, _ := resource["type"].(map[string]any)
	return stringOf(t, "key")
}

// propertyTitles are the titles a type's property schema gives its
// properties.
func propertyTitles(schema json.RawMessage) map[string]string {
	var s struct {
		Properties map[string]struct {
			Title string `json:"title"`
		} `json:"properties"`
	}
	_ = json.Unmarshal(schema, &s)
	out := map[string]string{}
	for k, p := range s.Properties {
		if p.Title != "" {
			out[k] = p.Title
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func orEmpty(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

// normalized is a value as JSON gives it back, to compare inputs worked
// out now with those stored.
func normalized(v map[string]any) any {
	b, _ := json.Marshal(orEmpty(v))
	var out any
	_ = json.Unmarshal(b, &out)
	return out
}
