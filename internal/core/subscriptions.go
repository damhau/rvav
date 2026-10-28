package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/dispatch"
	"github.com/damhau/rvav/internal/hosts"
	"github.com/damhau/rvav/internal/ids"
	"github.com/damhau/rvav/internal/outbox"
	"github.com/damhau/rvav/internal/policy"
	"github.com/damhau/rvav/internal/store"
)

// Event subscriptions (ADR-0021): administrators manage them, auditors
// read them.

var topicDescriptions = map[string]string{
	outbox.RequestSubmitted: "A request, catalog or day-2, is accepted, before any approval.",
	outbox.ProvisioningPre:  "A catalog request is approved and about to build.",
	outbox.ProvisioningPost: "A catalog request ended: succeeded, failed or cancelled.",
	outbox.Day2Pre:          "A day-2 request is about to run (not a deletion).",
	outbox.Day2Post:         "A day-2 request ended.",
	outbox.DeletePre:        "A deployment is about to be deleted, by someone or at the end of its lease.",
	outbox.DeletePost:       "The deletion ended.",
	outbox.LeaseExpiring:    "The reminder before a lease ends.",
	outbox.LeaseExpired:     "A lease ended and rvav acts on it.",
}

func asEvent(ev outbox.Event) api.IntegrationEvent {
	return api.IntegrationEvent{Id: ev.ID, Topic: api.EventTopicKey(ev.Topic), Contract: ev.Contract, OccurredAt: ev.OccurredAt, Data: ev.Data}
}

// EventTopics lists the topics with a sample event of each.
func (s *Service) EventTopics(ctx context.Context, p *auth.Principal) ([]api.EventTopic, error) {
	if !p.SeesEverything() {
		return nil, ErrForbidden
	}
	out := make([]api.EventTopic, len(outbox.Topics))
	for i, t := range outbox.Topics {
		out[i] = api.EventTopic{Key: api.EventTopicKey(t), Description: topicDescriptions[t], MayBlock: outbox.MayBlock(t),
			Sample: asEvent(outbox.Sample(t))}
	}
	return out, nil
}

func presentSubscription(sub store.Subscription, counts map[string]int, last time.Time) api.Subscription {
	out := api.Subscription{Id: sub.ID, Name: sub.Name, Topic: api.EventTopicKey(sub.Topic), Condition: sub.Condition,
		Blocking: sub.Blocking, OnFailure: api.SubscriptionOnFailure(sub.OnFailure), Priority: sub.Priority,
		TimeoutSeconds: sub.TimeoutSeconds, Enabled: sub.Enabled, CreatedAt: sub.CreatedAt, UpdatedAt: &sub.UpdatedAt}
	if sub.Description != "" {
		out.Description = &sub.Description
	}
	_ = json.Unmarshal(sub.Target, &out.Target)
	out.Runs.Delivered, out.Runs.Failed = counts["delivered"], counts["failed"]+counts["blocked"]
	out.Runs.Pending = counts["queued"] + counts["delivering"]
	if !last.IsZero() {
		out.LastRunAt = &last
	}
	return out
}

// Subscriptions lists them by topic and priority, with their recent runs.
func (s *Service) Subscriptions(ctx context.Context, p *auth.Principal) ([]api.Subscription, error) {
	if !p.SeesEverything() {
		return nil, ErrForbidden
	}
	subs, err := s.Store.Subscriptions(ctx)
	if err != nil {
		return nil, err
	}
	counts, last, err := s.Store.SubscriptionRunCounts(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]api.Subscription, len(subs))
	for i, sub := range subs {
		out[i] = presentSubscription(sub, counts[sub.ID], last[sub.ID])
	}
	return out, nil
}

func (s *Service) GetSubscription(ctx context.Context, p *auth.Principal, id string) (api.Subscription, error) {
	if !p.SeesEverything() {
		return api.Subscription{}, ErrForbidden
	}
	sub, err := s.Store.GetSubscription(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return api.Subscription{}, ErrNotFound
	} else if err != nil {
		return api.Subscription{}, err
	}
	counts, last, err := s.Store.SubscriptionRunCounts(ctx)
	if err != nil {
		return api.Subscription{}, err
	}
	return presentSubscription(sub, counts[sub.ID], last[sub.ID]), nil
}

// checkEventCondition compiles a condition and evaluates it on the topic's
// sample event: it must give true or false. A missing input is fine (forms
// differ); a misspelt field of the event is not.
func checkEventCondition(topic, expr string) error {
	if err := policy.CheckEvent(expr); err != nil {
		return err
	}
	sample := outbox.Sample(topic)
	_, err := policy.EventBool(expr, policy.Vars(outbox.Vars(sample.Data)))
	if err == nil {
		return nil
	}
	if _, key, ok := strings.Cut(err.Error(), "no such key: "); ok {
		key = strings.TrimSpace(key)
		if strings.Contains(expr, "inputs."+key) || strings.Contains(expr, `inputs["`+key+`"]`) {
			return nil
		}
		return fmt.Errorf("the event has no %s", key)
	}
	return err
}

// SaveSubscription creates (id empty) or replaces a subscription.
func (s *Service) SaveSubscription(ctx context.Context, p *auth.Principal, id string, w api.SubscriptionWrite) (api.Subscription, error) {
	if !p.IsAdmin() {
		return api.Subscription{}, ErrForbidden
	}
	action := "subscription.create"
	if id != "" {
		if _, err := s.Store.GetSubscription(ctx, id); errors.Is(err, store.ErrNotFound) {
			return api.Subscription{}, ErrNotFound
		} else if err != nil {
			return api.Subscription{}, err
		}
		action = "subscription.update"
	} else {
		id = ids.New(ids.Subscription)
	}
	sub := store.Subscription{ID: id, Name: strings.TrimSpace(w.Name), Description: deref(w.Description), Topic: string(w.Topic),
		Condition: strings.TrimSpace(deref(w.Condition)), Blocking: w.Blocking != nil && *w.Blocking, OnFailure: "stop_request",
		Priority: 100, TimeoutSeconds: 30, Enabled: w.Enabled == nil || *w.Enabled}
	if w.OnFailure != nil {
		sub.OnFailure = string(*w.OnFailure)
	}
	if w.Priority != nil {
		sub.Priority = *w.Priority
	}
	if w.TimeoutSeconds != nil {
		sub.TimeoutSeconds = *w.TimeoutSeconds
	}
	var errs []api.FieldError
	if sub.Name == "" {
		errs = append(errs, fieldErr("/name", "required", "Give the subscription a name."))
	}
	topicOK := slices.Contains(outbox.Topics, sub.Topic)
	if !topicOK {
		errs = append(errs, fieldErr("/topic", "enum", "One of "+strings.Join(outbox.Topics, ", ")+"."))
	}
	if sub.Blocking && topicOK && !outbox.MayBlock(sub.Topic) {
		errs = append(errs, fieldErr("/blocking", "not_pre", "Only provisioning.pre, day2.pre and delete.pre subscriptions may block."))
	}
	if sub.Condition != "" && topicOK {
		if err := checkEventCondition(sub.Topic, sub.Condition); err != nil {
			errs = append(errs, fieldErr("/condition", "invalid", "This condition is not valid CEL: "+err.Error()))
		}
	}
	if sub.OnFailure != "stop_request" && sub.OnFailure != "continue_request" {
		errs = append(errs, fieldErr("/on_failure", "enum", "stop_request or continue_request."))
	}
	if sub.TimeoutSeconds < 1 || sub.TimeoutSeconds > 3600 {
		errs = append(errs, fieldErr("/timeout_seconds", "range", "From 1 second to an hour."))
	}
	target, targetErrs, err := s.checkTarget(ctx, w.Target)
	if err != nil {
		return api.Subscription{}, err
	}
	if errs = append(errs, targetErrs...); len(errs) > 0 {
		return api.Subscription{}, &ValidationError{Errors: errs}
	}
	sub.Target = target
	if err := s.Store.SaveSubscription(ctx, sub); err != nil {
		return api.Subscription{}, err
	}
	s.audit(ctx, p, action, "subscription", id, nil, map[string]any{"name": sub.Name, "topic": sub.Topic,
		"blocking": sub.Blocking, "target": json.RawMessage(target)})
	return s.GetSubscription(ctx, p, id)
}

// checkTarget checks a target and returns it as stored.
func (s *Service) checkTarget(ctx context.Context, t api.SubscriptionTarget) (json.RawMessage, []api.FieldError, error) {
	var errs []api.FieldError
	n := 0
	for _, set := range []bool{t.Webhook != nil, t.Awx != nil, t.Workflow != nil} {
		if set {
			n++
		}
	}
	if n != 1 {
		return nil, []api.FieldError{fieldErr("/target", "one_of", "Give one target: a webhook, an AWX template or a custom workflow.")}, nil
	}
	switch {
	case t.Webhook != nil:
		u, err := url.Parse(t.Webhook.Url)
		switch {
		case err != nil || u.Host == "":
			errs = append(errs, fieldErr("/target/webhook/url", "invalid", "A URL such as https://cmdb.corp.example/rvav."))
		case !hosts.Allowed(s.Config.WebhookHosts, u):
			errs = append(errs, fieldErr("/target/webhook/url", "host", u.Host+" is not in RVAV_WEBHOOK_HOSTS."))
		}
		if _, err := s.Store.GetSecret(ctx, t.Webhook.Secret); errors.Is(err, store.ErrNotFound) {
			errs = append(errs, fieldErr("/target/webhook/secret", "unknown", "There is no stored secret "+t.Webhook.Secret+"."))
		} else if err != nil {
			return nil, nil, err
		}
	case t.Awx != nil:
		name, templateID := strings.TrimSpace(deref(t.Awx.JobTemplate)), 0
		if t.Awx.TemplateId != nil {
			templateID = *t.Awx.TemplateId
		}
		if templateID <= 0 && name == "" {
			errs = append(errs, fieldErr("/target/awx", "required", "Name the job template, or give its AWX ID."))
		} else if templateID <= 0 && s.AWX != nil {
			// Kept by ID, so renaming it in AWX doesn't break the subscription.
			id, err := s.AWX.LookupJobTemplate(ctx, name, false)
			if err != nil {
				errs = append(errs, fieldErr("/target/awx/job_template", "unknown", "AWX has no job template "+name+"."))
			} else {
				t.Awx.TemplateId = &id
			}
		}
	case t.Workflow != nil:
		errs = append(errs, s.checkWorkflowTarget(ctx, t.Workflow.Key)...)
	}
	b, err := json.Marshal(t)
	return b, errs, err
}

func (s *Service) DeleteSubscription(ctx context.Context, p *auth.Principal, id string) error {
	if !p.IsAdmin() {
		return ErrForbidden
	}
	sub, err := s.Store.GetSubscription(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if err := s.Store.DeleteSubscription(ctx, id); err != nil {
		return err
	}
	s.audit(ctx, p, "subscription.delete", "subscription", id, nil, map[string]any{"name": sub.Name, "topic": sub.Topic})
	return nil
}

func presentRun(r store.SubscriptionRun) api.SubscriptionRun {
	out := api.SubscriptionRun{Id: r.ID, SubscriptionId: r.SubscriptionID, EventId: r.EventID, Topic: api.EventTopicKey(r.Topic),
		Status: api.SubscriptionRunStatus(r.Status), Blocking: r.Blocking, Test: r.Test, Attempts: r.Attempts,
		Outcome: r.Outcome, Reason: r.Reason, CreatedAt: r.CreatedAt, FinishedAt: r.FinishedAt}
	if r.Status == "queued" {
		out.NextAttemptAt = &r.NextAttemptAt
	}
	if r.RequestID != nil && r.RequestNumber != nil {
		out.Request = &struct {
			Id     string `json:"id"`
			Number string `json:"number"`
		}{Id: *r.RequestID, Number: *r.RequestNumber}
	}
	return out
}

// SubscriptionRuns lists a subscription's runs, newest first.
func (s *Service) SubscriptionRuns(ctx context.Context, p *auth.Principal, id, status, cursor string, limit int) ([]api.SubscriptionRun, *string, error) {
	if !p.SeesEverything() {
		return nil, nil, ErrForbidden
	}
	if _, err := s.Store.GetSubscription(ctx, id); errors.Is(err, store.ErrNotFound) {
		return nil, nil, ErrNotFound
	} else if err != nil {
		return nil, nil, err
	}
	f := store.RunFilter{SubscriptionID: id, Status: status, Limit: limit}
	if cursor != "" {
		at, runID, ok := strings.Cut(cursor, "|")
		ts, err := strconv.ParseInt(at, 10, 64)
		if !ok || err != nil {
			return nil, nil, &BadRequestError{Msg: "That cursor is not one this API gave."}
		}
		f.Before = &struct {
			At time.Time
			ID string
		}{At: time.UnixMicro(ts), ID: runID}
	}
	runs, err := s.Store.SubscriptionRuns(ctx, f)
	if err != nil {
		return nil, nil, err
	}
	out := make([]api.SubscriptionRun, len(runs))
	for i, r := range runs {
		out[i] = presentRun(r)
	}
	var next *string
	if limit > 0 && len(runs) == limit {
		last := runs[len(runs)-1]
		c := strconv.FormatInt(last.CreatedAt.UnixMicro(), 10) + "|" + last.ID
		next = &c
	}
	return out, next, nil
}

// SubscriptionRun is one run with its payload and every attempt.
func (s *Service) SubscriptionRun(ctx context.Context, p *auth.Principal, id string) (api.SubscriptionRunDetail, error) {
	if !p.SeesEverything() {
		return api.SubscriptionRunDetail{}, ErrForbidden
	}
	run, payload, err := s.Store.GetSubscriptionRun(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return api.SubscriptionRunDetail{}, ErrNotFound
	} else if err != nil {
		return api.SubscriptionRunDetail{}, err
	}
	attempts, err := s.Store.RunAttempts(ctx, id)
	if err != nil {
		return api.SubscriptionRunDetail{}, err
	}
	base := presentRun(run)
	out := api.SubscriptionRunDetail{Id: base.Id, SubscriptionId: base.SubscriptionId, EventId: base.EventId, Topic: base.Topic,
		Status: base.Status, Blocking: base.Blocking, Test: base.Test, Attempts: base.Attempts, NextAttemptAt: base.NextAttemptAt,
		Outcome: base.Outcome, Reason: base.Reason, Request: base.Request, CreatedAt: base.CreatedAt, FinishedAt: base.FinishedAt,
		History: make([]api.SubscriptionRunAttempt, len(attempts))}
	_ = json.Unmarshal(payload, &out.Payload)
	if len(run.Properties) > 0 {
		var props map[string]string
		if json.Unmarshal(run.Properties, &props) == nil {
			out.Properties = &props
		}
	}
	for i, a := range attempts {
		out.History[i] = api.SubscriptionRunAttempt{At: a.At, Ok: a.OK, StatusCode: a.StatusCode, JobId: a.JobID,
			WorkflowId: a.WorkflowID, Response: a.Response, Error: a.Error, DurationMs: a.DurationMS}
	}
	return out, nil
}

// RetrySubscriptionRun queues a failed run again.
func (s *Service) RetrySubscriptionRun(ctx context.Context, p *auth.Principal, id string) (api.SubscriptionRun, error) {
	if !p.IsAdmin() {
		return api.SubscriptionRun{}, ErrForbidden
	}
	run, _, err := s.Store.GetSubscriptionRun(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return api.SubscriptionRun{}, ErrNotFound
	} else if err != nil {
		return api.SubscriptionRun{}, err
	}
	ok, err := s.Store.RetryRun(ctx, id)
	if err != nil {
		return api.SubscriptionRun{}, err
	}
	if !ok {
		why := "Only failed runs are retried."
		if run.Blocking {
			why = "A blocking run belonged to its request: retry the request."
		}
		return api.SubscriptionRun{}, &ConflictError{Msg: why}
	}
	s.audit(ctx, p, "subscription.retry", "subscription", run.SubscriptionID, nil, map[string]any{"run": id})
	run, _, err = s.Store.GetSubscriptionRun(ctx, id)
	return presentRun(run), err
}

// TestSubscription delivers the topic's sample event once, now, and keeps
// it as a test run.
func (s *Service) TestSubscription(ctx context.Context, p *auth.Principal, id string) (api.SubscriptionRunDetail, error) {
	if !p.IsAdmin() {
		return api.SubscriptionRunDetail{}, ErrForbidden
	}
	sub, err := s.Store.GetSubscription(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return api.SubscriptionRunDetail{}, ErrNotFound
	} else if err != nil {
		return api.SubscriptionRunDetail{}, err
	}
	if s.Dispatcher == nil {
		return api.SubscriptionRunDetail{}, &ConflictError{Msg: "This rvav can't deliver events."}
	}
	ev := outbox.Sample(sub.Topic)
	ev.ID, ev.OccurredAt = ids.New(ids.Event), time.Now().UTC()
	payload, _ := json.Marshal(ev)
	if err := s.Store.InsertTestEvent(ctx, ev.ID, sub.Topic, payload); err != nil {
		return api.SubscriptionRunDetail{}, err
	}
	run := store.SubscriptionRun{ID: ids.New(ids.Run), SubscriptionID: sub.ID, EventID: ev.ID, Status: "delivering",
		Blocking: sub.Blocking, Test: true}
	if err := s.Store.InsertRun(ctx, s.Store.Pool, run); err != nil {
		return api.SubscriptionRunDetail{}, err
	}
	start := time.Now()
	res := s.Dispatcher.Attempt(ctx, dispatch.Delivery{Sub: sub, Event: payload, Blocking: sub.Blocking})
	u := store.RunUpdate{Status: "delivered"}
	if !res.OK {
		u.Status, u.Reason = "failed", &res.Err
	} else if res.Outcome != "" {
		u.Outcome = &res.Outcome
		if res.Reason != "" {
			u.Reason = &res.Reason
		}
		if len(res.Properties) > 0 {
			u.Properties, _ = json.Marshal(res.Properties)
		}
	}
	if err := s.Store.RecordAttempt(ctx, run.ID, dispatch.AttemptRow(start, time.Now(), res), u); err != nil {
		return api.SubscriptionRunDetail{}, err
	}
	s.audit(ctx, p, "subscription.test", "subscription", sub.ID, nil, map[string]any{"ok": res.OK})
	return s.SubscriptionRun(ctx, p, run.ID)
}

// checkWorkflowTarget checks a custom workflow named as a subscription's
// target (ADR-0022).
func (s *Service) checkWorkflowTarget(_ context.Context, key string) []api.FieldError {
	return []api.FieldError{fieldErr("/target/workflow/key", "unknown", "There is no custom workflow "+key+" registered.")}
}
