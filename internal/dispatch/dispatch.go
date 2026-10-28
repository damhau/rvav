// Package dispatch delivers integration events (ADR-0021): it makes one run
// per event and enabled subscription whose condition is true, and delivers
// each at least once, with back-off, keeping every attempt.
package dispatch

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/damhau/rvav/internal/ids"
	"github.com/damhau/rvav/internal/outbox"
	"github.com/damhau/rvav/internal/policy"
	"github.com/damhau/rvav/internal/store"
)

// Result is what one attempt at a target gave.
type Result struct {
	OK         bool
	StatusCode int
	JobID      int
	WorkflowID string
	// Response is what came back, at most MaxResponse bytes.
	Response string
	Err      string
	// A blocking target's answer: continue or stop, why, and the
	// properties it sets on the deployment.
	Outcome    string
	Reason     string
	Properties map[string]string
}

// MaxResponse is how much of an answer a run keeps.
const MaxResponse = 4096

// maxAnswer is how much of a webhook's answer is read.
const maxAnswer = 256 << 10

// truncate keeps the first n bytes of s, cut on a rune boundary.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// Delivery is one attempt to make.
type Delivery struct {
	// RunID names the run: a custom workflow started for it is found again
	// on the next attempt.
	RunID    string
	Sub      store.Subscription
	Event    json.RawMessage
	Blocking bool
	// LastJobID is the AWX job an earlier attempt launched: it is followed
	// rather than launched again.
	LastJobID int
}

// Target delivers events to one kind of target.
type Target interface {
	Deliver(ctx context.Context, d Delivery) Result
}

// Targets are the kinds of target rvav has; a nil one is not available.
type Targets struct {
	Webhook  Target
	AWX      Target
	Workflow Target
}

// For picks the target a subscription names.
func (t Targets) For(sub store.Subscription) (Target, error) {
	var kinds map[string]json.RawMessage
	if err := json.Unmarshal(sub.Target, &kinds); err != nil {
		return nil, err
	}
	var tg Target
	switch {
	case kinds["webhook"] != nil:
		tg = t.Webhook
	case kinds["awx"] != nil:
		tg = t.AWX
	case kinds["workflow"] != nil:
		tg = t.Workflow
	}
	if tg == nil {
		return nil, errors.New("this kind of target is not available in this rvav")
	}
	return tg, nil
}

// Dispatcher fans events out to runs and delivers them.
type Dispatcher struct {
	Store   *store.Store
	Targets Targets
	Log     *slog.Logger
	// Interval between rounds; 2 s when zero.
	Interval time.Duration
	// Now is the clock; time.Now when nil.
	Now func() time.Time
	// Workers is how many attempts run at once; 8 when zero.
	Workers int

	init     sync.Once
	slots    chan struct{}
	inFlight sync.WaitGroup
}

// Back-off between attempts; the last repeats until a run is a day old.
var backoff = []time.Duration{10 * time.Second, 30 * time.Second, time.Minute, 5 * time.Minute, 15 * time.Minute}

// GiveUpAfter is how long a run is retried.
const GiveUpAfter = 24 * time.Hour

// Retention is how long finished runs are kept.
const Retention = 30 * 24 * time.Hour

func (d *Dispatcher) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

// Run dispatches until the context ends, pruning old runs every hour.
func (d *Dispatcher) Run(ctx context.Context) {
	interval := d.Interval
	if interval == 0 {
		interval = 2 * time.Second
	}
	tick, prune := time.NewTicker(interval), time.NewTicker(time.Hour)
	defer tick.Stop()
	defer prune.Stop()
	for {
		// Attempts go on in the background: a slow target holds one worker,
		// not the round.
		if _, err := d.round(ctx); err != nil && ctx.Err() == nil {
			d.Log.Warn("dispatch", "err", err)
		}
		select {
		case <-ctx.Done():
			d.inFlight.Wait()
			return
		case <-tick.C:
		case <-prune.C:
			if n, err := d.Store.PruneRuns(ctx, d.now().Add(-Retention)); err != nil {
				d.Log.Warn("prune subscription runs", "err", err)
			} else if n > 0 {
				d.Log.Info("pruned subscription runs", "runs", n)
			}
		}
	}
}

// Once makes the runs of new events, then delivers what is due, and waits
// for the attempts. It returns how many it started.
func (d *Dispatcher) Once(ctx context.Context) (int, error) {
	n, err := d.round(ctx)
	d.inFlight.Wait()
	return n, err
}

// round makes the runs of new events and starts attempts at what is due.
func (d *Dispatcher) round(ctx context.Context) (int, error) {
	for {
		n, err := d.fanOut(ctx)
		if err != nil {
			return 0, err
		}
		if n < 100 {
			break
		}
	}
	return d.deliver(ctx)
}

// fanOut makes a run per event and matching non-blocking subscription;
// blocking ones run in the request's workflow.
func (d *Dispatcher) fanOut(ctx context.Context) (int, error) {
	var n int
	err := d.Store.Tx(ctx, func(tx pgx.Tx) error {
		events, err := d.Store.UndispatchedEvents(ctx, tx, 100)
		if err != nil || len(events) == 0 {
			return err
		}
		n = len(events)
		subs, err := d.Store.ActiveSubscriptions(ctx, "", false)
		if err != nil {
			return err
		}
		done := make([]string, 0, len(events))
		for _, ev := range events {
			for _, sub := range subs {
				// A subscription gets the events that happened once it existed.
				if sub.Topic != ev.Topic || sub.CreatedAt.After(ev.CreatedAt) {
					continue
				}
				run := store.SubscriptionRun{ID: ids.New(ids.Run), SubscriptionID: sub.ID, EventID: ev.ID, Status: "queued",
					RequestID: ev.RequestID, NextAttemptAt: d.now()}
				match, err := Matches(sub.Condition, ev.Payload)
				switch {
				case err != nil:
					// Kept as a failed run, so administrators see why it didn't go.
					now := d.now()
					reason := "The condition can't be evaluated: " + err.Error()
					run.Status, run.Reason, run.FinishedAt = "failed", &reason, &now
				case !match:
					continue
				}
				if err := d.Store.InsertRun(ctx, tx, run); err != nil {
					return err
				}
			}
			done = append(done, ev.ID)
		}
		return d.Store.MarkDispatched(ctx, tx, done)
	})
	return n, err
}

// Matches says whether a subscription's condition is true for an event.
func Matches(condition string, payload json.RawMessage) (bool, error) {
	if condition == "" {
		return true, nil
	}
	var ev outbox.Event
	if err := json.Unmarshal(payload, &ev); err != nil {
		return false, err
	}
	return policy.EventBool(condition, policy.Vars(outbox.Vars(ev.Data)))
}

// deliver claims as many due runs as there are free workers and starts
// them: each starts as soon as it is claimed, within its lease.
func (d *Dispatcher) deliver(ctx context.Context) (int, error) {
	d.init.Do(func() {
		n := d.Workers
		if n == 0 {
			n = 8
		}
		d.slots = make(chan struct{}, n)
	})
	free := cap(d.slots) - len(d.slots)
	if free == 0 {
		return 0, nil
	}
	runs, err := d.Store.ClaimRuns(ctx, free, d.now())
	if err != nil {
		return 0, err
	}
	for _, r := range runs {
		d.slots <- struct{}{}
		d.inFlight.Add(1)
		go func() {
			defer d.inFlight.Done()
			defer func() { <-d.slots }()
			d.attemptRun(ctx, r)
		}()
	}
	return len(runs), nil
}

func (d *Dispatcher) attemptRun(ctx context.Context, r store.ClaimedRun) {
	start := d.now()
	res := d.Attempt(ctx, Delivery{RunID: r.ID, Sub: r.Subscription, Event: r.Payload, LastJobID: deref(r.LastJobID)})
	u := store.RunUpdate{Status: "delivered", Lease: r.LeaseUntil}
	if !res.OK {
		reason := res.Err
		u.Reason = &reason
		if d.now().Sub(r.CreatedAt) >= GiveUpAfter {
			u.Status = "failed"
		} else {
			next := d.now().Add(backoff[min(r.Attempts, len(backoff))-1])
			u.Status, u.NextAttempt = "queued", &next
		}
	}
	if err := d.Store.RecordAttempt(ctx, r.ID, AttemptRow(start, d.now(), res), u); err != nil {
		d.Log.Warn("record subscription attempt", "run", r.ID, "err", err)
	}
}

// Attempt delivers an event to a subscription's target once.
func (d *Dispatcher) Attempt(ctx context.Context, dl Delivery) Result {
	tg, err := d.Targets.For(dl.Sub)
	if err != nil {
		return Result{Err: err.Error()}
	}
	return tg.Deliver(ctx, dl)
}

func deref(n *int) int {
	if n == nil {
		return 0
	}
	return *n
}

// AttemptRow is how an attempt is kept.
func AttemptRow(start, end time.Time, res Result) store.Attempt {
	a := store.Attempt{At: start, OK: res.OK, DurationMS: int(end.Sub(start).Milliseconds())}
	if res.StatusCode != 0 {
		a.StatusCode = &res.StatusCode
	}
	if res.JobID != 0 {
		a.JobID = &res.JobID
	}
	if res.WorkflowID != "" {
		a.WorkflowID = &res.WorkflowID
	}
	if res.Response != "" {
		a.Response = &res.Response
	}
	if res.Err != "" {
		a.Error = &res.Err
	}
	return a
}
