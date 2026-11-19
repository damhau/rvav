package e2e

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/find"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/types"

	"github.com/damhau/rvav/internal/api"
)

// backings are a VM's disks' thinProvisioned and eagerlyScrub in vCenter,
// as "thin", "lazy" or "eager", in their order.
func backings(t *testing.T, ctx context.Context, c *vim25.Client, name string) []string {
	t.Helper()
	vm, err := find.NewFinder(c, true).VirtualMachine(ctx, "/GVA/vm/rvav/web/dev/"+name)
	if err != nil {
		t.Fatal(err)
	}
	devices, err := vm.Device(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, d := range devices.SelectByType((*types.VirtualDisk)(nil)) {
		b := d.(*types.VirtualDisk).Backing.(*types.VirtualDiskFlatVer2BackingInfo)
		switch {
		case b.ThinProvisioned != nil && *b.ThinProvisioned:
			out = append(out, "thin")
		case b.EagerlyScrub != nil && *b.EagerlyScrub:
			out = append(out, "eager")
		default:
			out = append(out, "lazy")
		}
	}
	return out
}

// TestDiskProvisioning provisions disks as blueprints and the day-2 Add a
// disk say (T1.3, ADR-0036): a disk's own provisioning, its blueprint's,
// and the VM's first disk's when neither says; vRA's names are refused.
func TestDiskProvisioning(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin = "mkeller"
		def := targetItem("dvpg-app-dev-vl310", nil, nil)
		def.Blueprint["provisioning"] = "thin"
		def.Blueprint["disks"] = []any{map[string]any{"size_gb": 10, "provisioning": "thick_eager_zeroed"}, map[string]any{"size_gb": 5}}

		bad := targetItem("dvpg-app-dev-vl310", nil, nil)
		bad.Blueprint["disks"] = []any{map[string]any{"size_gb": 10, "provisioning": "eagerZeroedThick"}}
		var problem struct{ Errors []api.FieldError }
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "t13-bad-disks", Name: "Bad", Summary: "Bad.",
			Category: "compute", Type: api.VmBlueprint, Definition: &bad}, 422, &problem)
		if !slices.Contains(fieldsOf(problem.Errors), "/definition/blueprint/disks/0/provisioning") {
			t.Fatalf("vRA's provisioning name = %+v", problem.Errors)
		}

		var it api.AdminCatalogItem
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "t13-disks", Name: "Disks", Summary: "Disks.",
			Category: "compute", Type: api.VmBlueprint, Definition: &def}, 201, &it)
		h.json("POST", "/api/v1/admin/catalog/items/t13-disks/versions/"+it.DraftVersion.Id+"/publish", admin, nil, 200, nil)
		h.json("PUT", "/api/v1/admin/catalog/items/t13-disks/entitlements", admin, api.CatalogEntitlements{AllProjects: true, ProjectIds: []string{}}, 200, nil)
		var req api.Request
		h.json("POST", "/api/v1/requests", "tnguyen", api.RequestDraft{CatalogItemId: it.Id, VersionId: it.DraftVersion.Id,
			ProjectId: "prj_web", Environment: "dev", Inputs: map[string]any{"role": "disks"}}, 202, &req, "Idempotency-Key", uuid.NewString())
		h.runWorkflow(req.Id, nil)
		if req = h.waitStatus(req.Id, "tnguyen", api.RequestStatusSucceeded); req.DeploymentIds == nil {
			t.Fatalf("request = %+v", req)
		}
		// vcsim ignores the clone's disk locators: the template's disk keeps
		// its backing there, while the added ones are as the blueprint says.
		got := backings(t, ctx, c, "web-dev-disks01")
		if len(got) != 3 || got[1] != "eager" || got[2] != "thin" {
			t.Fatalf("disks after the clone = %v", got)
		}
		d := &vmDeployment{h: h, id: (*req.DeploymentIds)[0], viewer: "tnguyen"}
		d.resource = *d.actions("tnguyen")["power_off"].ResourceId
		vm := d.vm()
		if len(vm.Disks) != 3 || vm.Disks[1].Provisioning == nil || *vm.Disks[1].Provisioning != api.ThickEagerZeroed ||
			vm.Disks[2].Provisioning == nil || *vm.Disks[2].Provisioning != api.Thin {
			t.Fatalf("VM disks = %+v", vm.Disks)
		}

		// Day 2: as chosen, or as the VM's first disk.
		form := *d.actions("tnguyen")["disk_add"].Form
		if form.InitialValues["provisioning"] != "as_first_disk" {
			t.Fatalf("Add a disk form = %+v", form.InitialValues)
		}
		r := d.succeed("tnguyen", "disk_add", map[string]any{"size_gb": 20, "provisioning": "thick_eager_zeroed"})
		var steps struct{ Items []api.Step }
		h.json("GET", "/api/v1/requests/"+r.Id+"/steps", "tnguyen", nil, 200, &steps)
		// The datastore left out is the one the form starts with (ADR-0055).
		want := fmt.Sprint("Add a 20 GB disk on ", form.InitialValues["datastore"])
		if !slices.ContainsFunc(steps.Items, func(s api.Step) bool { return s.Title == want }) {
			t.Fatalf("Add a disk steps = %+v", steps.Items)
		}

		d.succeed("tnguyen", "disk_add", map[string]any{"size_gb": 30, "provisioning": "as_first_disk"})
		d.succeed("tnguyen", "disk_add", map[string]any{"size_gb": 40})
		got = backings(t, ctx, c, "web-dev-disks01")
		if len(got) != 6 || got[3] != "eager" || got[4] != got[0] || got[5] != got[0] {
			t.Fatalf("disks after day 2 = %v", got)
		}
	})
}
