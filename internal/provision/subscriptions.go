package provision

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/damhau/rvav/internal/dispatch"
	"github.com/damhau/rvav/internal/ids"
	"github.com/damhau/rvav/internal/outbox"
	"github.com/damhau/rvav/internal/store"
)

// Blocking subscriptions (ADR-0021) run in the request's workflow, right
// before it builds, runs its action or deletes, as steps under "Checks".

// KeyChecks is the step of the blocking subscriptions.
const KeyChecks = "checks"

// BlockingSub is a blocking subscription that applies to a request.
type BlockingSub struct {
	ID      string `json:"id"`
	EventID string `json:"event_id"`
	Name    string `json:"name"`
	Timeout int    `json:"timeout_seconds"`
}

// CheckOutcome is what one blocking subscription decided.
type CheckOutcome struct {
	Stop    bool   `json:"stop"`
	StepKey string `json:"step_key"`
	Reason  string `json:"reason"`
}

func checkKey(subID string) string { return "check:" + subID }

// BlockingSubscriptions lists the blocking subscriptions whose condition is
// true for the request's pre event, by priority, and adds their steps.
func (a *Activities) BlockingSubscriptions(ctx context.Context, requestID string) ([]BlockingSub, error) {
	r, err := a.Store.GetRequest(ctx, requestID)
	if err != nil {
		return nil, err
	}
	pre, _ := outbox.RequestTopics(r.Kind, deref(r.Action))
	subs, err := a.Store.ActiveSubscriptions(ctx, pre, true)
	if err != nil || len(subs) == 0 {
		return nil, err
	}
	if a.Subscriptions == nil {
		a.Log.Warn("blocking subscriptions not checked: no dispatcher in this worker", "request", requestID)
		return nil, nil
	}
	ev, err := a.Store.LatestEvent(ctx, requestID, pre)
	if err != nil {
		return nil, err
	}
	var out []BlockingSub
	for _, s := range subs {
		match, err := dispatch.Matches(s.Condition, ev.Payload)
		if err != nil {
			// A condition that can't be evaluated applies: a check is never
			// skipped by mistake.
			a.Log.Warn("blocking subscription condition", "subscription", s.ID, "err", err)
			match = true
		}
		if match {
			out = append(out, BlockingSub{ID: s.ID, EventID: ev.ID, Name: s.Name, Timeout: s.TimeoutSeconds})
		}
	}
	if len(out) == 0 {
		return nil, nil
	}
	st, err := a.Store.InsertStepBeforePending(ctx, a.Store.Pool, requestID, KeyChecks, "Checks",
		"Subscriptions to "+pre+" that may stop the request", "rvav")
	if err != nil {
		return nil, err
	}
	a.Events.Step(ctx, r.ProjectID, requestID, st)
	for _, b := range out {
		a.child(ctx, JobInput{RequestID: requestID, ProjectID: r.ProjectID, StepKey: KeyChecks}, checkKey(b.ID), b.Name, "pending", nil)
	}
	return out, nil
}

// RunBlockingSubscription asks one blocking subscription, records its run
// and step, and says whether the request stops.
func (a *Activities) RunBlockingSubscription(ctx context.Context, requestID string, b BlockingSub) (CheckOutcome, error) {
	out := CheckOutcome{StepKey: checkKey(b.ID)}
	r, err := a.Store.GetRequest(ctx, requestID)
	if err != nil {
		return out, err
	}
	sub, err := a.Store.GetSubscription(ctx, b.ID)
	if err != nil {
		return out, err
	}
	_, payload, err := a.eventPayload(ctx, b.EventID)
	if err != nil {
		return out, err
	}
	if payload, err = a.withProperties(ctx, requestID, payload); err != nil {
		return out, err
	}
	in := JobInput{RequestID: requestID, ProjectID: r.ProjectID, StepKey: KeyChecks}
	a.child(ctx, in, out.StepKey, sub.Name, "running", nil)
	step, _ := a.Store.GetStepByKey(ctx, requestID, out.StepKey)
	run := store.SubscriptionRun{ID: ids.New(ids.Run), SubscriptionID: sub.ID, EventID: b.EventID, Status: "delivering",
		Blocking: true, RequestID: &requestID, StepID: &step.ID}
	if err := a.Store.InsertRun(ctx, a.Store.Pool, run); err != nil {
		return out, err
	}

	// One more try within the timeout when the first fails fast.
	start := time.Now()
	deadline := start.Add(time.Duration(sub.TimeoutSeconds) * time.Second)
	res := a.Subscriptions.Attempt(ctx, dispatch.Delivery{RunID: run.ID, Sub: sub, Event: payload, Blocking: true})
	if !res.OK && time.Until(deadline) > time.Duration(sub.TimeoutSeconds)*time.Second/2 {
		_ = a.Store.RecordAttempt(ctx, run.ID, dispatch.AttemptRow(start, time.Now(), res),
			store.RunUpdate{Status: "delivering", Unclaimed: true})
		start = time.Now()
		res = a.Subscriptions.Attempt(ctx, dispatch.Delivery{RunID: run.ID, Sub: sub, Event: payload, Blocking: true})
	}
	if res.OK && len(res.Properties) > 0 {
		if problem := checkProperties(sub.Topic, res.Properties); problem != "" {
			res.OK, res.Err = false, problem
		}
	}

	u := store.RunUpdate{Unclaimed: true}
	switch {
	case !res.OK && sub.OnFailure == "continue_request":
		u.Status, u.Reason = "failed", &res.Err
		detail := "Failed (" + res.Err + "); its setting lets the request go on."
		a.childDetail(ctx, in, out.StepKey, "skipped", detail)
	case !res.OK:
		u.Status, u.Reason = "failed", &res.Err
		out.Stop, out.Reason = true, sub.Name+" couldn't check the request: "+res.Err
		a.child(ctx, in, out.StepKey, "", "failed", &out.Reason)
	case res.Outcome == "stop":
		u.Status, u.Outcome, u.Reason = "blocked", &res.Outcome, &res.Reason
		out.Stop, out.Reason = true, sub.Name+" stopped the request: "+res.Reason
		a.child(ctx, in, out.StepKey, "", "failed", &out.Reason)
	default:
		cont := "continue"
		u.Status, u.Outcome = "delivered", &cont
		detail := "Went on."
		if len(res.Properties) > 0 {
			b, _ := json.Marshal(res.Properties)
			u.Properties = b
			if err := a.Store.SetDeploymentProperties(ctx, a.Store.Pool, requestID, res.Properties); err != nil {
				return out, err
			}
			detail = "Went on, setting " + propertyList(res.Properties) + "."
		}
		a.childDetail(ctx, in, out.StepKey, "done", detail)
	}
	if err := a.Store.RecordAttempt(ctx, run.ID, dispatch.AttemptRow(start, time.Now(), res), u); err != nil {
		return out, err
	}
	return out, nil
}

// FinishChecks closes the checks step once every subscription let the
// request go on.
func (a *Activities) FinishChecks(ctx context.Context, requestID, projectID string) error {
	st, err := a.Store.UpdateStep(ctx, a.Store.Pool, requestID, KeyChecks, store.StepUpdate{Status: "done"})
	if err != nil {
		return err
	}
	a.Events.Step(ctx, projectID, requestID, st)
	return nil
}

// withProperties gives a check the deployment properties earlier checks
// set (ADR-0021: later subscriptions see them).
func (a *Activities) withProperties(ctx context.Context, requestID string, payload json.RawMessage) (json.RawMessage, error) {
	props, err := a.Store.RequestProperties(ctx, requestID)
	if err != nil || len(props) == 0 {
		return payload, err
	}
	var ev map[string]any
	if err := json.Unmarshal(payload, &ev); err != nil {
		return payload, nil
	}
	data, _ := ev["data"].(map[string]any)
	if data == nil {
		return payload, nil
	}
	dep, _ := data["deployment"].(map[string]any)
	if dep == nil {
		dep = map[string]any{}
		data["deployment"] = dep
	}
	merged, _ := dep["properties"].(map[string]any)
	if merged == nil {
		merged = map[string]any{}
	}
	for k, v := range props {
		merged[k] = v
	}
	dep["properties"] = merged
	return json.Marshal(ev)
}

func (a *Activities) eventPayload(ctx context.Context, eventID string) (string, json.RawMessage, error) {
	var topic string
	var payload json.RawMessage
	err := a.Store.Pool.QueryRow(ctx, `select topic, payload from integration_events where id = $1`, eventID).Scan(&topic, &payload)
	return topic, payload, err
}

// childDetail sets a check's status and what it did.
func (a *Activities) childDetail(ctx context.Context, in JobInput, key, status, detail string) {
	a.child(ctx, in, key, "", status, nil)
	st, err := a.Store.UpdateStep(ctx, a.Store.Pool, in.RequestID, key, store.StepUpdate{Detail: &detail})
	if err == nil {
		a.Events.Step(ctx, in.ProjectID, in.RequestID, st)
	}
}

var propertyKey = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)

// checkProperties says what is wrong with properties a subscription sets.
func checkProperties(topic string, props map[string]string) string {
	if topic != outbox.ProvisioningPre {
		return "Only provisioning.pre subscriptions may set properties."
	}
	if len(props) > 50 {
		return "At most 50 properties."
	}
	for k, v := range props {
		if !propertyKey.MatchString(k) {
			return fmt.Sprintf("The property %q is not lower-case letters, digits and underscores.", k)
		}
		if len(v) > 1000 {
			return fmt.Sprintf("The property %s is longer than 1000 characters.", k)
		}
	}
	return ""
}

func propertyList(props map[string]string) string {
	keys := make([]string, 0, len(props))
	for k := range props {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}
