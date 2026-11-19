package e2e

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
)

// farmKeyed is webFarm with the web component's extra disks, each
// "key:size", or "size" for a disk without a key.
func farmKeyed(disks ...string) api.CatalogVersionDefinition {
	def := webFarm(false)
	web := def.Blueprint["components"].([]any)[0].(map[string]any)
	var list []any
	for _, d := range disks {
		key, size, ok := strings.Cut(d, ":")
		if !ok {
			key, size = "", d
		}
		gb, _ := strconv.Atoi(size)
		disk := map[string]any{"size_gb": gb}
		if key != "" {
			disk["key"] = key
		}
		list = append(list, disk)
	}
	web["disks"] = list
	return def
}

// keyedDisks are a VM's disks as "size:blueprint disk:key".
func keyedDisks(vm api.VmProperties) []string {
	var out []string
	for _, d := range vm.Disks {
		j := "-"
		if d.BlueprintDisk != nil {
			j = fmt.Sprint(*d.BlueprintDisk)
		}
		out = append(out, fmt.Sprintf("%d:%s:%s", d.CapacityGb, j, deref(d.BlueprintDiskKey)))
	}
	return out
}

// TestUpdateDiskKeys: once a version gives its extra disks keys (#191),
// an update tells the versions' disks apart by them, so it removes one and
// grows another in one go, on the right disks; each update that succeeds
// records where the version it leaves puts the disks.
func TestUpdateDiskKeys(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, owner, web = "mkeller", "tnguyen", "prj_web"
		def := farmKeyed("10", "20", "30")
		var item api.AdminCatalogItem
		h.json("POST", "/api/v1/admin/catalog/items", admin, api.CatalogItemCreate{Slug: "web-farm", Name: "Web farm",
			Summary: "Web servers.", Category: "compute", Type: api.VmBlueprint, Definition: &def}, 201, &item)
		h.json("POST", "/api/v1/admin/catalog/items/web-farm/versions/"+item.DraftVersion.Id+"/publish", admin, nil, 200, nil)
		h.json("PUT", "/api/v1/admin/catalog/items/web-farm/entitlements", admin, api.CatalogEntitlements{AllProjects: true, ProjectIds: []string{}}, 200, nil)
		publish := func(def api.CatalogVersionDefinition) string {
			var draft api.CatalogItemVersionDetail
			h.json("POST", "/api/v1/admin/catalog/items/web-farm/versions", admin, api.CatalogItemVersionCreate{Definition: &def}, 201, &draft)
			h.json("POST", "/api/v1/admin/catalog/items/web-farm/versions/"+draft.Id+"/publish", admin, nil, 200, nil)
			return draft.Id
		}
		inputs := map[string]any{"app": "farm", "web_nodes": 1, "size": "S", "cluster": "GVA-DEV-CL01", "network": "dvpg-app-dev-vl310"}
		var built api.Request
		h.json("POST", "/api/v1/requests", owner, api.RequestDraft{CatalogItemId: item.Id, VersionId: item.DraftVersion.Id, ProjectId: web,
			Environment: "dev", Inputs: inputs}, 202, &built, "Idempotency-Key", uuid.NewString())
		h.runWorkflow(built.Id, nil)
		h.waitStatus(built.Id, owner, api.RequestStatusSucceeded)
		depID := (*built.DeploymentIds)[0]
		const web01 = "web-dev-farmweb01"
		vm := func() api.VmProperties {
			t.Helper()
			var dep api.DeploymentDetail
			h.json("GET", "/api/v1/deployments/"+depID, owner, nil, 200, &dep)
			for _, r := range dep.Resources {
				if v, _ := r.ValueByDiscriminator(); v != nil {
					if vm, ok := v.(api.VmResource); ok && vm.Name == web01 {
						return vm.Vm
					}
				}
			}
			t.Fatal("no " + web01)
			return api.VmProperties{}
		}
		disks := func(want ...string) {
			t.Helper()
			v := vm()
			// In any order: a disk added takes the unit of one removed.
			if got := keyedDisks(v); !slices.Equal(slices.Sorted(slices.Values(got)), slices.Sorted(slices.Values(want))) {
				t.Fatalf("disks = %v, want %v", got, want)
			}
			if inVC, err := h.vc.GetVM(ctx, v.Moref); err != nil || len(inVC.Disks) != len(want) {
				t.Fatalf("in vCenter: %+v %v", inVC, err)
			}
		}
		label := func(key string) string {
			for _, d := range vm().Disks {
				if deref(d.BlueprintDiskKey) == key {
					return d.Label
				}
			}
			return "no disk " + key
		}
		update := func(version string) []string {
			t.Helper()
			var pv api.UpdatePreview
			h.json("POST", "/api/v1/deployments/"+depID+"/update/preview", owner, api.UpdateDraft{VersionId: version, Inputs: inputs}, 200, &pv)
			var req api.Request
			h.json("POST", "/api/v1/deployments/"+depID+"/update", owner, api.UpdateDraft{VersionId: version, Inputs: inputs}, 202, &req,
				"Idempotency-Key", uuid.NewString())
			h.runWorkflow(req.Id, nil)
			h.waitStatus(req.Id, owner, api.RequestStatusSucceeded)
			var out []string
			for _, ch := range pv.Changes {
				out = append(out, ch.Title)
			}
			return out
		}
		disks("50:0:", "10:1:", "20:2:", "30:3:")

		// v2 gives the disks keys: nothing changes on the VM, and the
		// update records them.
		if got := update(publish(farmKeyed("data:10", "logs:20", "tmp:30"))); len(got) > 0 {
			t.Fatalf("v2's changes = %v", got)
		}
		disks("50:0:", "10:1:data", "20:2:logs", "30:3:tmp")

		// v3 removes logs and grows tmp, which takes logs's place.
		tmp, logs := label("tmp"), label("logs")
		if got := update(publish(farmKeyed("data:10", "tmp:40"))); !slices.Equal(got, []string{
			"Grow " + tmp + " of " + web01 + " to 40 GB", "Remove " + logs + " from " + web01}) {
			t.Fatalf("v3's changes = %v", got)
		}
		disks("50:0:", "10:1:data", "40:2:tmp")

		// v4 grows tmp at its new place and adds a disk after it.
		if got := update(publish(farmKeyed("data:10", "tmp:50", "swap:5"))); !slices.Equal(got, []string{
			"Grow " + label("tmp") + " of " + web01 + " to 50 GB", "Add a 5 GB disk to " + web01}) {
			t.Fatalf("v4's changes = %v", got)
		}
		disks("50:0:", "10:1:data", "50:2:tmp", "5:3:swap")
	})
}
