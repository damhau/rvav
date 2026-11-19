package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/core"
	"github.com/damhau/rvav/internal/events"
	"github.com/damhau/rvav/internal/store"
)

// replayLimit is how many missed events a reconnecting client can catch up
// on. Past that, or past the retention window, it gets resync.
const replayLimit = 1000

// StreamEvents serves Server-Sent Events. Browsers' EventSource cannot send
// an Authorization header, so web clients use a fetch-based SSE reader.
func (s *Server) StreamEvents(w http.ResponseWriter, r *http.Request, params api.StreamEventsParams) {
	p := principal(r)
	flusher, ok := w.(http.Flusher)
	if !ok {
		s.problem(w, r, http.StatusInternalServerError, "internal", "Streaming is not supported here", "", nil)
		return
	}
	var topics []string
	for _, t := range strings.Split(params.Topics, ",") {
		t = strings.TrimSpace(t)
		switch {
		case t == "":
		case t == "requests", t == "deployments", t == "notifications", t == "approvals",
			strings.HasPrefix(t, "request:"), strings.HasPrefix(t, "deployment:"):
			topics = append(topics, t)
		default:
			s.problem(w, r, http.StatusBadRequest, "bad-request", "Unknown topic",
				fmt.Sprintf("%q is not a topic. Use requests, deployments, notifications, approvals, request:<id> or deployment:<id>.", t), nil)
			return
		}
	}
	if len(topics) == 0 {
		s.problem(w, r, http.StatusBadRequest, "bad-request", "No topics", "Pass at least one topic.", nil)
		return
	}
	// Subscribed before the caller's roles are read again, the stream holds
	// no rights from before an access change: one made before the read is
	// in what it reads, one made after ends the stream with resync.
	sub := s.Hub.Subscribe(events.Filter{})
	defer s.Hub.Unsubscribe(sub)
	p, err := auth.Again(r.Context(), s.Store, p)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	r = r.WithContext(auth.WithPrincipal(r.Context(), p))
	projectID := deref(params.ProjectId)
	if projectID != "" {
		pr, ok := s.visibleProject(w, r, projectID)
		if !ok {
			return
		}
		projectID = pr.ID
	}
	sc, err := s.Core.DeploymentScope(r.Context(), p)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	filter := events.Filter{Topics: topics, ProjectID: projectID, UserID: p.User.ID, Requests: map[string]bool{}, Deployments: map[string]bool{},
		CanSeeProject: func(id string) bool { return p.In(auth.ProjectSee, id) && !slices.Contains(sc.OwnOnly, id) }}
	// A single request may be followed by anyone who can read it, approvers
	// outside its project included.
	for i, t := range filter.Topics {
		key, ok := strings.CutPrefix(t, "request:")
		if !ok {
			continue
		}
		row, err := s.Store.GetRequest(r.Context(), key)
		if err == nil {
			var readable bool
			if readable, err = s.Core.CanReadRequest(r.Context(), p, row); err == nil && !readable {
				err = store.ErrNotFound
			}
		}
		if err != nil {
			s.fail(w, r, err)
			return
		}
		filter.Topics[i] = "request:" + row.ID
		filter.Requests[row.ID] = true
	}
	// A deployment followed passes in a project whose other deployments
	// don't; one not seen stays filtered by its project.
	for _, t := range filter.Topics {
		key, ok := strings.CutPrefix(t, "deployment:")
		if !ok {
			continue
		}
		d, err := s.Store.GetDeployment(r.Context(), key)
		switch {
		case errors.Is(err, store.ErrNotFound):
		case err != nil:
			s.fail(w, r, err)
			return
		case core.SeesDeployment(p, d.ProjectID, d.ProjectSharing, d.OwnerID):
			filter.Deployments[d.ID] = true
		}
	}

	// Filtered before replaying so nothing falls between the two.
	s.Hub.SetFilter(sub, filter)

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	ctx := r.Context()
	var lastSent int64
	send := func(e store.Event) bool {
		if e.ID <= lastSent {
			return true
		}
		b, err := events.Encode(e)
		if err != nil {
			return true
		}
		if _, err := fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", e.ID, e.Type, b); err != nil {
			return false
		}
		lastSent = e.ID
		flusher.Flush()
		return true
	}
	control := func(typ string) bool {
		b, _ := json.Marshal(map[string]any{"id": fmt.Sprintf("%s-%d", typ, time.Now().UnixNano()), "type": typ, "at": time.Now().UTC()})
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", typ, b); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}

	if params.LastEventID != nil {
		if last, ok := events.ParseID(*params.LastEventID); ok {
			lastSent = last
			oldest, err := s.Store.OldestEventID(ctx)
			if err != nil {
				return
			}
			list, err := s.Store.EventsAfter(ctx, last, replayLimit+1)
			if err != nil {
				return
			}
			// After an access change the client's state may hold what it no
			// longer may see: resync, as for a gap.
			changed := slices.ContainsFunc(list, func(e store.Event) bool { return e.Type == events.AccessChangedType })
			if (oldest > 0 && last+1 < oldest) || len(list) > replayLimit || changed {
				if !control("resync") {
					return
				}
				list = nil
				if latest, err := s.Store.LatestEventID(ctx); err == nil {
					lastSent = latest
				}
			}
			for _, e := range list {
				if filter.Matches(e) && !send(e) {
					return
				}
				if e.ID > lastSent {
					lastSent = e.ID
				}
			}
		}
	}

	beat := s.Heartbeat
	if beat == 0 {
		beat = 15 * time.Second
	}
	ticker := time.NewTicker(beat)
	defer ticker.Stop()
	// A comment line opens the stream right away for proxies and clients.
	if _, err := fmt.Fprint(w, ": connected\n\n"); err != nil {
		return
	}
	flusher.Flush()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stopping():
			return // the client reconnects, to this process's successor
		case <-sub.Lagged:
			control("resync")
			return // the client reconnects with Last-Event-ID
		case e := <-sub.C:
			// An event delivered after an access change waits for the
			// filter of the stream the client reconnects with.
			select {
			case <-sub.Lagged:
				control("resync")
				return
			default:
			}
			if !send(e) {
				return
			}
		case <-ticker.C:
			if !control("heartbeat") {
				return
			}
		}
	}
}
