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
	"unicode/utf8"

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
// evaluation, an AWX job, a custom workflow, a REST call, a wait, a branch or
// a message.
type compositePart struct {
	step  blueprint.CompositeStep
	group int
	title string
	sub   *Evaluation
	job   *provision.Job
	call  *provision.WorkflowCall
	rest  *blueprint.RESTCall
	ask   *blueprint.Ask
	wait  *blueprint.Wait
	poll  *blueprint.Poll
	// branch holds its sides' steps, planned; none of them is a VM step.
	branch *provision.BranchPlan
	notify *blueprint.Notify
	undo   *provision.CompositeUndo
}

// planned is a part as a plan holds it, but for what a VM step reserves, which
// the request's transaction does.
func (part compositePart) planned() provision.CompositeStep {
	c := provision.CompositeStep{Key: part.step.Key, Title: part.title, Group: part.group, When: part.step.When, Undo: part.undo}
	switch {
	case part.job != nil:
		c.AWX, c.With = part.job, part.step.With
	case part.call != nil:
		c.Workflow, c.With = part.call, part.step.With
	case part.rest != nil:
		c.REST = part.rest
	case part.ask != nil:
		c.Ask = part.ask
	case part.wait != nil:
		c.Wait = part.wait
	case part.poll != nil:
		c.Poll = part.poll
	case part.branch != nil:
		c.Branch = part.branch
	case part.notify != nil:
		c.Notify = part.notify
	}
	return c
}

var compositeKey = regexp.MustCompile(`^[a-z][a-z0-9_]{0,40}$`)
var stepRef = regexp.MustCompile(`\bsteps\.([a-z][a-z0-9_]*)`)

// inputRead finds the inputs an expression reads, by name or by index.
var inputRead = regexp.MustCompile(`\binputs\s*(?:\.\s*([A-Za-z_][A-Za-z0-9_]*)|\[\s*["']([^"']+)["']\s*\])`)

// maxStepMessage bounds a message as written, in characters (runes).
const maxStepMessage = 2000

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

// eachStep visits a composite's steps with where each is written: a parallel
// group and the steps it lists, a branch and those of its sides.
func eachStep(b blueprint.Blueprint, ptr string, visit func(s blueprint.CompositeStep, at string)) {
	var walk func(s blueprint.CompositeStep, at string)
	walk = func(s blueprint.CompositeStep, at string) {
		visit(s, at)
		for j, p := range s.Parallel {
			walk(p, fmt.Sprintf("%s/parallel/%d", at, j))
		}
		if s.Branch != nil {
			for i, c := range s.Branch.Cases {
				for j, x := range c.Steps {
					walk(x, fmt.Sprintf("%s/branch/cases/%d/steps/%d", at, i, j))
				}
			}
			for j, x := range s.Branch.Else {
				walk(x, fmt.Sprintf("%s/branch/else/%d", at, j))
			}
		}
	}
	for g, st := range b.Steps {
		walk(st, fmt.Sprintf("%s/steps/%d", ptr, g))
	}
}

// sideScope is what a step on a side of a branch reads besides earlier groups:
// its branch and the earlier steps of its side. The other sides' steps never
// ran when it does.
type sideScope struct{ open, others map[string]bool }

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
	var step func(s blueprint.CompositeStep, sp string, g int, side *sideScope)
	step = func(s blueprint.CompositeStep, sp string, g int, side *sideScope) {
		if !compositeKey.MatchString(s.Key) {
			errs = append(errs, fieldErr(sp+"/key", "pattern", "A key of lower-case letters, digits and underscores."))
		} else if _, dup := seen[s.Key]; dup {
			errs = append(errs, fieldErr(sp+"/key", "duplicate", "Another step has the key "+s.Key+"."))
		}
		kinds := 0
		for _, set := range []bool{s.Item != "", s.AWX != nil, s.Workflow != "", s.REST != nil, s.Ask != nil, s.Wait != nil, s.Poll != nil, s.Branch != nil, s.Notify != nil} {
			if set {
				kinds++
			}
		}
		if kinds != 1 {
			errs = append(errs, fieldErr(sp, "one_of", "A step is one of: item, awx, workflow, rest, ask, wait, poll, branch or notify."))
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
		if s.Ask != nil {
			if len(s.With) > 0 {
				errs = append(errs, fieldErr(sp+"/with", "invalid", "A wait for a person has no inputs."))
			}
			if s.Undo != nil {
				errs = append(errs, fieldErr(sp+"/undo", "invalid", "A wait for a person has nothing to undo."))
			}
			errs = append(errs, checkAsk(*s.Ask, sp+"/ask")...)
		}
		if s.Wait != nil {
			if len(s.With) > 0 {
				errs = append(errs, fieldErr(sp+"/with", "invalid", "A wait for a time has no inputs."))
			}
			if s.Undo != nil {
				errs = append(errs, fieldErr(sp+"/undo", "invalid", "A wait for a time has nothing to undo."))
			}
			waitErrs, waitExprs := checkWait(*s.Wait, sp+"/wait")
			errs, exprs = append(errs, waitErrs...), append(exprs, waitExprs...)
		}
		var ownExprs []stepExpr
		if s.Poll != nil {
			if len(s.With) > 0 {
				errs = append(errs, fieldErr(sp+"/with", "invalid", "A poll has no inputs: its URL, body and condition read inputs and steps themselves."))
			}
			if s.Undo != nil {
				errs = append(errs, fieldErr(sp+"/undo", "invalid", "A poll has nothing to undo."))
			}
			pollErrs, pollExprs, until := checkPoll(*s.Poll, sp+"/poll")
			errs, exprs, ownExprs = append(errs, pollErrs...), append(exprs, pollExprs...), until
			if s.Poll.Until != "" && !readsOwnAnswer(s.Poll.Until, s.Key) {
				errs = append(errs, fieldErr(sp+"/poll/until", "invalid", "Read the call's answer, as steps."+s.Key+".json or .status: nothing else changes during the wait."))
			}
		}
		if s.Branch != nil {
			if s.When != "" {
				errs = append(errs, fieldErr(sp+"/when", "invalid", "A branch has no condition of its own: its cases have."))
			}
			if len(s.With) > 0 {
				errs = append(errs, fieldErr(sp+"/with", "invalid", "A branch has no inputs: its conditions read inputs and steps themselves."))
			}
			if s.Undo != nil {
				errs = append(errs, fieldErr(sp+"/undo", "invalid", "A branch has nothing to undo: only the steps that ran on its side are undone."))
			}
			branchErrs, branchExprs := checkBranch(*s.Branch, sp+"/branch")
			errs, exprs = append(errs, branchErrs...), append(exprs, branchExprs...)
		}
		if s.Notify != nil {
			if len(s.With) > 0 {
				errs = append(errs, fieldErr(sp+"/with", "invalid", "A message has no inputs: its text reads inputs and steps itself."))
			}
			if s.Undo != nil {
				errs = append(errs, fieldErr(sp+"/undo", "invalid", "A message can't be unsent: it has nothing to undo."))
			}
			notifyErrs, notifyExprs := checkNotify(*s.Notify, sp+"/notify")
			errs, exprs = append(errs, notifyErrs...), append(exprs, notifyExprs...)
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
				gr, ok := seen[m[1]]
				switch {
				case m[1] == self, side != nil && side.open[m[1]]:
				case side != nil && side.others[m[1]]:
					errs = append(errs, fieldErr(x.at, "other_side", "steps."+m[1]+" is on another side of this branch: only one side runs."))
				case !ok || gr >= g:
					errs = append(errs, fieldErr(x.at, "unknown_step", "steps."+m[1]+" is not an earlier step."))
				}
			}
		}
		for _, x := range exprs {
			check(x, "")
		}
		// An undo runs after its step, and a poll's condition after each of
		// its calls: they read that step's outputs too.
		for _, x := range append(undoExprs, ownExprs...) {
			check(x, s.Key)
		}
		if s.Key != "" {
			seen[s.Key] = g
		}
		if s.Branch == nil {
			return
		}
		// A side's steps come after its branch, which they read, and share its
		// group: only later groups read them, as they read a step `when` skipped.
		sides := branchSides(*s.Branch, sp+"/branch")
		for i, side := range sides {
			scope := &sideScope{open: map[string]bool{s.Key: true}, others: map[string]bool{}}
			for k, other := range sides {
				if k != i {
					for _, x := range other.steps {
						scope.others[x.Key] = true
					}
				}
			}
			for j, x := range side.steps {
				xp := fmt.Sprintf(side.at, j)
				switch {
				case len(x.Parallel) > 0:
					errs = append(errs, fieldErr(xp, "invalid", "A side of a branch lists steps in order: a parallel group can't be one of them."))
				case x.Branch != nil:
					errs = append(errs, fieldErr(xp, "invalid", "A branch can't be on a side of a branch: give it more cases instead, each with its conditions."))
				default:
					step(x, xp, g, scope)
					scope.open[x.Key] = true
				}
			}
		}
	}
	for g, st := range b.Steps {
		list, at := []blueprint.CompositeStep{st}, fmt.Sprintf("%s/steps/%d", ptr, g)
		if len(st.Parallel) > 0 {
			list = st.Parallel
			if st.Key != "" || st.Item != "" || st.AWX != nil || st.Workflow != "" || st.REST != nil || st.Ask != nil || st.Wait != nil || st.Poll != nil || st.Branch != nil || st.Notify != nil {
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
				if s.Branch != nil {
					errs = append(errs, fieldErr(sp, "invalid", "A branch can't run in a parallel group: put it before or after the group."))
					continue
				}
			}
			step(s, sp, g, nil)
		}
	}
	return errs
}

// branchSide is a side's steps, and where they are written: at is a format for
// a step's index.
type branchSide struct {
	steps []blueprint.CompositeStep
	at    string
}

func branchSides(b blueprint.Branch, ptr string) []branchSide {
	sides := make([]branchSide, 0, len(b.Cases)+1)
	for i, c := range b.Cases {
		sides = append(sides, branchSide{c.Steps, fmt.Sprintf("%s/cases/%d/steps/", ptr, i) + "%d"})
	}
	return append(sides, branchSide{b.Else, ptr + "/else/%d"})
}

// checkBranch checks a branch's cases, and gives their conditions. Its sides'
// steps are checked as any step.
func checkBranch(b blueprint.Branch, ptr string) ([]api.FieldError, []stepExpr) {
	var errs []api.FieldError
	var exprs []stepExpr
	if len(b.Cases) == 0 {
		errs = append(errs, fieldErr(ptr+"/cases", "required", "Give it at least one case: a condition and the steps it runs."))
	}
	for i, c := range b.Cases {
		at := fmt.Sprintf("%s/cases/%d/when", ptr, i)
		if c.When == "" {
			errs = append(errs, fieldErr(at, "required", "Say when this case is taken: a CEL condition."))
			continue
		}
		exprs = append(exprs, stepExpr{at, c.When})
	}
	return errs, exprs
}

// checkNotify checks a message on its own (the people it names and the secret
// inputs it would read are checkSourceRefs'), and gives the CEL it holds.
func checkNotify(n blueprint.Notify, ptr string) ([]api.FieldError, []stepExpr) {
	var errs []api.FieldError
	if !n.Requester && !n.Owner && len(n.Groups) == 0 && len(n.Users) == 0 {
		errs = append(errs, fieldErr(ptr, "required", "Say who is told: the requester, the owner, groups or people."))
	}
	for i, g := range n.Groups {
		if strings.TrimSpace(g) == "" {
			errs = append(errs, fieldErr(fmt.Sprintf("%s/groups/%d", ptr, i), "required", "Name the OIDC group."))
		}
	}
	at := ptr + "/message"
	if strings.TrimSpace(n.Message) == "" {
		return append(errs, fieldErr(at, "required", "Say what it says.")), nil
	}
	if utf8.RuneCountInString(n.Message) > maxStepMessage {
		errs = append(errs, fieldErr(at, "too_long", fmt.Sprintf("At most %d characters.", maxStepMessage)))
	}
	if _, err := restcall.ParseURL(n.Message); err != nil {
		return append(errs, fieldErr(at, "invalid", "This message can't be read: "+err.Error()+".")), nil
	}
	var exprs []stepExpr
	for _, e := range restcall.Expressions(n.Message) {
		exprs = append(exprs, stepExpr{at, e})
	}
	return errs, exprs
}

// checkNotifyRefs checks what a composite's messages depend on: the people they
// name exist, and none reads a secret input, which a message can't show. (One
// that reads it some other way fails when it runs: its value is a marker.)
func (s *Service) checkNotifyRefs(ctx context.Context, b blueprint.Blueprint, ui api.UiSchema, ptr string) ([]api.FieldError, error) {
	secret := map[string]bool{}
	for k, f := range ui.Fields {
		if f.Secret != nil && *f.Secret || f.Widget == api.UiWidgetSecret {
			secret[k] = true
		}
	}
	type named struct {
		at string
		n  *blueprint.Notify
	}
	var list []named
	eachStep(b, ptr, func(st blueprint.CompositeStep, at string) {
		if st.Notify != nil {
			list = append(list, named{at + "/notify", st.Notify})
		}
	})
	var errs []api.FieldError
	for _, m := range list {
		for _, e := range restcall.Expressions(m.n.Message) {
			for _, r := range inputRead.FindAllStringSubmatch(e, -1) {
				if field := r[1] + r[2]; secret[field] {
					errs = append(errs, fieldErr(m.at+"/message", "secret", "A message can't show the secret input "+field+": it reaches other people and their mailboxes."))
				}
			}
		}
		if len(m.n.Users) == 0 {
			continue
		}
		found, err := s.Store.FindUsers(ctx, m.n.Users)
		if err != nil {
			return nil, err
		}
		for i, id := range m.n.Users {
			if _, ok := found[id]; !ok {
				errs = append(errs, fieldErr(fmt.Sprintf("%s/users/%d", m.at, i), "unknown", "There is no user "+id+"."))
			}
		}
	}
	return errs, nil
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
	eachStep(b, ptr, func(s blueprint.CompositeStep, at string) {
		if s.REST != nil {
			out[at+"/rest"] = s.REST
		}
		if s.Poll != nil && s.Poll.REST != nil {
			out[at+"/poll/rest"] = s.Poll.REST
		}
		if s.Undo != nil && s.Undo.REST != nil {
			out[at+"/undo/rest"] = s.Undo.REST
		}
	})
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
	// One step, or nil if it was refused. A step on a side of a branch runs only
	// if its side is taken, so it can't be a VM, planned at submit.
	var evalStep func(st blueprint.CompositeStep, group int, onSide bool) (*compositePart, error)
	evalStep = func(st blueprint.CompositeStep, group int, onSide bool) (*compositePart, error) {
		part := compositePart{step: st, group: group, title: st.Title}
		label := st.Key
		if st.Title != "" {
			label = st.Title
		}
		switch {
		case st.Item != "":
			item, err := s.Store.GetItem(ctx, st.Item, p.User.ID)
			if errors.Is(err, store.ErrNotFound) || err == nil && item.PublishedVersionID == nil {
				add("", "not_available", label+" uses "+st.Item+", which isn't published.")
				return nil, nil
			} else if err != nil {
				return nil, err
			}
			version, err := s.Store.GetVersion(ctx, *item.PublishedVersionID)
			if err != nil {
				return nil, err
			}
			bp, err := blueprint.Parse(version.Blueprint)
			if err != nil {
				return nil, err
			}
			if part.title == "" {
				part.title = item.Name
			}
			switch bp.Kind {
			case blueprint.KindVM:
				if onSide {
					add("", "invalid", label+": a VM is planned when the request is submitted, before a branch chooses, so it can't be on a side of one.")
					return nil, nil
				}
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
					return nil, err
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
					return nil, nil
				}
				// As the item launches it (awxLaunch): by its AWX ID when known,
				// its launch inputs the job's inventory and credentials.
				job := awxItemJob(item, bp.AWX, st.Key)
				part.job = &job
			case blueprint.KindWorkflow:
				reg, why, err := s.usableWorkflow(ctx, bp.Workflow, "catalog_item", ev.Project.ID)
				if err != nil {
					return nil, err
				}
				if why != "" {
					add("", "not_available", label+": "+why)
					return nil, nil
				}
				part.call = callOf(reg)
			default:
				add("", "not_available", label+": "+st.Item+" is a composite item: composites don't nest yet.")
				return nil, nil
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
				return nil, err
			}
			if why != "" {
				add("", "not_available", label+": "+why)
				return nil, nil
			}
			part.call = callOf(reg)
			if part.title == "" {
				part.title = reg.Title
			}
		case st.REST != nil:
			why, err := s.restUnavailable(ctx, st.REST)
			if err != nil {
				return nil, err
			}
			if why != "" {
				add("", "not_available", label+" "+why)
				return nil, nil
			}
			part.rest = st.REST
		case st.Ask != nil:
			if asksNobodyElse(st.Ask, p.User.ID) {
				add("", "not_available", label+" asks only you, and you don't answer your own request. Ask a catalog admin to check this item.")
				return nil, nil
			}
			part.ask = st.Ask
		case st.Wait != nil:
			part.wait = st.Wait
		case st.Poll != nil:
			if st.Poll.REST != nil {
				why, err := s.restUnavailable(ctx, st.Poll.REST)
				if err != nil {
					return nil, err
				}
				if why != "" {
					add("", "not_available", label+" "+why)
					return nil, nil
				}
			}
			part.poll = st.Poll
		case st.Branch != nil:
			plan := &provision.BranchPlan{}
			side := func(list []blueprint.CompositeStep) ([]provision.CompositeStep, error) {
				var out []provision.CompositeStep
				for _, x := range list {
					sub, err := evalStep(x, group, true)
					if err != nil {
						return nil, err
					}
					if sub != nil {
						out = append(out, sub.planned())
					}
				}
				return out, nil
			}
			for _, c := range st.Branch.Cases {
				list, err := side(c.Steps)
				if err != nil {
					return nil, err
				}
				plan.Cases = append(plan.Cases, provision.BranchSide{Title: c.Title, When: c.When, Steps: list})
			}
			var err error
			if plan.Else, err = side(st.Branch.Else); err != nil {
				return nil, err
			}
			part.branch = plan
		case st.Notify != nil:
			part.notify = st.Notify
		}
		if u := st.Undo; u != nil {
			part.undo = &provision.CompositeUndo{With: u.With}
			switch {
			case u.REST != nil:
				why, err := s.restUnavailable(ctx, u.REST)
				if err != nil {
					return nil, err
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
					return nil, err
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
		return &part, nil
	}
	for i, st := range steps {
		part, err := evalStep(st, groups[i], false)
		if err != nil {
			return err
		}
		if part != nil {
			ev.Composite = append(ev.Composite, *part)
		}
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
