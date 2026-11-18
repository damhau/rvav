package e2e

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/view"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/inventory"
	"github.com/damhau/rvav/internal/present"
)

// TestLinkedClones: a catalogue template's VMs are linked clones of its
// current snapshot, unless the blueprint says otherwise; without a
// snapshot, with a disk larger than the template's, or of a content
// library item, it is refused and said why (T1.4, ADR-0037).
func TestLinkedClones(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin = "mkeller"
		sync := &inventory.Syncer{Store: h.store, VSphere: h.vc, Events: h.acts.Events, Log: h.acts.Log}
		if err := sync.SyncOnce(ctx); err != nil {
			t.Fatal(err)
		}

		var problem struct{ Errors []api.FieldError }
		write := api.VmTemplateWrite{Key: "rhel9-lc", Label: "RHEL 9.4 linked", OsFamily: api.VmTemplateWriteOsFamilyLinux,
			Offers: &[]api.Offer{{}}, LinkedClone: present.Ptr(true), Sources: []api.VmTemplateSourceWrite{
				{Template: "rhel9-library", ContentLibrary: present.Ptr("rvav-templates")}}}
		h.json("POST", "/api/v1/admin/templates", admin, write, 422, &problem)
		if !slices.Contains(fieldsOf(problem.Errors), "/linked_clone") {
			t.Fatalf("a library item = %+v", problem.Errors)
		}
		write.Sources = sourceOn("", "tpl-rhel9.4-2026.10")
		var tpl api.VmTemplate
		h.json("POST", "/api/v1/admin/templates", admin, write, 201, &tpl)
		if !tpl.LinkedClone {
			t.Fatalf("template = %+v", tpl)
		}

		guest := map[string]any{"os_family": "linux", "domain": "corp.example"}
		item := imageItem(h, "rhel-linked", "rhel9-lc", guest)
		run := func(item api.AdminCatalogItem, role string, want api.RequestStatus) api.Step {
			t.Helper()
			draft := api.RequestDraft{CatalogItemId: item.Id, VersionId: item.DraftVersion.Id, ProjectId: "prj_web", Environment: "dev",
				Inputs: map[string]any{"role": role, "cluster": "-", "datastore": "-", "network": "dvpg-app-dev-vl310"}}
			var req api.Request
			h.json("POST", "/api/v1/requests", "tnguyen", draft, 202, &req, "Idempotency-Key", uuid.NewString())
			h.runWorkflow(req.Id, nil)
			h.waitStatus(req.Id, "tnguyen", want)
			var list struct{ Items []api.Step }
			h.json("GET", "/api/v1/requests/"+req.Id+"/steps", "tnguyen", nil, 200, &list)
			i := slices.IndexFunc(list.Items, func(s api.Step) bool { return s.Key == "vsphere.clone" })
			if i < 0 {
				t.Fatalf("steps = %+v", list.Items)
			}
			return list.Items[i]
		}
		draft := api.RequestDraft{CatalogItemId: item.Id, VersionId: item.DraftVersion.Id, ProjectId: "prj_web", Environment: "dev",
			Inputs: map[string]any{"role": "lc", "cluster": "-", "datastore": "-", "network": "dvpg-app-dev-vl310"}}
		var pv api.RequestPreview
		h.json("POST", "/api/v1/requests/preview", "tnguyen", draft, 200, &pv)
		if comp := (*pv.Components)[0]; !pv.Valid || comp.LinkedClone == nil || !*comp.LinkedClone {
			t.Fatalf("preview = %+v / %+v", pv.Errors, comp)
		}

		// No snapshot yet: refused before any clone.
		if step := run(item, "nosnap", api.RequestStatusFailed); !strings.Contains(deref(step.Error),
			"template tpl-rhel9.4-2026.10 has no snapshot to clone from") || vmExists(ctx, c, "web-dev-nosnap01") {
			t.Fatalf("without a snapshot = %+v", step)
		}
		tplObj := object.NewVirtualMachine(c, vmRefByName(t, ctx, c, "tpl-rhel9.4-2026.10"))
		task, err := tplObj.CreateSnapshot(ctx, "golden", "", false, false)
		if err != nil {
			t.Fatal(err)
		}
		if err := task.Wait(ctx); err != nil {
			t.Fatal(err)
		}

		// The item's 60 GB disk can't be the template's 40 GB one grown.
		if step := run(item, "big", api.RequestStatusFailed); !strings.Contains(deref(step.Error), "keeps the 40 GB disk") {
			t.Fatalf("a larger disk = %+v", step)
		}

		fits := guestItem("", guest)
		bindings := fits.Blueprint["bindings"].(map[string]any)
		delete(bindings, "cluster")
		delete(bindings, "datastore")
		bindings["template"], bindings["disk_gb"] = `"rhel9-lc"`, "40"
		small := publishItem(h, "rhel-linked-40", fits)
		if step := run(small, "lc", api.RequestStatusSucceeded); step.Title != "Linked clone of tpl-rhel9.4-2026.10" ||
			!vmExists(ctx, c, "web-dev-lc01") {
			t.Fatalf("linked clone = %+v", step)
		}

		// The blueprint says full clones: a full clone, 60 GB.
		full := guestItem("", guest)
		bindings = full.Blueprint["bindings"].(map[string]any)
		delete(bindings, "cluster")
		delete(bindings, "datastore")
		bindings["template"] = `"rhel9-lc"`
		full.Blueprint["linked_clone"] = false
		fullItem := publishItem(h, "rhel-full", full)
		if step := run(fullItem, "full", api.RequestStatusSucceeded); step.Title != "Cloned from tpl-rhel9.4-2026.10" {
			t.Fatalf("full clone = %+v", step)
		}
	})
}

// publishItem publishes a VM blueprint item every project may request.
func publishItem(h *harness, slug string, def api.CatalogVersionDefinition) api.AdminCatalogItem {
	h.t.Helper()
	var item api.AdminCatalogItem
	h.json("POST", "/api/v1/admin/catalog/items", "mkeller", api.CatalogItemCreate{Slug: slug, Name: slug, Summary: slug,
		Category: "compute", Type: api.VmBlueprint, Definition: &def}, 201, &item)
	h.json("POST", "/api/v1/admin/catalog/items/"+slug+"/versions/"+item.DraftVersion.Id+"/publish", "mkeller", nil, 200, nil)
	h.json("PUT", "/api/v1/admin/catalog/items/"+slug+"/entitlements", "mkeller",
		api.CatalogEntitlements{AllProjects: true, ProjectIds: []string{}}, 200, nil)
	return item
}

func vmRefByName(t *testing.T, ctx context.Context, c *vim25.Client, name string) types.ManagedObjectReference {
	t.Helper()
	v, err := view.NewManager(c).CreateContainerView(ctx, c.ServiceContent.RootFolder, []string{"VirtualMachine"}, true)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = v.Destroy(ctx) }()
	var vms []mo.VirtualMachine
	if err := v.Retrieve(ctx, []string{"VirtualMachine"}, []string{"name"}, &vms); err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(vms, func(vm mo.VirtualMachine) bool { return vm.Name == name })
	if i < 0 {
		t.Fatalf("no VM %s", name)
	}
	return vms[i].Self
}
