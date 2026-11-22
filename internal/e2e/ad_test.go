package e2e

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/integrations/ad/adtest"
	"github.com/damhau/rvav/internal/trust"
)

// TestActiveDirectoryConnection: the Active Directory integration binds
// over LDAPS or StartTLS, its certificate shown and pinned as any
// connection's; plain LDAP only when ticked, and the saved password then
// asked for again (ADR-0064).
func TestActiveDirectoryConnection(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin = "mkeller"
		dir := adtest.Start(t, nil)
		settings := func(url string, extra map[string]any) map[string]any {
			m := map[string]any{"url": url, "username": dir.BindDN, "base_dn": adtest.BaseDN}
			for k, v := range extra {
				m[k] = v
			}
			return m
		}
		var test api.IntegrationTest
		for _, url := range []string{dir.LDAPS, dir.StartTLS} {
			h.json("POST", "/api/v1/admin/integrations/ad/test", admin, api.IntegrationWrite{Settings: settings(url, nil),
				Secret: new(dir.Password)}, 200, &test)
			if test.Ok || test.Certificate == nil || test.Certificate.Trusted || !test.Certificate.Pinnable ||
				!strings.HasSuffix(url, test.Certificate.Address) || len(test.Checks) != 1 {
				t.Fatalf("%s untrusted = %+v", url, test)
			}
			pin := test.Certificate.Sha256
			h.json("POST", "/api/v1/admin/integrations/ad/test", admin, api.IntegrationWrite{Settings: settings(url,
				map[string]any{"cert_sha256": pin}), Secret: new(dir.Password)}, 200, &test)
			if !test.Ok || !test.Certificate.Pinned || !slices.ContainsFunc(test.Checks, func(c api.IntegrationCheck) bool {
				return c.Name == "Bind" && strings.Contains(c.Message, "read "+adtest.BaseDN)
			}) {
				t.Fatalf("%s pinned = %+v", url, test)
			}
		}
		var problem struct{ Errors []api.FieldError }
		h.json("PUT", "/api/v1/admin/integrations/ad", admin, api.IntegrationWrite{Settings: settings("https://dc01.corp.example", nil)},
			422, &problem)
		if !slices.Contains(fieldsOf(problem.Errors), "/settings/url") {
			t.Fatalf("an https URL = %v", problem.Errors)
		}
		var saved api.Integration
		h.json("PUT", "/api/v1/admin/integrations/ad", admin, api.IntegrationWrite{Settings: settings(dir.StartTLS,
			map[string]any{"cert_sha256": trust.Fingerprint(dir.Cert)}), Secret: new(dir.Password)}, 200, &saved)
		if !saved.Configured || !saved.SecretSet {
			t.Fatalf("saved = %+v", saved)
		}
		// Plain LDAP: the password would go in the clear, so it is given again.
		h.json("PUT", "/api/v1/admin/integrations/ad", admin, api.IntegrationWrite{Settings: map[string]any{"plain_ldap": true}},
			422, &problem)
		if !slices.Contains(fieldsOf(problem.Errors), "/secret") {
			t.Fatalf("plain LDAP without the password = %v", problem.Errors)
		}
		h.json("POST", "/api/v1/admin/integrations/ad/test", admin, nil, 200, &test)
		if !test.Ok || test.Certificate == nil || !test.Certificate.Pinned {
			t.Fatalf("saved test = %+v", test)
		}
	})
}
