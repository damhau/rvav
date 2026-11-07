package core

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/ohler55/ojg/jp"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/blueprint"
	"github.com/damhau/rvav/internal/ids"
	"github.com/damhau/rvav/internal/policy"
	"github.com/damhau/rvav/internal/provision"
	"github.com/damhau/rvav/internal/restcall"
	"github.com/damhau/rvav/internal/store"
)

// Composite items (ADR-0022).

// compositePart is one step of a composite item, evaluated: a VM item's
// evaluation, an AWX job, a custom workflow or a REST call.
type compositePart struct {
	step  blueprint.CompositeStep
	group int
	title string
	sub   *Evaluation
	job   *provision.Job
	call  *provision.WorkflowCall
	rest  *blueprint.RESTCall
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
			if st.Key != "" || st.Item != "" || st.AWX != nil || st.Workflow != "" || st.REST != nil {
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
			for _, set := range []bool{s.Item != "", s.AWX != nil, s.Workflow != "", s.REST != nil} {
				if set {
					kinds++
				}
			}
			if kinds != 1 {
				errs = append(errs, fieldErr(sp, "one_of", "A step is one of: item, awx, workflow or rest."))
			}
			if s.AWX != nil && s.AWX.JobTemplate == "" {
				errs = append(errs, fieldErr(sp+"/awx/job_template", "required", "Name the AWX template."))
			}
			var exprs, undoExprs []stepExpr
			if s.When != "" {
				exprs = append(exprs, stepExpr{sp + "/when", s.When})
			}
			for _, k := range sortedKeys(s.With) {
				if e, ok := s.With[k].(string); ok {
					exprs = append(exprs, stepExpr{sp + "/with/" + k, e})
				}
			}
			if s.REST != nil {
				if len(s.With) > 0 {
					errs = append(errs, fieldErr(sp+"/with", "invalid", "A REST call has no inputs: its URL and body read inputs and steps themselves."))
				}
				callErrs, callExprs := checkRESTCall(*s.REST, sp+"/rest")
				errs, exprs = append(errs, callErrs...), append(exprs, callExprs...)
			}
			if s.Undo != nil {
				undoKinds := 0
				for _, set := range []bool{s.Undo.JobTemplate != "", s.Undo.Workflow != "", s.Undo.REST != nil} {
					if set {
						undoKinds++
					}
				}
				if undoKinds != 1 {
					errs = append(errs, fieldErr(sp+"/undo", "one_of", "Undo with a job_template, a workflow or a rest call."))
				}
				if s.Undo.REST != nil {
					if len(s.Undo.With) > 0 {
						errs = append(errs, fieldErr(sp+"/undo/with", "invalid", "A REST call has no inputs: its URL and body read inputs and steps themselves."))
					}
					callErrs, callExprs := checkRESTCall(*s.Undo.REST, sp+"/undo/rest")
					errs, undoExprs = append(errs, callErrs...), append(undoExprs, callExprs...)
				}
				for _, k := range sortedKeys(s.Undo.With) {
					if e, ok := s.Undo.With[k].(string); ok {
						undoExprs = append(undoExprs, stepExpr{sp + "/undo/with/" + k, e})
					}
				}
			}
			check := func(x stepExpr, self string) {
				if err := policy.CheckComposite(x.expr); err != nil {
					errs = append(errs, fieldErr(x.at, "invalid", "Not valid CEL: "+err.Error()))
					return
				}
				for _, m := range stepRef.FindAllStringSubmatch(x.expr, -1) {
					if gr, ok := seen[m[1]]; (!ok || gr >= g) && m[1] != self {
						errs = append(errs, fieldErr(x.at, "unknown_step", "steps."+m[1]+" is not an earlier step."))
					}
				}
			}
			for _, x := range exprs {
				check(x, "")
			}
			// An undo runs after its step: it reads that step's outputs too.
			for _, x := range undoExprs {
				check(x, s.Key)
			}
			if s.Key != "" {
				seen[s.Key] = g
			}
		}
	}
	return errs
}

// stepExpr is a step's CEL expression and where it is written.
type stepExpr struct{ at, expr string }

// restMethods are what a REST call may do.
var restMethods = []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete}

// checkRESTCall checks a REST call on its own (its host and secret are
// checkSourceRefs'), and gives the CEL it holds.
func checkRESTCall(c blueprint.RESTCall, ptr string) ([]api.FieldError, []stepExpr) {
	var errs []api.FieldError
	var exprs []stepExpr
	if !slices.Contains(restMethods, c.Method) {
		errs = append(errs, fieldErr(ptr+"/method", "invalid", "GET, POST, PUT, PATCH or DELETE."))
	}
	if c.URL == "" {
		errs = append(errs, fieldErr(ptr+"/url", "required", "Give the URL to call."))
	} else if _, err := restcall.ParseURL(c.URL); err != nil {
		errs = append(errs, fieldErr(ptr+"/url", "invalid", "This URL can't be read: "+err.Error()+"."))
	} else if u, err := url.Parse(restcall.Skeleton(c.URL)); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		errs = append(errs, fieldErr(ptr+"/url", "invalid", "Give an http or https URL."))
	} else if !restcall.FixedOrigin(c.URL) {
		errs = append(errs, fieldErr(ptr+"/url", "invalid", "Expressions may fill the URL's path and query, not its scheme, host or port."))
	} else if u.User != nil {
		errs = append(errs, fieldErr(ptr+"/url", "invalid", "Put credentials in a stored secret, not in the URL."))
	}
	for _, e := range restcall.Expressions(c.URL) {
		exprs = append(exprs, stepExpr{ptr + "/url", e})
	}
	if c.Body != "" {
		if c.Method == http.MethodGet {
			errs = append(errs, fieldErr(ptr+"/body", "invalid", "A GET has no body."))
		}
		exprs = append(exprs, stepExpr{ptr + "/body", c.Body})
	}
	if c.Keep != "" {
		if _, err := jp.ParseString(c.Keep); err != nil {
			errs = append(errs, fieldErr(ptr+"/keep", "invalid", fmt.Sprintf("This is not valid JSONPath: %v", err)))
		}
	}
	for i, status := range c.Accept {
		if status < 100 || status > 599 {
			errs = append(errs, fieldErr(fmt.Sprintf("%s/accept/%d", ptr, i), "invalid", "An HTTP status, 100 to 599."))
		}
	}
	return errs, exprs
}

// restCalls are a composite's REST calls, by pointer.
func restCalls(b blueprint.Blueprint, ptr string) map[string]*blueprint.RESTCall {
	out := map[string]*blueprint.RESTCall{}
	add := func(s blueprint.CompositeStep, at string) {
		if s.REST != nil {
			out[at+"/rest"] = s.REST
		}
		if s.Undo != nil && s.Undo.REST != nil {
			out[at+"/undo/rest"] = s.Undo.REST
		}
	}
	for g, st := range b.Steps {
		at := fmt.Sprintf("%s/steps/%d", ptr, g)
		add(st, at)
		for j, p := range st.Parallel {
			add(p, fmt.Sprintf("%s/parallel/%d", at, j))
		}
	}
	return out
}

// restUnavailable says why a REST call can't be made any more: what the
// hosts allowed and the secrets were when it was saved may have changed. A
// requester reads it, so it names neither.
func (s *Service) restUnavailable(ctx context.Context, c *blueprint.RESTCall) (string, error) {
	if u, err := url.Parse(restcall.Skeleton(c.URL)); err != nil || !s.REST.Allowed(ctx, u) {
		return "calls a host rvav may not call any more. Ask a catalog admin to check this item.", nil
	}
	if c.Secret != "" {
		if _, err := s.Store.GetSecret(ctx, c.Secret); errors.Is(err, store.ErrNotFound) {
			return "uses a secret that is gone. Ask a catalog admin to check this item.", nil
		} else if err != nil {
			return "", err
		}
	}
	return "", nil
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
				if st.When != "" {
					add("", "invalid", label+": a VM step has no condition: its VMs are planned when the request is submitted.")
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
				// As the item launches it (awxLaunch): by its AWX ID when known,
				// its launch inputs the job's inventory and credentials.
				job := awxItemJob(item, bp.AWX, st.Key)
				part.job = &job
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
				Workflow: st.AWX.Workflow, InventoryInput: st.AWX.Inventory, CredentialInputs: st.AWX.Credentials}
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
		case st.REST != nil:
			why, err := s.restUnavailable(ctx, st.REST)
			if err != nil {
				return err
			}
			if why != "" {
				add("", "not_available", label+" "+why)
				continue
			}
			part.rest = st.REST
		}
		if u := st.Undo; u != nil {
			part.undo = &provision.CompositeUndo{With: u.With}
			switch {
			case u.REST != nil:
				why, err := s.restUnavailable(ctx, u.REST)
				if err != nil {
					return err
				}
				if why != "" {
					add("", "not_available", label+" (undo) "+why)
				} else {
					part.undo.REST = u.REST
				}
			case u.JobTemplate != "":
				part.undo.AWX = &provision.Job{Key: st.Key + "-undo", Title: "Undo " + part.title, Template: u.JobTemplate}
			default:
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
			vms += part.sub.VMCount
		}
	}
	ev.Count, ev.VMCount = max(vms, 1), vms
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
