package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/blueprint"
	"github.com/damhau/rvav/internal/outbox"
	"github.com/damhau/rvav/internal/policy"
	"github.com/damhau/rvav/internal/store"
)

// A composite's loops, catch paths, default error handler and waits for a
// signal (ADR-0057).

var signalName = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,62}$`)

// flowVar finds what a loop's pass (each, index) or a catch path (error)
// reads: a name of its own, not a field of something else.
var flowVar = regexp.MustCompile(`(?:^|[^.\w])(each|index|error)\b`)

// maxSignalTimeout bounds a wait for a signal, as a poll's deadline.
const maxSignalTimeout = 90 * 24 * time.Hour

// checkSignal checks a wait for a signal or an event.
func checkSignal(s blueprint.Signal, ptr string) []api.FieldError {
	var errs []api.FieldError
	switch {
	case (s.Name == "") == (s.Event == ""):
		errs = append(errs, fieldErr(ptr, "one_of", "Wait for a signal by its name, or for an event by its topic: one of the two."))
	case s.Name != "" && !signalName.MatchString(s.Name):
		errs = append(errs, fieldErr(ptr+"/name", "pattern", "Lower-case letters, digits, dots, dashes and underscores: it is part of a URL."))
	case s.Event != "" && !slices.Contains(outbox.Topics, s.Event):
		errs = append(errs, fieldErr(ptr+"/event", "enum", "One of "+strings.Join(outbox.Topics, ", ")+"."))
	}
	if s.When != "" {
		switch {
		case s.Event == "":
			errs = append(errs, fieldErr(ptr+"/when", "invalid", "Only a wait for an event picks one with a condition: a signal is sent to this request."))
		case policy.CheckSignalEvent(s.When) != nil:
			errs = append(errs, fieldErr(ptr+"/when", "invalid", "Not valid CEL: "+policy.CheckSignalEvent(s.When).Error()))
		}
	}
	switch d, err := time.ParseDuration(s.Timeout); {
	case s.Timeout == "":
		errs = append(errs, fieldErr(ptr+"/timeout", "required", "Give it a deadline: a duration such as 72h."))
	case err != nil || d < time.Minute:
		errs = append(errs, fieldErr(ptr+"/timeout", "invalid", "A duration of at least a minute, such as 72h."))
	case d > maxSignalTimeout:
		errs = append(errs, fieldErr(ptr+"/timeout", "invalid", "At most 90 days (2160h)."))
	}
	if s.OnTimeout != "" && !slices.Contains([]string{"fail", "continue"}, s.OnTimeout) {
		errs = append(errs, fieldErr(ptr+"/on_timeout", "enum", "fail or continue."))
	}
	return errs
}

// checkForEach checks a loop: its list, how many passes run at once, and
// that its step is one that can run again and again.
func checkForEach(s blueprint.CompositeStep, ptr string) ([]api.FieldError, []stepExpr) {
	var errs []api.FieldError
	var exprs []stepExpr
	f := *s.ForEach
	if strings.TrimSpace(f.Items) == "" {
		errs = append(errs, fieldErr(ptr+"/for_each/items", "required", "Give the list: CEL over inputs and steps, such as inputs.hosts."))
	} else {
		exprs = append(exprs, stepExpr{ptr + "/for_each/items", f.Items})
	}
	if f.Parallel < 0 || f.Parallel > blueprint.MaxForEachParallel {
		errs = append(errs, fieldErr(ptr+"/for_each/parallel", "maximum", fmt.Sprintf("From 1, one pass after the other, to %d at a time.", blueprint.MaxForEachParallel)))
	}
	if why := loopRefused(s); why != "" {
		errs = append(errs, fieldErr(ptr+"/for_each", "invalid", why))
	}
	if s.Undo != nil {
		errs = append(errs, fieldErr(ptr+"/undo", "invalid", "A loop has no undo: each pass would need its own. Put the passes' work in a custom workflow with its compensation, or add a catch path."))
	}
	return errs, exprs
}

// loopRefused says why a step can't loop.
func loopRefused(s blueprint.CompositeStep) string {
	switch {
	case s.Resource != nil:
		return "A custom resource can't loop: it is one resource of its deployment, under this step's key, and its type's delete rolls back that one."
	case s.Ask != nil, s.Wait != nil, s.Poll != nil, s.Signal != nil:
		return "A wait can't loop: a pass would wait across runs of the workflow. Use a parallel group, or a custom workflow."
	case s.Branch != nil:
		return "A branch can't loop: loop the steps of its sides."
	}
	return ""
}

// catchRefused says why a step can't be on a catch path or in the error
// handler, which run because something failed: only steps that run once,
// with nothing to undo or wait for.
func catchRefused(s blueprint.CompositeStep) string {
	switch {
	case len(s.Parallel) > 0, s.Branch != nil:
		return "A catch path lists steps in order: a parallel group or a branch can't be one of them."
	case s.Ask != nil, s.Wait != nil, s.Poll != nil, s.Signal != nil:
		return "A catch path doesn't wait: it runs while the request is failing. Use a message, a REST call, an AWX job or a custom workflow."
	case s.Resource != nil:
		return "A catch path makes no custom resource: nothing would roll it back."
	case s.ForEach != nil:
		return "A step of a catch path doesn't loop."
	case len(s.Catch) > 0:
		return "A step of a catch path has no catch path of its own."
	case s.Undo != nil:
		return "A step of a catch path isn't undone: it runs because something failed."
	}
	return ""
}

// caughtRefused says why a step can't have a catch path, or be in a
// parallel group that has one.
func caughtRefused(s blueprint.CompositeStep) string {
	switch {
	case s.Resource != nil:
		return "A custom resource step has no catch path: what it made is its type's delete step's to roll back, so the request can't go on past it."
	case s.Branch != nil:
		return "A branch has no catch path: put one on the steps of its sides."
	}
	return ""
}

// flowVarsRead says what of each, index and error an expression reads that
// its step doesn't have.
func flowVarsRead(expr string, loop, caught bool) string {
	for _, m := range flowVar.FindAllStringSubmatch(policy.MaskLiterals(expr, false), -1) {
		switch name := m[1]; {
		case (name == "each" || name == "index") && !loop:
			return name + " is a loop's: this step doesn't repeat over a list."
		case name == "error" && !caught:
			return "error is a catch path's: this step doesn't run because another failed."
		}
	}
	return ""
}

// checkVMSteps refuses, where they are written, what a VM step can't be or
// have: its VMs are planned when the request is submitted, before a branch
// chooses or a step fails. evaluateComposite says the same of a version
// saved before.
func (s *Service) checkVMSteps(ctx context.Context, d api.CatalogVersionDefinition, prefix string) ([]api.FieldError, error) {
	raw, _ := json.Marshal(d.Blueprint)
	b, err := blueprint.Parse(raw)
	if err != nil || len(b.Steps) == 0 {
		return nil, nil
	}
	kinds := map[string]bool{}
	isVM := func(slug string) (bool, error) {
		if slug == "" {
			return false, nil
		}
		if vm, ok := kinds[slug]; ok {
			return vm, nil
		}
		item, err := s.Store.GetItem(ctx, slug, "")
		if errors.Is(err, store.ErrNotFound) {
			return false, nil
		} else if err != nil {
			return false, err
		}
		kinds[slug] = item.Type == "vm_blueprint"
		return kinds[slug], nil
	}
	var errs []api.FieldError
	var walk func(st blueprint.CompositeStep, at string, onSide bool) error
	walk = func(st blueprint.CompositeStep, at string, onSide bool) error {
		vm, err := isVM(st.Item)
		if err != nil {
			return err
		}
		switch {
		case vm && onSide:
			errs = append(errs, fieldErr(at, "invalid", "A VM is planned when the request is submitted, before a branch chooses or a step fails, so it can't be on a side of a branch, on a catch path or in the error handler."))
		case vm:
			if st.ForEach != nil {
				errs = append(errs, fieldErr(at+"/for_each", "invalid", "A VM step can't loop: its VMs are planned when the request is submitted. Give its item a count instead."))
			}
			if len(st.Catch) > 0 {
				errs = append(errs, fieldErr(at+"/catch", "invalid", "A VM step has no catch path: what it built is the rollback's to delete, so the request can't go on past it."))
			}
			if st.Undo != nil {
				errs = append(errs, fieldErr(at+"/undo", "invalid", "A VM isn't undone by a job or workflow; a failure deletes it."))
			}
			if st.When != "" {
				errs = append(errs, fieldErr(at+"/when", "invalid", "A VM step has no condition: its VMs are planned when the request is submitted."))
			}
			for _, k := range sortedKeys(st.With) {
				if e, ok := st.With[k].(string); ok && stepRef.MatchString(e) {
					errs = append(errs, fieldErr(at+"/with/"+k, "invalid", "A VM is planned when the request is submitted, so its inputs come from the form, not from steps."))
				}
			}
		}
		groupVM := false
		for j, x := range st.Parallel {
			if err := walk(x, fmt.Sprintf("%s/parallel/%d", at, j), onSide); err != nil {
				return err
			}
			vm, err := isVM(x.Item)
			if err != nil {
				return err
			}
			groupVM = groupVM || vm
		}
		if groupVM && len(st.Catch) > 0 && !onSide {
			errs = append(errs, fieldErr(at+"/catch", "invalid", "This group has a VM step, so it has no catch path: what it built is the rollback's to delete, so the request can't go on past it."))
		}
		for j, x := range st.Catch {
			if err := walk(x, fmt.Sprintf("%s/catch/%d", at, j), true); err != nil {
				return err
			}
		}
		if st.Branch != nil {
			for i, c := range st.Branch.Cases {
				for j, x := range c.Steps {
					if err := walk(x, fmt.Sprintf("%s/branch/cases/%d/steps/%d", at, i, j), true); err != nil {
						return err
					}
				}
			}
			for j, x := range st.Branch.Else {
				if err := walk(x, fmt.Sprintf("%s/branch/else/%d", at, j), true); err != nil {
					return err
				}
			}
		}
		return nil
	}
	ptr := prefix + "/blueprint"
	for g, st := range b.Steps {
		if err := walk(st, fmt.Sprintf("%s/steps/%d", ptr, g), false); err != nil {
			return nil, err
		}
	}
	for j, x := range b.OnError {
		if err := walk(x, fmt.Sprintf("%s/on_error/%d", ptr, j), true); err != nil {
			return nil, err
		}
	}
	return errs, nil
}
