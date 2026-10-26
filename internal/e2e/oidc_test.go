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
	up  atomic.Bool
}

func newFakeIdP(t *testing.T) *fakeIdP {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	p := &fakeIdP{t: t, key: key}
	p.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !p.up.Load() {
			http.Error(w, "starting", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"issuer": p.srv.URL, "jwks_uri": p.srv.URL + "/certs",
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
		o.issuer = p.srv.URL
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
// identity provider that is down when the API starts.
func TestOIDCTokens(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		idp := newFakeIdP(t)

		// The provider is still starting: the API comes up anyway.
		o, err := auth.NewOIDC(ctx, h.store, idp.srv.URL, "", "rvav", "groups")
		if err == nil {
			t.Fatal("NewOIDC should report the unreachable provider")
		}
		srv := httptest.NewServer((&httpapi.Server{Store: h.store, Core: h.svc, Hub: h.hub, Log: discardLog(),
			AuthMode: "oidc", Auth: o, Heartbeat: time.Second}).Handler())
		defer srv.Close()

		me := func(token string) (int, api.Me, http.Header) {
			req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"/api/v1/me", nil)
			req.Header.Set("Authorization", "Bearer "+token)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			var m api.Me
			_ = json.NewDecoder(resp.Body).Decode(&m)
			return resp.StatusCode, m, resp.Header
		}

		lea := idp.token("kc-lea", "Léa Martin", "lea.martin@corp.example", []string{"/rvav-web-admins"}, tokenOpts{})
		if code, _, hdr := me(lea); code != http.StatusServiceUnavailable || hdr.Get("Retry-After") == "" {
			t.Fatalf("provider down: %d, Retry-After %q", code, hdr.Get("Retry-After"))
		}

		// Once it answers, the same token works, and groups become roles.
		idp.up.Store(true)
		code, m, _ := me(lea)
		if code != 200 || m.User.Name != "Léa Martin" || len(m.Projects) != 1 ||
			m.Projects[0].Project.Id != "prj_web" || m.Projects[0].Role != "project-admin" {
			t.Fatalf("lea: %d %+v", code, m)
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
	})
}

func discardLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
