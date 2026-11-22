package e2e

import (
	"context"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/integrations/servicenow"
	"github.com/damhau/rvav/internal/trust"
)

// snowInstance is a fake ServiceNow instance over HTTPS.
func snowInstance(t *testing.T) (*servicenow.Fake, *httptest.Server) {
	t.Helper()
	f := servicenow.NewFake()
	srv := httptest.NewTLSServer(f)
	t.Cleanup(srv.Close)
	return f, srv
}

// TestServiceNowConnection: the ServiceNow integration takes an https URL
// only, signs in as a user or an OAuth client, and its test reads a
// requested item; its certificate is shown and pinned as any connection's
// (ADR-0065).
func TestServiceNowConnection(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin = "mkeller"
		snow, srv := snowInstance(t)
		basic := map[string]any{"url": srv.URL, "auth": "basic", "username": snow.Username}
		with := func(m map[string]any, extra map[string]any) map[string]any {
			out := map[string]any{}
			for k, v := range m {
				out[k] = v
			}
			for k, v := range extra {
				out[k] = v
			}
			return out
		}
		var test api.IntegrationTest
		h.json("POST", "/api/v1/admin/integrations/servicenow/test", admin, api.IntegrationWrite{Settings: basic,
			Secret: new(snow.Password)}, 200, &test)
		if test.Ok || test.Certificate == nil || test.Certificate.Trusted || !test.Certificate.Pinnable {
			t.Fatalf("untrusted = %+v", test)
		}
		pin := test.Certificate.Sha256
		pinned := with(basic, map[string]any{"cert_sha256": pin})
		h.json("POST", "/api/v1/admin/integrations/servicenow/test", admin, api.IntegrationWrite{Settings: pinned,
			Secret: new(snow.Password)}, 200, &test)
		if !test.Ok || !test.Certificate.Pinned || !slices.ContainsFunc(test.Checks, func(c api.IntegrationCheck) bool {
			return c.Name == "Requested items" && strings.Contains(c.Message, "as "+snow.Username)
		}) {
			t.Fatalf("pinned = %+v", test)
		}
		oauth := with(pinned, map[string]any{"auth": "oauth", "client_id": snow.ClientID})
		h.json("POST", "/api/v1/admin/integrations/servicenow/test", admin, api.IntegrationWrite{Settings: oauth,
			Secret: new(snow.ClientSecret)}, 200, &test)
		if !test.Ok || !slices.ContainsFunc(test.Checks, func(c api.IntegrationCheck) bool {
			return strings.Contains(c.Message, "the OAuth client "+snow.ClientID)
		}) {
			t.Fatalf("oauth = %+v", test)
		}
		h.json("POST", "/api/v1/admin/integrations/servicenow/test", admin, api.IntegrationWrite{Settings: oauth,
			Secret: new("wrong")}, 200, &test)
		if test.Ok || !strings.Contains(test.Checks[len(test.Checks)-1].Message, "OAuth client was refused") {
			t.Fatalf("a wrong client secret = %+v", test)
		}

		var problem struct{ Errors []api.FieldError }
		h.json("PUT", "/api/v1/admin/integrations/servicenow", admin, api.IntegrationWrite{Settings: with(basic,
			map[string]any{"url": "http://corp.service-now.com", "auth": "kerberos"})}, 422, &problem)
		if f := fieldsOf(problem.Errors); !slices.Contains(f, "/settings/url") || !slices.Contains(f, "/settings/auth") {
			t.Fatalf("plain http and an unknown sign-in = %v", problem.Errors)
		}
		var saved api.Integration
		h.json("PUT", "/api/v1/admin/integrations/servicenow", admin, api.IntegrationWrite{Settings: with(basic,
			map[string]any{"cert_sha256": trust.Fingerprint(srv.Certificate())}), Secret: new(snow.Password)}, 200, &saved)
		if !saved.Configured || !saved.SecretSet || saved.Applying {
			t.Fatalf("saved = %+v", saved)
		}
		// The password only goes to the instance it was given for.
		h.json("PUT", "/api/v1/admin/integrations/servicenow", admin, api.IntegrationWrite{Settings: map[string]any{
			"url": "https://other.service-now.com"}}, 422, &problem)
		if !slices.Contains(fieldsOf(problem.Errors), "/secret") {
			t.Fatalf("a new URL without the password = %v", problem.Errors)
		}
		h.json("POST", "/api/v1/admin/integrations/servicenow/test", admin, nil, 200, &test)
		if !test.Ok || test.Certificate == nil || !test.Certificate.Pinned {
			t.Fatalf("saved test = %+v", test)
		}
	})
}
