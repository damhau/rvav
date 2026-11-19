package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/provision"
	"github.com/damhau/rvav/internal/store"
)

// maxSignalPayload bounds what a signal brings: it is the step's outputs,
// in Temporal's history and the database.
const maxSignalPayload = 64 << 10

// Signal gives what a composite step waits for by name (ADR-0057): payload,
// a JSON object, becomes its outputs. Those who manage the project's
// requests send it, as a system they gave a token would: the requester
// can't, so a wait the item puts in their way stays there.
func (s *Service) Signal(ctx context.Context, p *auth.Principal, idOrNumber, name string, payload map[string]any) (store.SignalWait, error) {
	r, err := s.Store.GetRequest(ctx, idOrNumber)
	if errors.Is(err, store.ErrNotFound) || (err == nil && !CanSeeRequest(p, r)) {
		return store.SignalWait{}, ErrNotFound
	}
	if err != nil {
		return store.SignalWait{}, err
	}
	if !p.In(auth.RequestManage, r.ProjectID) {
		return store.SignalWait{}, fmt.Errorf("%w: only those who manage the project's requests send it signals", ErrForbidden)
	}
	if payload == nil {
		payload = map[string]any{}
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return store.SignalWait{}, err
	}
	if len(b) > maxSignalPayload {
		return store.SignalWait{}, &ValidationError{Errors: []api.FieldError{fieldErr("", "too_large", "At most 64 KiB of JSON.")}}
	}
	if r.Status != "running" {
		return store.SignalWait{}, &ConflictError{Msg: "The request isn't running: nothing of it waits for a signal."}
	}
	w, err := s.Store.ReceiveSignal(ctx, r.ID, name, b)
	if errors.Is(err, store.ErrNotFound) {
		return w, &ConflictError{Msg: "No step of " + r.Number + " waits for the signal " + name + " now."}
	}
	if err != nil {
		return w, err
	}
	// Lost, the workflow reads it at its next recheck.
	if r.WorkflowID != nil {
		if err := s.Workflows.Signal(ctx, *r.WorkflowID, provision.SignalStep(w.StepKey), nil); err != nil {
			s.Log.Warn("signal step", "request", r.Number, "step", w.StepKey, "err", err)
		}
	}
	s.audit(ctx, p, "request.signal", "request", r.ID, &r.ProjectID, map[string]any{"number": r.Number, "name": name, "step": w.StepKey})
	return w, nil
}
