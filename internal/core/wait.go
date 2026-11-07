package core

import (
	"net/http"
	"slices"
	"time"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/blueprint"
)

// A composite's waits for a time and for a condition (ADR-0033): what they may
// say on save.

// checkWait checks a step's wait for a time, and gives the CEL it holds.
func checkWait(w blueprint.Wait, ptr string) ([]api.FieldError, []stepExpr) {
	var errs []api.FieldError
	var exprs []stepExpr
	switch {
	case w.For == "" && w.Until == "":
		errs = append(errs, fieldErr(ptr, "required", "Say how long it waits, or until when."))
	case w.For != "" && w.Until != "":
		errs = append(errs, fieldErr(ptr, "one_of", "A wait is for a duration or until a time, not both."))
	}
	if w.For != "" {
		if d, err := time.ParseDuration(w.For); err != nil || d < time.Second {
			errs = append(errs, fieldErr(ptr+"/for", "invalid", "A duration such as 72h."))
		}
	}
	if w.Until != "" {
		exprs = append(exprs, stepExpr{ptr + "/until", w.Until})
	}
	return errs, exprs
}

// checkPoll checks a step's wait for a condition on its own (its host and
// secret are checkSourceRefs'), and gives the CEL its call holds and its
// condition, which also reads the step's own outputs.
func checkPoll(p blueprint.Poll, ptr string) ([]api.FieldError, []stepExpr, []stepExpr) {
	var errs []api.FieldError
	var exprs, own []stepExpr
	if p.REST == nil {
		errs = append(errs, fieldErr(ptr+"/rest", "required", "Give the call it repeats."))
	} else {
		callErrs, callExprs := checkRESTCall(*p.REST, ptr+"/rest")
		errs, exprs = append(errs, callErrs...), callExprs
		if p.REST.Method == http.MethodPost || p.REST.Method == http.MethodPatch {
			errs = append(errs, fieldErr(ptr+"/rest/method", "invalid", "A poll repeats its call, so it may only GET, PUT or DELETE: a POST or a PATCH may do something each time."))
		}
	}
	if p.Until == "" {
		errs = append(errs, fieldErr(ptr+"/until", "required", "Say what it waits for: a condition on the call's answer."))
	} else {
		own = append(own, stepExpr{ptr + "/until", p.Until})
	}
	every, err := time.ParseDuration(p.Every)
	switch {
	case p.Every == "":
		errs = append(errs, fieldErr(ptr+"/every", "required", "Say how often it checks: a duration such as 10m."))
	case err != nil:
		errs = append(errs, fieldErr(ptr+"/every", "invalid", "A duration such as 10m."))
	case every < blueprint.MinPollEvery:
		errs = append(errs, fieldErr(ptr+"/every", "invalid", "At least 1m: each check calls that API and adds to the run's history."))
	}
	timeout, err := time.ParseDuration(p.Timeout)
	switch {
	case p.Timeout == "":
		errs = append(errs, fieldErr(ptr+"/timeout", "required", "Give it a deadline: a duration such as 72h."))
	case err != nil:
		errs = append(errs, fieldErr(ptr+"/timeout", "invalid", "A duration such as 72h."))
	case every > 0 && timeout < every:
		errs = append(errs, fieldErr(ptr+"/timeout", "invalid", "At least as long as how often it checks."))
	}
	if p.OnTimeout != "" && !slices.Contains([]string{"fail", "continue"}, p.OnTimeout) {
		errs = append(errs, fieldErr(ptr+"/on_timeout", "enum", "fail or continue."))
	}
	return errs, exprs, own
}

// readsOwnAnswer says whether a poll's condition reads its call's answer:
// nothing else changes during the wait.
func readsOwnAnswer(until, key string) bool {
	for _, m := range stepRef.FindAllStringSubmatch(until, -1) {
		if m[1] == key {
			return true
		}
	}
	return false
}
