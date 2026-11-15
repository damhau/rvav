package e2e

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/events"
	"github.com/damhau/rvav/internal/httpapi"
	"github.com/damhau/rvav/internal/seed"
)

// meanwhile makes an access change once a request to /events is
// authenticated, before the stream subscribes, and waits for the hub to
// deliver it: the principal is then from before the change.
type meanwhile struct {
	auth.Authenticator
	change func()
}

func (m meanwhile) Authenticate(r *http.Request) (*auth.Principal, error) {
	p, err := m.Authenticator.Authenticate(r)
	if err == nil && strings.HasSuffix(r.URL.Path, "/events") {
		m.change()
	}
	return p, err
}

// TestStreamsAfterAnAccessChange: a stream holds no rights from before an
// access change, and a replay across one ends with resync (ADR-0034).
func TestStreamsAfterAnAccessChange(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, user = "mkeller", "cdubois"
		member := "/api/v1/projects/prj_web/members/" + seed.UserID(user)
		h.json("PUT", member, admin, api.ProjectMemberWrite{Roles: &[]string{"member"}}, 200, nil)

		// Removed from Web Platform between signing in and subscribing.
		removed := make(chan int, 1)
		remove := func() {
			watch := h.hub.Subscribe(events.Filter{})
			defer h.hub.Unsubscribe(watch)
			req, _ := http.NewRequestWithContext(ctx, "DELETE", h.srv.URL+member, nil)
			req.Header.Set(auth.DevUserHeader, admin)
			code := 0
			if resp, err := http.DefaultClient.Do(req); err == nil {
				code = resp.StatusCode
				resp.Body.Close()
			}
			select {
			case <-watch.Lagged:
			case <-time.After(10 * time.Second):
				code = -1
			}
			removed <- code
		}
		srv := httptest.NewServer((&httpapi.Server{Store: h.store, Core: h.svc, Hub: h.hub, Log: discardLog(), AuthMode: "dev",
			Auth: meanwhile{&auth.Dev{Store: h.store, DefaultUser: "lmartin"}, remove}, Heartbeat: time.Second}).Handler())
		t.Cleanup(srv.Close)
		sctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		req, _ := http.NewRequestWithContext(sctx, "GET", srv.URL+"/api/v1/events?topics=requests", nil)
		req.Header.Set(auth.DevUserHeader, user)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if code := <-removed; code != http.StatusNoContent || resp.StatusCode != http.StatusOK {
			t.Fatalf("removal = %d, stream = %d", code, resp.StatusCode)
		}
		// Subscribed (its headers are sent after): an event of Web Platform,
		// then an access change, which ends the stream with resync.
		web := "prj_web"
		if _, err := h.store.InsertEvent(ctx, h.store.Pool, "request.updated", &web, []string{"requests", "request:req_x"},
			map[string]any{"type": "request.updated", "at": time.Now().UTC(), "data": map[string]any{}}); err != nil {
			t.Fatal(err)
		}
		h.json("PUT", "/api/v1/projects/prj_data/members/"+seed.UserID(user), admin, api.ProjectMemberWrite{Roles: &[]string{"viewer"}}, 200, nil)
		if first := firstEvent(resp); first != "resync" {
			t.Fatalf("first event = %q: the stream kept Web Platform's events after its user was removed", first)
		}

		// A replay across an access change: resync first.
		last, err := h.store.LatestEventID(ctx)
		if err != nil {
			t.Fatal(err)
		}
		h.json("PUT", member, admin, api.ProjectMemberWrite{Roles: &[]string{"viewer"}}, 200, nil)
		out := make(chan string, 16)
		go readSSE(sctx, h.srv.URL+"/api/v1/events?topics=requests", user, strconv.FormatInt(last, 10), out)
		select {
		case got := <-out:
			if got != " resync" {
				t.Fatalf("replay across an access change began with %q", got)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("replay across an access change: no resync")
		}
	})
}

// firstEvent is the type of a stream's first event, heartbeats left out.
func firstEvent(resp *http.Response) string {
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		if typ, ok := strings.CutPrefix(sc.Text(), "event: "); ok && typ != "heartbeat" {
			return typ
		}
	}
	return ""
}
