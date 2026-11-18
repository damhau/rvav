package e2e

import (
	"context"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/find"
	"github.com/vmware/govmomi/property"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/mo"

	"github.com/damhau/rvav/internal/api"
)

// TestFolderPicker: a vsphere.folders field offers the VM folders of the
// datacenters the project's placement allows, the server refuses another,
// and the blueprint's folder: binding puts the VM in the one picked (F1.1).
func TestFolderPicker(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		def := guestItem("tpl-rhel9.4-2026.10", map[string]any{"os_family": "linux", "domain": "corp.example"})
		def.FormSchema["properties"].(map[string]any)["folder"] = map[string]any{"type": "string"}
		def.UiSchema.Fields["folder"] = api.UiField{Widget: api.UiWidgetVsphereFolder, Label: "Folder",
			ValueSource: &api.ValueSource{Kind: api.Builtin, Name: new("vsphere.folders")}}
		def.UiSchema.Layout.Sections[0].Fields = append(def.UiSchema.Layout.Sections[0].Fields, "folder")
		def.Blueprint["folder"] = "${inputs.folder}"
		item := publishItem(h, "foldered", def)

		var values api.FieldValues
		h.json("POST", "/api/v1/forms/values", "tnguyen", api.FieldValuesQuery{VersionId: item.DraftVersion.Id, ProjectId: "prj_web",
			Environment: new(api.EnvironmentKey("dev")), Field: "folder", Inputs: map[string]any{}}, 200, &values)
		var paths []string
		for _, o := range *values.Options {
			paths = append(paths, o.Value)
			if deref(o.Meta) != "GVA" {
				t.Errorf("%s: meta %q, want its datacenter", o.Value, deref(o.Meta))
			}
		}
		if !slices.Contains(paths, "/GVA/vm/rvav/web/tst") || slices.ContainsFunc(paths, func(p string) bool { return p == "/DC0/vm" }) {
			t.Fatalf("folders for web/dev = %v", paths)
		}

		inputs := map[string]any{"role": "fold", "cluster": "GVA-DEV-CL01", "datastore": "vsan-gva-dev-cl01",
			"network": "dvpg-app-dev-vl310", "folder": "/DC0/vm"}
		draft := api.RequestDraft{CatalogItemId: item.Id, VersionId: item.DraftVersion.Id, ProjectId: "prj_web", Environment: "dev",
			Inputs: inputs}
		var pv api.RequestPreview
		h.json("POST", "/api/v1/requests/preview", "tnguyen", draft, 200, &pv)
		if pv.Valid || !slices.ContainsFunc(pv.Errors, func(e api.FieldError) bool { return e.Field == "/inputs/folder" && deref(e.Code) == "not_allowed" }) {
			t.Fatalf("another datacenter's folder: %+v", pv.Errors)
		}

		inputs["folder"] = "/GVA/vm/rvav/web/tst"
		var req api.Request
		h.json("POST", "/api/v1/requests", "tnguyen", draft, 202, &req, "Idempotency-Key", uuid.NewString())
		h.runWorkflow(req.Id, nil)
		h.waitStatus(req.Id, "tnguyen", api.RequestStatusSucceeded)
		folder, err := find.NewFinder(c).Folder(ctx, "/GVA/vm/rvav/web/tst")
		if err != nil {
			t.Fatal(err)
		}
		var vm mo.VirtualMachine
		if err := property.DefaultCollector(c).RetrieveOne(ctx, vmRefByName(t, ctx, c, "web-dev-fold01"), []string{"parent"}, &vm); err != nil {
			t.Fatal(err)
		}
		if vm.Parent == nil || *vm.Parent != folder.Reference() {
			t.Fatalf("the VM is in %v, not the folder picked", vm.Parent)
		}
	})
}
