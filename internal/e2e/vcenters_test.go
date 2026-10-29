package e2e

import (
	"context"
	"crypto/tls"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/vmware/govmomi"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/core"
	"github.com/damhau/rvav/internal/integrations/vsphere"
	"github.com/damhau/rvav/internal/inventory"
	"github.com/damhau/rvav/internal/present"
	"github.com/damhau/rvav/internal/vcenters"
)

// zurich is a second vCenter's layout: a dev cluster of four hosts, with
// the dev network's name, and a cluster named as one of Geneva's.
func zurich() vsphere.Layout {
	return vsphere.Layout{
		Datacenter: "ZRH",
		Clusters: []vsphere.ClusterSpec{
			{Name: "ZRH-DEV-CL01", HostPrefix: "esx-zrh-dev", Hosts: 4, Datastores: []string{"vsan-zrh-dev-cl01"}},
			{Name: "GVA-TST-CL01", HostPrefix: "esx-zrh-tst", Hosts: 1, Datastores: []string{"vsan-zrh-tst-cl01"}},
		},
		Switch:     "ZRH-DVS01",
		Portgroups: []string{"dvpg-app-dev-vl310", "dvpg-app-tst-vl410"},
		Folders:    []string{"/ZRH/vm/templates", "/ZRH/vm/rvav/web/dev"},
		Templates: []vsphere.TemplateSpec{{Name: "tpl-rhel9.4-2026.10", GuestID: "rhel9_64Guest", Cluster: "ZRH-DEV-CL01",
			Datastore: "vsan-zrh-dev-cl01", Network: "dvpg-app-dev-vl310", Folder: "/ZRH/vm/templates", VCPU: 2, MemoryMB: 4096, DiskGB: 40}},
	}
}

// TestSeveralVCenters: an administrator adds a second vCenter; the sync
// reads both; a placement rule for its clusters puts a VM there, chosen
// by rvav when the blueprint doesn't name a cluster; day-2 actions and the
// deletion act on it there; a cluster name in both vCenters needs its rule
// to name one (S12.6, ADR-0024).
func TestSeveralVCenters(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin = "mkeller"
		const vc02 = "vc02.corp.example"

		// Zurich: another vcsim.
		m := simulator.VPX()
		defer m.Remove()
		if err := m.Create(); err != nil {
			t.Fatal(err)
		}
		m.Service.TLS = new(tls.Config)
		m.Service.RegisterEndpoints = true // tags and content libraries
		srv := m.Service.NewServer()
		defer srv.Close()
		gc, err := govmomi.NewClient(ctx, srv.URL, true)
		if err != nil {
			t.Fatal(err)
		}
		if err := vsphere.Bootstrap(ctx, gc.Client, zurich()); err != nil {
			t.Fatal(err)
		}

		// Added by an administrator, its password sealed; tested first (it has
		// vcsim's own cluster too).
		h.svc.Config.VCenter.Name, h.svc.Config.VCenter.URL = "vc01.corp.example", "https://vc01.corp.example/sdk"
		url := *srv.URL
		url.User = nil
		var problem struct{ Errors []api.FieldError }
		write := api.VCenterWrite{Name: vc02, Url: url.String(), Username: "user", Insecure: present.Ptr(true)}
		h.json("POST", "/api/v1/admin/vcenters", admin, write, 422, &problem)
		if !slices.Contains(fieldsOf(problem.Errors), "/password") {
			t.Fatalf("no password = %+v", problem.Errors)
		}
		write.Password = present.Ptr("pass")
		var test api.IntegrationTest
		h.json("POST", "/api/v1/admin/vcenters/"+vc02+"/test", admin, write, 200, &test)
		if !test.Ok {
			t.Fatalf("test = %+v", test.Checks)
		}
		h.json("POST", "/api/v1/admin/vcenters", admin, write, 201, nil)
		h.json("POST", "/api/v1/admin/vcenters", admin, write, 409, nil)
		// Its address changed without the password: refused.
		moved := write
		moved.Url, moved.Password = "https://elsewhere.example/sdk", nil
		h.json("PUT", "/api/v1/admin/vcenters/"+vc02, admin, moved, 422, &problem)

		set := &vcenters.Set{Store: h.store, Secrets: h.svc.Secrets, PrimaryName: "vc01.corp.example",
			Primary: func(context.Context) (vsphere.Driver, error) { return h.vc, nil }}
		h.acts.VCenters = set
		h.svc.VCenter = func(ctx context.Context, name string) (core.ConsoleSource, error) { return set.Get(ctx, name) }
		sync := &inventory.Syncer{Store: h.store, VCenters: set, Events: h.acts.Events, Log: h.acts.Log}
		if err := sync.SyncOnce(ctx); err != nil {
			t.Fatal(err)
		}
		var list api.VCenterList
		h.json("GET", "/api/v1/admin/vcenters", admin, nil, 200, &list)
		if len(list.Items) != 2 || !list.Items[0].Primary || list.Items[1].Name != vc02 || !list.Items[1].PasswordSet ||
			list.Items[1].Inventory == nil || list.Items[1].Inventory.Clusters == nil || *list.Items[1].Inventory.Clusters != 3 {
			t.Fatalf("vCenters = %+v", list.Items)
		}

		// A placement rule for Zurich's dev cluster, in vc02.
		var dev api.PlacementRule
		h.json("POST", "/api/v1/admin/placement-rules", admin, api.PlacementRuleWrite{ProjectId: "prj_web", Environment: "dev",
			Clusters: []string{"ZRH-DEV-*"}, Datastores: []string{"vsan-zrh-*"}, Networks: []string{"dvpg-app-dev-*"},
			Folder: "/ZRH/vm/rvav/web/dev", Vcenter: present.Ptr("vc03.corp.example")}, 422, nil)
		h.json("POST", "/api/v1/admin/placement-rules", admin, api.PlacementRuleWrite{ProjectId: "prj_web", Environment: "dev",
			Clusters: []string{"ZRH-DEV-*"}, Datastores: []string{"vsan-zrh-*"}, Networks: []string{"dvpg-app-dev-*"},
			Folder: "/ZRH/vm/rvav/web/dev", Vcenter: present.Ptr(vc02)}, 201, &dev)

		// A blueprint without cluster or datastore: rvav chooses, here
		// Zurich's cluster with the most free memory.
		def := guestItem("tpl-rhel9.4-2026.10", map[string]any{"os_family": "linux", "domain": "corp.example"})
		delete(def.Blueprint["bindings"].(map[string]any), "cluster")
		delete(def.Blueprint["bindings"].(map[string]any), "datastore")
		var item api.AdminCatalogItem
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "anywhere", Name: "Anywhere",
			Summary: "Placed by rvav.", Category: "compute", Type: api.VmBlueprint, Definition: &def}, 201, &item)
		h.json("POST", "/api/v1/admin/catalog/items/anywhere/versions/"+item.DraftVersion.Id+"/publish", admin, nil, 200, nil)
		h.json("PUT", "/api/v1/admin/catalog/items/anywhere/entitlements", admin, api.CatalogEntitlements{AllProjects: true, ProjectIds: []string{}}, 200, nil)
		draft := api.RequestDraft{CatalogItemId: item.Id, VersionId: item.DraftVersion.Id, ProjectId: "prj_web", Environment: "dev",
			Inputs: map[string]any{"role": "zrh", "cluster": "-", "datastore": "-", "network": "dvpg-app-dev-vl310"}}
		var pv api.RequestPreview
		h.json("POST", "/api/v1/requests/preview", "tnguyen", draft, 200, &pv)
		pl := (*pv.Components)[0].Placement
		if !pv.Valid || deref(pl.Cluster) != "ZRH-DEV-CL01" || deref(pl.Vcenter) != vc02 || pl.Chosen == nil || !*pl.Chosen ||
			deref(pl.Datastore) != "vsan-zrh-dev-cl01" {
			t.Fatalf("preview = %+v / %+v", pv.Errors, pl)
		}
		// A template only Geneva has: rvav chooses Geneva's cluster.
		ubuntu := guestItem("tpl-ubuntu24.04-2026.10", map[string]any{"os_family": "linux", "domain": "corp.example"})
		delete(ubuntu.Blueprint["bindings"].(map[string]any), "cluster")
		delete(ubuntu.Blueprint["bindings"].(map[string]any), "datastore")
		var genevaOnly api.AdminCatalogItem
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "ubuntu-anywhere", Name: "Ubuntu anywhere",
			Summary: "Placed by rvav.", Category: "compute", Type: api.VmBlueprint, Definition: &ubuntu}, 201, &genevaOnly)
		h.json("POST", "/api/v1/admin/catalog/items/ubuntu-anywhere/versions/"+genevaOnly.DraftVersion.Id+"/publish", admin, nil, 200, nil)
		h.json("PUT", "/api/v1/admin/catalog/items/ubuntu-anywhere/entitlements", admin, api.CatalogEntitlements{AllProjects: true, ProjectIds: []string{}}, 200, nil)
		geneva := draft
		geneva.CatalogItemId, geneva.VersionId = genevaOnly.Id, genevaOnly.DraftVersion.Id
		h.json("POST", "/api/v1/requests/preview", "tnguyen", geneva, 200, &pv)
		if pl := (*pv.Components)[0].Placement; !pv.Valid || deref(pl.Vcenter) != "vc01.corp.example" || deref(pl.Cluster) != "GVA-DEV-CL01" {
			t.Fatalf("Geneva's template = %+v / %+v", pv.Errors, pl)
		}

		var req api.Request
		h.json("POST", "/api/v1/requests", "tnguyen", draft, 202, &req, "Idempotency-Key", uuid.NewString())
		h.runWorkflow(req.Id, nil)
		h.json("GET", "/api/v1/requests/"+req.Id, "tnguyen", nil, 200, &req)
		if req.Status != api.RequestStatusSucceeded {
			t.Fatalf("request = %s %s", req.Status, errorOf(req))
		}
		if !vmExists(ctx, gc.Client, "web-dev-zrh01") || vmExists(ctx, c, "web-dev-zrh01") {
			t.Fatal("the VM isn't in Zurich's vCenter alone")
		}
		d := &vmDeployment{h: h, model: m, id: (*req.DeploymentIds)[0], built: req, viewer: "tnguyen"}
		if vm := d.vm(); vm.Vcenter != vc02 || deref(vm.Cluster) != "ZRH-DEV-CL01" {
			t.Fatalf("VM = %+v", vm)
		}

		// Both synced: the VM isn't missing from Geneva's.
		if err := sync.SyncOnce(ctx); err != nil {
			t.Fatal(err)
		}
		var dep api.DeploymentDetail
		h.json("GET", "/api/v1/deployments/"+d.id, "tnguyen", nil, 200, &dep)
		if dep.SyncState != api.InSync {
			t.Fatalf("deployment sync state = %s", dep.SyncState)
		}

		// Day-2 there: power off, then the vCenter can't go while it has VMs.
		d.resource = *d.actions("tnguyen")["power_off"].ResourceId
		d.succeed("tnguyen", "power_off", nil)
		if d.vm().PowerState != api.Off {
			t.Fatal("not powered off in Zurich")
		}
		h.json("DELETE", "/api/v1/admin/vcenters/"+vc02, admin, nil, 409, nil)
		var del api.Request
		h.json("POST", "/api/v1/deployments/"+d.id+"/actions", "tnguyen", api.Day2Draft{Action: "delete", Inputs: map[string]any{}},
			202, &del, "Idempotency-Key", uuid.NewString())
		h.runWorkflow(del.Id, nil)
		h.waitStatus(del.Id, "tnguyen", api.RequestStatusSucceeded)
		if vmExists(ctx, gc.Client, "web-dev-zrh01") {
			t.Fatal("the VM is still in Zurich")
		}

		// GVA-TST-CL01 is a name in both vCenters: its rule must name one.
		tst := draft
		tst.Environment = "tst"
		tst.Inputs = map[string]any{"role": "amb", "cluster": "-", "datastore": "-", "network": "dvpg-app-tst-vl410"}
		def2 := guestItem("tpl-rhel9.4-2026.10", map[string]any{"os_family": "linux", "domain": "corp.example"})
		var pinned api.AdminCatalogItem
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "pinned", Name: "Pinned",
			Summary: "Its cluster from the form.", Category: "compute", Type: api.VmBlueprint, Definition: &def2}, 201, &pinned)
		h.json("POST", "/api/v1/admin/catalog/items/pinned/versions/"+pinned.DraftVersion.Id+"/publish", admin, nil, 200, nil)
		h.json("PUT", "/api/v1/admin/catalog/items/pinned/entitlements", admin, api.CatalogEntitlements{AllProjects: true, ProjectIds: []string{}}, 200, nil)
		tst.CatalogItemId, tst.VersionId = pinned.Id, pinned.DraftVersion.Id
		tst.Inputs["cluster"], tst.Inputs["datastore"] = "GVA-TST-CL01", "vsan-gva-tst-cl01"
		h.json("POST", "/api/v1/requests/preview", "tnguyen", tst, 200, &pv)
		if !slices.ContainsFunc(pv.Errors, func(e api.FieldError) bool { return deref(e.Code) == "ambiguous" }) {
			t.Fatalf("ambiguous cluster = %+v", pv.Errors)
		}
		var rules struct{ Items []api.PlacementRule }
		h.json("GET", "/api/v1/admin/placement-rules?project_id=prj_web", admin, nil, 200, &rules)
		r := rules.Items[slices.IndexFunc(rules.Items, func(r api.PlacementRule) bool { return r.Environment == "tst" })]
		h.json("PUT", "/api/v1/admin/placement-rules/"+r.Id, admin, api.PlacementRuleWrite{ProjectId: r.Project.Id, Environment: "tst",
			Clusters: r.Clusters, Datastores: r.Datastores, Networks: r.Networks, Folder: r.Folder, Vcenter: present.Ptr("vc01.corp.example")}, 200, nil)
		h.json("POST", "/api/v1/requests/preview", "tnguyen", tst, 200, &pv)
		if !pv.Valid || deref((*pv.Components)[0].Placement.Vcenter) != "vc01.corp.example" {
			t.Fatalf("pinned to vc01 = %+v / %+v", pv.Errors, (*pv.Components)[0].Placement)
		}

		// Without VMs there any more, Zurich can go, with its inventory, once
		// no placement rule names it.
		h.json("DELETE", "/api/v1/admin/vcenters/"+vc02, admin, nil, 409, nil)
		h.json("DELETE", "/api/v1/admin/placement-rules/"+dev.Id, admin, nil, 204, nil)
		h.json("DELETE", "/api/v1/admin/vcenters/"+vc02, admin, nil, 204, nil)
		var left int
		_ = h.store.Pool.QueryRow(ctx, `select count(*) from inv_clusters where vcenter = $1`, vc02).Scan(&left)
		if left != 0 {
			t.Fatalf("%d of Zurich's clusters left", left)
		}
	})
}
