package core

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/blueprint"
	"github.com/damhau/rvav/internal/ids"
	"github.com/damhau/rvav/internal/policy"
	"github.com/damhau/rvav/internal/provision"
	"github.com/damhau/rvav/internal/store"
)

// Composite items (ADR-0022).

// compositePart is one step of a composite item, evaluated: a VM item's
// evaluation, an AWX job or a custom workflow.
type compositePart struct {
	step  blueprint.CompositeStep
	group int
	title string
	sub   *Evaluation
	job   *provision.Job
	call  *provision.WorkflowCall
	undo  *provision.CompositeUndo
}

var compositeKey = regexp.MustCompile(`^[a-z][a-z0-9_]{0,40}$`)
var stepRef = regexp.MustCompile(`\bsteps\.([a-z][a-z0-9_]*)`)

// flattenSteps numbers a composite's steps by group: a parallel group's
// steps share one.
func flattenSteps(steps []blueprint.CompositeStep) ([]blueprint.CompositeStep, []int) {
	var out []blueprint.CompositeStep
	var groups []int
	for g, st := range steps {
		if len(st.Parallel) > 0 {
			for _, p := range st.Parallel {
				out, groups = append(out, p), append(groups, g)
			}
			continue
		}
		out, groups = append(out, st), append(groups, g)
	}
	return out, groups
}

// checkComposite checks a composite blueprint on its own (the items and
// workflows it names are checked when it is requested).
func checkComposite(b blueprint.Blueprint, ptr string, publish bool) []api.FieldError {
	var errs []api.FieldError
	if len(b.Steps) == 0 {
		if publish {
			errs = append(errs, fieldErr(ptr+"/steps", "required", "List the steps."))
		}
		return errs
	}
	seen := map[string]int{} // key -> group
	for g, st := range b.Steps {
		list, at := []blueprint.CompositeStep{st}, fmt.Sprintf("%s/steps/%d", ptr, g)
		if len(st.Parallel) > 0 {
			list = st.Parallel
			if st.Key != "" || st.Item != "" || st.AWX != nil || st.Workflow != "" {
				errs = append(errs, fieldErr(at, "invalid", "A parallel group only lists steps."))
			}
		}
		for j, s := range list {
			sp := at
			if len(st.Parallel) > 0 {
				sp = fmt.Sprintf("%s/parallel/%d", at, j)
				if len(s.Parallel) > 0 {
					errs = append(errs, fieldErr(sp, "invalid", "Parallel groups don't nest."))
					continue
				}
			}
			if !compositeKey.MatchString(s.Key) {
				errs = append(errs, fieldErr(sp+"/key", "pattern", "A key of lower-case letters, digits and underscores."))
			} else if _, dup := seen[s.Key]; dup {
				errs = append(errs, fieldErr(sp+"/key", "duplicate", "Another step has the key "+s.Key+"."))
			}
			kinds := 0
			for _, set := range []bool{s.Item != "", s.AWX != nil, s.Workflow != ""} {
				if set {
					kinds++
				}
			}
			if kinds != 1 {
				errs = append(errs, fieldErr(sp, "one_of", "A step is one of: item, awx or workflow."))
			}
			if s.AWX != nil && s.AWX.JobTemplate == "" {
				errs = append(errs, fieldErr(sp+"/awx/job_template", "required", "Name the AWX template."))
			}
			exprs := map[string]string{}
			if s.When != "" {
				exprs[sp+"/when"] = s.When
			}
			for k, v := range s.With {
				if e, ok := v.(string); ok {
					exprs[sp+"/with/"+k] = e
				}
			}
			if s.Undo != nil {
				if (s.Undo.JobTemplate == "") == (s.Undo.Workflow == "") {
					errs = append(errs, fieldErr(sp+"/undo", "one_of", "Undo with a job_template or a workflow."))
				}
				for k, v := range s.Undo.With {
					if e, ok := v.(string); ok {
						exprs[sp+"/undo/with/"+k] = e
					}
				}
			}
			for at, e := range exprs {
				if err := policy.CheckComposite(e); err != nil {
					errs = append(errs, fieldErr(at, "invalid", "Not valid CEL: "+err.Error()))
					continue
				}
				for _, m := range stepRef.FindAllStringSubmatch(e, -1) {
					if gr, ok := seen[m[1]]; !ok || gr >= g {
						errs = append(errs, fieldErr(at, "unknown_step", "steps."+m[1]+" is not an earlier step."))
					}
				}
			}
			if s.Key != "" {
				seen[s.Key] = g
			}
		}
	}
	return errs
}

// evaluateComposite evaluates each step: a VM item step as a request of
// that item with the bound inputs (placement, names, quota), the others as
// what they will run.
func (s *Service) evaluateComposite(ctx context.Context, p *auth.Principal, ev *Evaluation, strict bool) error {
	add := func(field, code, msg string) { ev.Errors = append(ev.Errors, fieldErr(field, code, msg)) }
	if len(ev.Blueprint.Steps) == 0 {
		add("", "not_available", ev.Item.Name+" has no steps yet.")
		return nil
	}
	steps, groups := flattenSteps(ev.Blueprint.Steps)
	vars := policy.Vars{"inputs": ev.Inputs, "steps": map[string]any{}, "environment": ev.Env.Key,
		"project": map[string]any{"id": ev.Project.ID, "code": ev.Project.Code, "name": ev.Project.Name}}
	for i, st := range steps {
		part := compositePart{step: st, group: groups[i], title: st.Title}
		label := st.Key
		if st.Title != "" {
			label = st.Title
		}
		switch {
		case st.Item != "":
			item, err := s.Store.GetItem(ctx, st.Item, p.User.ID)
			if errors.Is(err, store.ErrNotFound) || err == nil && item.PublishedVersionID == nil {
				add("", "not_available", label+" uses "+st.Item+", which isn't published.")
				continue
			} else if err != nil {
				return err
			}
			version, err := s.Store.GetVersion(ctx, *item.PublishedVersionID)
			if err != nil {
				return err
			}
			bp, err := blueprint.Parse(version.Blueprint)
			if err != nil {
				return err
			}
			if part.title == "" {
				part.title = item.Name
			}
			switch bp.Kind {
			case blueprint.KindVM:
				if st.Undo != nil {
					add("", "invalid", label+": a VM isn't undone by a job or workflow; a failure deletes it.")
				}
				inputs := map[string]any{}
				for k, v := range st.With {
					e, ok := v.(string)
					if !ok {
						inputs[k] = v
						continue
					}
					if stepRef.MatchString(e) {
						add("", "invalid", label+": a VM is planned when the request is submitted, so its inputs come from the form, not from steps.")
						continue
					}
					val, err := policy.CompositeEval(e, vars)
					if err != nil {
						continue // an input not given yet: the item's own check says so
					}
					inputs[k] = val
				}
				sub, err := s.evaluate(ctx, p, api.RequestDraft{CatalogItemId: item.ID, VersionId: version.ID,
					ProjectId: ev.Project.ID, Environment: ev.Env.Key, Inputs: inputs}, strict, true)
				if err != nil {
					return err
				}
				for _, e := range sub.Errors {
					field := ""
					if f, ok := strings.CutPrefix(e.Field, "/inputs/"); ok {
						if src, ok := st.With[f].(string); ok && strings.HasPrefix(src, "inputs.") {
							field = "/inputs/" + strings.TrimPrefix(src, "inputs.")
						}
					}
					add(field, deref(e.Code), label+": "+e.Message)
				}
				part.sub = sub
			case blueprint.KindAWX:
				if bp.AWX == nil {
					add("", "not_available", label+": "+st.Item+" has no AWX template.")
					continue
				}
				part.job = &provision.Job{Key: st.Key, Title: bp.AWX.Title, Template: bp.AWX.JobTemplate,
					TemplateID: bp.AWX.TemplateID, Workflow: bp.AWX.Workflow}
			case blueprint.KindWorkflow:
				reg, why, err := s.usableWorkflow(ctx, bp.Workflow, "catalog_item", ev.Project.ID)
				if err != nil {
					return err
				}
				if why != "" {
					add("", "not_available", label+": "+why)
					continue
				}
				part.call = callOf(reg)
			default:
				add("", "not_available", label+": "+st.Item+" is a composite item: composites don't nest yet.")
				continue
			}
		case st.AWX != nil:
			title := st.AWX.Title
			if title == "" {
				title = st.AWX.JobTemplate
			}
			if part.title == "" {
				part.title = title
			}
			part.job = &provision.Job{Key: st.Key, Title: title, Template: st.AWX.JobTemplate, TemplateID: st.AWX.TemplateID,
				Workflow: st.AWX.Workflow}
		case st.Workflow != "":
			reg, why, err := s.usableWorkflow(ctx, st.Workflow, "composite_step", ev.Project.ID)
			if err != nil {
				return err
			}
			if why != "" {
				add("", "not_available", label+": "+why)
				continue
			}
			part.call = callOf(reg)
			if part.title == "" {
				part.title = reg.Title
			}
		}
		if u := st.Undo; u != nil {
			part.undo = &provision.CompositeUndo{With: u.With}
			if u.JobTemplate != "" {
				part.undo.AWX = &provision.Job{Key: st.Key + "-undo", Title: "Undo " + part.title, Template: u.JobTemplate}
			} else {
				reg, why, err := s.usableWorkflow(ctx, u.Workflow, "composite_step", ev.Project.ID)
				if err != nil {
					return err
				}
				if why != "" {
					add("", "not_available", label+" (undo): "+why)
				} else {
					part.undo.Workflow = callOf(reg)
				}
			}
		}
		if part.title == "" {
			part.title = st.Key
		}
		ev.Composite = append(ev.Composite, part)
	}
	// Policies see the first VM and how many there are in all.
	vms := 0
	for _, part := range ev.Composite {
		if part.sub != nil {
			if vms == 0 {
				ev.VM = part.sub.VM
			}
			vms += part.sub.Count
		}
	}
	ev.Count = max(vms, 1)
	return nil
}

func callOf(reg store.WorkflowRegistration) *provision.WorkflowCall {
	return &provision.WorkflowCall{Key: reg.Key, Title: reg.Title, Type: reg.WorkflowType, TaskQueue: reg.TaskQueue,
		Timeout: reg.TimeoutSeconds}
}

// compositeVMs says whether a composite builds VMs.
func (ev *Evaluation) compositeVMs() bool {
	for _, part := range ev.Composite {
		if part.sub != nil {
			return true
		}
	}
	return false
}

// stepsOf turns a plan's step definitions into the request's steps, under
// their parents.
func stepsOf(requestID string, defs []provision.StepDef) []store.Step {
	now := time.Now().UTC()
	idOf, positions := map[string]string{}, map[string]int{}
	steps := make([]store.Step, 0, len(defs))
	for _, sd := range defs {
		st := store.Step{ID: ids.New(ids.Step), RequestID: requestID, Position: positions[sd.Parent], Key: sd.Key, Title: sd.Title,
			Detail: sd.Detail, System: &sd.System, Status: sd.Status}
		positions[sd.Parent]++
		if sd.Parent != "" {
			if id, ok := idOf[sd.Parent]; ok {
				st.ParentID = &id
			}
		}
		if sd.Status != "pending" {
			st.StartedAt = &now
		}
		if sd.Status == "done" {
			st.FinishedAt = &now
		}
		idOf[sd.Key] = st.ID
		steps = append(steps, st)
	}
	return steps
}
