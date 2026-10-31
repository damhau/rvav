package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/app"
	"github.com/damhau/rvav/internal/integrations/awx"
	"github.com/damhau/rvav/internal/integrations/vsphere"
)

// TestU1Acceptance is U1's (ADR-0030), on rvav's own processes, each from
// app.Open and started again by its supervisor, on a new database given
// the install settings only:
//
//   - First run: the administrator signs in with the install password and
//     changes it, sets the identity provider (a fake one) and tests it,
//     maps groups, and sets vCenter (vcsim) and EfficientIP, with no
//     restart by hand. A user signing in through the provider requests a
//     VM, and it is provisioned.
//   - A change while running: vCenter is changed while a provisioning
//     runs. The running one finishes, and the next one uses the new
//     vCenter.
//   - Break-glass: with the identity provider down, the local
//     administrator still signs in.
func TestU1Acceptance(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	ts := startTemporal(t)
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		if err := vsphere.Bootstrap(ctx, c, vsphere.DefaultLayout()); err != nil {
			t.Fatal(err)
		}
		// AWX is the in-process fake, as in the fresh compose stack.
		in := startInstall(t, ctx, ts, map[string]string{"RVAV_FAKE_AWX": "1", "RVAV_FAKE_AWX_TASK_DELAY": "1s"})
		defer in.stop()
		in.until("the processes to record themselves", func() bool { return len(in.processes()) == 3 })
		in.ready()
		starts := func() map[string]int32 {
			out := map[string]int32{}
			for role, p := range in.procs {
				out[role] = p.starts.Load()
			}
			return out
		}

		// First run: the install password, changed.
		const password = "a password of my own"
		admin := newBrowser(t, ctx, in.url)
		if code, res, _ := admin.signIn("admin", installPassword); code != http.StatusOK || !res.PasswordChangeRequired {
			t.Fatalf("sign-in with the install password = %d %+v", code, res)
		}
		admin.json("GET", "/api/v1/admin/setup", nil, 403, nil)
		admin.json("POST", "/api/v1/auth/password", api.PasswordChange{CurrentPassword: installPassword, NewPassword: password}, 204, nil)

		// The identity provider, tested, then saved.
		idp := newFakeIdP(t, "")
		idp.up.Store(true)
		provider := map[string]any{"issuer": idp.issuer, "client_id": "rvav", "groups_claim": "groups"}
		var test api.IntegrationTest
		admin.json("POST", "/api/v1/admin/integrations/oidc/test", api.IntegrationWrite{Settings: provider}, 200, &test)
		if !test.Ok || len(test.Checks) != 2 {
			t.Fatalf("the provider's test = %+v", test)
		}
		admin.json("PUT", "/api/v1/admin/integrations/oidc", api.IntegrationWrite{Settings: provider}, 200, nil)

		// An environment and a project, and the provider's groups mapped.
		admin.json("POST", "/api/v1/admin/environments", api.AdminEnvironment{Key: "dev", Name: "Development", Order: 1}, 201, nil)
		var web api.AdminProject
		admin.json("POST", "/api/v1/admin/projects", api.AdminProjectCreate{Code: "web", Name: "Web", Environments: []string{"dev"}}, 201, &web)
		admin.json("POST", "/api/v1/admin/group-mappings", api.GroupMappingWrite{OidcGroup: "rvav-admins", Role: "admin"}, 201, nil)
		admin.json("POST", "/api/v1/admin/group-mappings", api.GroupMappingWrite{OidcGroup: "u1-ops", ProjectId: &web.Id, Role: "member"}, 201, nil)
		anaToken := idp.token("u1-ana", "Ana Lima", "ana.lima@corp.example", []string{"u1-ops"}, tokenOpts{})
		marcToken := idp.token("u1-marc", "Marc Weber", "marc.weber@corp.example", []string{"rvav-admins"}, tokenOpts{})
		var trial api.OidcTrial
		admin.json("POST", "/api/v1/admin/integrations/oidc/trial", api.OidcTrialRequest{Settings: provider, AccessToken: &anaToken}, 200, &trial)
		if !trial.Accepted || len(trial.GlobalRoles) != 0 || len(trial.ProjectRoles) != 1 || trial.ProjectRoles[0].Project.Id != web.Id ||
			trial.ProjectRoles[0].Role != "member" {
			t.Fatalf("the trial of Ana's token = %+v", trial)
		}
		admin.json("POST", "/api/v1/admin/integrations/oidc/trial", api.OidcTrialRequest{Settings: provider, AccessToken: &marcToken}, 200, &trial)
		if !trial.Accepted || !slices.Equal(trial.GlobalRoles, []string{"admin"}) {
			t.Fatalf("the trial of Marc's token = %+v", trial)
		}

		// The provider's users sign in at once, with the roles their groups
		// give: no process started again.
		as := func(token string) func(method, path string, body any, want int, out any) {
			b := newBrowser(t, ctx, in.url)
			return func(method, path string, body any, want int, out any) {
				t.Helper()
				code, raw, _ := b.do(method, path, body, true, "Authorization", "Bearer "+token, "Idempotency-Key", uuid.NewString())
				if code != want {
					t.Fatalf("%s %s = %d, want %d: %s", method, path, code, want, raw)
				}
				if out != nil {
					if err := json.Unmarshal(raw, out); err != nil {
						t.Fatalf("%s %s: %v: %s", method, path, err, raw)
					}
				}
			}
		}
		ana, marc := as(anaToken), as(marcToken)
		// Read on next use: once the API's two-second cache of the saved
		// settings has passed.
		accepted := time.Now()
		in.until("the provider's tokens to be accepted", func() bool {
			code, _, _ := newBrowser(t, ctx, in.url).do("GET", "/api/v1/me", nil, true, "Authorization", "Bearer "+anaToken)
			return code == http.StatusOK
		})
		if d := time.Since(accepted); d > 3*time.Second {
			t.Errorf("the provider's tokens were accepted after %s", d)
		}
		var me api.Me
		ana("GET", "/api/v1/me", nil, 200, &me)
		if len(me.Projects) != 1 || me.Projects[0].Project.Id != web.Id || me.Projects[0].Role != "member" || slices.Contains(me.GlobalRoles, api.MeGlobalRolesAdmin) {
			t.Fatalf("Ana = %+v", me)
		}
		marc("GET", "/api/v1/admin/setup", nil, 200, nil)
		for role, n := range starts() {
			if n != 1 {
				t.Fatalf("the %s started again (%d starts) for the provider's settings, read on next use", role, n)
			}
		}

		// vCenter, through the first of two ways to vcsim, and EfficientIP:
		// each process drains and its supervisor starts it again with them.
		p1, p2 := newTCPProxy(t, c.URL().Host), newTCPProxy(t, c.URL().Host)
		sdk := func(p *tcpProxy) string { u := *c.URL(); u.Host = p.addr(); u.User = nil; return u.String() }
		saveVCenter := func(url string) api.Integration {
			var it api.Integration
			admin.json("PUT", "/api/v1/admin/integrations/vcenter", api.IntegrationWrite{Settings: map[string]any{"url": url,
				"username": "user", "insecure": true, "name": "vc01.corp.example"}, Secret: new("pass")}, 200, &it)
			return it
		}
		saveVCenter(sdk(p1))
		fake, err := app.NewFakeEfficientIP(ctx, in.store)
		if err != nil {
			t.Fatal(err)
		}
		ipam := httptest.NewServer(fake)
		defer ipam.Close()
		var saved api.Integration
		admin.json("PUT", "/api/v1/admin/integrations/efficientip", api.IntegrationWrite{Settings: map[string]any{
			"url": ipam.URL, "username": "rvav", "dns_server": "ns1.corp.example"}, Secret: new("rvav")}, 200, &saved)
		in.applied(saved.Revision)
		in.until("the sync to read vcsim's inventory and EfficientIP's subnets", func() bool {
			var clusters, mappings int
			if err := in.store.Pool.QueryRow(ctx, `select (select count(*) from inv_clusters),
				(select count(*) from network_subnets where source = 'efficientip')`).Scan(&clusters, &mappings); err != nil {
				t.Fatal(err)
			}
			return clusters > 0 && mappings > 0
		})

		// A first catalog item, by an administrator from the provider.
		def := guestItem("tpl-rhel9.4-2026.10", map[string]any{"os_family": "linux", "domain": "corp.example"})
		def.Blueprint["bindings"].(map[string]any)["post_config"] = `["linux-baseline"]`
		def.Blueprint["awx_templates"] = map[string]any{"linux-baseline": map[string]any{"title": "Corporate baseline", "job_template": "linux-baseline"}}
		var item api.AdminCatalogItem
		marc("POST", "/api/v1/admin/catalog/categories", api.AdminCatalogCategory{Key: "compute", Name: "Compute"}, 201, nil)
		marc("POST", "/api/v1/admin/catalog/items", api.CatalogItemCreate{Slug: "rhel9", Name: "RHEL 9 server", Summary: "A Linux server.",
			Category: "compute", Type: api.VmBlueprint, Definition: &def}, 201, &item)
		marc("POST", "/api/v1/admin/catalog/items/rhel9/versions/"+item.DraftVersion.Id+"/publish", nil, 200, nil)
		marc("PUT", "/api/v1/admin/catalog/items/rhel9/entitlements", api.CatalogEntitlements{AllProjects: true, ProjectIds: []string{}}, 200, nil)

		// Where Web's VMs go in Development.
		marc("POST", "/api/v1/admin/placement-rules", api.PlacementRuleWrite{ProjectId: web.Id, Environment: "dev",
			Clusters: []string{"GVA-DEV-CL01"}, Datastores: []string{"vsan-gva-dev-cl01"}, Networks: []string{"dvpg-*-dev-*"},
			Folder: "/GVA/vm/rvav/web/dev"}, 201, nil)

		// Ana's VM, provisioned through the vCenter saved.
		submit := func(role string) api.Request {
			var r api.Request
			ana("POST", "/api/v1/requests", api.RequestDraft{CatalogItemId: item.Id, VersionId: item.DraftVersion.Id,
				ProjectId: web.Id, Environment: "dev", Inputs: map[string]any{"role": role, "cluster": "GVA-DEV-CL01",
					"datastore": "vsan-gva-dev-cl01", "network": "dvpg-app-dev-vl310"}}, 202, &r)
			return r
		}
		finished := func(id string) api.Request {
			t.Helper()
			var r api.Request
			in.until("request "+id+" to finish", func() bool {
				ana("GET", "/api/v1/requests/"+id, nil, 200, &r)
				return r.Status == api.RequestStatusSucceeded || r.Status == api.RequestStatusFailed
			})
			if r.Status != api.RequestStatusSucceeded {
				t.Fatalf("request %s = %s: %s", r.Number, r.Status, errorOf(r))
			}
			return r
		}
		first := finished(submit("app").Id)
		if p1.sent() == 0 {
			t.Fatal("the first VM was built without going through the vCenter saved")
		}
		if !vmExists(ctx, c, "web-dev-app01") {
			t.Fatalf("request %s succeeded without its VM", first.Number)
		}

		// A change while running: once the post-configuration job runs,
		// vCenter is changed to the second way to vcsim.
		second := submit("db")
		in.until("the second request's post-configuration job", func() bool {
			return jobLaunched(in.procs["worker"].current.Load().AWX.(*awx.Fake))
		})
		before := starts()
		saved = saveVCenter(sdk(p2))
		in.applied(saved.Revision)
		for role, n := range starts() {
			if n != before[role]+1 {
				t.Errorf("the %s started %d times for the new vCenter, want once", role, n-before[role])
			}
		}
		// The first way is gone: the running request finishes, and the next
		// one goes through the second.
		p1.close()
		finished(second.Id)
		sentBefore := p2.sent()
		finished(submit("web").Id)
		if p2.sent() == sentBefore {
			t.Error("the next request didn't go through the new vCenter")
		}
		var system api.System
		admin.json("GET", "/api/v1/admin/system", nil, 200, &system)
		for _, p := range system.Processes {
			if p.Revision != system.Revision {
				t.Errorf("the %s runs revision %d, want %d", p.Role, p.Revision, system.Revision)
			}
		}
		applies := map[api.IntegrationKind]api.SettingsRevisionApplies{}
		for _, s := range system.Settings {
			applies[s.Kind] = s.Applies
		}
		if applies[api.IntegrationKindVcenter] != api.Applied || applies[api.IntegrationKindOidc] != api.NextUse {
			t.Errorf("settings applied = %v", applies)
		}

		// Break-glass: the provider down, the local administrator still
		// signs in, and the API stays ready.
		idp.up.Store(false)
		again := newBrowser(t, ctx, in.url)
		if code, res, _ := again.signIn("admin", password); code != http.StatusOK || res.PasswordChangeRequired {
			t.Fatalf("the local administrator with the provider down = %d %+v", code, res)
		}
		again.json("GET", "/api/v1/admin/integrations", nil, 200, nil)
		in.ready()
	})
}
