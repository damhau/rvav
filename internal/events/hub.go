package events

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/damhau/rvav/internal/store"
)

// Hub listens for new outbox rows (Postgres LISTEN) and delivers them to
// subscribers. Every API process runs one hub.
type Hub struct {
	Store *store.Store
	Log   *slog.Logger

	mu   sync.Mutex
	subs map[*Subscription]struct{}
}

// Filter decides which events a subscriber receives.
type Filter struct {
	// Topics as sent by the client: requests, deployments, notifications,
	// approvals, request:<id>, deployment:<id>.
	Topics []string
	// ProjectID limits requests and deployments topics to one project.
	ProjectID string
	UserID    string
	// CanSeeProject reports whether the subscriber may see a project's events.
	CanSeeProject func(projectID string) bool
	// Requests were checked readable when subscribing to request:<id>: their
	// events pass even when the project is not visible (approvers).
	Requests map[string]bool
}

type Subscription struct {
	filter Filter
	C      chan store.Event
	// Lagged is closed when the subscriber fell behind and lost events.
	Lagged chan struct{}
	once   sync.Once
}

func NewHub(s *store.Store, log *slog.Logger) *Hub {
	return &Hub{Store: s, Log: log, subs: map[*Subscription]struct{}{}}
}

func (h *Hub) Subscribe(f Filter) *Subscription {
	s := &Subscription{filter: f, C: make(chan store.Event, 256), Lagged: make(chan struct{})}
	h.mu.Lock()
	h.subs[s] = struct{}{}
	h.mu.Unlock()
	return s
}

func (h *Hub) Unsubscribe(s *Subscription) {
	h.mu.Lock()
	delete(h.subs, s)
	h.mu.Unlock()
}

// Matches reports whether an event is for this filter.
func (f Filter) Matches(e store.Event) bool {
	has := func(t string) bool {
		for _, x := range e.Topics {
			if x == t {
				return true
			}
		}
		return false
	}
	if e.ProjectID != nil && f.CanSeeProject != nil && !f.CanSeeProject(*e.ProjectID) {
		allowed := false
		for _, t := range e.Topics {
			if id, ok := strings.CutPrefix(t, "request:"); ok && f.Requests[id] {
				allowed = true
			}
		}
		if !allowed {
			return false
		}
	}
	for _, t := range f.Topics {
		switch {
		case t == "notifications":
			if has("notifications") && has("user:"+f.UserID) {
				return true
			}
		case t == "approvals":
			if has("approvals") && has("user:"+f.UserID) {
				return true
			}
		case t == "requests" || t == "deployments":
			if has(t) && (f.ProjectID == "" || (e.ProjectID != nil && *e.ProjectID == f.ProjectID)) {
				return true
			}
		case strings.HasPrefix(t, "request:") || strings.HasPrefix(t, "deployment:"):
			if has(t) {
				return true
			}
		}
	}
	return false
}

func (h *Hub) deliver(e store.Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for s := range h.subs {
		if !s.filter.Matches(e) {
			continue
		}
		select {
		case s.C <- e:
		default:
			s.once.Do(func() { close(s.Lagged) })
		}
	}
}

// Run listens until ctx is done, reconnecting after connection errors.
func (h *Hub) Run(ctx context.Context) {
	for ctx.Err() == nil {
		if err := h.listen(ctx); err != nil && ctx.Err() == nil {
			h.Log.Warn("event listener stopped, reconnecting", "err", err)
			select {
			case <-ctx.Done():
			case <-time.After(2 * time.Second):
			}
		}
	}
}

func (h *Hub) listen(ctx context.Context) error {
	conn, err := h.Store.Pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "listen "+store.EventsChannel); err != nil {
		return err
	}
	// Deliver anything written while we were not listening.
	last, err := h.Store.LatestEventID(ctx)
	if err != nil {
		return err
	}
	for {
		n, err := conn.Conn().WaitForNotification(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return nil
			}
			return err
		}
		id, err := strconv.ParseInt(n.Payload, 10, 64)
		if err != nil {
			continue
		}
		// Notifications can arrive out of order across transactions; fetch
		// everything after the last delivered ID so none is skipped.
		if id <= last {
			continue
		}
		list, err := h.Store.EventsAfter(ctx, last, 500)
		if err != nil {
			return err
		}
		for _, e := range list {
			h.deliver(e)
			last = e.ID
		}
	}
}

// Purge deletes events older than the replay window, every hour.
func (h *Hub) Purge(ctx context.Context, keep time.Duration) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		if err := h.Store.PurgeEvents(ctx, time.Now().Add(-keep)); err != nil && ctx.Err() == nil {
			h.Log.Warn("purge events", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func formatID(id int64) string { return strconv.FormatInt(id, 10) }

// ParseID parses an SSE Last-Event-ID.
func ParseID(s string) (int64, bool) {
	id, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	return id, err == nil && id >= 0
}
