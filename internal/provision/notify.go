package provision

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/damhau/rvav/internal/blueprint"
	"github.com/damhau/rvav/internal/policy"
	"github.com/damhau/rvav/internal/restcall"
	"github.com/damhau/rvav/internal/secretinput"
	"github.com/damhau/rvav/internal/store"
)

// A composite step that sends a message through rvav's notifications
// (ADR-0033). Its text is a template over inputs and earlier steps, and what
// it holds is data: each channel escapes it, as it does a name or a comment.

// maxMessage bounds a message once filled: what it reads may be long.
const maxMessage = 4000

// NotifyInput is a step's message to send.
type NotifyInput struct {
	RequestID string           `json:"request_id"`
	ProjectID string           `json:"project_id"`
	StepKey   string           `json:"step_key"`
	Notify    blueprint.Notify `json:"notify"`
	Steps     map[string]any   `json:"steps,omitempty"`
}

// stepMessageKey is what makes a step's message reach each person once, whichever
// attempt or run sends it. A Retry after a rollback that finished frees it
// (store.ResetForRetry): everything runs again, this message too.
func stepMessageKey(requestID, stepKey string) string {
	return "step_message:" + requestID + ":" + stepKey
}

// SendStepMessage fills a step's message and tells those it names. A template
// that can't be filled fails the step, masked, with nobody told. A channel that
// fails doesn't: the notifier retries it, as for any notification. Nobody to
// tell (a group nobody has signed in from yet) isn't a failure either: it says
// so and goes on. Its text stays out of the step, its outputs and Temporal's
// history. It leaves the step running: FinishStep closes it.
func (a *Activities) SendStepMessage(ctx context.Context, in NotifyInput) (map[string]any, error) {
	r, err := a.Store.GetRequest(ctx, in.RequestID)
	if err != nil {
		return nil, err
	}
	if r.Status == "cancelling" {
		return map[string]any{"told": 0}, nil // nobody is to be told
	}
	a.update(ctx, in.RequestID, in.ProjectID, in.StepKey, store.StepUpdate{Status: "running"})
	vars, err := a.stepVars(ctx, in.RequestID, in.Steps)
	if err != nil {
		return nil, err
	}
	sensitive, err := a.secretInputValues(ctx, in.RequestID)
	if err != nil {
		return nil, permanent("%v", err)
	}
	text, err := fillMessage(in.Notify.Message, vars)
	if err != nil {
		msg := errMessage(err)
		a.update(ctx, in.RequestID, in.ProjectID, in.StepKey, store.StepUpdate{Status: "failed", Error: &msg})
		return nil, err
	}
	// An API or a workflow may echo a secret input into what it gave.
	text = restcall.NewScrubber(sensitive...).Text(text)
	if runes := []rune(text); len(runes) > maxMessage {
		text = string(runes[:maxMessage]) + "…"
	}
	recipients, err := a.told(ctx, in.Notify, r.RequesterID, r.OwnerID)
	if err != nil {
		return nil, err
	}
	for _, id := range recipients {
		err := a.Events.NotifyKept(ctx, store.NewNotification{UserID: id, Kind: "step_message", Title: r.Number + " · " + r.Title,
			Body: text, Level: "info", Link: "/requests/" + r.Number, RequestID: &r.ID, DedupeKey: stepMessageKey(r.ID, in.StepKey)})
		if err != nil {
			return nil, err
		}
	}
	detail := "Message sent."
	if len(recipients) == 0 {
		detail = "Nobody to tell: it went on."
	}
	a.update(ctx, in.RequestID, in.ProjectID, in.StepKey, store.StepUpdate{Detail: &detail})
	return map[string]any{"told": len(recipients)}, nil
}

// told lists who a step's message reaches: those it names who still exist, the
// members of its groups seen so far, and the request's own.
func (a *Activities) told(ctx context.Context, n blueprint.Notify, requesterID, ownerID string) ([]string, error) {
	var ids []string
	if len(n.Users) > 0 {
		found, err := a.Store.FindUsers(ctx, n.Users)
		if err != nil {
			return nil, err
		}
		for _, id := range n.Users {
			if _, ok := found[id]; ok {
				ids = append(ids, id)
			}
		}
	}
	if len(n.Groups) > 0 {
		members, err := a.Store.GroupMembers(ctx, n.Groups)
		if err != nil {
			return nil, err
		}
		ids = append(ids, members...)
	}
	if n.Requester {
		ids = append(ids, requesterID)
	}
	if n.Owner {
		ids = append(ids, ownerID)
	}
	return dedupe(ids), nil
}

// fillMessage gives a message: each ${...} evaluated and written as text. What
// isn't a single value (a list, a map, a secret input's marker) fails, its
// expression masked: a requester reads it.
func fillMessage(tpl string, vars policy.Vars) (string, error) {
	parts, err := restcall.ParseURL(tpl)
	if err != nil {
		return "", permanent("The message wasn't sent: it can't be read: %v. Ask a catalog admin to check this item.", err)
	}
	var b strings.Builder
	for _, p := range parts {
		if p.Expr == "" {
			b.WriteString(p.Text)
			continue
		}
		v, err := restEval(p.Expr, vars)
		if err == nil {
			var s string
			if s, err = messageValue(v); err == nil {
				b.WriteString(s)
				continue
			}
		}
		return "", permanent("The message wasn't sent: its ${%s}: %v", policy.MaskLiterals(p.Expr, false), err)
	}
	return b.String(), nil
}

// messageValue writes a value in a message. What is empty is left empty.
func messageValue(v any) (string, error) {
	switch x := v.(type) {
	case nil:
		return "", nil
	case string:
		return x, nil
	case bool:
		return strconv.FormatBool(x), nil
	case int64:
		return strconv.FormatInt(x, 10), nil
	case uint64:
		return strconv.FormatUint(x, 10), nil
	case float64:
		// A number comes back from JSON as a float: 42, not 4.2e+01.
		return strconv.FormatFloat(x, 'f', -1, 64), nil
	}
	if secretinput.IsMarker(v) {
		return "", errors.New("it is a secret input, which a message can't show")
	}
	return "", errors.New("it isn't a single value")
}
