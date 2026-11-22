package e2e

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auditlog"
	"github.com/damhau/rvav/internal/observe"
	"github.com/damhau/rvav/internal/store"
)

// tcpSyslog is a plain-TCP syslog server, octet-counted. While closeAfter
// is above zero, a connection reads that many frames and closes, as a
// server restarting does.
type tcpSyslog struct {
	t    *testing.T
	ln   net.Listener
	mu   sync.Mutex
	msgs []string
	// closeAfter is how many more connections close early, after frames.
	closeAfter, frames, conns int
}

func newTCPSyslog(t *testing.T) *tcpSyslog {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	r := &tcpSyslog{t: t, ln: ln}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			r.mu.Lock()
			r.conns++
			limit := 0
			if r.closeAfter > 0 {
				r.closeAfter--
				limit = r.frames
			}
			r.mu.Unlock()
			go r.serve(c, limit)
		}
	}()
	return r
}

func (r *tcpSyslog) serve(c net.Conn, limit int) {
	defer c.Close()
	in := bufio.NewReader(c)
	for n := 0; limit == 0 || n < limit; n++ {
		l, err := in.ReadString(' ')
		if err != nil {
			return
		}
		size, err := strconv.Atoi(strings.TrimSuffix(l, " "))
		if err != nil || size <= 0 {
			return
		}
		msg := make([]byte, size)
		if _, err := io.ReadFull(in, msg); err != nil {
			return
		}
		r.mu.Lock()
		r.msgs = append(r.msgs, string(msg))
		r.mu.Unlock()
	}
}

func (r *tcpSyslog) received() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.msgs)
}

func (r *tcpSyslog) destination(name string) api.AuditDestinationWrite {
	host, port, _ := net.SplitHostPort(r.ln.Addr().String())
	p, _ := strconv.Atoi(port)
	tcp := api.SyslogTCP
	return api.AuditDestinationWrite{Name: name, Syslog: &api.AuditSyslog{Host: host, Port: &p, Transport: &tcp}}
}

// TestAuditForwardingWebhookCredentials: a webhook's credential goes in an
// authentication header whose value is sealed and never shown; a URL with
// a user name or password is refused, and its query's values are hidden
// from readers, the audit log and the destination's last error.
func TestAuditForwardingWebhookCredentials(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, auditor = "mkeller", "svc-awx"
		const query, value = "s3cr3t-in-the-query", "Bearer s3cr3t-header-value"
		type call struct{ auth, token string }
		var mu sync.Mutex
		var calls []call
		siem := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			calls = append(calls, call{auth: r.Header.Get("Authorization"), token: r.URL.Query().Get("token")})
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
		}))
		t.Cleanup(siem.Close)
		last := func() call {
			t.Helper()
			mu.Lock()
			defer mu.Unlock()
			if len(calls) == 0 {
				t.Fatal("the webhook wasn't called")
			}
			return calls[len(calls)-1]
		}
		h.json("POST", "/api/v1/admin/secrets", admin, api.SecretWrite{Name: "siem-hmac", Kind: api.SecretWriteKindToken,
			Value: new("siem-signing-secret")}, 201, nil)
		h.allowWebhooks(siem.URL)
		other := httptest.NewServer(http.NotFoundHandler())
		t.Cleanup(other.Close)
		h.allowWebhooks(other.URL)

		var problem struct{ Errors []api.FieldError }
		refused := func(hook api.AuditWebhook, field, code string) {
			t.Helper()
			h.json("POST", "/api/v1/admin/audit-destinations", admin, api.AuditDestinationWrite{Name: "SIEM", Webhook: &hook}, 422, &problem)
			if len(problem.Errors) != 1 || problem.Errors[0].Field != field || deref(problem.Errors[0].Code) != code {
				t.Fatalf("%+v: errors %+v, want %s %s", hook, problem.Errors, field, code)
			}
		}
		withPassword := strings.Replace(siem.URL, "http://", "http://svc:hunter2-password@", 1) + "/in"
		refused(api.AuditWebhook{Url: withPassword, Secret: "siem-hmac"}, "/webhook/url", "credentials")
		refused(api.AuditWebhook{Url: siem.URL + "/in", Secret: "siem-hmac", AuthHeader: new("X-Rvav-Signature"), AuthValue: new("x")},
			"/webhook/auth_header", "reserved")
		refused(api.AuditWebhook{Url: siem.URL + "/in", Secret: "siem-hmac", AuthValue: new(value)}, "/webhook/auth_header", "required")
		refused(api.AuditWebhook{Url: siem.URL + "/in", Secret: "siem-hmac", AuthHeader: new("Authorization")}, "/webhook/auth_value", "required")
		refused(api.AuditWebhook{Url: siem.URL + "/in", Secret: "siem-hmac", AuthHeader: new("Authorization"), AuthValue: new("a\r\nX-Evil: 1")},
			"/webhook/auth_value", "invalid")

		var dest api.AuditDestination
		h.json("POST", "/api/v1/admin/audit-destinations", admin, api.AuditDestinationWrite{Name: "SIEM", Webhook: &api.AuditWebhook{
			Url: siem.URL + "/in?token=" + query + "&site=lab", Secret: "siem-hmac", AuthHeader: new("Authorization"),
			AuthValue: new(value)}}, 201, &dest)
		shownURL := siem.URL + "/in?token=REDACTED&site=REDACTED"
		for _, user := range []string{admin, auditor} {
			var d api.AuditDestination
			_, body, _ := h.do("GET", "/api/v1/admin/audit-destinations/"+dest.Id, user, nil)
			if err := json.Unmarshal(body, &d); err != nil || d.Webhook == nil || d.Webhook.Url != shownURL ||
				deref(d.Webhook.AuthHeader) != "Authorization" || strings.Contains(string(body), "s3cr3t") {
				t.Fatalf("%s reads %s", user, body)
			}
		}
		var settings string
		var sealed []byte
		if err := h.store.Pool.QueryRow(ctx, `select settings::text, auth_ciphertext from audit_destinations where id = $1`,
			dest.Id).Scan(&settings, &sealed); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(settings, "header-value") || len(sealed) == 0 || strings.Contains(string(sealed), "header-value") {
			t.Fatalf("stored: settings %s, sealed %q", settings, sealed)
		}
		h.noSecretsAudited(query, "header-value")

		// What the webhook gets: the stored URL and the header.
		fwd := h.svc.AuditLog
		fwd.Now = func() time.Time { return time.Now().Add(time.Minute) }
		defer func() { fwd.Now = nil }()
		if err := fwd.Once(ctx); err != nil {
			t.Fatal(err)
		}
		if c := last(); c.auth != value || c.token != query {
			t.Fatalf("sent %+v", c)
		}

		// Sent back as read, both are kept.
		h.json("PUT", "/api/v1/admin/audit-destinations/"+dest.Id, admin, api.AuditDestinationWrite{Name: "SIEM",
			Webhook: dest.Webhook}, 200, &dest)
		if err := fwd.Once(ctx); err != nil {
			t.Fatal(err)
		}
		if c := last(); c.auth != value || c.token != query {
			t.Fatalf("after a change sent back as read: %+v", c)
		}
		h.noSecretsAudited(query, "header-value")
		// Another server doesn't get the value kept; a changed URL doesn't
		// keep the query's values.
		h.json("PUT", "/api/v1/admin/audit-destinations/"+dest.Id, admin, api.AuditDestinationWrite{Name: "SIEM",
			Webhook: &api.AuditWebhook{Url: other.URL + "/in", Secret: "siem-hmac", AuthHeader: new("Authorization")}}, 422, &problem)
		if got := fieldsOf(problem.Errors); !slices.Equal(got, []string{"/webhook/auth_value"}) {
			t.Fatalf("value kept for another host = %v", got)
		}
		h.json("PUT", "/api/v1/admin/audit-destinations/"+dest.Id, admin, api.AuditDestinationWrite{Name: "SIEM",
			Webhook: &api.AuditWebhook{Url: siem.URL + "/other?token=REDACTED", Secret: "siem-hmac"}}, 422, &problem)
		if got := fieldsOf(problem.Errors); !slices.Equal(got, []string{"/webhook/url"}) {
			t.Fatalf("redacted values in a new URL = %v", got)
		}
		// Without the header, it goes.
		var bare api.AuditDestination
		h.json("PUT", "/api/v1/admin/audit-destinations/"+dest.Id, admin, api.AuditDestinationWrite{Name: "SIEM",
			Webhook: &api.AuditWebhook{Url: shownURL, Secret: "siem-hmac"}}, 200, &bare)
		if err := fwd.Once(ctx); err != nil {
			t.Fatal(err)
		}
		if c := last(); c.auth != "" || c.token != query || bare.Webhook.AuthHeader != nil {
			t.Fatalf("without the header: %+v, %+v", c, bare.Webhook)
		}

		// A server that can't be reached: the error shows no query value.
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		down := "http://" + ln.Addr().String()
		_ = ln.Close()
		h.allowWebhooks(down)
		var gone api.AuditDestination
		// 80 characters, 160 bytes: a name's length is counted in characters.
		downHook := api.AuditWebhook{Url: down + "/in?token=" + query, Secret: "siem-hmac"}
		h.json("POST", "/api/v1/admin/audit-destinations", admin, api.AuditDestinationWrite{Name: strings.Repeat("é", 81),
			Webhook: &downHook}, 422, nil)
		h.json("POST", "/api/v1/admin/audit-destinations", admin, api.AuditDestinationWrite{Name: strings.Repeat("é", 80),
			Webhook: &downHook}, 201, &gone)
		if err := fwd.Once(ctx); err != nil {
			t.Fatal(err)
		}
		h.json("GET", "/api/v1/admin/audit-destinations/"+gone.Id, auditor, nil, 200, &gone)
		if e := deref(gone.State.LastError); e == "" || strings.Contains(e, query) || !strings.Contains(e, "REDACTED") {
			t.Fatalf("last error %q", e)
		}
	})
}

// TestAuditForwardingSetAside: an event a destination refuses for good, a
// webhook's 413 or one too long for syslog over TCP, waits aside with why,
// and the events after it go; saving the destination tries it again. The
// queue's gauge counts neither those nor a disabled destination's.
func TestAuditForwardingSetAside(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin = "mkeller"
		var mu sync.Mutex
		var got []string
		siem := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			b, _ := io.ReadAll(r.Body)
			if strings.Contains(string(b), "webhook-too-large") {
				w.WriteHeader(http.StatusRequestEntityTooLarge)
				return
			}
			var body auditlog.Body
			_ = json.Unmarshal(b, &body)
			mu.Lock()
			for _, e := range body.Events {
				got = append(got, e.TargetID)
			}
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
		}))
		t.Cleanup(siem.Close)
		h.json("POST", "/api/v1/admin/secrets", admin, api.SecretWrite{Name: "siem-hmac", Kind: api.SecretWriteKindToken,
			Value: new("siem-signing-secret")}, 201, nil)
		h.allowWebhooks(siem.URL)
		var hook api.AuditDestination
		h.json("POST", "/api/v1/admin/audit-destinations", admin, api.AuditDestinationWrite{Name: "SIEM",
			Categories: &[]api.AuditCategory{api.AuditCategoryToken},
			Webhook:    &api.AuditWebhook{Url: siem.URL + "/in", Secret: "siem-hmac"}}, 201, &hook)
		rcv := newTCPSyslog(t)
		var sys api.AuditDestination
		w := rcv.destination("Lab")
		w.Categories = &[]api.AuditCategory{api.AuditCategoryToken}
		h.json("POST", "/api/v1/admin/audit-destinations", admin, w, 201, &sys)

		audit := func(target string, size int) {
			t.Helper()
			if err := h.store.Audit(ctx, h.store.Pool, store.AuditEvent{Action: "token.create", TargetType: "api_token",
				TargetID: target, Detail: map[string]any{"note": strings.Repeat("x", size)}}); err != nil {
				t.Fatal(err)
			}
		}
		audit("small-1", 10)
		audit("webhook-too-large", 10)
		audit("syslog-too-large", auditlog.MaxFrame)
		audit("small-2", 10)
		fwd := h.svc.AuditLog
		if err := fwd.Once(ctx); err != nil {
			t.Fatal(err)
		}
		state := func(id string) api.AuditDeliveryState {
			t.Helper()
			var d api.AuditDestination
			h.json("GET", "/api/v1/admin/audit-destinations/"+id, admin, nil, 200, &d)
			return d.State
		}
		mu.Lock()
		sent := slices.Clone(got)
		mu.Unlock()
		if !slices.Equal(sent, []string{"small-1", "syslog-too-large", "small-2"}) {
			t.Fatalf("the webhook got %v", sent)
		}
		if s := state(hook.Id); s.Backlog != 0 || s.SetAside != 1 || s.Delivered != 3 || s.Failures != 0 ||
			!strings.Contains(deref(s.SetAsideReason), "413") {
			t.Fatalf("webhook state %+v", s)
		}
		var targets []string
		for _, m := range rcv.received() {
			_, target, _ := strings.Cut(m, `"target_id":"`)
			target, _, _ = strings.Cut(target, `"`)
			targets = append(targets, target)
		}
		if !slices.Equal(targets, []string{"small-1", "webhook-too-large", "small-2"}) {
			t.Fatalf("syslog got %v", targets)
		}
		if s := state(sys.Id); s.Backlog != 0 || s.SetAside != 1 || s.Delivered != 3 || !strings.Contains(deref(s.SetAsideReason), "64 KB") {
			t.Fatalf("syslog state %+v", s)
		}
		depth := func() float64 {
			t.Helper()
			r := prometheus.NewRegistry()
			r.MustRegister(observe.Backlog{Pool: h.store.Pool})
			return sample(t, r, "rvav_queue_depth", map[string]string{"queue": "audit"})
		}
		if n := depth(); n != 0 {
			t.Fatalf("queue depth with two events set aside = %v", n)
		}

		// Saved again, the webhook tries its event again, and sets it aside again.
		h.json("PUT", "/api/v1/admin/audit-destinations/"+hook.Id, admin, api.AuditDestinationWrite{Name: "SIEM",
			Categories: &[]api.AuditCategory{api.AuditCategoryToken}, Webhook: hook.Webhook}, 200, &hook)
		if s := hook.State; s.Backlog != 1 || s.SetAside != 0 {
			t.Fatalf("saved again: %+v", s)
		}
		if n := depth(); n != 1 {
			t.Fatalf("queue depth with one event waiting = %v", n)
		}
		if err := fwd.Once(ctx); err != nil {
			t.Fatal(err)
		}
		if s := state(hook.Id); s.Backlog != 0 || s.SetAside != 1 {
			t.Fatalf("tried again: %+v", s)
		}

		// Disabled, what waits for it isn't counted.
		audit("while-on", 10)
		w.Enabled = new(false)
		h.json("PUT", "/api/v1/admin/audit-destinations/"+sys.Id, admin, w, 200, &sys)
		if sys.State.Backlog != 2 {
			t.Fatalf("disabled: %+v", sys.State)
		}
		if n := depth(); n != 1 {
			t.Fatalf("queue depth with the syslog server disabled = %v", n)
		}
	})
}
