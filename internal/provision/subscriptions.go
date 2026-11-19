package provision

import (
	"cmp"
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
	res, err := a.ask(ctx, asking{sub: sub, eventID: b.EventID, payload: payload, in: in, key: out.StepKey,
		accept: func(res dispatch.Result) (taken, error) {
			if len(res.Machine) > 0 {
				return taken{problem: "Only compute.allocation.pre and network.configure subscriptions may change a VM."}, nil
			}
			if len(res.Properties) == 0 {
				return taken{}, nil
			}
			if problem := checkProperties(sub.Topic, res.Properties); problem != "" {
				return taken{problem: problem}, nil
			}
			if err := a.Store.SetDeploymentProperties(ctx, a.Store.Pool, requestID, res.Properties); err != nil {
				return taken{}, err
			}
			props, _ := json.Marshal(res.Properties)
			return taken{detail: "Went on, setting " + propertyList(res.Properties) + ".", properties: props}, nil
		}})
	out.Stop, out.Reason = res.stop, res.reason
	return out, err
}

// asking is a blocking subscription asked about an event, under its
// check's step, key, in the step in names.
type asking struct {
	sub     store.Subscription
	eventID string
	payload json.RawMessage
	in      JobInput
	key     string
	// accept takes an answer letting the request go on and does what it
	// asks: a problem says why rvav refuses it, an error that rvav couldn't
	// tell.
	accept func(dispatch.Result) (taken, error)
}

// taken is what an answer rvav took did, for the step and the run.
type taken struct {
	detail, problem     string
	properties, changes json.RawMessage
	// refused: the problem is a change rvav refused, not a malformed answer.
	refused bool
}

// asked is what asking came to.
type asked struct {
	stop   bool
	reason string
}

// ask delivers the event to a blocking subscription, once more within its
// timeout when the first try fails fast, and records its run: a request
// retried keeps one run per event. A failure, a timeout or an answer rvav
// refuses tells its recovery target, and its answer or the subscription's
// on_failure decides (ADR-0054).
func (a *Activities) ask(ctx context.Context, q asking) (asked, error) {
	requestID := q.in.RequestID
	step, _ := a.Store.GetStepByKey(ctx, requestID, q.key)
	runID, err := a.Store.StartBlockingRun(ctx, store.SubscriptionRun{ID: ids.New(ids.Run), SubscriptionID: q.sub.ID,
		EventID: q.eventID, RequestID: &requestID, StepID: &step.ID})
	if err != nil {
		return asked{}, err
	}
	start, res := a.deliverBlocking(ctx, dispatch.Delivery{RunID: runID, Sub: q.sub, Event: q.payload, Blocking: true, StepKey: q.key})
	var took taken
	if res.OK && res.Outcome != "stop" {
		if took, err = q.accept(res); err != nil {
			return asked{}, err
		}
		if took.problem != "" {
			res.OK, res.Err = false, took.problem
		}
	}
	u := store.RunUpdate{Unclaimed: true}
	var out asked
	switch {
	case !res.OK:
		u.Status, u.Reason = "failed", &res.Err
		if err := a.Store.RecordAttempt(ctx, runID, dispatch.AttemptRow(start, time.Now(), res), u); err != nil {
			return out, err
		}
		reason := q.sub.Name + " couldn't check the request: " + res.Err
		if took.refused {
			reason = q.sub.Name + " asked for a change rvav refuses: " + res.Err
		}
		return a.failed(ctx, q, runID, res.Err, reason)
	case res.Outcome == "stop":
		u.Status, u.Outcome, u.Reason = "blocked", &res.Outcome, &res.Reason
		out.stop, out.reason = true, q.sub.Name+" stopped the request: "+res.Reason
		a.child(ctx, q.in, q.key, "", "failed", &out.reason)
	default:
		cont := "continue"
		u.Status, u.Outcome, u.Properties, u.Changes = "delivered", &cont, took.properties, took.changes
		a.childDetail(ctx, q.in, q.key, "done", cmp.Or(took.detail, "Went on."))
	}
	return out, a.Store.RecordAttempt(ctx, runID, dispatch.AttemptRow(start, time.Now(), res), u)
}

// failed settles a blocking subscription that failed: its recovery
// target's outcome, when it gives one, then its on_failure.
func (a *Activities) failed(ctx context.Context, q asking, runID, why, reason string) (asked, error) {
	var rec *dispatch.Result
	if len(q.sub.Recovery) > 0 {
		r, err := a.recover(ctx, q, runID, why)
		if err != nil {
			return asked{}, err
		}
		if r.OK && r.Decided {
			rec = &r
		}
	}
	switch {
	case rec != nil && rec.Outcome == "stop":
		out := asked{stop: true, reason: reason + " Its recovery stopped the request: " + rec.Reason}
		a.child(ctx, q.in, q.key, "", "failed", &out.reason)
		return out, nil
	case rec != nil:
		a.childDetail(ctx, q.in, q.key, "skipped", "Failed ("+why+"); its recovery let the request go on.")
	case q.sub.OnFailure == "continue_request":
		a.childDetail(ctx, q.in, q.key, "skipped", "Failed ("+why+"); its setting lets the request go on.")
	default:
		a.child(ctx, q.in, q.key, "", "failed", &reason)
		return asked{stop: true, reason: reason}, nil
	}
	return asked{}, nil
}

// recover tells a failed subscription's recovery target, once, with the
// event and why it failed (vRA's recover workflow, ADR-0054), as a run and
// a step of its own under the subscription's.
func (a *Activities) recover(ctx context.Context, q asking, runID, why string) (dispatch.Result, error) {
	requestID := q.in.RequestID
	in := JobInput{RequestID: requestID, ProjectID: q.in.ProjectID, StepKey: q.key}
	key := q.key + "/recovery"
	a.child(ctx, in, key, "Recovery", "running", nil)
	step, _ := a.Store.GetStepByKey(ctx, requestID, key)
	id, err := a.Store.StartBlockingRun(ctx, store.SubscriptionRun{ID: ids.New(ids.Run), SubscriptionID: q.sub.ID,
		EventID: q.eventID, Recovery: true, RequestID: &requestID, StepID: &step.ID})
	if err != nil {
		return dispatch.Result{}, err
	}
	sub := q.sub
	sub.Target = q.sub.Recovery
	start, res := a.deliverBlocking(ctx, dispatch.Delivery{RunID: id, Sub: sub, Event: withRecovery(q.payload, why, runID),
		Blocking: true, Recovery: true, StepKey: key})
	u := store.RunUpdate{Unclaimed: true}
	switch {
	case !res.OK:
		u.Status, u.Reason = "failed", &res.Err
		a.childDetail(ctx, in, key, "failed", "Failed too: "+res.Err+".")
	case res.Outcome == "stop":
		u.Status, u.Outcome, u.Reason = "blocked", &res.Outcome, &res.Reason
		a.childDetail(ctx, in, key, "done", "Stopped the request: "+res.Reason)
	default:
		cont := "continue"
		u.Status, u.Outcome = "delivered", &cont
		detail := "Let the request go on."
		if !res.Decided {
			detail = "Answered without an outcome: the subscription's setting decides."
		}
		a.childDetail(ctx, in, key, "done", detail)
	}
	return res, a.Store.RecordAttempt(ctx, id, dispatch.AttemptRow(start, time.Now(), res), u)
}

// withRecovery is the event a recovery target gets: the subscription's,
// with why it failed and its run.
func withRecovery(payload json.RawMessage, why, runID string) json.RawMessage {
	var ev map[string]any
	if json.Unmarshal(payload, &ev) != nil {
		return payload
	}
	ev["recovery"] = map[string]any{"reason": why, "run_id": runID}
	b, err := json.Marshal(ev)
	if err != nil {
		return payload
	}
	return b
}

// deliverBlocking delivers to a blocking target, once more within its
// timeout when the first try fails fast; it returns when the last try
// started and how it went.
func (a *Activities) deliverBlocking(ctx context.Context, d dispatch.Delivery) (time.Time, dispatch.Result) {
	start := time.Now()
	timeout := time.Duration(d.Sub.TimeoutSeconds) * time.Second
	deadline := start.Add(timeout)
	res := a.Subscriptions.Attempt(ctx, d)
	if !res.OK && time.Until(deadline) > timeout/2 {
		_ = a.Store.RecordAttempt(ctx, d.RunID, dispatch.AttemptRow(start, time.Now(), res),
			store.RunUpdate{Status: "delivering", Unclaimed: true})
		start = time.Now()
		res = a.Subscriptions.Attempt(ctx, d)
	}
	return start, res
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
