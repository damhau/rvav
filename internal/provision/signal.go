package provision

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/damhau/rvav/internal/blueprint"
	"github.com/damhau/rvav/internal/store"
)

// A composite step that waits for a signal (ADR-0057): a call to the
// request's signal/<name> or an event of its topic. The API and the
// dispatcher keep what came in signal_waits and then signal the workflow, so
// the workflow holds a timer and a channel only, and a lost signal is read at
// the next recheck.

// SignalStep is the signal that tells a step's wait something came for it.
func SignalStep(stepKey string) string { return "rvav.signal." + stepKey }

// SignalInput is a step's wait to open.
type SignalInput struct {
	RequestID string           `json:"request_id"`
	ProjectID string           `json:"project_id"`
	StepKey   string           `json:"step_key"`
	Signal    blueprint.Signal `json:"signal"`
	Steps     map[string]any   `json:"steps,omitempty"`
}

// SignalState is a step's wait as it stands.
type SignalState struct {
	DueAt     time.Time      `json:"due_at"`
	Received  bool           `json:"received,omitempty"`
	Payload   map[string]any `json:"payload,omitempty"`
	Cancelled bool           `json:"cancelled,omitempty"`
}

// signalPlanned is a wait's words before it begins.
func signalPlanned(s *blueprint.Signal) string {
	if s.Event != "" {
		return "Waits for the event " + s.Event
	}
	return "Waits for the signal " + s.Name
}

// runSignal waits until what a step waits for comes, or its timeout. It says
// stopped when a sibling's failure ended the wait, and gives errContinueAsNew
// on a wait that has gone on long.
func runSignal(ctx, stop workflow.Context, info Info, c CompositeStep, outputs map[string]any) (map[string]any, bool, error) {
	var a *Activities
	key := c.StepKey()
	in := SignalInput{RequestID: info.RequestID, ProjectID: info.ProjectID, StepKey: key, Signal: *c.Signal}
	if c.Signal.Event != "" {
		in.Steps = outputs
	}
	var st SignalState
	if err := workflow.ExecuteActivity(ctx, a.OpenSignal, in).Get(ctx, &st); err != nil {
		return nil, false, err
	}
	if st.Cancelled {
		return nil, false, temporal.NewCanceledError()
	}
	signals := workflow.GetSignalChannel(ctx, SignalStep(key))
	for !st.Received {
		end := waitRound(ctx, stop, signals, func(ch workflow.ReceiveChannel) { ch.Receive(ctx, nil) }, &st.DueAt)
		switch end {
		case waitStopped:
			if ctx.Err() == nil {
				_ = workflow.ExecuteActivity(ctx, a.CloseSignal, info.RequestID, key).Get(ctx, nil)
			}
			stopped, err := waitStoppedBy(ctx, info, key)
			return nil, stopped, err
		case waitDue:
			var out map[string]any
			err := workflow.ExecuteActivity(ctx, a.SignalTimedOut, in).Get(ctx, &out)
			return out, false, err
		}
		if err := workflow.ExecuteActivity(ctx, a.SignalState, info.RequestID, key).Get(ctx, &st); err != nil {
			return nil, false, err
		}
		if !st.Received && workflow.GetInfo(ctx).GetContinueAsNewSuggested() {
			return nil, false, errContinueAsNew
		}
	}
	return st.Payload, false, nil
}

// OpenSignal opens a step's wait and says how it stands. Called again (an
// activity retried, a workflow continued as new), it finds the one it opened.
func (a *Activities) OpenSignal(ctx context.Context, in SignalInput) (SignalState, error) {
	r, err := a.Store.GetRequest(ctx, in.RequestID)
	if err != nil {
		return SignalState{}, err
	}
	if r.Status == "cancelling" {
		return SignalState{Cancelled: true}, nil
	}
	d, err := time.ParseDuration(in.Signal.Timeout)
	if err != nil {
		return SignalState{}, permanent("Its timeout can't be read. Ask a catalog admin to check this item.")
	}
	now, err := a.Store.Now(ctx)
	if err != nil {
		return SignalState{}, err
	}
	w := store.SignalWait{RequestID: in.RequestID, StepKey: in.StepKey, Condition: in.Signal.When, Waiting: json.RawMessage(`{}`), DueAt: now.Add(d)}
	detail := "Waiting for the signal " + in.Signal.Name
	if in.Signal.Event != "" {
		w.Topic, detail = &in.Signal.Event, "Waiting for the event "+in.Signal.Event
		if w.Waiting, err = json.Marshal(map[string]any{"request": map[string]any{"id": r.ID, "number": r.Number}, "steps": orEmpty(in.Steps)}); err != nil {
			return SignalState{}, err
		}
	} else {
		w.Name = &in.Signal.Name
	}
	got, err := a.Store.OpenSignalWait(ctx, w)
	if err != nil {
		return SignalState{}, err
	}
	if got.ReceivedAt == nil {
		if _, err := a.beginWait(ctx, r.ProjectID, in.RequestID, in.StepKey, detail, got.DueAt); err != nil {
			return SignalState{}, err
		}
	}
	return signalState(got)
}

func signalState(w store.SignalWait) (SignalState, error) {
	st := SignalState{DueAt: w.DueAt, Received: w.ReceivedAt != nil}
	if st.Received && len(w.Payload) > 0 {
		if err := json.Unmarshal(w.Payload, &st.Payload); err != nil {
			return st, permanent("What came for it isn't a JSON object.")
		}
	}
	if st.Received && st.Payload == nil {
		st.Payload = map[string]any{}
	}
	return st, nil
}

// SignalState reads a step's wait again: on its signal, and at each recheck.
func (a *Activities) SignalState(ctx context.Context, requestID, key string) (SignalState, error) {
	w, err := a.Store.SignalWaitFor(ctx, requestID, key)
	if err != nil {
		return SignalState{}, err
	}
	return signalState(w)
}

// SignalTimedOut closes a wait that reached its timeout: the step fails, or
// goes on with timed_out as it says. Something that came just before is
// still taken.
func (a *Activities) SignalTimedOut(ctx context.Context, in SignalInput) (map[string]any, error) {
	if err := a.Store.CloseSignalWait(ctx, in.RequestID, in.StepKey); err != nil {
		return nil, err
	}
	w, err := a.Store.SignalWaitFor(ctx, in.RequestID, in.StepKey)
	if err != nil {
		return nil, err
	}
	if w.ReceivedAt != nil {
		st, err := signalState(w)
		return st.Payload, err
	}
	what := "signal " + in.Signal.Name
	if in.Signal.Event != "" {
		what = "event " + in.Signal.Event
	}
	if in.Signal.OnTimeout == "continue" {
		detail := fmt.Sprintf("No %s came in %s, so it went on.", what, in.Signal.Timeout)
		a.update(ctx, in.RequestID, in.ProjectID, in.StepKey, store.StepUpdate{Detail: &detail})
		return map[string]any{"timed_out": true}, nil
	}
	return nil, permanent("No %s came in %s.", what, in.Signal.Timeout)
}

// CloseSignal ends a wait a sibling's failure stopped.
func (a *Activities) CloseSignal(ctx context.Context, requestID, key string) error {
	err := a.Store.CloseSignalWait(ctx, requestID, key)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	return err
}
