package e2e

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/present"
)

// TestLibraryAndStoragePolicies: a VM deployed from a content library's VM
// template, offered in the templates catalogue; its storage policy comes
// from the placement rule, a disk's from the blueprint; a policy or item
// vCenter doesn't have is refused (S12.5, ADR-0024).
func TestLibraryAndStoragePolicies(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin = "mkeller"
		var problem struct{ Errors []api.FieldError }

		// The templates catalogue knows the library's item.
		var tpl api.VmTemplate
		h.json("POST", "/api/v1/admin/templates", admin, api.VmTemplateWrite{Key: "rhel9-lib", Label: "RHEL 9 (library)",
			VcenterTemplate: "rhel9-library", ContentLibrary: present.Ptr("rvav-templates"), OsFamily: api.VmTemplateWriteOsFamilyLinux}, 201, &tpl)
		if tpl.ContentLibrary != "rvav-templates" || tpl.InInventory == nil || !*tpl.InInventory {
			t.Fatalf("template = %+v", tpl)
		}

		// The web project's dev placement puts VMs on the vSAN policy.
		var rules struct{ Items []api.PlacementRule }
		h.json("GET", "/api/v1/admin/placement-rules?project_id=prj_web", admin, nil, 200, &rules)
		i := slices.IndexFunc(rules.Items, func(r api.PlacementRule) bool { return r.Environment == "dev" })
		r := rules.Items[i]
		write := api.PlacementRuleWrite{ProjectId: r.Project.Id, Environment: r.Environment, Clusters: r.Clusters, Datastores: r.Datastores,
			Networks: r.Networks, Folder: r.Folder, StoragePolicy: present.Ptr("Platinum")}
		h.json("PUT", "/api/v1/admin/placement-rules/"+r.Id, admin, write, 422, &problem)
		if !slices.Contains(fieldsOf(problem.Errors), "/storage_policy") {
			t.Fatalf("an unknown policy = %+v", problem.Errors)
		}
		write.StoragePolicy = present.Ptr("vSAN Default Storage Policy")
		h.json("PUT", "/api/v1/admin/placement-rules/"+r.Id, admin, write, 200, &r)
		if r.StoragePolicy != "vSAN Default Storage Policy" {
			t.Fatalf("rule = %+v", r)
		}

		def := guestItem("rhel9-library", map[string]any{"os_family": "linux", "domain": "corp.example"})
		def.Blueprint["library"] = "rvav-templates"
		def.Blueprint["disks"] = []any{map[string]any{"size_gb": 10, "storage_policy": "VM Encryption Policy"}}
		var item api.AdminCatalogItem
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "rhel-lib", Name: "RHEL from the library",
			Summary: "From a content library.", Category: "compute", Type: api.VmBlueprint, Definition: &def}, 201, &item)
		h.json("POST", "/api/v1/admin/catalog/items/rhel-lib/versions/"+item.DraftVersion.Id+"/publish", admin, nil, 200, nil)
		h.json("PUT", "/api/v1/admin/catalog/items/rhel-lib/entitlements", admin, api.CatalogEntitlements{AllProjects: true, ProjectIds: []string{}}, 200, nil)
		draft := api.RequestDraft{CatalogItemId: item.Id, VersionId: item.DraftVersion.Id, ProjectId: "prj_web", Environment: "dev",
			Inputs: map[string]any{"role": "lib", "cluster": "GVA-DEV-CL01", "datastore": "vsan-gva-dev-cl01", "network": "dvpg-app-dev-vl310"}}

		var pv api.RequestPreview
		h.json("POST", "/api/v1/requests/preview", "tnguyen", draft, 200, &pv)
		comp := (*pv.Components)[0]
		if !pv.Valid || deref(comp.Placement.StoragePolicy) != "vSAN Default Storage Policy" || len(comp.Disks) != 2 ||
			deref(comp.Disks[0].StoragePolicy) != "vSAN Default Storage Policy" || deref(comp.Disks[1].StoragePolicy) != "VM Encryption Policy" {
			t.Fatalf("preview = %+v / %+v", pv.Errors, comp)
		}
		var req api.Request
		h.json("POST", "/api/v1/requests", "tnguyen", draft, 202, &req, "Idempotency-Key", uuid.NewString())
		h.runWorkflow(req.Id, nil)
		h.json("GET", "/api/v1/requests/"+req.Id, "tnguyen", nil, 200, &req)
		if req.Status != api.RequestStatusSucceeded {
			t.Fatalf("request = %s %s", req.Status, errorOf(req))
		}
		var steps struct{ Items []api.Step }
		h.json("GET", "/api/v1/requests/"+req.Id+"/steps", "tnguyen", nil, 200, &steps)
		clone := steps.Items[slices.IndexFunc(steps.Items, func(s api.Step) bool { return s.Key == "vsphere.clone" })]
		if clone.Title != "Deployed rhel9-library from rvav-templates" || !strings.Contains(deref(clone.Detail), "vSAN Default Storage Policy") {
			t.Fatalf("clone step = %q %q", clone.Title, deref(clone.Detail))
		}
		if !vmExists(ctx, c, "web-dev-lib01") {
			t.Fatal("no VM web-dev-lib01 in vCenter")
		}

		// A policy, or a library item, vCenter doesn't have.
		bad := def
		bad.Blueprint = map[string]any{}
		for k, v := range def.Blueprint {
			bad.Blueprint[k] = v
		}
		bad.Blueprint["storage_policy"] = "Platinum"
		bad.Blueprint["template"] = "rhel10-library"
		var badItem api.AdminCatalogItem
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "rhel-lib-bad", Name: "Bad", Summary: "Bad.",
			Category: "compute", Type: api.VmBlueprint, Definition: &bad}, 201, &badItem)
		h.json("POST", "/api/v1/admin/catalog/items/rhel-lib-bad/versions/"+badItem.DraftVersion.Id+"/publish", admin, nil, 200, nil)
		h.json("PUT", "/api/v1/admin/catalog/items/rhel-lib-bad/entitlements", admin, api.CatalogEntitlements{AllProjects: true, ProjectIds: []string{}}, 200, nil)
		draft.CatalogItemId, draft.VersionId = badItem.Id, badItem.DraftVersion.Id
		h.json("POST", "/api/v1/requests/preview", "tnguyen", draft, 200, &pv)
		var codes []string
		for _, e := range pv.Errors {
			codes = append(codes, deref(e.Code))
		}
		if !slices.Contains(codes, "storage_policy_missing") || !slices.Contains(codes, "template_missing") {
			t.Fatalf("preview errors = %+v", pv.Errors)
		}
	})
}
