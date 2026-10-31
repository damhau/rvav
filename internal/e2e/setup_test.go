package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/core"
	"github.com/damhau/rvav/internal/httpapi"
	"github.com/damhau/rvav/internal/inventory"
	"github.com/damhau/rvav/internal/seed"
)

// json sends a request as the web app does, checks the status and decodes
// the answer into out.
func (b *browser) json(method, path string, body any, want int, out any) {
	b.t.Helper()
	code, raw, _ := b.do(method, path, body, false)
	if code != want {
		b.t.Fatalf("%s %s = %d, want %d: %s", method, path, code, want, raw)
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			b.t.Fatalf("%s %s: %v: %s", method, path, err, raw)
		}
	}
}

// TestSetupChecklist: an install with the schema only, no seed nor
// integration settings, opens on the setup checklist with nothing done. The
// local administrator follows it, each step's page ticking its step, until
// it is complete and no longer opens. Hidden, it stays closed until shown
// again; a seeded install never opens on it (U1.5, ADR-0030).
func TestSetupChecklist(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		// Seeded: closed whatever remains, and for administrators only.
		seeded := setupWith(t, ctx, c, true)
		var st api.Setup
		seeded.json("GET", "/api/v1/admin/setup", "mkeller", nil, 200, &st)
		if st.Open || st.Complete || len(st.Steps) != 8 {
			t.Fatalf("seeded = %+v", st)
		}
		seeded.json("GET", "/api/v1/admin/setup", "lmartin", nil, 403, nil)

		h := setupWith(t, ctx, c, false)
		const installPassword, password = "install-password-1", "a password of my own"
		h.svc.Config.Auth.AdminPassword = installPassword
		if _, _, err := h.svc.EnsureLocalAdmin(ctx); err != nil {
			t.Fatal(err)
		}
		none, err := auth.NewOIDC(ctx, h.store, "", "", "rvav", "groups")
		if err != nil {
			t.Fatal(err)
		}
		srv := httptest.NewServer((&httpapi.Server{Store: h.store, Core: h.svc, Hub: h.hub, Log: discardLog(),
			AuthMode: "oidc", Auth: none, Heartbeat: time.Second}).Handler())
		t.Cleanup(srv.Close)
		a := newBrowser(t, ctx, srv.URL)
		if code, _, _ := a.signIn("admin", installPassword); code != http.StatusOK {
			t.Fatalf("sign-in = %d", code)
		}
		// Changing the password comes first, before the checklist can be read.
		a.json("GET", "/api/v1/admin/setup", nil, 403, nil)
		a.json("POST", "/api/v1/auth/password", api.PasswordChange{CurrentPassword: installPassword, NewPassword: password}, 204, nil)

		check := func(want map[api.SetupStepKey]string) api.Setup {
			t.Helper()
			var st api.Setup
			a.json("GET", "/api/v1/admin/setup", nil, 200, &st)
			done := 0
			for _, step := range st.Steps {
				detail, ok := want[step.Key]
				if step.Done != ok || ok && detail != "" && step.Detail != detail {
					t.Fatalf("step %s = %+v, want done=%v %q", step.Key, step, ok, detail)
				}
				if ok {
					done++
				}
			}
			if done != len(want) || st.Complete != (done == 8) || st.Open != (done < 8 && !st.Hidden) {
				t.Fatalf("checklist = %+v, want %d done", st, len(want))
			}
			return st
		}
		want := map[api.SetupStepKey]string{api.SetupStepKeyPassword: "Changed from the install's."}
		st = check(want)
		if st.Steps[1].Detail != "None: only the local administrator can sign in." || st.Steps[3].Detail != "Not set." ||
			st.Steps[4].Detail != "0 environments, 0 projects, 0 placement rules" || st.Steps[7].Detail != "0 items, 0 published" {
			t.Fatalf("an empty install = %+v", st.Steps)
		}

		// The identity provider, and a group of it mapped to admin.
		idp := newFakeIdP(t, "")
		idp.up.Store(true)
		a.json("PUT", "/api/v1/admin/integrations/oidc", api.IntegrationWrite{Settings: map[string]any{
			"issuer": idp.srv.URL, "client_id": "rvav"}}, 200, nil)
		want[api.SetupStepKeyProvider] = idp.srv.URL
		check(want)
		a.json("POST", "/api/v1/admin/group-mappings", api.GroupMappingWrite{OidcGroup: "rvav-admins", Role: "admin"}, 201, nil)
		want[api.SetupStepKeyAdminGroup] = "rvav-admins"
		check(want)

		// vCenter: vcsim.
		vcsim := c.URL().String()
		a.json("PUT", "/api/v1/admin/integrations/vcenter", api.IntegrationWrite{Settings: map[string]any{
			"url": vcsim, "username": "user", "name": "vc01.corp.example", "insecure": true}, Secret: new("pass")}, 200, nil)
		want[api.SetupStepKeyVcenter] = vcsim
		check(want)

		// An environment is not enough: a project too, and where its VMs
		// go, without which it can't request any.
		a.json("POST", "/api/v1/admin/environments", api.AdminEnvironment{Key: "dev", Name: "Development", Order: 1}, 201, nil)
		check(want)
		var web api.AdminProject
		a.json("POST", "/api/v1/admin/projects", api.AdminProjectCreate{Code: "web", Name: "Web", Environments: []string{"dev"}}, 201, &web)
		if st = check(want); st.Steps[4].Detail != "1 environment, 1 project, 0 placement rules" {
			t.Fatalf("a project without placement = %+v", st.Steps[4])
		}
		a.json("POST", "/api/v1/admin/placement-rules", api.PlacementRuleWrite{ProjectId: web.Id, Environment: "dev",
			Clusters: []string{"GVA-DEV-*"}, Datastores: []string{"vsan-*"}, Networks: []string{"dvpg-*-dev-*"}, Folder: "/GVA/vm/rvav/web/dev"}, 201, nil)
		want[api.SetupStepKeyProjects] = "1 environment, 1 project, 1 placement rule"
		check(want)

		// IPAM and AWX.
		a.json("PUT", "/api/v1/admin/integrations/efficientip", api.IntegrationWrite{Settings: map[string]any{
			"url": h.ipamURL, "username": "rvav", "dns_server": "ns1.corp.example"}, Secret: new("rvav")}, 200, nil)
		want[api.SetupStepKeyIpam] = h.ipamURL
		check(want)
		// On a database without network mappings the development fake serves
		// vcsim's networks; the sync reads them as it would a SOLIDserver's.
		if err := (&inventory.SubnetSync{Store: h.store, IPAM: h.svc.IPAM, Log: discardLog()}).SyncOnce(ctx); err != nil {
			t.Fatal(err)
		}
		var mappings api.NetworkMappingList
		a.json("GET", "/api/v1/admin/network-mappings", nil, 200, &mappings)
		got := map[string]string{}
		for _, m := range mappings.Items {
			if m.Source == api.NetworkMappingSourceEfficientip {
				got[m.Network] = m.Subnet
			}
		}
		for _, n := range seed.Subnets() {
			if got[n.Network] != n.Subnet {
				t.Fatalf("network mappings = %+v, want %s on %s", mappings.Items, n.Subnet, n.Network)
			}
		}
		a.json("PUT", "/api/v1/admin/integrations/awx", api.IntegrationWrite{Settings: map[string]any{
			"url": "https://awx.corp.example"}, Secret: new("awx-token")}, 200, nil)
		want[api.SetupStepKeyAwx] = "https://awx.corp.example"
		check(want)

		// A catalog item, done once published.
		def := guestItem("tpl-rhel9.4-2026.10", map[string]any{"os_family": "linux", "domain": "corp.example"})
		var item api.AdminCatalogItem
		a.json("POST", "/api/v1/admin/catalog/items", api.CatalogItemCreate{Slug: "rhel9", Name: "RHEL 9 server",
			Summary: "A Linux server.", Category: "automation", Type: api.VmBlueprint, Definition: &def}, 201, &item)
		st = check(want)
		if st.Steps[7].Detail != "1 item, 0 published" {
			t.Fatalf("a draft = %+v", st.Steps[7])
		}
		a.json("POST", "/api/v1/admin/catalog/items/rhel9/versions/"+item.DraftVersion.Id+"/publish", nil, 200, nil)
		want[api.SetupStepKeyCatalogItem] = "1 item, 1 published"
		if st = check(want); !st.Complete || st.Open {
			t.Fatalf("complete = %+v", st)
		}

		// Hidden while a step remains: it doesn't open, until shown again.
		a.json("DELETE", "/api/v1/admin/group-mappings/"+mappingID(t, a, "rvav-admins"), nil, 204, nil)
		delete(want, api.SetupStepKeyAdminGroup)
		if st = check(want); !st.Open {
			t.Fatalf("a step undone = %+v", st)
		}
		a.json("PUT", "/api/v1/admin/setup", api.SetupWrite{Hidden: true}, 200, &st)
		if !st.Hidden || st.Open {
			t.Fatalf("hidden = %+v", st)
		}
		a.json("PUT", "/api/v1/admin/setup", api.SetupWrite{Hidden: false}, 200, &st)
		if st.Hidden || !st.Open {
			t.Fatalf("shown again = %+v", st)
		}
		for _, action := range []string{"setup.hidden", "setup.shown"} {
			var audit struct{ Items []api.AuditEvent }
			a.json("GET", "/api/v1/audit?action="+action, nil, 200, &audit)
			if len(audit.Items) != 1 || audit.Items[0].Actor == nil || audit.Items[0].Actor.Id != core.LocalAdminUserID {
				t.Fatalf("audit of %s = %+v", action, audit.Items)
			}
		}
	})
}

func mappingID(t *testing.T, a *browser, group string) string {
	t.Helper()
	var list api.GroupMappingList
	a.json("GET", "/api/v1/admin/group-mappings", nil, 200, &list)
	for _, m := range list.Items {
		if m.OidcGroup == group {
			return m.Id
		}
	}
	t.Fatalf("no mapping of %s", group)
	return ""
}
