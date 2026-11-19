package dispatch

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"

	"github.com/damhau/rvav/internal/outbox"
	"github.com/damhau/rvav/internal/policy"
	"github.com/damhau/rvav/internal/store"
)

// eventWaits gives an event to the composite steps waiting for one of its
// topic that its condition picks (ADR-0057), keeping it for them in the
// round's transaction. It gives the waits to tell once that is committed.
// A step only gets its own project's events: what it gets is its outputs,
// which its requester reads.
func (d *Dispatcher) eventWaits(ctx context.Context, tx pgx.Tx, ev store.IntegrationEvent) ([]store.SignalTarget, error) {
	waits, err := d.Store.EventWaits(ctx, tx, ev.Topic)
	if err != nil || len(waits) == 0 {
		return nil, err
	}
	var e outbox.Event
	if err := json.Unmarshal(ev.Payload, &e); err != nil {
		return nil, nil
	}
	data, err := json.Marshal(e.Data)
	if err != nil {
		return nil, err
	}
	project, _ := e.Data["project"].(map[string]any)
	var got []store.SignalTarget
	for _, w := range waits {
		if ev.Seq <= w.AfterSeq || project["id"] != w.ProjectID {
			continue
		}
		if w.Condition != "" {
			vars := policy.Vars(outbox.Vars(e.Data))
			var waiting map[string]any
			_ = json.Unmarshal(w.Waiting, &waiting)
			vars["waiting"] = waiting
			ok, err := policy.SignalEventBool(w.Condition, vars)
			if err != nil {
				d.Log.Warn("event wait condition", "request", w.RequestID, "step", w.StepKey, "err", err)
				continue
			}
			if !ok {
				continue
			}
		}
		if err := d.Store.ReceiveEvent(ctx, tx, w.RequestID, w.StepKey, data); err != nil {
			return nil, err
		}
		got = append(got, w)
	}
	return got, nil
}

// tell signals the workflows whose waits got an event. A signal lost is
// read at the wait's next recheck.
func (d *Dispatcher) tell(ctx context.Context, waits []store.SignalTarget) {
	if d.Signal == nil {
		return
	}
	for _, w := range waits {
		if w.WorkflowID == nil {
			continue
		}
		if err := d.Signal(ctx, *w.WorkflowID, w.StepKey); err != nil {
			d.Log.Warn("signal event wait", "request", w.RequestID, "step", w.StepKey, "err", err)
		}
	}
}
