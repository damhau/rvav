package e2e

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/config"
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

// directoryConfig is the integration's settings for an adtest server.
func directoryConfig(dir *adtest.Server) config.ADConfig {
	return config.ADConfig{URL: dir.LDAPS, Username: dir.BindDN, Password: dir.Password, BaseDN: adtest.BaseDN,
		CertSHA256: trust.Fingerprint(dir.Cert)}
}

// TestActiveDirectoryPlacements: OUs and groups are given to a project, an
// environment, both or neither, the most specific applying; checked as
// DNs under the base DN, and in the directory once there is one
// (ADR-0064).
func TestActiveDirectoryPlacements(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin = "mkeller"
		web, dev, prd := new("prj_web"), new(api.EnvironmentKey("dev")), new(api.EnvironmentKey("prd"))
		var problem struct{ Errors []api.FieldError }

		// No directory yet: DNs are checked as DNs, relative ones kept.
		var both, project, env, all api.AdPlacement
		h.json("POST", "/api/v1/admin/ad-placements", admin, api.AdPlacementWrite{ProjectId: web, Environment: dev,
			Ou: "OU=Dev,OU=Web", Groups: &[]string{"CN=Web servers,OU=Groups"}, AllowBlueprintOu: new(true)}, 201, &both)
		if both.OuDn != "OU=Dev,OU=Web" || !both.AllowBlueprintOu || deref(both.ProjectName) != "Web Platform" {
			t.Fatalf("created = %+v", both)
		}
		h.json("POST", "/api/v1/admin/ad-placements", admin, api.AdPlacementWrite{ProjectId: web, Environment: dev, Ou: "OU=Other"},
			409, nil)
		h.json("POST", "/api/v1/admin/ad-placements", admin, api.AdPlacementWrite{ProjectId: web, Ou: "Servers",
			Groups: &[]string{"OU=x,,"}}, 422, &problem)
		if f := fieldsOf(problem.Errors); !slices.Equal(f, []string{"/ou", "/groups/0"}) {
			t.Fatalf("not DNs = %v", f)
		}
		h.json("POST", "/api/v1/admin/ad-placements", admin, api.AdPlacementWrite{ProjectId: new("prj_fin"),
			Environment: new(api.EnvironmentKey("sbx")), Ou: "OU=Fin"}, 422, &problem)
		if f := fieldsOf(problem.Errors); !slices.Contains(f, "/environment") {
			t.Fatalf("an environment the project lacks = %v", f)
		}
		h.json("POST", "/api/v1/admin/ad-placements", admin, api.AdPlacementWrite{ProjectId: web, Ou: "OU=Web"}, 201, &project)
		h.json("POST", "/api/v1/admin/ad-placements", admin, api.AdPlacementWrite{Environment: prd, Ou: "OU=Production"}, 201, &env)
		h.json("POST", "/api/v1/admin/ad-placements", admin, api.AdPlacementWrite{Ou: "OU=Servers"}, 201, &all)
		h.json("POST", "/api/v1/admin/ad-placements", admin, api.AdPlacementWrite{Ou: "OU=Again"}, 409, nil)

		// The most specific row applies.
		for _, c := range []struct{ project, env, want string }{
			{"prj_web", "dev", both.Id}, {"prj_web", "prd", project.Id}, {"prj_fin", "prd", env.Id}, {"prj_fin", "dev", all.Id},
		} {
			got, err := h.store.ADPlacementFor(ctx, c.project, c.env)
			if err != nil || got == nil || got.ID != c.want {
				t.Errorf("%s in %s = %+v, %v; want %s", c.project, c.env, got, err, c.want)
			}
		}

		// A project's rows and those of every project, for whoever reads its
		// settings; every project's only for those who read them all.
		var list api.AdPlacementList
		h.json("GET", "/api/v1/admin/ad-placements?project_id=prj_web", "lmartin", nil, 200, &list)
		if len(list.Items) != 4 {
			t.Fatalf("prj_web's = %+v", list.Items)
		}
		h.json("GET", "/api/v1/admin/ad-placements?project_id=prj_fin", admin, nil, 200, &list)
		if len(list.Items) != 2 || list.Items[0].ProjectId != nil {
			t.Fatalf("prj_fin's = %+v", list.Items)
		}
		h.json("GET", "/api/v1/admin/ad-placements", "lmartin", nil, 403, nil)
		h.json("GET", "/api/v1/admin/ad-placements", admin, nil, 200, &list)
		if len(list.Items) != 4 {
			t.Fatalf("all = %+v", list.Items)
		}
		// Managed by those who manage placement.
		h.json("POST", "/api/v1/admin/ad-placements", "lmartin", api.AdPlacementWrite{ProjectId: web, Environment: prd,
			Ou: "OU=Prod,OU=Web"}, 403, nil)
		h.json("DELETE", "/api/v1/admin/ad-placements/"+all.Id, "lmartin", nil, 403, nil)

		// A directory: DNs resolved under its base DN, and looked for there.
		dir := adtest.Start(t, []string{"OU=Web," + adtest.BaseDN, "OU=Dev,OU=Web," + adtest.BaseDN},
			"CN=Web servers,OU=Groups,"+adtest.BaseDN)
		h.svc.Config.AD = directoryConfig(dir)
		h.json("PUT", "/api/v1/admin/ad-placements/"+both.Id, admin, api.AdPlacementWrite{ProjectId: web, Environment: dev,
			Ou: "OU=Dev,OU=Web", Groups: &[]string{"CN=Web servers,OU=Groups", "CN=Nobody,OU=Groups"}}, 422, &problem)
		if len(problem.Errors) != 1 || problem.Errors[0].Field != "/groups/1" ||
			!strings.Contains(problem.Errors[0].Message, "has no CN=Nobody,OU=Groups,"+adtest.BaseDN) {
			t.Fatalf("a group the directory lacks = %+v", problem.Errors)
		}
		h.json("PUT", "/api/v1/admin/ad-placements/"+both.Id, admin, api.AdPlacementWrite{ProjectId: web, Environment: dev,
			Ou: "OU=Dev,OU=Web,DC=other,DC=example"}, 422, &problem)
		if f := fieldsOf(problem.Errors); !slices.Equal(f, []string{"/ou"}) {
			t.Fatalf("another domain = %v", f)
		}
		h.json("PUT", "/api/v1/admin/ad-placements/"+both.Id, admin, api.AdPlacementWrite{ProjectId: web, Environment: dev,
			Ou: "OU=Dev,OU=Web", Groups: &[]string{"CN=Web servers,OU=Groups"}}, 200, &both)
		if both.OuDn != "OU=Dev,OU=Web,"+adtest.BaseDN || both.AllowBlueprintOu {
			t.Fatalf("changed = %+v", both)
		}
		h.json("DELETE", "/api/v1/admin/ad-placements/"+all.Id, admin, nil, 204, nil)
		h.json("DELETE", "/api/v1/admin/ad-placements/"+all.Id, admin, nil, 404, nil)
		if got, err := h.store.ADPlacementFor(ctx, "prj_fin", "dev"); got != nil || err != nil {
			t.Fatalf("none left for prj_fin in dev = %+v, %v", got, err)
		}
	})
}
