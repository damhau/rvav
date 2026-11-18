package e2e

import (
	"context"
	"maps"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/find"
	"github.com/vmware/govmomi/property"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"

	"github.com/damhau/rvav/internal/api"
)

// TestCustomProperties: a project's custom properties and a blueprint's
// reach its VMs as vSphere custom attributes and in their notes; bindings
// read the project's; the blueprint's folder, below the datacenter's VM
// folder, wins over the placement rule's (T1.5, ADR-0038).
func TestCustomProperties(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin = "mkeller"
		var problem struct{ Errors []api.FieldError }
		h.json("PATCH", "/api/v1/admin/projects/prj_web", admin, map[string]any{"properties": map[string]string{"1bad": "x"}}, 422, &problem)
		if len(problem.Errors) != 1 || problem.Errors[0].Field != "/properties/1bad" {
			t.Fatalf("a bad key = %+v", problem.Errors)
		}
		var proj api.AdminProject
		h.json("PATCH", "/api/v1/admin/projects/prj_web", admin, api.AdminProjectUpdate{
			Properties: &api.CustomProperties{"costCenter": "CC-1001", "owner": "web platform"}}, 200, &proj)
		if proj.Properties["costCenter"] != "CC-1001" || len(proj.Properties) != 2 {
			t.Fatalf("project = %+v", proj.Properties)
		}

		def := guestItem("tpl-rhel9.4-2026.10", map[string]any{"os_family": "linux", "domain": "corp.example"})
		def.Blueprint["folder"] = "../outside"
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "props-bad", Name: "p", Summary: "p",
			Category: "compute", Type: api.VmBlueprint, Definition: &def}, 422, &problem)
		if f := fieldsOf(problem.Errors); len(f) != 1 || !strings.HasSuffix(f[0], "/folder") {
			t.Fatalf("a folder outside = %+v", problem.Errors)
		}
		def.Blueprint["folder"], def.Blueprint["prevent_delete"] = "apps/${inputs.role}", true
		def.Blueprint["properties"] = map[string]any{"owner": "shop team", "app": "${inputs.role}",
			"billing": "${project.properties.costCenter + '/shop'}"}
		item := publishItem(h, "props", def)
		def.Blueprint["folder"] = "/ZRH/vm/${inputs.role}"
		zrh := publishItem(h, "props-zrh", def)
		def.Blueprint["folder"] = "apps/${inputs.role}"

		draft := api.RequestDraft{CatalogItemId: item.Id, VersionId: item.DraftVersion.Id, ProjectId: "prj_web", Environment: "dev",
			Inputs: map[string]any{"role": "prop", "cluster": "GVA-DEV-CL01", "datastore": "vsan-gva-dev-cl01", "network": "dvpg-app-dev-vl310"}}
		var pv api.RequestPreview
		h.json("POST", "/api/v1/requests/preview", "tnguyen", draft, 200, &pv)
		if !pv.Valid || deref((*pv.Components)[0].Placement.Folder) != "/GVA/vm/apps/prop" {
			t.Fatalf("preview = %+v / %+v", pv.Errors, (*pv.Components)[0].Placement)
		}
		// A folder of another datacenter than the cluster's is refused
		// before anything is made (T1.7).
		other := draft
		other.CatalogItemId, other.VersionId = zrh.Id, zrh.DraftVersion.Id
		h.json("POST", "/api/v1/requests", "tnguyen", other, 422, &problem, "Idempotency-Key", uuid.NewString())
		if len(problem.Errors) != 1 || problem.Errors[0].Field != "/inputs/role" ||
			problem.Errors[0].Message != "Folder /ZRH/vm/prop is in datacenter ZRH, and cluster GVA-DEV-CL01 in GVA." {
			t.Fatalf("a ZRH folder = %+v", problem.Errors)
		}
		var req api.Request
		h.json("POST", "/api/v1/requests", "tnguyen", draft, 202, &req, "Idempotency-Key", uuid.NewString())
		h.runWorkflow(req.Id, nil)
		h.waitStatus(req.Id, "tnguyen", api.RequestStatusSucceeded)

		folder, err := find.NewFinder(c).Folder(ctx, "/GVA/vm/apps/prop")
		if err != nil {
			t.Fatal(err)
		}
		var vm mo.VirtualMachine
		if err := property.DefaultCollector(c).RetrieveOne(ctx, vmRefByName(t, ctx, c, "web-dev-prop01"),
			[]string{"parent", "config.annotation", "customValue", "availableField"}, &vm); err != nil {
			t.Fatal(err)
		}
		if vm.Parent == nil || *vm.Parent != folder.Reference() {
			t.Fatalf("the VM is in %v, not /GVA/vm/apps/prop", vm.Parent)
		}
		names := map[int32]string{}
		for _, f := range vm.AvailableField {
			names[f.Key] = f.Name
		}
		attrs := map[string]string{}
		for _, v := range vm.CustomValue {
			if s, ok := v.(*types.CustomFieldStringValue); ok {
				attrs[names[s.Key]] = s.Value
			}
		}
		want := map[string]string{"costCenter": "CC-1001", "owner": "shop team", "app": "prop", "billing": "CC-1001/shop"}
		if !maps.Equal(attrs, want) {
			t.Fatalf("custom attributes = %v", attrs)
		}
		if !strings.HasPrefix(vm.Config.Annotation, "Managed by rvav. Deployment ") ||
			!strings.Contains(vm.Config.Annotation, "\nbilling: CC-1001/shop\n") || !strings.HasSuffix(vm.Config.Annotation, "\nowner: shop team") {
			t.Fatalf("notes = %q", vm.Config.Annotation)
		}

		// prevent_delete: the owner can't delete it until an administrator
		// allows it; a project admin can't either.
		dep := &vmDeployment{h: h, id: (*req.DeploymentIds)[0]}
		del := dep.actions("tnguyen")["delete"]
		if del.Enabled || !strings.Contains(deref(del.DisabledReason), "keeps web-dev-prop01 from being deleted") {
			t.Fatalf("owner's delete = %+v", del)
		}
		for _, u := range []string{"tnguyen", "lmartin"} {
			if _, ok := dep.actions(u)["allow_delete"]; ok {
				t.Fatalf("%s is offered Allow deletion", u)
			}
		}
		action := func(user, key string, want int) api.Request {
			var r api.Request
			var out any
			if want == 202 {
				out = &r
			}
			h.json("POST", "/api/v1/deployments/"+dep.id+"/actions", user, api.Day2Draft{Action: key, Inputs: map[string]any{}},
				want, out, "Idempotency-Key", uuid.NewString())
			if want == 202 {
				h.runWorkflow(r.Id, nil)
				r = h.waitStatus(r.Id, user, api.RequestStatusSucceeded)
			}
			return r
		}
		action("tnguyen", "delete", 409)
		action("tnguyen", "allow_delete", 422)
		if a := dep.actions(admin)["allow_delete"]; !a.Enabled {
			t.Fatalf("administrator's allow_delete = %+v", a)
		}
		action(admin, "allow_delete", 202)
		if a := dep.actions(admin)["allow_delete"]; a.Enabled {
			t.Fatalf("allow_delete once allowed = %+v", a)
		}
		action("tnguyen", "delete", 202)
		if vmExists(ctx, c, "web-dev-prop01") {
			t.Fatal("the VM allowed to be deleted is still in vCenter")
		}
	})
}
