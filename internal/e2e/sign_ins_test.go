package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/httpapi"
)

// authEvent is a sign-in, sign-out or token use in the audit log.
type authEvent struct {
	ID         int64
	Action     string
	Actor      *string
	TargetType string
	TargetID   string
	Detail     map[string]any
}

// authEvents are the auth.* events after the one with ID after.
func (h *harness) authEvents(after int64) []authEvent {
	h.t.Helper()
	rows, err := h.store.Pool.Query(h.ctx, `select id, action, actor_id, target_type, target_id, detail from audit_events
		where action like 'auth.%' and id > $1 order by id`, after)
	if err != nil {
		h.t.Fatal(err)
	}
	defer rows.Close()
	var out []authEvent
	for rows.Next() {
		var e authEvent
		if err := rows.Scan(&e.ID, &e.Action, &e.Actor, &e.TargetType, &e.TargetID, &e.Detail); err != nil {
			h.t.Fatal(err)
		}
		out = append(out, e)
	}
	return out
}

func (h *harness) lastAuditID() (id int64) {
	_ = h.store.Pool.QueryRow(h.ctx, `select coalesce(max(id), 0) from audit_events`).Scan(&id)
	return id
}

// noSecretsAudited fails when the audit log holds any of these: tokens and
// codes are never recorded.
func (h *harness) noSecretsAudited(secrets ...string) {
	h.t.Helper()
	for _, s := range secrets {
		var n int
		_ = h.store.Pool.QueryRow(h.ctx, `select count(*) from audit_events where strpos(detail::text, $1) > 0
			or strpos(target_id, $1) > 0`, s).Scan(&n)
		if n > 0 {
			h.t.Errorf("%d audit events hold %.12s…", n, s)
		}
	}
}

// TestSignInsAudited records sign-ins at the identity provider as the web
// app and rvavctl report them, the failures before there is a token, the
// tokens rvav refuses, sign-outs and API tokens' use, with where they came
// from (ADR-0058).
func TestSignInsAudited(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		idp := newFakeIdP(t, "")
		idp.up.Store(true)
		o, err := auth.NewOIDC(ctx, h.store, idp.srv.URL, "", "rvav", "groups")
		if err != nil {
			t.Fatal(err)
		}
		o.RetryInterval = time.Millisecond
		// The web container in front: its X-Forwarded-For is believed.
		srv := httptest.NewServer((&httpapi.Server{Store: h.store, Core: h.svc, Hub: h.hub, Log: discardLog(),
			AuthMode: "oidc", Auth: o, Heartbeat: time.Second,
			TrustedProxies: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}}).Handler())
		t.Cleanup(srv.Close)
		callAt := func(base, method, path, token, from string, body any) (int, string) {
			t.Helper()
			var r io.Reader
			if body != nil {
				b, _ := json.Marshal(body)
				r = bytes.NewReader(b)
			}
			req, _ := http.NewRequestWithContext(ctx, method, base+path, r)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("User-Agent", "Mozilla/5.0 (rvav e2e)")
			if from != "" {
				req.Header.Set("X-Forwarded-For", from)
			}
			if token != "" {
				req.Header.Set("Authorization", "Bearer "+token)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			b, _ := io.ReadAll(resp.Body)
			return resp.StatusCode, string(b)
		}
		call := func(method, path, token, from string, body any) (int, string) {
			t.Helper()
			return callAt(srv.URL, method, path, token, from, body)
		}
		report := func(token, from string, body any) (int, string) {
			t.Helper()
			return call("POST", "/api/v1/auth/sign-ins", token, from, body)
		}
		start := h.lastAuditID()

		// Signed in in the browser: who, how and from where. The address
		// the client gave left of the proxy's is not believed.
		lea := idp.token("kc-lea", "Léa Martin", "lea.martin@corp.example", []string{"/rvav-web-admins"}, tokenOpts{})
		if code, b := report(lea, "198.51.100.1, 203.0.113.7", map[string]any{"client": "web", "method": "oidc"}); code != 204 {
			t.Fatalf("web sign-in = %d %s", code, b)
		}
		// On another device, with rvavctl.
		marc := idp.token("kc-marc", "Marc Keller", "marc.keller@corp.example", []string{"/rvav-admins"}, tokenOpts{})
		if code, b := report(marc, "203.0.113.8", map[string]any{"client": "rvavctl", "method": "device"}); code != 204 {
			t.Fatalf("device sign-in = %d %s", code, b)
		}
		ev := h.authEvents(start)
		if len(ev) != 2 {
			t.Fatalf("sign-ins audited: %+v", ev)
		}
		leaID := *ev[0].Actor
		if d := ev[0].Detail; ev[0].Action != "auth.sign_in" || ev[0].TargetID != leaID || d["client"] != "web" || d["method"] != "oidc" ||
			d["ip"] != "203.0.113.7" || d["user_agent"] != "Mozilla/5.0 (rvav e2e)" ||
			!slices.Equal(anyStrings(d["groups"]), []string{"rvav-web-admins"}) || !slices.Contains(anyStrings(d["roles"]), "user") {
			t.Errorf("lea's sign-in = %+v", ev[0])
		}
		if d := ev[1].Detail; ev[1].Action != "auth.sign_in" || d["client"] != "rvavctl" || d["method"] != "device" || d["ip"] != "203.0.113.8" {
			t.Errorf("marc's sign-in = %+v", ev[1])
		}

		// Failed before there was a token, as the client says: the
		// provider refused (a disabled user), the state didn't match, the
		// code couldn't be exchanged.
		for _, f := range []map[string]any{
			{"stage": "provider", "error": "access_denied", "description": "User is disabled"},
			{"stage": "state", "description": "No matching state found in storage"},
			{"stage": "token", "error": "invalid_grant", "description": "Code not valid"},
		} {
			if code, b := report("", "203.0.113.9", map[string]any{"client": "web", "method": "oidc", "failure": f}); code != 204 {
				t.Fatalf("failure %v = %d %s", f, code, b)
			}
		}
		// A token rvav refuses: expired, or from another provider.
		expired := idp.token("kc-lea", "Léa Martin", "lea.martin@corp.example", nil, tokenOpts{expires: time.Now().Add(-time.Minute)})
		if code, b := report(expired, "203.0.113.9", map[string]any{"client": "web", "method": "oidc"}); code != 401 {
			t.Fatalf("expired token = %d %s", code, b)
		}
		ev = h.authEvents(ev[1].ID)
		if len(ev) != 4 {
			t.Fatalf("failures audited: %+v", ev)
		}
		for i, want := range []map[string]any{
			{"stage": "provider", "reason": "provider refused", "error": "access_denied", "description": "User is disabled", "reported_by": "client"},
			{"stage": "state", "reason": "state mismatch", "description": "No matching state found in storage", "reported_by": "client"},
			{"stage": "token", "reason": "token exchange failed", "error": "invalid_grant", "reported_by": "client"},
			{"reason": "token refused"},
		} {
			e := ev[i]
			if e.Action != "auth.sign_in_failed" || e.Actor != nil || e.Detail["ip"] != "203.0.113.9" || e.Detail["client"] != "web" {
				t.Errorf("failure %d = %+v", i, e)
			}
			for k, v := range want {
				if e.Detail[k] != v {
					t.Errorf("failure %d: %s = %v, want %v", i, k, e.Detail[k], v)
				}
			}
		}
		if msg, _ := ev[3].Detail["error"].(string); !strings.Contains(msg, "expired") {
			t.Errorf("why the token was refused = %q", msg)
		}

		// Nothing recorded when the token can't be checked, nor when there
		// is none, nor for a report that isn't valid.
		down := newFakeIdP(t, "")
		down.up.Store(true)
		down.keysDown.Store(true)
		o2, err := auth.NewOIDC(ctx, h.store, down.srv.URL, "", "rvav", "groups")
		if err != nil {
			t.Fatal(err)
		}
		srv2 := httptest.NewServer((&httpapi.Server{Store: h.store, Core: h.svc, Hub: h.hub, Log: discardLog(),
			AuthMode: "oidc", Auth: o2, Heartbeat: time.Second}).Handler())
		t.Cleanup(srv2.Close)
		other := down.token("kc-new", "New Person", "new@corp.example", nil, tokenOpts{})
		if code, b := callAt(srv2.URL, "POST", "/api/v1/auth/sign-ins", other, "", map[string]any{"client": "web", "method": "oidc"}); code != 503 {
			t.Fatalf("keys unavailable = %d %s", code, b)
		}
		if code, b := report("", "", map[string]any{"client": "web", "method": "oidc"}); code != 401 ||
			!strings.Contains(b, "Authorization header") || strings.Contains(b, "refused the token") {
			t.Fatalf("no token = %d %s", code, b)
		}
		// Only as JSON: a form on another site can post text/plain.
		before := h.lastAuditID()
		req, _ := http.NewRequestWithContext(ctx, "POST", srv.URL+"/api/v1/auth/sign-ins",
			strings.NewReader(`{"client":"web","method":"oidc","failure":{"stage":"provider"}}`))
		req.Header.Set("Content-Type", "text/plain")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusUnsupportedMediaType || h.lastAuditID() != before {
			t.Fatalf("a text/plain report = %d, audited %v", resp.StatusCode, h.lastAuditID() != before)
		}
		for _, bad := range []map[string]any{
			{},
			{"client": "web", "method": "oidc", "failure": map[string]any{"stage": "elsewhere"}},
			{"client": "web", "method": "oidc", "failure": map[string]any{"stage": "provider", "error": "access\ndenied"}},
			{"client": "web", "method": "oidc", "code": "abc"},
		} {
			if code, b := report("", "", bad); code != 400 && code != 422 {
				t.Errorf("%v = %d %s", bad, code, b)
			}
		}
		if code, b := report(lea, "", map[string]any{"client": "web", "method": "oidc", "failure": map[string]any{"stage": "state"}}); code != 422 {
			t.Errorf("a failure with a token = %d %s", code, b)
		}
		// At most 10 failures a minute from one address.
		for range 12 {
			report("", "192.0.2.50", map[string]any{"client": "web", "method": "oidc", "failure": map[string]any{"stage": "provider"}})
		}
		var flood int
		_ = h.store.Pool.QueryRow(ctx, `select count(*) from audit_events where detail->>'ip' = '192.0.2.50'`).Scan(&flood)
		if flood != 10 {
			t.Errorf("failures kept from one address = %d, want 10", flood)
		}
		mark := h.lastAuditID()
		if ev := h.authEvents(ev[3].ID); len(ev) != 10 {
			t.Errorf("events after the refused token = %d, want the 10 failures only", len(ev))
		}

		// Signing out with the provider's token.
		if code, b := call("POST", "/api/v1/auth/sign-out", lea, "203.0.113.7", nil); code != 204 {
			t.Fatalf("sign-out = %d %s", code, b)
		}
		ev = h.authEvents(mark)
		if len(ev) != 1 || ev[0].Action != "auth.sign_out" || *ev[0].Actor != leaID || ev[0].Detail["method"] != "oidc" ||
			ev[0].Detail["ip"] != "203.0.113.7" {
			t.Fatalf("sign-out = %+v", ev)
		}

		// An API token: its first use of the day, not each one; once
		// expired, its first refusal of the day.
		pat := auth.NewToken()
		if err := h.store.CreateAPIToken(ctx, "tok_signin", leaID, "terraform", auth.HashToken(pat), "", nil); err != nil {
			t.Fatal(err)
		}
		for range 3 {
			if code, b := call("GET", "/api/v1/me", pat, "203.0.113.10", nil); code != 200 {
				t.Fatalf("/me with the token = %d %s", code, b)
			}
		}
		if code, b := report(pat, "203.0.113.10", map[string]any{"client": "rvavctl", "method": "token"}); code != 204 {
			t.Fatalf("rvavctl login --with-token = %d %s", code, b)
		}
		// Used yesterday: today's first use is audited again.
		_, _ = h.store.Pool.Exec(ctx, `update api_tokens set last_used_at = now() - interval '1 day' where id = 'tok_signin'`)
		call("GET", "/api/v1/me", pat, "203.0.113.10", nil)
		_, _ = h.store.Pool.Exec(ctx, `update api_tokens set expires_at = now() - interval '1 minute' where id = 'tok_signin'`)
		for range 2 {
			if code, _ := call("GET", "/api/v1/me", pat, "203.0.113.11", nil); code != 401 {
				t.Fatalf("an expired token = %d", code)
			}
		}
		ev = h.authEvents(ev[0].ID)
		var got []string
		for _, e := range ev {
			got = append(got, e.Action+" "+e.Detail["method"].(string)+" "+e.Detail["ip"].(string))
		}
		if want := []string{"auth.token_used token 203.0.113.10", "auth.sign_in token 203.0.113.10", "auth.token_used token 203.0.113.10",
			"auth.sign_in_failed token 203.0.113.11"}; !slices.Equal(got, want) {
			t.Fatalf("API token events = %q, want %q", got, want)
		}
		if e := ev[0]; e.TargetType != "api_token" || e.TargetID != "tok_signin" || *e.Actor != leaID || e.Detail["name"] != "terraform" {
			t.Errorf("token use = %+v", e)
		}
		if e := ev[3]; e.TargetID != "tok_signin" || e.Detail["reason"] != "token_expired" {
			t.Errorf("expired token = %+v", e)
		}
		h.noSecretsAudited(lea, marc, expired, other, pat, auth.HashToken(pat))

		// Without a trusted proxy, X-Forwarded-For is the client's say:
		// the peer's address is recorded.
		mark = h.lastAuditID()
		code, b := callAt(srv2.URL, "POST", "/api/v1/auth/sign-ins", "", "203.0.113.99",
			map[string]any{"client": "web", "method": "oidc", "failure": map[string]any{"stage": "provider"}})
		if ev := h.authEvents(mark); code != 204 || len(ev) != 1 || ev[0].Detail["ip"] != "127.0.0.1" {
			t.Errorf("without a trusted proxy = %d %s %+v", code, b, ev)
		}
	})
}

func anyStrings(v any) []string {
	l, _ := v.([]any)
	var out []string
	for _, s := range l {
		if s, ok := s.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
