// Package auditlog sends audit events to syslog servers and webhooks
// (ADR-0058). Each destination's backlog is written with the event, in the
// audited action's transaction; the forwarder sends it in order, at least
// once, and tries again with back-off until it goes.
package auditlog

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/dispatch"
	"github.com/damhau/rvav/internal/secrets"
	"github.com/damhau/rvav/internal/store"
	"github.com/damhau/rvav/internal/trust"
)

// Contract names the webhook body's shape.
const Contract = "rvav.audit.v1"

// Event is an audit event as a destination gets it.
type Event struct {
	ID         string       `json:"id"`
	At         time.Time    `json:"at"`
	Category   string       `json:"category"`
	Action     string       `json:"action"`
	Actor      *api.UserRef `json:"actor"`
	TargetType string       `json:"target_type"`
	TargetID   string       `json:"target_id"`
	// TargetName is what the target was called when the event was recorded;
	// left out when rvav has no name for it.
	TargetName string          `json:"target_name,omitempty"`
	Project    *api.ProjectRef `json:"project"`
	Detail     json.RawMessage `json:"detail"`
}

// FromRow is the event of an audit log row.
func FromRow(r store.AuditRow) Event {
	e := Event{ID: strconv.FormatInt(r.ID, 10), At: r.At.UTC(), Category: store.AuditCategory(r.Action), Action: r.Action,
		TargetType: r.TargetType, TargetID: r.TargetID, Detail: r.Detail}
	if r.TargetName != nil {
		e.TargetName = *r.TargetName
	}
	if len(e.Detail) == 0 {
		e.Detail = json.RawMessage(`{}`)
	}
	if r.ActorID != nil {
		e.Actor = &api.UserRef{Id: *r.ActorID}
		if r.ActorName != nil {
			e.Actor.Name = *r.ActorName
		}
	}
	if r.ProjectID != nil {
		e.Project = &api.ProjectRef{Id: *r.ProjectID}
		if r.ProjectCode != nil {
			e.Project.Code = *r.ProjectCode
		}
		if r.ProjectName != nil {
			e.Project.Name = *r.ProjectName
		}
	}
	return e
}

// Forwarder sends the destinations' backlogs. Every worker replica runs
// one: a destination is leased to one of them at a time.
type Forwarder struct {
	Store *store.Store
	Log   *slog.Logger
	// Secret opens the stored secrets that sign webhooks and hold client keys.
	Secret dispatch.SecretFunc
	// Box opens the webhooks' authentication header values.
	Box *secrets.Box
	// WebhookHosts are the hosts webhooks may call, as saved now.
	WebhookHosts func(context.Context) []string
	// Hostname is syslog's HOSTNAME field.
	Hostname string
	// HTTP posts to webhooks; one that follows no redirect when nil.
	HTTP *http.Client
	// Interval between rounds; 2 s when zero.
	Interval time.Duration
	// Now is the clock; time.Now when nil.
	Now func() time.Time
	// Timeout bounds a connection and each write; 10 s when zero.
	Timeout time.Duration

	inFlight sync.WaitGroup
}

// Batch is how many events go in one webhook call, or one write to syslog.
const Batch = 100

// lease is how long a destination is one worker's; a round stops sending
// well before it ends.
const lease = 2 * time.Minute

// settle is how long a syslog server has to close a connection after a
// batch was written to it, before the batch counts as received.
const settle = 250 * time.Millisecond

// reconnects is how many times a round opens a syslog connection again
// after the server closed it, before it counts as a failure.
const reconnects = 3

var backoff = []time.Duration{5 * time.Second, 30 * time.Second, time.Minute, 5 * time.Minute}

// Backoff is the wait after the n-th failure in a row.
func Backoff(n int) time.Duration {
	if n < 1 {
		n = 1
	}
	return backoff[min(n, len(backoff))-1]
}

func (f *Forwarder) now() time.Time {
	if f.Now != nil {
		return f.Now()
	}
	return time.Now()
}

func (f *Forwarder) timeout() time.Duration {
	if f.Timeout > 0 {
		return f.Timeout
	}
	return 10 * time.Second
}

// Run sends until the context ends.
func (f *Forwarder) Run(ctx context.Context) {
	interval := f.Interval
	if interval == 0 {
		interval = 2 * time.Second
	}
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		if err := f.round(ctx); err != nil && ctx.Err() == nil {
			f.Log.Warn("audit forwarding", "err", err)
		}
		select {
		case <-ctx.Done():
			f.inFlight.Wait()
			return
		case <-tick.C:
		}
	}
}

// Once sends what is due now and waits until it is sent or has failed.
func (f *Forwarder) Once(ctx context.Context) error {
	err := f.round(ctx)
	f.inFlight.Wait()
	return err
}

// round sweeps what waited for destinations removed, leases the due ones
// and sends to each in the background: a destination that doesn't answer
// holds back none of the others.
func (f *Forwarder) round(ctx context.Context) error {
	if _, err := f.Store.SweepAuditDeliveries(ctx); err != nil && ctx.Err() == nil {
		f.Log.Warn("audit forwarding: sweep the destinations removed", "err", err)
	}
	due, err := f.Store.ClaimAuditDestinations(ctx, f.now(), lease)
	if err != nil {
		return err
	}
	for _, d := range due {
		f.inFlight.Add(1)
		go func() {
			defer f.inFlight.Done()
			f.drain(ctx, d)
		}()
	}
	return nil
}

// drain sends a destination's backlog, a batch at a time, while its lease
// lasts and until the forwarder stops: a batch begun is sent, and what
// came of it recorded.
func (f *Forwarder) drain(parent context.Context, d store.AuditDestination) {
	ctx := context.WithoutCancel(parent)
	defer func() {
		if d.LeaseUntil == nil {
			return
		}
		if err := f.Store.ReleaseAuditDestination(ctx, d.ID, *d.LeaseUntil); err != nil {
			f.Log.Warn("audit forwarding: release", "destination", d.Name, "err", err)
		}
	}()
	until := f.now().Add(lease / 2)
	var s sender
	defer func() {
		if s != nil {
			_ = s.close()
		}
	}()
	// A syslog server answers nothing: a batch written to it counts as
	// received once the connection is seen still open settle after it was
	// written. One that stops reading and closes a moment later, as one
	// killed while it shuts down does, never read the batches written in
	// between. Closed, what isn't counted goes again on a new connection.
	var unconfirmed []written
	var after int64
	opened := 0
	for {
		more := f.now().Before(until) && parent.Err() == nil
		var rows []store.AuditRow
		if more {
			var err error
			if rows, err = f.Store.AuditBacklog(ctx, d.ID, after, Batch); err != nil {
				f.Log.Warn("audit forwarding: backlog", "destination", d.Name, "err", err)
				return
			}
		}
		if len(unconfirmed) > 0 {
			wait := time.Duration(0)
			if len(rows) == 0 {
				wait = settle
			}
			if err := s.(prober).alive(wait); err != nil {
				_ = s.close()
				s, unconfirmed, after = nil, nil, 0
				if !more || opened > reconnects {
					f.failed(ctx, d, err)
					return
				}
				f.Log.Info("audit forwarding: the server closed the connection; what it may have missed goes again",
					"destination", d.Name, "err", err)
				continue
			}
			seen, n := time.Now(), 0
			var ids []int64
			for ; n < len(unconfirmed) && (wait > 0 || seen.Sub(unconfirmed[n].at) >= settle); n++ {
				ids = append(ids, unconfirmed[n].ids...)
			}
			if len(ids) > 0 && !f.delivered(ctx, d, ids) {
				return
			}
			unconfirmed = unconfirmed[n:]
		}
		if len(rows) == 0 {
			return
		}
		after = rows[len(rows)-1].ID
		events, sent := make([]Event, len(rows)), make([]int64, len(rows))
		for i, r := range rows {
			events[i], sent[i] = FromRow(r), r.ID
		}
		if s == nil {
			var err error
			if s, err = f.open(ctx, d); err != nil {
				f.failed(ctx, d, err)
				return
			}
			opened++
		}
		if events, sent = f.setAsideOversize(ctx, d, s, events, sent); len(events) == 0 {
			continue
		}
		if _, ok := s.(prober); ok {
			if _, err := s.send(ctx, events); err != nil {
				f.failed(ctx, d, err)
				return
			}
			unconfirmed = append(unconfirmed, written{ids: sent, at: time.Now()})
			continue
		}
		if !f.sendBatch(ctx, d, s, events, sent) {
			return
		}
	}
}

// written is a batch sent to a syslog server and when.
type written struct {
	ids []int64
	at  time.Time
}

// prober is a sender whose server answers nothing, syslog's.
type prober interface {
	// alive says whether the server still has the connection open,
	// waiting up to wait for it to close it.
	alive(wait time.Duration) error
}

func (f *Forwarder) delivered(ctx context.Context, d store.AuditDestination, ids []int64) bool {
	if err := f.Store.AuditDelivered(ctx, d.ID, ids, f.now()); err != nil {
		f.Log.Warn("audit forwarding: delivered", "destination", d.Name, "err", err)
		return false
	}
	return true
}

// setAsideOversize sets aside the events the sender would never send, and
// leaves the others.
func (f *Forwarder) setAsideOversize(ctx context.Context, d store.AuditDestination, s sender, events []Event, ids []int64) ([]Event, []int64) {
	keep, kept := events[:0:0], ids[:0:0]
	for i, e := range events {
		if why := s.oversize(e); why != "" {
			if !f.setAside(ctx, d, ids[i], why) {
				return keep, kept
			}
			continue
		}
		keep, kept = append(keep, e), append(kept, ids[i])
	}
	return keep, kept
}

// sendBatch sends events and records what came of them: delivered, or
// failed. Events the destination refuses as too large go again one by one,
// and one it refuses alone is set aside. False when sending stops.
func (f *Forwarder) sendBatch(ctx context.Context, d store.AuditDestination, s sender, events []Event, ids []int64) bool {
	_, err := s.send(ctx, events)
	var tooLarge *tooLargeError
	switch {
	case err == nil:
		return f.delivered(ctx, d, ids)
	case errors.As(err, &tooLarge) && len(events) == 1:
		return f.setAside(ctx, d, ids[0], tooLarge.Error())
	case errors.As(err, &tooLarge):
		for i := range events {
			if !f.sendBatch(ctx, d, s, events[i:i+1], ids[i:i+1]) {
				return false
			}
		}
	default:
		f.failed(ctx, d, err)
		return false
	}
	return true
}

func (f *Forwarder) setAside(ctx context.Context, d store.AuditDestination, id int64, why string) bool {
	f.Log.Warn("audit forwarding: event set aside", "destination", d.Name, "event", id, "reason", why)
	if err := f.Store.AuditSetAside(ctx, d.ID, id, truncate(why, 1000), f.now()); err != nil {
		f.Log.Warn("audit forwarding: set aside", "destination", d.Name, "err", err)
		return false
	}
	return true
}

// tooLargeError is a destination's refusal, for good, of what was sent.
type tooLargeError struct{ msg string }

func (e *tooLargeError) Error() string { return e.msg }

func (f *Forwarder) failed(ctx context.Context, d store.AuditDestination, err error) {
	now := f.now()
	next := now.Add(Backoff(d.Failures + 1))
	f.Log.Warn("audit forwarding failed", "destination", d.Name, "err", err, "next_attempt_at", next)
	if err := f.Store.AuditDeliveryFailed(ctx, d.ID, truncate(err.Error(), 1000), now, next); err != nil {
		f.Log.Warn("audit forwarding: record failure", "destination", d.Name, "err", err)
	}
}

// Send sends events to a destination once, now: a test's. It says what
// the destination answered; a syslog server, that it kept the connection
// open after.
func (f *Forwarder) Send(ctx context.Context, d store.AuditDestination, events []Event) (string, error) {
	s, err := f.open(ctx, d)
	if err != nil {
		return "", err
	}
	defer s.close()
	msg, err := s.send(ctx, events)
	if p, ok := s.(prober); ok && err == nil {
		err = p.alive(settle)
	}
	return msg, err
}

// sender is an open connection to a destination.
type sender interface {
	send(ctx context.Context, events []Event) (string, error)
	// oversize says why the sender would never send an event, or "".
	oversize(e Event) string
	close() error
}

func (f *Forwarder) open(ctx context.Context, d store.AuditDestination) (sender, error) {
	switch d.Kind {
	case "syslog":
		var cfg api.AuditSyslog
		if err := json.Unmarshal(d.Settings, &cfg); err != nil {
			return nil, err
		}
		return f.dialSyslog(ctx, cfg)
	case "webhook":
		var cfg api.AuditWebhook
		if err := json.Unmarshal(d.Settings, &cfg); err != nil {
			return nil, err
		}
		return f.webhook(ctx, d, cfg)
	}
	return nil, errors.New("not a syslog server or a webhook")
}

func (f *Forwarder) client() *http.Client {
	if f.HTTP != nil {
		return f.HTTP
	}
	// No redirects: an allowed host can't send the events elsewhere.
	return &http.Client{Transport: trust.Transport(trust.Options{}), Timeout: f.timeout(),
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

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
