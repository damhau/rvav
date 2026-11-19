package e2e

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/integrations/vsphere"
)

// farmWith is webFarm with the web component's extra disks of these sizes.
func farmWith(disks ...int) api.CatalogVersionDefinition {
	def := webFarm(false)
	web := def.Blueprint["components"].([]any)[0].(map[string]any)
	var list []any
	for _, d := range disks {
		list = append(list, map[string]any{"size_gb": d})
	}
	if len(list) > 0 {
		web["disks"] = list
	}
	return def
}

// failAddDisk fails adding a disk to one VM.
type failAddDisk struct {
	vsphere.Driver
	moref string
}

func (f failAddDisk) AddDisk(ctx context.Context, moref string, d vsphere.DiskSpec) (string, error) {
	if moref == f.moref {
		return "", errors.New("the datastore is full")
	}
	return f.Driver.AddDisk(ctx, moref, d)
}

// diskList is a VM's disks as "size:blueprint disk", "-" for a disk that
// isn't one.
func diskList(vm api.VmProperties) []string {
	var out []string
	for _, d := range vm.Disks {
		j := "-"
		if d.BlueprintDisk != nil {
			j = fmt.Sprint(*d.BlueprintDisk)
		}
		out = append(out, fmt.Sprintf("%d:%s", d.CapacityGb, j))
	}
	return out
}

// TestUpdateDisksRecorded: rvav records which blueprint disk each disk of
// a VM is (X1.6), so an update acts on those and on no other. A disk the
// owner added between them stays; an update that failed part-way and is
// made again adds a disk only where it is missing; a disk gone since the
// update was planned fails its step.
func TestUpdateDisksRecorded(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, owner, web = "mkeller", "tnguyen", "prj_web"
		def := farmWith(20)
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
		inputs := map[string]any{"app": "farm", "web_nodes": 2, "size": "S", "cluster": "GVA-DEV-CL01", "network": "dvpg-app-dev-vl310"}
		var built api.Request
		h.json("POST", "/api/v1/requests", owner, api.RequestDraft{CatalogItemId: item.Id, VersionId: item.DraftVersion.Id, ProjectId: web,
			Environment: "dev", Inputs: inputs}, 202, &built, "Idempotency-Key", uuid.NewString())
		h.runWorkflow(built.Id, nil)
		h.waitStatus(built.Id, owner, api.RequestStatusSucceeded)
		depID := (*built.DeploymentIds)[0]
		vms := func() map[string]api.VmResource {
			t.Helper()
			var dep api.DeploymentDetail
			h.json("GET", "/api/v1/deployments/"+depID, owner, nil, 200, &dep)
			out := map[string]api.VmResource{}
			for _, r := range dep.Resources {
				if v, _ := r.ValueByDiscriminator(); v != nil {
					if vm, ok := v.(api.VmResource); ok {
						out[vm.Name] = vm
					}
				}
			}
			return out
		}
		const web01, web02 = "web-dev-farmweb01", "web-dev-farmweb02"
		disks := func(want map[string][]string) {
			t.Helper()
			for name, vm := range vms() {
				if got := diskList(vm.Vm); !slices.Equal(got, want[name]) {
					t.Fatalf("%s's disks = %v, want %v", name, got, want[name])
				}
				// As vCenter has them.
				inVC, err := h.vc.GetVM(ctx, vm.Vm.Moref)
				if err != nil || len(inVC.Disks) != len(want[name]) {
					t.Fatalf("%s in vCenter: %+v %v", name, inVC, err)
				}
			}
		}
		disks(map[string][]string{web01: {"50:0", "20:1"}, web02: {"50:0", "20:1"}})
		update := func(version string, want api.RequestStatus) (api.UpdatePreview, api.Request) {
			t.Helper()
			var pv api.UpdatePreview
			h.json("POST", "/api/v1/deployments/"+depID+"/update/preview", owner, api.UpdateDraft{VersionId: version, Inputs: inputs}, 200, &pv)
			var req api.Request
			h.json("POST", "/api/v1/deployments/"+depID+"/update", owner, api.UpdateDraft{VersionId: version, Inputs: inputs}, 202, &req,
				"Idempotency-Key", uuid.NewString())
			h.runWorkflow(req.Id, nil)
			return pv, h.waitStatus(req.Id, owner, want)
		}
		titles := func(pv api.UpdatePreview) []string {
			var out []string
			for _, ch := range pv.Changes {
				out = append(out, ch.Title)
			}
			return out
		}

		// The owner adds a 100 GB disk to web01: it isn't the blueprint's.
		var r api.Request
		rid := vms()[web01].Id
		h.json("POST", "/api/v1/deployments/"+depID+"/actions", owner, api.Day2Draft{Action: "disk_add", ResourceId: &rid,
			Inputs: map[string]any{"size_gb": 100}}, 202, &r, "Idempotency-Key", uuid.NewString())
		h.runWorkflow(r.Id, nil)
		h.waitStatus(r.Id, owner, api.RequestStatusSucceeded)

		// v2 adds a 30 GB disk; adding it to web02 fails, so the update
		// fails with web01's added and recorded.
		v2 := publish(farmWith(20, 30))
		vc := h.acts.VSphere
		h.acts.VSphere = failAddDisk{vc, vms()[web02].Vm.Moref}
		update(v2, api.RequestStatusFailed)
		h.acts.VSphere = vc
		disks(map[string][]string{web01: {"50:0", "20:1", "100:-", "30:2"}, web02: {"50:0", "20:1"}})
		// Made again, it adds the disk to web02 only.
		pv, _ := update(v2, api.RequestStatusSucceeded)
		if got := titles(pv); !slices.Equal(got, []string{"Add a 30 GB disk to " + web02}) {
			t.Fatalf("the update made again = %v", got)
		}
		disks(map[string][]string{web01: {"50:0", "20:1", "100:-", "30:2"}, web02: {"50:0", "20:1", "30:2"}})

		// v3 drops the 30 GB disk: the owner's 100 GB disk stays.
		pv, _ = update(publish(farmWith(20)), api.RequestStatusSucceeded)
		if len(pv.Changes) != 2 || !strings.HasPrefix(pv.Changes[0].Title, "Remove ") || !strings.Contains(deref(pv.Changes[0].Detail), "Its 30 GB") {
			t.Fatalf("v3's changes = %v", titles(pv))
		}
		disks(map[string][]string{web01: {"50:0", "20:1", "100:-"}, web02: {"50:0", "20:1"}})

		// v4 grows the 20 GB disk; web02's is removed in vCenter after the
		// update is planned: its step fails, and the deployment stays at v3.
		v4 := publish(farmWith(40))
		var req api.Request
		h.json("POST", "/api/v1/deployments/"+depID+"/update", owner, api.UpdateDraft{VersionId: v4, Inputs: inputs}, 202, &req,
			"Idempotency-Key", uuid.NewString())
		gone := vms()[web02].Vm.Disks[1]
		if err := h.vc.RemoveDisk(ctx, vms()[web02].Vm.Moref, vsphere.DiskRef{Key: int32(*gone.Key)}); err != nil {
			t.Fatal(err)
		}
		h.runWorkflow(req.Id, nil)
		h.waitStatus(req.Id, owner, api.RequestStatusFailed)
		steps := timelineOf(h, owner, req.Id)
		if s := steps["vsphere.disk_extend@"+web02]; s.Status != api.StepStatusFailed || !strings.Contains(deref(s.Error), "is no longer on the VM") {
			t.Fatalf("the step on the disk gone = %+v", s)
		}
		if got := diskList(vms()[web01].Vm); !slices.Equal(got, []string{"50:0", "40:1", "100:-"}) {
			t.Fatalf("web01's disks = %v", got)
		}
		var dep api.DeploymentDetail
		if h.json("GET", "/api/v1/deployments/"+depID, owner, nil, 200, &dep); dep.Version.Id == v4 {
			t.Fatal("the failed update recorded its version")
		}
	})
}
