package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
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

// TestIdentityProvider is the provider set in Administration (ADR-0030,
// U1.2): its test reads discovery and keys and lets the sign-in page reach
// it; a trial sign-in's token shows its claims and the roles it would give,
// saving nothing; saved, it signs users in at once, without a restart, and
// the web app reads it from the API; changed or cleared, the earlier
// provider's tokens are refused; an environment variable wins; and a
// provider set in the UI doesn't hold readiness, so the local
// administrator can still correct it.
func TestIdentityProvider(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin = "mkeller"
		live := &core.LiveSettings{Store: h.store, Secrets: h.svc.Secrets, Base: h.svc.Config, Log: discardLog()}
		h.svc.Live = live
		o, err := auth.NewLiveOIDC(ctx, h.store, false, live.Provider)
		if err != nil {
			t.Fatalf("no provider yet: %v", err)
		}
		o.RetryInterval = time.Millisecond
		srv := httptest.NewServer((&httpapi.Server{Store: h.store, Core: h.svc, Hub: h.hub, Log: discardLog(),
			AuthMode: "oidc", Auth: o, Heartbeat: time.Second}).Handler())
		t.Cleanup(srv.Close)
		b := newBrowser(t, ctx, srv.URL)
		options := func() (api.AuthOptions, string) {
			t.Helper()
			code, body, hdr := b.do("GET", "/api/v1/auth/options", nil, true)
			var out api.AuthOptions
			if code != http.StatusOK || json.Unmarshal(body, &out) != nil {
				t.Fatalf("GET /auth/options = %d %s", code, body)
			}
			if hdr.Get("Cache-Control") != "no-store" {
				t.Errorf("/auth/options is cached: %q", hdr.Get("Cache-Control"))
			}
			return out, hdr.Get("X-Rvav-Idp-Origins")
		}
		me := func(token string) int {
			t.Helper()
			code, _, _ := newBrowser(t, ctx, srv.URL).do("GET", "/api/v1/me", nil, true, "Authorization", "Bearer "+token)
			return code
		}

		idp := newFakeIdP(t, "")
		idp.up.Store(true)
		origin := auth.Origin(idp.issuer)
		token := idp.token("u1-sub-1", "Ana Lima", "ana.lima@corp.example", []string{"u1-ops", "u1-auditors"}, tokenOpts{})

		// No provider: the sign-in page offers the local administrator only.
		if opts, origins := options(); opts.Provider != nil || origins != "" {
			t.Fatalf("without a provider: %+v, origins %q", opts.Provider, origins)
		}
		if code := me(token); code != http.StatusUnauthorized {
			t.Fatalf("a provider's token without a provider = %d", code)
		}

		// The test: discovery and keys, with settings not saved yet. Once
		// they answer, the page may reach the provider, for its trial.
		var test api.IntegrationTest
		h.json("POST", "/api/v1/admin/integrations/oidc/test", admin, api.IntegrationWrite{Settings: map[string]any{
			"issuer": idp.issuer, "client_id": "rvav"}}, 200, &test)
		if !test.Ok || len(test.Checks) != 2 || test.Checks[0].Name != "Discovery" || test.Checks[1].Name != "Signing keys" ||
			!strings.Contains(test.Checks[1].Message, "1 published: k1 (RS256)") {
			t.Fatalf("test of a working provider: %+v", test)
		}
		if opts, origins := options(); opts.Provider != nil || origins != origin {
			t.Fatalf("after the test: provider %+v, origins %q, want %q for the trial", opts.Provider, origins, origin)
		}
		h.json("POST", "/api/v1/admin/integrations/oidc/test", admin, api.IntegrationWrite{Settings: map[string]any{
			"issuer": "http://127.0.0.1:1/realms/none", "client_id": "rvav"}}, 200, &test)
		if test.Ok || len(test.Checks) != 1 || test.Checks[0].Name != "Discovery" {
			t.Fatalf("test of an unreachable provider: %+v", test)
		}
		h.json("POST", "/api/v1/admin/integrations/oidc/test", admin, api.IntegrationWrite{Settings: map[string]any{
			"issuer": idp.issuer, "client_id": ""}}, 200, &test)
		if test.Ok || test.Checks[0].Name != "Client" {
			t.Fatalf("test without a client ID: %+v", test)
		}
		h.json("POST", "/api/v1/admin/integrations/oidc/test", admin, api.IntegrationWrite{Settings: map[string]any{
			"issuer": idp.issuer, "scopes": "profile email"}}, 422, nil)
		h.json("POST", "/api/v1/admin/integrations/oidc/test", admin, api.IntegrationWrite{Settings: map[string]any{
			"issuer": "login.example"}}, 422, nil)

		// The trial: what rvav reads from a token, and the roles the group
		// mappings give. No user is made.
		var web api.AdminProject
		h.json("GET", "/api/v1/admin/projects/prj_web", admin, nil, 200, &web)
		h.json("POST", "/api/v1/admin/group-mappings", admin, api.GroupMappingWrite{OidcGroup: "u1-ops", ProjectId: &web.Id, Role: "member"}, 201, nil)
		h.json("POST", "/api/v1/admin/group-mappings", admin, api.GroupMappingWrite{OidcGroup: "u1-auditors", Role: "auditor"}, 201, nil)
		trial := func(settings map[string]any, token string) api.OidcTrial {
			t.Helper()
			var out api.OidcTrial
			h.json("POST", "/api/v1/admin/integrations/oidc/trial", admin, api.OidcTrialRequest{Settings: settings, AccessToken: &token}, 200, &out)
			return out
		}
		given := map[string]any{"issuer": idp.issuer, "client_id": "rvav", "groups_claim": "groups"}
		tr := trial(given, token)
		if !tr.Accepted || tr.Message != nil || deref(tr.Subject) != "u1-sub-1" || deref(tr.Name) != "Ana Lima" ||
			!slices.Equal(tr.Groups, []string{"u1-ops", "u1-auditors"}) || !slices.Equal(tr.GlobalRoles, []string{"auditor"}) ||
			len(tr.ProjectRoles) != 1 || tr.ProjectRoles[0].Project.Id != "prj_web" || tr.ProjectRoles[0].Role != "member" ||
			tr.Claims["email"] != "ana.lima@corp.example" {
			t.Fatalf("trial of a good token: %+v", tr)
		}
		var users int
		if err := h.store.Pool.QueryRow(ctx, `select count(*) from users where subject like '%u1-sub-1'`).Scan(&users); err != nil || users != 0 {
			t.Fatalf("the trial made a user: %d %v", users, err)
		}
		// Refused: why, with the claims, and what to change.
		tr = trial(given, idp.token("u1-sub-1", "Ana Lima", "", nil, tokenOpts{audience: "account"}))
		if tr.Accepted || !strings.Contains(deref(tr.Message), "expected audience") || !strings.Contains(deref(tr.Message), "audience mapper") ||
			tr.Claims["aud"] != "account" || len(tr.GlobalRoles) != 0 {
			t.Fatalf("trial of a token for another audience: %+v", tr)
		}
		tr = trial(given, idp.token("u1-sub-1", "Ana Lima", "", nil, tokenOpts{audience: "00000003-0000-0000-c000-000000000000"}))
		if tr.Accepted || !strings.Contains(deref(tr.Message), "Microsoft Graph") {
			t.Fatalf("trial of a Graph token: %+v", tr)
		}
		tr = trial(given, "opaque-token")
		if tr.Accepted || !strings.Contains(deref(tr.Message), "not a JWT") {
			t.Fatalf("trial of an opaque token: %+v", tr)
		}
		tr = trial(map[string]any{"issuer": idp.issuer, "client_id": "rvav", "groups_claim": "roles"}, idp.token("u1-sub-1", "Ana Lima", "", []string{"u1-ops"}, tokenOpts{}))
		if !strings.Contains(deref(tr.Message), "no roles claim") {
			t.Fatalf("trial with a claim the token lacks: %+v", tr)
		}
		h.json("POST", "/api/v1/admin/integrations/oidc/trial", admin, api.OidcTrialRequest{Settings: given}, 422, nil)
		h.json("POST", "/api/v1/admin/integrations/oidc/trial", "tnguyen", api.OidcTrialRequest{Settings: given, AccessToken: &token}, 403, nil)

		// Saved: users sign in at once, and the web app reads it.
		h.json("PUT", "/api/v1/admin/integrations/oidc", admin, api.IntegrationWrite{Settings: map[string]any{"issuer": idp.issuer,
			"client_id": "rvav", "scopes": "openid profile email offline_access"}, Secret: new("a-secret")}, 422, nil)
		var it api.Integration
		h.json("PUT", "/api/v1/admin/integrations/oidc", admin, api.IntegrationWrite{Settings: map[string]any{"issuer": idp.issuer,
			"client_id": "rvav", "scopes": "openid profile email offline_access", "groups_claim": "groups"}}, 200, &it)
		if !it.Configured || it.Applying || it.SecretSet {
			t.Fatalf("saved provider: %+v", it)
		}
		if code := me(token); code != http.StatusOK {
			t.Fatalf("a provider's token once saved, without a restart = %d", code)
		}
		opts, origins := options()
		if opts.Provider == nil || opts.Provider.Authority != idp.issuer || opts.Provider.ClientId != "rvav" ||
			opts.Provider.Scope != "openid profile email offline_access" || origins != origin {
			t.Fatalf("options once saved: %+v, origins %q", opts.Provider, origins)
		}
		// A provider set in the UI doesn't hold readiness: the local
		// administrator can sign in while it is down.
		idp.up.Store(false)
		if code, _, _ := b.do("GET", "/readyz", nil, true); code != http.StatusNoContent {
			t.Fatalf("/readyz with the provider saved in the UI down = %d", code)
		}
		idp.up.Store(true)

		// Another provider: the first one's tokens no longer sign in.
		idp2 := newFakeIdP(t, "")
		idp2.up.Store(true)
		h.json("PUT", "/api/v1/admin/integrations/oidc", admin, api.IntegrationWrite{Settings: map[string]any{"issuer": idp2.issuer,
			"client_id": "rvav"}}, 200, nil)
		if code := me(token); code != http.StatusUnauthorized {
			t.Fatalf("the earlier provider's token after a change = %d", code)
		}
		if code := me(idp2.token("u1-sub-2", "Ben Ito", "", nil, tokenOpts{})); code != http.StatusOK {
			t.Fatalf("the new provider's token = %d", code)
		}
		// Cleared: only the local administrator and API tokens sign in.
		h.json("PUT", "/api/v1/admin/integrations/oidc", admin, api.IntegrationWrite{Settings: map[string]any{"issuer": ""}}, 200, nil)
		if code := me(idp2.token("u1-sub-2", "Ben Ito", "", nil, tokenOpts{})); code != http.StatusUnauthorized {
			t.Fatalf("a token once the provider is cleared = %d", code)
		}
		if opts, _ := options(); opts.Provider != nil {
			t.Fatalf("options once cleared: %+v", opts.Provider)
		}

		// RVAV_OIDC_ISSUER wins, and its field is locked.
		t.Setenv("RVAV_OIDC_ISSUER", idp.issuer)
		live.Base.Auth.OIDCIssuer = idp.issuer
		h.json("PUT", "/api/v1/admin/integrations/oidc", admin, api.IntegrationWrite{Settings: map[string]any{"issuer": idp2.issuer, "client_id": "rvav"}}, 200, &it)
		if !slices.Contains(it.Locked, "issuer") || it.Settings["issuer"] != idp.issuer {
			t.Fatalf("the issuer set by the environment: %+v", it)
		}
		if code := me(token); code != http.StatusOK {
			t.Fatalf("a token of the provider the environment sets = %d", code)
		}

		for action, want := range map[string]int{"settings.integration.oidc": 4, "settings.test.oidc": 3, "settings.trial.oidc": 5} {
			if n := h.auditCount(action); n != want {
				t.Errorf("audit %s: %d, want %d", action, n, want)
			}
		}
	})
}
