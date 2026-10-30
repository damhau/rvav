package e2e

import (
	"context"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/integrations/nsx"
)

// TestNSXIntegration: the NSX Manager set from Administration ›
// Integrations, its password sealed, and tested (N1.1, ADR-0028); the
// notification channels' settings saved too, which the database refused
// until N1.1.
func TestNSXIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin = "mkeller"
		fake := httptest.NewServer(nsx.NewFakeServer())
		defer fake.Close()

		var it api.Integration
		h.json("PUT", "/api/v1/admin/integrations/nsx", admin, api.IntegrationWrite{Settings: map[string]any{"url": fake.URL,
			"username": nsx.FakeUser}, Secret: new(nsx.FakePassword)}, 200, &it)
		if it.Kind != "nsx" || !it.SecretSet || it.Settings["url"] != fake.URL || it.Settings["password"] != nil {
			t.Fatalf("saved nsx = %+v", it)
		}
		var list api.IntegrationList
		h.json("GET", "/api/v1/admin/integrations", admin, nil, 200, &list)
		if !slices.ContainsFunc(list.Items, func(x api.Integration) bool { return x.Kind == "nsx" && x.Configured }) {
			t.Fatalf("integrations = %+v", list.Items)
		}

		var test api.IntegrationTest
		h.json("POST", "/api/v1/admin/integrations/nsx/test", admin, nil, 200, &test)
		if !test.Ok || len(test.Checks) != 4 || test.Checks[0].Message != "Signed in to NSX 4.1.2.0.0.22589037." ||
			test.Checks[1].Message != "3 Tier-1 gateways, for segments made for deployments." {
			t.Fatalf("NSX test = %+v", test)
		}
		h.json("POST", "/api/v1/admin/integrations/nsx/test", admin, api.IntegrationWrite{Settings: map[string]any{"url": fake.URL,
			"username": nsx.FakeUser}, Secret: new("wrong")}, 200, &test)
		if test.Ok || test.Checks[0].Name != "Sign in" || test.Checks[0].Ok {
			t.Fatalf("a wrong password tested fine: %+v", test)
		}
		h.json("PUT", "/api/v1/admin/integrations/nsx", admin, api.IntegrationWrite{Settings: map[string]any{"tier0": "x"}}, 422, nil)

		// Email and Teams settings are saved.
		h.json("PUT", "/api/v1/admin/integrations/smtp", admin, api.IntegrationWrite{Settings: map[string]any{
			"host": "smtp.corp.example", "port": 587, "from": "rvav <rvav@corp.example>"}}, 200, &it)
		if it.Settings["host"] != "smtp.corp.example" {
			t.Fatalf("saved smtp = %+v", it)
		}
		h.json("PUT", "/api/v1/admin/integrations/teams", admin, api.IntegrationWrite{Settings: map[string]any{
			"app_id": "a1", "tenant_id": "t1"}, Secret: new("s")}, 200, nil)
	})
}

// TestPlacementSegments: a placement rule's segments made for deployments
// (N1.2): refused without an NSX Manager, or with a Tier-1 NSX doesn't
// have, a subnet size out of range or a DNS server that isn't an address;
// saved, read back, and cleared.
func TestPlacementSegments(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin = "mkeller"
		segments := &api.PlacementSegments{Tier1: "t1-gva-dev", IpBlock: "nsx-gva-dev", PrefixLength: 26, DnsZone: "corp.example",
			DnsServers: &[]string{"10.10.0.53"}}
		rule := api.PlacementRuleWrite{ProjectId: "prj_web", Environment: "dev", Clusters: []string{"GVA-DEV-*"},
			Datastores: []string{"vsan-gva-dev-*"}, Networks: []string{"dvpg-app-dev-*"}, Folder: "/GVA/vm/rvav/web/dev", Segments: segments}
		var problem struct{ Errors []api.FieldError }
		h.json("POST", "/api/v1/admin/placement-rules", admin, rule, 422, &problem)
		if len(problem.Errors) != 1 || problem.Errors[0].Field != "/segments/tier1" || deref(problem.Errors[0].Code) != "no_nsx" {
			t.Fatalf("without NSX = %+v", problem.Errors)
		}

		fake := httptest.NewServer(nsx.NewFakeServer())
		defer fake.Close()
		h.svc.Config.NSX.URL, h.svc.Config.NSX.Username, h.svc.Config.NSX.Password = fake.URL, nsx.FakeUser, nsx.FakePassword
		bad := *segments
		bad.Tier1, bad.PrefixLength, bad.DnsServers = "t1-nope", 30, &[]string{"ns1"}
		rule.Segments = &bad
		h.json("POST", "/api/v1/admin/placement-rules", admin, rule, 422, &problem)
		if fields := fieldsOf(problem.Errors); !slices.Equal(fields, []string{"/segments/prefix_length", "/segments/dns_servers/0", "/segments/tier1"}) {
			t.Fatalf("bad segments = %v", fields)
		}

		rule.Segments = segments
		var saved api.PlacementRule
		h.json("POST", "/api/v1/admin/placement-rules", admin, rule, 201, &saved)
		if saved.Segments == nil || saved.Segments.Tier1 != "t1-gva-dev" || saved.Segments.PrefixLength != 26 ||
			(*saved.Segments.DnsServers)[0] != "10.10.0.53" {
			t.Fatalf("saved = %+v", saved.Segments)
		}
		var rules api.PlacementRuleList
		h.json("GET", "/api/v1/admin/placement-rules?project_id=prj_web", admin, nil, 200, &rules)
		if i := slices.IndexFunc(rules.Items, func(r api.PlacementRule) bool { return r.Id == saved.Id }); i < 0 || rules.Items[i].Segments == nil {
			t.Fatalf("listed = %+v", rules.Items)
		}
		rule.Segments = nil
		var cleared api.PlacementRule
		h.json("PUT", "/api/v1/admin/placement-rules/"+saved.Id, admin, rule, 200, &cleared)
		if cleared.Segments != nil {
			t.Fatalf("cleared = %+v", cleared.Segments)
		}
	})
}
