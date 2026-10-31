package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/core"
	"github.com/damhau/rvav/internal/httpapi"
)

// browser is a client with its own cookies, as a browser tab is.
type browser struct {
	t   *testing.T
	ctx context.Context
	url string
	c   *http.Client
}

func newBrowser(t *testing.T, ctx context.Context, url string) *browser {
	jar, _ := cookiejar.New(nil)
	return &browser{t: t, ctx: ctx, url: url, c: &http.Client{Jar: jar}}
}

// do sends a request as rvav's web app does (with X-Requested-With) unless
// bare, and returns the status, body and headers.
func (b *browser) do(method, path string, body any, bare bool, headers ...string) (int, []byte, http.Header) {
	b.t.Helper()
	var rd io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rd = bytes.NewReader(raw)
	}
	req, _ := http.NewRequestWithContext(b.ctx, method, b.url+path, rd)
	req.Header.Set("Content-Type", "application/json")
	if !bare {
		req.Header.Set("X-Requested-With", "rvav")
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := b.c.Do(req)
	if err != nil {
		b.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, out, resp.Header
}

func (b *browser) signIn(username, password string) (int, api.SignInResult, http.Header) {
	b.t.Helper()
	code, body, hdr := b.do("POST", "/api/v1/auth/sign-in", api.SignInRequest{Username: username, Password: password}, false)
	var res api.SignInResult
	if code == http.StatusOK {
		if err := json.Unmarshal(body, &res); err != nil {
			b.t.Fatalf("sign-in: %v: %s", err, body)
		}
	}
	return code, res, hdr
}

func (b *browser) me() (int, api.Me) {
	b.t.Helper()
	code, body, _ := b.do("GET", "/api/v1/me", nil, false)
	var m api.Me
	if code == http.StatusOK {
		_ = json.Unmarshal(body, &m)
	}
	return code, m
}

func problemType(body []byte) string {
	var p api.Problem
	_ = json.Unmarshal(body, &p)
	return strings.TrimPrefix(p.Type, "https://rvav.dev/problems/")
}

// TestLocalAdmin is the local administrator of ADR-0030: made at first start
// with the install password, which must be changed before anything else;
// failed attempts slowed; sessions that end; a reset from the command line;
// and a sign-in that works while the identity provider is down.
func TestLocalAdmin(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const installPassword = "install-password-1"
		const newPassword = "a much longer password"

		// First start, then the next starts: the account is made once.
		h.svc.Config.Auth.AdminPassword = installPassword
		created, generated, err := h.svc.EnsureLocalAdmin(ctx)
		if err != nil || !created || generated != "" {
			t.Fatalf("first start: created=%v generated=%q err=%v", created, generated, err)
		}
		h.svc.Config.Auth.AdminPassword = "another-install-password"
		if created, _, err := h.svc.EnsureLocalAdmin(ctx); err != nil || created {
			t.Fatalf("second start: created=%v err=%v", created, err)
		}
		if n := h.auditCount("auth.local_admin_created"); n != 1 {
			t.Fatalf("auth.local_admin_created audited %d times", n)
		}

		// The API as in production: the provider's tokens beside the session.
		idp := newFakeIdP(t, "")
		idp.up.Store(true)
		o, _ := auth.NewOIDC(ctx, h.store, idp.issuer, "", "rvav", "groups")
		o.RetryInterval = time.Millisecond
		srv := httptest.NewServer((&httpapi.Server{Store: h.store, Core: h.svc, Hub: h.hub, Log: discardLog(),
			AuthMode: "oidc", Auth: o, Heartbeat: time.Second}).Handler())
		t.Cleanup(srv.Close)

		a := newBrowser(t, ctx, srv.URL)
		if code, _ := a.me(); code != http.StatusUnauthorized {
			t.Fatalf("/me before signing in = %d, want 401", code)
		}
		// The password given at the second start is not the account's.
		if code, _, _ := a.signIn("admin", "another-install-password"); code != http.StatusUnauthorized {
			t.Fatalf("sign-in with the second start's password = %d, want 401", code)
		}
		if code, _, _ := a.signIn("nobody", installPassword); code != http.StatusUnauthorized {
			t.Fatalf("sign-in as an unknown user = %d, want 401", code)
		}

		// b signs in first, to check that changing the password ends it.
		b := newBrowser(t, ctx, srv.URL)
		if code, _, _ := b.signIn("admin", installPassword); code != http.StatusOK {
			t.Fatalf("b signs in = %d", code)
		}
		code, res, hdr := a.signIn(" Admin ", installPassword)
		if code != http.StatusOK || !res.PasswordChangeRequired {
			t.Fatalf("sign-in with the install password = %d %+v", code, res)
		}
		cookie := hdr.Get("Set-Cookie")
		for _, want := range []string{"rvav_session=", "Path=/api/", "HttpOnly", "SameSite=Strict"} {
			if !strings.Contains(cookie, want) {
				t.Fatalf("Set-Cookie %q lacks %q", cookie, want)
			}
		}
		if strings.Contains(cookie, "Secure") {
			t.Fatalf("Set-Cookie %q is Secure over HTTP", cookie)
		}
		code, me := a.me()
		if code != http.StatusOK || me.Local == nil || !*me.Local || me.PasswordChangeRequired == nil || !*me.PasswordChangeRequired ||
			len(me.GlobalRoles) != 1 || me.GlobalRoles[0] != "admin" || me.User.Id != core.LocalAdminUserID {
			t.Fatalf("/me = %d %+v", code, me)
		}

		// Nothing else until the password is changed.
		if code, body, _ := a.do("GET", "/api/v1/projects", nil, false); code != http.StatusForbidden || problemType(body) != "password-change-required" {
			t.Fatalf("projects before the change = %d %s", code, body)
		}
		change := func(current, next string, bare bool) (int, []byte) {
			code, body, _ := a.do("POST", "/api/v1/auth/password", api.PasswordChange{CurrentPassword: current, NewPassword: next}, bare)
			return code, body
		}
		// A change by session needs X-Requested-With (another site can't send it).
		if code, body := change(installPassword, newPassword, true); code != http.StatusForbidden || problemType(body) != "forbidden" {
			t.Fatalf("change without X-Requested-With = %d %s", code, body)
		}
		for _, c := range []struct{ current, next, field, code string }{
			{installPassword, "short", "/new_password", "too_short"},
			{installPassword, installPassword, "/new_password", "unchanged"},
			{installPassword, "   ADMIN      ", "/new_password", "username"},
			{"not the password", newPassword, "/current_password", "wrong"},
		} {
			code, body := change(c.current, c.next, false)
			var p api.Problem
			_ = json.Unmarshal(body, &p)
			if code != http.StatusUnprocessableEntity || p.Errors == nil || len(*p.Errors) != 1 ||
				(*p.Errors)[0].Field != c.field || *(*p.Errors)[0].Code != c.code {
				t.Fatalf("change %q to %q = %d %s, want %s %s", c.current, c.next, code, body, c.field, c.code)
			}
		}
		if code, body := change(installPassword, newPassword, false); code != http.StatusNoContent {
			t.Fatalf("change = %d %s", code, body)
		}
		if code, me := a.me(); code != http.StatusOK || *me.PasswordChangeRequired {
			t.Fatalf("/me after the change = %d %+v", code, me)
		}
		if code, body, _ := a.do("GET", "/api/v1/projects", nil, false); code != http.StatusOK {
			t.Fatalf("projects after the change = %d %s", code, body)
		}
		if code, _ := b.me(); code != http.StatusUnauthorized {
			t.Fatalf("b's session after the change = %d, want 401", code)
		}
		if code, _, _ := b.signIn("admin", installPassword); code != http.StatusUnauthorized {
			t.Fatalf("the install password after the change = %d, want 401", code)
		}

		// With the identity provider down, the local administrator still
		// signs in; the provider's tokens can't be checked.
		token := idp.token("u-ali", "Ali Ozturk", "ali@example.org", []string{"rvav-web"}, tokenOpts{})
		if code, _, _ := newBrowser(t, ctx, srv.URL).do("GET", "/api/v1/me", nil, false, "Authorization", "Bearer "+token); code != http.StatusOK {
			t.Fatalf("a provider token while the provider is up = %d", code)
		}
		idp.up.Store(false)
		o2, _ := auth.NewOIDC(ctx, h.store, idp.issuer, "", "rvav", "groups")
		srv2 := httptest.NewServer((&httpapi.Server{Store: h.store, Core: h.svc, Hub: h.hub, Log: discardLog(),
			AuthMode: "oidc", Auth: o2, Heartbeat: time.Second}).Handler())
		t.Cleanup(srv2.Close)
		d := newBrowser(t, ctx, srv2.URL)
		if code, _, _ := d.do("GET", "/api/v1/me", nil, false, "Authorization", "Bearer "+token); code != http.StatusServiceUnavailable {
			t.Fatalf("a provider token while the provider is down = %d, want 503", code)
		}
		if code, res, _ := d.signIn("admin", newPassword); code != http.StatusOK || res.PasswordChangeRequired {
			t.Fatalf("local sign-in while the provider is down = %d %+v", code, res)
		}
		if code, me := d.me(); code != http.StatusOK || me.User.Id != core.LocalAdminUserID {
			t.Fatalf("/me while the provider is down = %d %+v", code, me)
		}

		// Failed attempts slowed: from the third in a row, the next waits
		// (429 with Retry-After), even with the right password.
		e := newBrowser(t, ctx, srv.URL)
		for i := range 3 {
			if code, _, _ := e.signIn("admin", "wrong-"+strconv.Itoa(i)); code != http.StatusUnauthorized {
				t.Fatalf("wrong password %d = %d", i, code)
			}
		}
		code, _, hdr = e.signIn("admin", newPassword)
		if retry, _ := strconv.Atoi(hdr.Get("Retry-After")); code != http.StatusTooManyRequests || retry != 1 {
			t.Fatalf("after three failures = %d, Retry-After %q", code, hdr.Get("Retry-After"))
		}
		// The schedule, by the clock the service is given: 1 s, 2 s, 4 s.
		now := time.Now().Add(time.Second)
		for _, wait := range []time.Duration{2 * time.Second, 4 * time.Second} {
			if _, _, err := h.svc.SignIn(ctx, "admin", "wrong", now); !errors.Is(err, core.ErrBadCredentials) {
				t.Fatalf("wrong password at %s: %v", now, err)
			}
			var tm *core.TooManyAttemptsError
			if _, _, err := h.svc.SignIn(ctx, "admin", newPassword, now.Add(wait-time.Millisecond)); !errors.As(err, &tm) {
				t.Fatalf("right password %s after a failure: %v", wait, err)
			}
			now = now.Add(wait)
		}
		// Succeeding starts the count again.
		if _, _, err := h.svc.SignIn(ctx, "admin", newPassword, now); err != nil {
			t.Fatalf("right password once the wait is over: %v", err)
		}
		if _, _, err := h.svc.SignIn(ctx, "admin", "wrong", now); !errors.Is(err, core.ErrBadCredentials) {
			t.Fatal(err)
		}
		if _, _, err := h.svc.SignIn(ctx, "admin", newPassword, now); err != nil {
			t.Fatalf("one failure after a success doesn't wait: %v", err)
		}

		// Sign-out ends the session.
		if code, _, _ := d.do("POST", "/api/v1/auth/sign-out", nil, false); code != http.StatusNoContent {
			t.Fatalf("sign-out = %d", code)
		}
		if code, _ := d.me(); code != http.StatusUnauthorized {
			t.Fatalf("/me after sign-out = %d, want 401", code)
		}
		if code, _, _ := d.do("POST", "/api/v1/auth/sign-out", nil, false); code != http.StatusNoContent {
			t.Fatalf("sign-out without a session = %d", code)
		}

		// rvav admin reset-password: a generated password, to be changed;
		// every session ends.
		if _, err := core.ResetLocalAdmin(ctx, h.store, "short"); err == nil {
			t.Fatal("a reset to a short password was accepted")
		}
		reset, err := core.ResetLocalAdmin(ctx, h.store, "")
		if err != nil || len(reset) != 24 {
			t.Fatalf("reset: %q %v", reset, err)
		}
		if code, _ := a.me(); code != http.StatusUnauthorized {
			t.Fatalf("a's session after the reset = %d, want 401", code)
		}
		if code, res, _ := a.signIn("admin", reset); code != http.StatusOK || !res.PasswordChangeRequired {
			t.Fatalf("sign-in with the reset password = %d %+v", code, res)
		}

		// Every sign-in, failure, change and sign-out is audited.
		for action, want := range map[string]int{"auth.sign_in": 6, "auth.sign_in_failed": 10, "auth.password_changed": 1,
			"auth.sign_out": 1, "auth.password_reset": 1} {
			if n := h.auditCount(action); n != want {
				t.Errorf("%s audited %d times, want %d", action, n, want)
			}
		}
		var who string
		_ = h.store.Pool.QueryRow(ctx, `select detail->>'username' from audit_events where action = 'auth.sign_in_failed' and actor_id is null`).Scan(&who)
		if who != "nobody" {
			t.Errorf("the unknown username audited as %q", who)
		}
	})
}
