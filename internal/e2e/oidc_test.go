package e2e

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/httpapi"
)

// fakeIdP is an OIDC provider that serves discovery and its signing key, and
// signs access tokens the way Keycloak does for the rvav client.
type fakeIdP struct {
	t   *testing.T
	srv *httptest.Server
	key *rsa.PrivateKey
	// issuer in discovery and tokens: the server URL unless set, as with
	// Keycloak in compose, where the browser and the API use different URLs.
	issuer   string
	up       atomic.Bool
	keysDown atomic.Bool
}

func newFakeIdP(t *testing.T, issuer string) *fakeIdP {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	p := &fakeIdP{t: t, key: key}
	p.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !p.up.Load() || (r.URL.Path == "/certs" && p.keysDown.Load()) {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"issuer": p.issuer, "jwks_uri": p.srv.URL + "/certs",
				"authorization_endpoint": p.srv.URL + "/auth", "token_endpoint": p.srv.URL + "/token",
				"id_token_signing_alg_values_supported": []string{"RS256"},
			})
		case "/certs":
			_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{
				{Key: &key.PublicKey, KeyID: "k1", Algorithm: "RS256", Use: "sig"}}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(p.srv.Close)
	p.issuer = issuer
	if p.issuer == "" {
		p.issuer = p.srv.URL
	}
	return p
}

type tokenOpts struct {
	issuer, audience string
	expires          time.Time
	key              *rsa.PrivateKey
}

func (p *fakeIdP) token(sub, name, email string, groups []string, o tokenOpts) string {
	p.t.Helper()
	if o.issuer == "" {
		o.issuer = p.issuer
	}
	if o.audience == "" {
		o.audience = "rvav"
	}
	if o.expires.IsZero() {
		o.expires = time.Now().Add(5 * time.Minute)
	}
	if o.key == nil {
		o.key = p.key
	}
	sig, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: o.key},
		(&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", "k1"))
	if err != nil {
		p.t.Fatal(err)
	}
	tok, err := jwt.Signed(sig).Claims(map[string]any{
		"iss": o.issuer, "sub": sub, "aud": o.audience, "azp": "rvav",
		"exp": o.expires.Unix(), "iat": time.Now().Unix(),
		"name": name, "email": email, "title": "Engineer", "groups": groups,
	}).Serialize()
	if err != nil {
		p.t.Fatal(err)
	}
	return tok
}

// TestOIDCTokens checks access tokens as the API does in production: the
// signature, issuer, audience and expiry, groups becoming roles, and an
// identity provider that is down when the API starts or when its keys are
// fetched.
func TestOIDCTokens(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		// serve runs the API with o and returns calls to /me and /readyz.
		serve := func(o *auth.OIDC) (func(token string) (int, api.Me, http.Header), func() int) {
			o.RetryInterval = time.Millisecond
			srv := httptest.NewServer((&httpapi.Server{Store: h.store, Core: h.svc, Hub: h.hub, Log: discardLog(),
				AuthMode: "oidc", Auth: o, Heartbeat: time.Second}).Handler())
			t.Cleanup(srv.Close)
			get := func(path, token string) *http.Response {
				req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+path, nil)
				if token != "" {
					req.Header.Set("Authorization", "Bearer "+token)
				}
				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				return resp
			}
			me := func(token string) (int, api.Me, http.Header) {
				resp := get("/api/v1/me", token)
				defer resp.Body.Close()
				var m api.Me
				_ = json.NewDecoder(resp.Body).Decode(&m)
				return resp.StatusCode, m, resp.Header
			}
			ready := func() int {
				resp := get("/readyz", "")
				resp.Body.Close()
				return resp.StatusCode
			}
			return me, ready
		}

		// The provider is still starting: the API comes up, but is not ready.
		idp := newFakeIdP(t, "")
		o, err := auth.NewOIDC(ctx, h.store, idp.srv.URL, "", "rvav", "groups")
		if err == nil {
			t.Fatal("NewOIDC should report the unreachable provider")
		}
		me, ready := serve(o)
		if code := ready(); code != http.StatusServiceUnavailable {
			t.Fatalf("readyz with the provider down = %d", code)
		}
		lea := idp.token("kc-lea", "Léa Martin", "lea.martin@corp.example", []string{"/rvav-web-admins"}, tokenOpts{})
		if code, _, hdr := me(lea); code != http.StatusServiceUnavailable || hdr.Get("Retry-After") == "" {
			t.Fatalf("provider down: %d, Retry-After %q", code, hdr.Get("Retry-After"))
		}

		// Once it answers, the same token works, and groups become roles.
		idp.up.Store(true)
		time.Sleep(5 * time.Millisecond) // past the retry interval
		if code := ready(); code != http.StatusNoContent {
			t.Fatalf("readyz with the provider up = %d", code)
		}
		code, m, _ := me(lea)
		if code != 200 || m.User.Name != "Léa Martin" || len(m.Projects) != 1 ||
			m.Projects[0].Project.Id != "prj_web" || m.Projects[0].Role != "project-admin" {
			t.Fatalf("lea: %d %+v", code, m)
		}
		// Dev authentication on the same database still finds the seeded
		// user that this sign-in linked to its OIDC subject.
		devReq := httptest.NewRequestWithContext(ctx, "GET", "/api/v1/me", nil)
		devReq.Header.Set(auth.DevUserHeader, "lmartin")
		if p, err := (&auth.Dev{Store: h.store, DefaultUser: "lmartin"}).Authenticate(devReq); err != nil || p.User.Name != "Léa Martin" {
			t.Fatalf("dev user after OIDC sign-in: %+v %v", p, err)
		}
		marc := idp.token("kc-marc", "Marc Keller", "marc.keller@corp.example", []string{"/rvav-admins"}, tokenOpts{})
		if code, m, _ := me(marc); code != 200 || !slices.Contains(m.GlobalRoles, api.MeGlobalRoles("admin")) {
			t.Fatalf("marc: %d %+v", code, m.GlobalRoles)
		}

		other, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Fatal(err)
		}
		for name, tok := range map[string]string{
			"another client": idp.token("kc-lea", "Léa Martin", "", nil, tokenOpts{audience: "grafana"}),
			"expired":        idp.token("kc-lea", "Léa Martin", "", nil, tokenOpts{expires: time.Now().Add(-time.Minute)}),
			"wrong key":      idp.token("kc-lea", "Léa Martin", "", nil, tokenOpts{key: other}),
			"another issuer": idp.token("kc-lea", "Léa Martin", "", nil, tokenOpts{issuer: "https://evil.example"}),
			"not a token":    "abc.def.ghi",
			"bad API token":  auth.TokenPrefix + "nope",
		} {
			if code, _, _ := me(tok); code != http.StatusUnauthorized {
				t.Errorf("%s: %d, want 401", name, code)
			}
		}

		// Keys that can't be fetched mean the provider is down, not a bad token.
		idp2 := newFakeIdP(t, "")
		idp2.up.Store(true)
		idp2.keysDown.Store(true)
		o2, err := auth.NewOIDC(ctx, h.store, idp2.srv.URL, "", "rvav", "groups")
		if err != nil {
			t.Fatal(err)
		}
		me2, _ := serve(o2)
		tok2 := idp2.token("kc-lea", "Léa Martin", "", []string{"/rvav-web-admins"}, tokenOpts{})
		if code, _, _ := me2(tok2); code != http.StatusServiceUnavailable {
			t.Fatalf("keys down: %d, want 503", code)
		}
		idp2.keysDown.Store(false)
		if code, _, _ := me2(tok2); code != 200 {
			t.Fatalf("keys back: %d", code)
		}

		// As in docker compose: tokens name the issuer the browser sees, and
		// the API discovers the provider at another URL.
		const browserIssuer = "http://localhost:8081/realms/rvav"
		idp3 := newFakeIdP(t, browserIssuer)
		idp3.up.Store(true)
		o3, err := auth.NewOIDC(ctx, h.store, browserIssuer, idp3.srv.URL, "rvav", "groups")
		if err != nil {
			t.Fatal(err)
		}
		me3, _ := serve(o3)
		if code, _, _ := me3(idp3.token("kc-lea", "Léa Martin", "", nil, tokenOpts{})); code != 200 {
			t.Fatalf("discovery URL differs from the issuer: %d", code)
		}
		if code, _, _ := me3(idp3.token("kc-lea", "Léa Martin", "", nil, tokenOpts{issuer: idp3.srv.URL})); code != http.StatusUnauthorized {
			t.Fatalf("token with the internal URL as issuer: %d, want 401", code)
		}
	})
}

func discardLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
