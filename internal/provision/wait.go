package provision

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/damhau/rvav/internal/blueprint"
	"github.com/damhau/rvav/internal/policy"
	"github.com/damhau/rvav/internal/store"
)

// A composite's waits for a time and for a condition (ADR-0033). Their state
// is in the database, the step's due_at and last answer, not in Temporal's
// history: a run continued as new, or a worker back after a while, finds it.

// WaitInput is a step's wait for a time.
type WaitInput struct {
	RequestID string         `json:"request_id"`
	ProjectID string         `json:"project_id"`
	StepKey   string         `json:"step_key"`
	Wait      blueprint.Wait `json:"wait"`
}

// PollInput is a step's wait for a condition. It holds only expressions and a
// secret's name: what they stand for is resolved in each check.
type PollInput struct {
	RequestID string         `json:"request_id"`
	ProjectID string         `json:"project_id"`
	StepKey   string         `json:"step_key"`
	Poll      blueprint.Poll `json:"poll"`
}

// WaitState is a wait that has begun: until when, and how often a poll checks.
type WaitState struct {
	// Cancelled: the request is being cancelled, so it never began.
	Cancelled bool          `json:"cancelled,omitempty"`
	DueAt     time.Time     `json:"due_at"`
	Every     time.Duration `json:"every,omitempty"`
}

// PollCheck says what a check found. The answer stays in the database.
type PollCheck struct {
	Met bool `json:"met"`
}

// waitPlanned is a wait's words before it begins.
func waitPlanned(w *blueprint.Wait) string {
	if w.For != "" {
		return "Waits for " + w.For
	}
	return "Waits until a time"
}

// pollPlanned is a poll's words before it begins.
func pollPlanned(p *blueprint.Poll) string {
	if p.REST == nil {
		return "Waits for a condition"
	}
	return fmt.Sprintf("Waits for a condition: %s every %s", p.REST.Method, p.Every)
}

// waitAway is one round of a wait for a time: nothing signals it, so it has
// only its timer and its stop.
func waitAway(ctx, stop workflow.Context, until time.Time, nothing workflow.ReceiveChannel) waitEnd {
	return waitRound(ctx, stop, nothing, func(workflow.ReceiveChannel) {}, &until)
}

// waitStoppedBy says how a wait's stop reads: the request's cancel, on its
// way, or a sibling's failure, which ends this step for a Retry to start again.
func waitStoppedBy(ctx workflow.Context, info Info, key string) (bool, error) {
	if ctx.Err() != nil {
		return false, temporal.NewCanceledError()
	}
	var a *Activities
	err := workflow.ExecuteActivity(ctx, a.MarkStep, info.RequestID, info.ProjectID, key, "cancelled", stoppedBySibling).Get(ctx, nil)
	return true, err
}

// runWait waits until a step's time. A time already passed goes on at once.
// It says stopped when a sibling's failure ended the wait, and gives
// errContinueAsNew on a wait that has gone on long.
func runWait(ctx, stop workflow.Context, info Info, c CompositeStep) (map[string]any, bool, error) {
	var a *Activities
	key := c.StepKey()
	var st WaitState
	in := WaitInput{RequestID: info.RequestID, ProjectID: info.ProjectID, StepKey: key, Wait: *c.Wait}
	if err := workflow.ExecuteActivity(ctx, a.OpenWait, in).Get(ctx, &st); err != nil {
		return nil, false, err
	}
	if st.Cancelled {
		return nil, false, temporal.NewCanceledError()
	}
	nothing := workflow.NewChannel(ctx)
	for st.DueAt.After(workflow.Now(ctx)) {
		end := waitAway(ctx, stop, st.DueAt, nothing)
		if end == waitStopped || ctx.Err() != nil {
			stopped, err := waitStoppedBy(ctx, info, key)
			return nil, stopped, err
		}
		if end == waitDue {
			break
		}
		if workflow.GetInfo(ctx).GetContinueAsNewSuggested() {
			return nil, false, errContinueAsNew
		}
	}
	return map[string]any{"until": st.DueAt.UTC().Format(time.RFC3339)}, false, nil
}

// pollCtx runs one check. The next check is its retry, so it is tried once;
// an activity that timed out, its worker gone, counts as a check that failed.
func pollCtx(ctx workflow.Context) workflow.Context {
	o := activityOptions
	o.StartToCloseTimeout = 2 * RESTTimeout
	retry := *activityOptions.RetryPolicy
	retry.MaximumAttempts = 1
	o.RetryPolicy = &retry
	return workflow.WithActivityOptions(ctx, o)
}

// runPoll checks a step's condition every interval, until it holds or its
// deadline: at the deadline, one last check, then what the step says. It
// leaves to continue as new only when a check is due, which is the new run's
// first act, so that continuing never checks early.
func runPoll(ctx, stop workflow.Context, info Info, c CompositeStep) (map[string]any, bool, error) {
	var a *Activities
	key := c.StepKey()
	in := PollInput{RequestID: info.RequestID, ProjectID: info.ProjectID, StepKey: key, Poll: *c.Poll}
	var st WaitState
	if err := workflow.ExecuteActivity(ctx, a.OpenPoll, in).Get(ctx, &st); err != nil {
		return nil, false, err
	}
	if st.Cancelled {
		return nil, false, temporal.NewCanceledError()
	}
	nothing := workflow.NewChannel(ctx)
	var check PollCheck
	for {
		check = PollCheck{}
		err := workflow.ExecuteActivity(pollCtx(ctx), a.CheckPoll, in).Get(ctx, &check)
		switch {
		case ctx.Err() != nil:
			return nil, false, temporal.NewCanceledError()
		case err != nil && gaveUp(err):
			return nil, false, err
		}
		if check.Met || !st.DueAt.After(workflow.Now(ctx)) {
			break
		}
		next := workflow.Now(ctx).Add(st.Every)
		if next.After(st.DueAt) {
			next = st.DueAt
		}
		for next.After(workflow.Now(ctx)) {
			end := waitAway(ctx, stop, next, nothing)
			if end == waitStopped || ctx.Err() != nil {
				stopped, err := waitStoppedBy(ctx, info, key)
				return nil, stopped, err
			}
			if end == waitDue {
				break
			}
		}
		if workflow.GetInfo(ctx).GetContinueAsNewSuggested() {
			return nil, false, errContinueAsNew
		}
	}
	var out map[string]any
	err := workflow.ExecuteActivity(ctx, a.PollOutcome, in, check.Met).Get(ctx, &out)
	return out, false, err
}

// gaveUp says a check's failure ends the step: anything else (a network error,
// a 5xx, a worker gone) only waits for the next check.
func gaveUp(err error) bool {
	var app *temporal.ApplicationError
	return errors.As(err, &app) && app.NonRetryable()
}

// OpenWait begins a step's wait, once: called again (an activity retried, a
// workflow continued as new), it finds the time it set.
func (a *Activities) OpenWait(ctx context.Context, in WaitInput) (WaitState, error) {
	r, err := a.Store.GetRequest(ctx, in.RequestID)
	if err != nil {
		return WaitState{}, err
	}
	if r.Status == "cancelling" {
		return WaitState{Cancelled: true}, nil
	}
	st, err := a.Store.GetStepByKey(ctx, in.RequestID, in.StepKey)
	if err != nil {
		return WaitState{}, err
	}
	if st.DueAt != nil {
		return WaitState{DueAt: *st.DueAt}, nil
	}
	now, err := a.Store.Now(ctx)
	if err != nil {
		return WaitState{}, err
	}
	due, detail := now, ""
	if in.Wait.For != "" {
		d, err := time.ParseDuration(in.Wait.For)
		if err != nil {
			return WaitState{}, permanent("Its duration can't be read. Ask a catalog admin to check this item.")
		}
		due, detail = now.Add(d), "Waiting for "+in.Wait.For
	} else {
		if due, err = a.waitUntil(ctx, in); err != nil {
			return WaitState{}, err
		}
		detail = "Waiting until the time it was given"
		if !due.After(now) {
			detail = "That time had already passed, so it went on at once."
		}
	}
	set, err := a.beginWait(ctx, r.ProjectID, in.RequestID, in.StepKey, detail, due)
	return WaitState{DueAt: set}, err
}

// waitUntil reads a step's time. What it says masks the expression's literals
// and quotes no value: a requester reads it.
func (a *Activities) waitUntil(ctx context.Context, in WaitInput) (time.Time, error) {
	steps, err := a.earlierOutputs(ctx, in.RequestID)
	if err != nil {
		return time.Time{}, err
	}
	vars, err := a.stepVars(ctx, in.RequestID, steps)
	if err != nil {
		return time.Time{}, err
	}
	v, err := stepEval(in.Wait.Until, vars)
	if err == nil {
		if t, ok := timeOf(v); ok {
			return t, nil
		}
	}
	return time.Time{}, permanent("Its time can't be read: %s doesn't give a time. Ask a catalog admin to check this item.",
		policy.MaskLiterals(in.Wait.Until, false))
}

// timeOf reads a time: a CEL timestamp, or a text in RFC 3339, or a date or a
// date and time with no zone, which is read as UTC.
func timeOf(v any) (time.Time, bool) {
	switch x := v.(type) {
	case time.Time:
		return x, true
	case string:
		for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02T15:04", time.DateOnly} {
			if t, err := time.Parse(layout, x); err == nil {
				return t, true
			}
		}
	}
	return time.Time{}, false
}

// beginWait marks a step running until a time, and gives the time kept: the
// first set. A wait's state must reach the database, so unlike update's its
// error counts.
func (a *Activities) beginWait(ctx context.Context, projectID, requestID, key, detail string, due time.Time) (time.Time, error) {
	st, err := a.Store.UpdateStep(ctx, a.Store.Pool, requestID, key, store.StepUpdate{Status: "running", Detail: &detail, DueAt: &due})
	if err != nil {
		return time.Time{}, err
	}
	a.Events.Step(ctx, projectID, requestID, st)
	a.Events.Request(ctx, requestID)
	return *st.DueAt, nil
}

// earlierOutputs are the outputs of the steps done, as a later step reads them:
// from the database, so that nothing large goes through each check's history.
func (a *Activities) earlierOutputs(ctx context.Context, requestID string) (map[string]any, error) {
	steps, err := a.Store.ListSteps(ctx, requestID)
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	for _, s := range steps {
		key, ok := strings.CutPrefix(s.Key, KeyStep)
		if !ok || strings.Contains(key, "/") || s.Status != "done" || len(s.Outputs) == 0 {
			continue
		}
		var o map[string]any
		if err := json.Unmarshal(s.Outputs, &o); err != nil {
			return nil, err
		}
		out[key] = o
	}
	return out, nil
}

// OpenPoll begins a step's wait for a condition, once: its deadline counts
// from the first time.
func (a *Activities) OpenPoll(ctx context.Context, in PollInput) (WaitState, error) {
	r, err := a.Store.GetRequest(ctx, in.RequestID)
	if err != nil {
		return WaitState{}, err
	}
	if r.Status == "cancelling" {
		return WaitState{Cancelled: true}, nil
	}
	every, err := time.ParseDuration(in.Poll.Every)
	if err != nil || every <= 0 {
		return WaitState{}, permanent("How often it checks can't be read. Ask a catalog admin to check this item.")
	}
	timeout, err := time.ParseDuration(in.Poll.Timeout)
	if err != nil || timeout <= 0 {
		return WaitState{}, permanent("Its deadline can't be read. Ask a catalog admin to check this item.")
	}
	if in.Poll.REST == nil {
		return WaitState{}, permanent("It has no call to make. Ask a catalog admin to check this item.")
	}
	st, err := a.Store.GetStepByKey(ctx, in.RequestID, in.StepKey)
	if err != nil {
		return WaitState{}, err
	}
	if st.DueAt != nil {
		return WaitState{DueAt: *st.DueAt, Every: every}, nil
	}
	now, err := a.Store.Now(ctx)
	if err != nil {
		return WaitState{}, err
	}
	due, err := a.beginWait(ctx, r.ProjectID, in.RequestID, in.StepKey, "Checks every "+in.Poll.Every, now.Add(timeout))
	return WaitState{DueAt: due, Every: every}, err
}

// CheckPoll makes a poll's call and reads its condition on the answer, kept as
// the step's outputs so far. A transient failure is said on the step and is no
// answer; a failure that a next check wouldn't change, or a condition that
// can't be read, fails the step at once.
func (a *Activities) CheckPoll(ctx context.Context, in PollInput) (PollCheck, error) {
	steps, err := a.earlierOutputs(ctx, in.RequestID)
	if err != nil {
		return PollCheck{}, err
	}
	call := *in.Poll.REST
	answer, err := a.callREST(ctx, RESTInput{RequestID: in.RequestID, ProjectID: in.ProjectID, StepKey: in.StepKey, Call: call, Steps: steps})
	if err != nil {
		if gaveUp(err) {
			return PollCheck{}, err
		}
		detail := fmt.Sprintf("Checks every %s. Last check failed: %s", in.Poll.Every, errMessage(err))
		a.update(ctx, in.RequestID, in.ProjectID, in.StepKey, store.StepUpdate{Detail: &detail})
		return PollCheck{}, nil
	}
	// As a later step reads it: through JSON.
	raw, err := json.Marshal(answer)
	if err != nil {
		return PollCheck{}, permanent("The answer can't be read: %v", err)
	}
	own := map[string]any{}
	if err := json.Unmarshal(raw, &own); err != nil {
		return PollCheck{}, permanent("The answer can't be read: %v", err)
	}
	steps[strings.TrimPrefix(in.StepKey, KeyStep)] = own
	vars, err := a.stepVars(ctx, in.RequestID, steps)
	if err != nil {
		return PollCheck{}, err
	}
	met, err := pollHolds(in.Poll.Until, vars)
	if err != nil {
		return PollCheck{}, err
	}
	own["met"] = met
	raw, err = json.Marshal(own)
	if err != nil {
		return PollCheck{}, permanent("The answer can't be read: %v", err)
	}
	if err := a.Store.SetStepOutputs(ctx, in.RequestID, in.StepKey, raw); err != nil {
		return PollCheck{}, err
	}
	detail := fmt.Sprintf("Checks every %s. Last check: %s answered %v, not yet.", in.Poll.Every, call.Method, answer["status"])
	if met {
		detail = fmt.Sprintf("Its condition holds: %s answered %v.", call.Method, answer["status"])
	}
	a.update(ctx, in.RequestID, in.ProjectID, in.StepKey, store.StepUpdate{Detail: &detail})
	return PollCheck{Met: met}, nil
}

// pollHolds reads a poll's condition. An error masks its literals and quotes
// no value: a requester reads it.
func pollHolds(expr string, vars policy.Vars) (bool, error) {
	met, err := stepBool(expr, vars)
	switch {
	case errors.Is(err, errNotTruth):
		return false, permanent("Its condition %s doesn't give true or false. Ask a catalog admin to check this item.", policy.MaskLiterals(expr, false))
	case err != nil:
		why := "it can't be read on the last answer"
		if errors.Is(err, errMissing) {
			why = "the last answer doesn't have what it reads"
		}
		return false, permanent("Its condition %s failed: %s. Ask a catalog admin to check this item.", policy.MaskLiterals(expr, false), why)
	}
	return met, nil
}

// PollOutcome closes a poll: its outputs when it goes on, a permanent error
// (the step fails) when its deadline fails it. Its outputs are the last answer
// any check got, null if none did, and whether the condition held.
func (a *Activities) PollOutcome(ctx context.Context, in PollInput, met bool) (map[string]any, error) {
	st, err := a.Store.GetStepByKey(ctx, in.RequestID, in.StepKey)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"status": nil, "json": nil, "met": met}
	if len(st.Outputs) > 0 {
		var kept map[string]any
		if err := json.Unmarshal(st.Outputs, &kept); err != nil {
			return nil, err
		}
		out["status"], out["json"] = kept["status"], kept["json"]
	}
	if err := a.Store.ClearStepDue(ctx, in.RequestID, in.StepKey); err != nil {
		return nil, err
	}
	if met {
		return out, nil
	}
	if in.Poll.OnTimeout != "continue" {
		return nil, permanent("Its condition wasn't met within %s.", in.Poll.Timeout)
	}
	detail := fmt.Sprintf("Its condition wasn't met within %s, so it went on.", in.Poll.Timeout)
	a.update(ctx, in.RequestID, in.ProjectID, in.StepKey, store.StepUpdate{Detail: &detail})
	return out, nil
}
