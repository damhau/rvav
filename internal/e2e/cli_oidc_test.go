package e2e

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/httpapi"
	"github.com/damhau/rvav/pkg/rvavclient"
)

// idpUser is who signs in at the fake provider next.
type idpUser struct {
	sub, name, email string
	groups           []string
}

// cliIdP is a provider for rvavctl's sign-ins, as Keycloak serves the rvav
// public client: the authorization code with PKCE on a loopback redirect,
// the device flow, and refresh tokens.
type cliIdP struct {
	*fakeIdP
	mu      sync.Mutex
	signsIn idpUser
	// What the last authorization request asked for.
	challenge, redirect, scope string
	refresh                    string
	refreshes, devicePolls     int
	revoked                    []string
	refuseRefresh              bool
	// authError refuses the sign-in in the browser; badCode sends back a
	// code the token endpoint refuses.
	authError string
	badCode   bool
}

func newCLIIdP(t *testing.T) *cliIdP {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	p := &cliIdP{fakeIdP: &fakeIdP{t: t, key: key}}
	p.up.Store(true)
	p.srv = httptest.NewServer(http.HandlerFunc(p.serve))
	t.Cleanup(p.srv.Close)
	p.issuer = p.srv.URL
	return p
}

func reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (p *cliIdP) serve(w http.ResponseWriter, r *http.Request) {
	base := p.srv.URL
	p.mu.Lock()
	defer p.mu.Unlock()
	switch r.URL.Path {
	case "/.well-known/openid-configuration":
		reply(w, 200, map[string]any{"issuer": p.issuer, "jwks_uri": base + "/certs",
			"authorization_endpoint": base + "/auth", "token_endpoint": base + "/token",
			"device_authorization_endpoint": base + "/device", "id_token_signing_alg_values_supported": []string{"RS256"},
			"revocation_endpoint": base + "/revoke",
			"scopes_supported":    []string{"openid", "profile", "email", "offline_access"}})
	case "/certs":
		reply(w, 200, jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &p.key.PublicKey, KeyID: "k1", Algorithm: "RS256", Use: "sig"}}})
	case "/auth":
		q := r.URL.Query()
		if q.Get("client_id") != "rvav" || q.Get("code_challenge_method") != "S256" ||
			!strings.HasPrefix(q.Get("redirect_uri"), "http://127.0.0.1:") || q.Get("response_type") != "code" {
			http.Error(w, "bad authorization request: "+r.URL.RawQuery, http.StatusBadRequest)
			return
		}
		p.challenge, p.redirect, p.scope = q.Get("code_challenge"), q.Get("redirect_uri"), q.Get("scope")
		back := "?code=code-1"
		switch {
		case p.authError != "":
			back = "?error=" + p.authError + "&error_description=User+is+disabled"
		case p.badCode:
			back = "?code=code-2"
		}
		http.Redirect(w, r, p.redirect+back+"&state="+url.QueryEscape(q.Get("state")), http.StatusFound)
	case "/device":
		_ = r.ParseForm()
		p.scope = r.PostForm.Get("scope")
		reply(w, 200, map[string]any{"device_code": "device-1", "user_code": "WDJB-MJHT",
			"verification_uri": base + "/activate", "verification_uri_complete": base + "/activate?user_code=WDJB-MJHT",
			"expires_in": 600, "interval": 1})
	case "/token":
		_ = r.ParseForm()
		f := r.PostForm
		if f.Get("client_id") != "rvav" {
			reply(w, 401, map[string]string{"error": "invalid_client"})
			return
		}
		switch f.Get("grant_type") {
		case "authorization_code":
			sum := sha256.Sum256([]byte(f.Get("code_verifier")))
			if f.Get("code") != "code-1" || f.Get("redirect_uri") != p.redirect ||
				base64.RawURLEncoding.EncodeToString(sum[:]) != p.challenge {
				reply(w, 400, map[string]string{"error": "invalid_grant"})
				return
			}
		case "urn:ietf:params:oauth:grant-type:device_code":
			if p.devicePolls++; p.devicePolls == 1 || f.Get("device_code") != "device-1" {
				reply(w, 400, map[string]string{"error": "authorization_pending"})
				return
			}
		case "refresh_token":
			if p.refuseRefresh || f.Get("refresh_token") != p.refresh {
				reply(w, 400, map[string]string{"error": "invalid_grant", "error_description": "Session not active"})
				return
			}
			p.refreshes++
		default:
			reply(w, 400, map[string]string{"error": "unsupported_grant_type"})
			return
		}
		// A new refresh token each time, the old one spent, as rotating
		// providers do.
		p.refresh = fmt.Sprintf("refresh-%d", time.Now().UnixNano())
		u := p.signsIn
		reply(w, 200, map[string]any{"access_token": p.token(u.sub, u.name, u.email, u.groups, tokenOpts{}),
			"token_type": "Bearer", "expires_in": 300, "refresh_token": p.refresh})
	case "/revoke":
		_ = r.ParseForm()
		f := r.PostForm
		p.revoked = append(p.revoked, f.Get("token_type_hint")+" "+f.Get("client_id"))
		if f.Get("token") == p.refresh {
			p.refresh = ""
		}
	default:
		http.NotFound(w, r)
	}
}

func (p *cliIdP) set(change func(p *cliIdP)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	change(p)
}

// idpSeen is what the provider saw so far.
type idpSeen struct {
	scope                  string
	refreshes, devicePolls int
	revoked                []string
	refresh                string
}

func (p *cliIdP) get() idpSeen {
	p.mu.Lock()
	defer p.mu.Unlock()
	return idpSeen{scope: p.scope, refreshes: p.refreshes, devicePolls: p.devicePolls, revoked: slices.Clone(p.revoked),
		refresh: p.refresh}
}

// TestCLIOIDC signs rvavctl in at rvav's identity provider, as people do
// with OIDC: in the browser, on another device, refreshing the sign-in and
// saving it, until it can't be refreshed any more. The audit log records
// each sign-in, failed or not, and the sign-out (ADR-0058).
func TestCLIOIDC(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		idp := newCLIIdP(t)
		o, err := auth.NewOIDC(ctx, h.store, idp.srv.URL, "", "rvav", "groups")
		if err != nil {
			t.Fatal(err)
		}
		h.svc.Config.Auth.OIDCIssuer, h.svc.Config.Auth.OIDCClientID = idp.srv.URL, "rvav"
		h.svc.Config.Auth.Scopes = "openid profile email"
		srv := httptest.NewServer((&httpapi.Server{Store: h.store, Core: h.svc, Hub: h.hub, Log: discardLog(),
			AuthMode: "oidc", Auth: o, Heartbeat: time.Second}).Handler())
		t.Cleanup(srv.Close)
		mark := h.lastAuditID()
		// audited checks the auth events since the mark, and moves it.
		audited := func(what string, want ...map[string]any) {
			t.Helper()
			ev := h.authEvents(mark)
			if len(ev) != len(want) {
				t.Fatalf("%s: audited %+v", what, ev)
			}
			for i, w := range want {
				for k, v := range w {
					if ev[i].Detail[k] != v && !(k == "action" && ev[i].Action == v) {
						t.Errorf("%s: event %d's %s = %v, want %v (%+v)", what, i, k, ev[i].Detail[k], v, ev[i])
					}
				}
				if ua, _ := ev[i].Detail["user_agent"].(string); !strings.HasPrefix(ua, "rvavctl/") || ev[i].Detail["ip"] != "127.0.0.1" {
					t.Errorf("%s: event %d from %v, %q", what, i, ev[i].Detail["ip"], ua)
				}
			}
			mark = ev[len(ev)-1].ID
		}

		// In the browser: the code comes back to a loopback port.
		lea := newRvavctl(t, ctx)
		var page string
		lea.browser = func(u string) error {
			resp, err := http.Get(u)
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			b, _ := io.ReadAll(resp.Body)
			page = string(b)
			return nil
		}
		idp.set(func(p *cliIdP) {
			p.signsIn = idpUser{"kc-lea", "Léa Martin", "lea.martin@corp.example", []string{"/rvav-web-admins"}}
		})
		out, stderr := lea.run(0, "", "login", "--server", srv.URL)
		mustContain(t, "browser login", out, "Signed in to "+srv.URL+" as Léa Martin")
		mustContain(t, "browser login", stderr, idp.srv.URL+"/auth?")
		mustContain(t, "the browser's page", page, "Signed in to rvav")
		audited("browser login", map[string]any{"action": "auth.sign_in", "client": "rvavctl", "method": "oidc"})
		if got := idp.get(); !slices.Contains(strings.Fields(got.scope), "offline_access") ||
			!slices.Contains(strings.Fields(got.scope), "openid") {
			t.Fatalf("scopes asked = %q", got.scope)
		}
		var me api.Me
		lea.json(&me, "whoami")
		if len(me.Projects) != 1 || me.Projects[0].Project.Id != "prj_web" || !slices.Contains(roleKeys(me.Projects[0].Roles), "project-admin") {
			t.Fatalf("lea's projects from her groups = %+v", me.Projects)
		}

		// An expired access token is refreshed, and the new one saved.
		expire := func(c *rvavctl) *rvavclient.Profile {
			ps, err := rvavclient.LoadProfiles(c.config)
			if err != nil {
				t.Fatal(err)
			}
			p := ps.Profiles["default"]
			p.OIDC.Token.Expiry = time.Now().Add(-time.Minute)
			if err := ps.Save(); err != nil {
				t.Fatal(err)
			}
			return p
		}
		before := expire(lea)
		lea.json(&me, "whoami")
		ps, _ := rvavclient.LoadProfiles(lea.config)
		after := ps.Profiles["default"].OIDC.Token
		if idp.get().refreshes != 1 || after.RefreshToken == before.OIDC.Token.RefreshToken || !after.Expiry.After(time.Now()) {
			t.Fatalf("refreshes = %d, saved token %+v", idp.get().refreshes, after)
		}
		lea.json(&me, "whoami")
		if idp.get().refreshes != 1 {
			t.Fatal("a valid access token was refreshed")
		}
		// Once the provider refuses, sign in again.
		idp.set(func(p *cliIdP) { p.refuseRefresh = true })
		expire(lea)
		_, stderr = lea.run(4, "", "whoami")
		mustContain(t, "refused refresh", stderr, "the sign-in has expired", "Sign in again: rvavctl login")
		idp.set(func(p *cliIdP) { p.refuseRefresh = false })

		// The provider refuses (a disabled user), or the code can't be
		// exchanged: rvav records why, never the code.
		idp.set(func(p *cliIdP) { p.authError = "access_denied" })
		_, stderr = lea.run(4, "", "login", "--server", srv.URL)
		mustContain(t, "refused login", stderr, "the identity provider refused: access_denied User is disabled")
		idp.set(func(p *cliIdP) { p.authError, p.badCode = "", true })
		_, stderr = lea.run(4, "", "login", "--server", srv.URL)
		mustContain(t, "refused code", stderr, "invalid_grant")
		idp.set(func(p *cliIdP) { p.badCode = false })
		audited("failed logins",
			map[string]any{"action": "auth.sign_in_failed", "client": "rvavctl", "method": "oidc", "stage": "provider",
				"error": "access_denied", "description": "User is disabled"},
			map[string]any{"action": "auth.sign_in_failed", "stage": "token", "error": "invalid_grant", "reason": "token exchange failed"})

		// On another device: a code to enter where a browser is.
		marc := newRvavctl(t, ctx)
		idp.set(func(p *cliIdP) {
			p.signsIn = idpUser{"kc-marc", "Marc Keller", "marc.keller@corp.example", []string{"/rvav-admins", "/rvav-infra-leads"}}
		})
		out, stderr = marc.run(0, "", "login", "--server", srv.URL, "--device", "--profile", "prod")
		mustContain(t, "device login", stderr, idp.srv.URL+"/activate?user_code=WDJB-MJHT", "WDJB-MJHT")
		mustContain(t, "device login", out, "as Marc Keller")
		if got := idp.get(); got.devicePolls != 2 || !slices.Contains(strings.Fields(got.scope), "offline_access") {
			t.Fatalf("device polls = %d, scopes %q", got.devicePolls, got.scope)
		}
		audited("device login", map[string]any{"action": "auth.sign_in", "client": "rvavctl", "method": "device"})
		marc.json(&me, "whoami", "--profile", "prod")
		if !slices.Contains(roleKeys(me.Roles), "admin") {
			t.Fatalf("marc's roles from his groups = %+v", me.Roles)
		}
		var inbox []api.ApprovalSummary
		marc.json(&inbox, "approvals", "list", "--profile", "prod")

		// A profile's sign-in goes to its own rvav only.
		var sent []string
		var sentMu sync.Mutex
		other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sentMu.Lock()
			sent = append(sent, r.Header.Get("Authorization"))
			sentMu.Unlock()
			reply(w, 401, map[string]string{"title": "Unauthorized"})
		}))
		t.Cleanup(other.Close)
		marc.run(4, "", "whoami", "--profile", "prod", "--server", other.URL)
		sentMu.Lock()
		if len(sent) != 1 || sent[0] != "" {
			t.Fatalf("another rvav got %q", sent)
		}
		sentMu.Unlock()
		if b, err := os.ReadFile(marc.config); err != nil || !strings.Contains(string(b), `"refresh_token"`) {
			t.Fatalf("saved profile: %v %s", err, b)
		}
		// Signing out revokes the sign-in at the provider.
		out, _ = marc.run(0, "", "logout", "--profile", "prod")
		mustContain(t, "logout", out, "Signed out of "+srv.URL+", and the identity provider revoked the sign-in.")
		if got := idp.get(); !slices.Equal(got.revoked, []string{"refresh_token rvav"}) || got.refresh != "" {
			t.Fatalf("revoked %q, refresh token left %q", got.revoked, got.refresh)
		}
		marc.run(4, "", "whoami", "--profile", "prod")
		audited("logout", map[string]any{"action": "auth.sign_out", "method": "oidc"})
		h.noSecretsAudited("code-1", "code-2", "device-1", "WDJB-MJHT", "refresh-", "eyJ")
	})
}
