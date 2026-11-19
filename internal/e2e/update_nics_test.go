package e2e

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/integrations/vsphere"
)

// farmOnNetwork is webFarm with a DHCP adapter backup on this network.
func farmOnNetwork(network string) api.CatalogVersionDefinition {
	def := webFarm(false)
	web := def.Blueprint["components"].([]any)[0].(map[string]any)
	web["nics"] = []any{map[string]any{"key": "backup", "network": `"` + network + `"`, "ipam": "dhcp"}}
	return def
}

// failAddNIC fails adding an adapter to one VM.
type failAddNIC struct {
	vsphere.Driver
	moref string
}

func (f failAddNIC) AddNIC(ctx context.Context, moref, network string) (string, error) {
	if moref == f.moref {
		return "", errors.New("no free PCI slot")
	}
	return f.Driver.AddNIC(ctx, moref, network)
}

// nicList is a VM's adapters as "network:blueprint adapter", "-" for an
// adapter that isn't one.
func nicList(vm api.VmProperties) []string {
	var out []string
	for _, n := range vm.Nics {
		out = append(out, n.Network+":"+cmp.Or(deref(n.BlueprintNic), "-"))
	}
	return out
}

// TestUpdateNICsRecorded: rvav records which blueprint adapter each
// adapter of a VM is (#192), so an update made again after one failed
// part-way adds a DHCP adapter only where it is missing, and moves and
// removes only the adapters it recorded, not one the owner added.
func TestUpdateNICsRecorded(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, owner, web = "mkeller", "tnguyen", "prj_web"
		const app, db = "dvpg-app-dev-vl310", "dvpg-db-dev-vl320"
		def := webFarm(false)
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
		inputs := map[string]any{"app": "farm", "web_nodes": 2, "size": "S", "cluster": "GVA-DEV-CL01", "network": app}
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
		nics := func(want map[string][]string) {
			t.Helper()
			for name, vm := range vms() {
				if got := nicList(vm.Vm); !slices.Equal(got, want[name]) {
					t.Fatalf("%s's adapters = %v, want %v", name, got, want[name])
				}
				inVC, err := h.vc.GetVM(ctx, vm.Vm.Moref)
				if err != nil || len(inVC.NICs) != len(want[name]) {
					t.Fatalf("%s in vCenter: %+v %v", name, inVC, err)
				}
			}
		}
		nics(map[string][]string{web01: {app + ":nic1"}, web02: {app + ":nic1"}})
		update := func(version string, want api.RequestStatus) api.UpdatePreview {
			t.Helper()
			var pv api.UpdatePreview
			h.json("POST", "/api/v1/deployments/"+depID+"/update/preview", owner, api.UpdateDraft{VersionId: version, Inputs: inputs}, 200, &pv)
			var req api.Request
			h.json("POST", "/api/v1/deployments/"+depID+"/update", owner, api.UpdateDraft{VersionId: version, Inputs: inputs}, 202, &req,
				"Idempotency-Key", uuid.NewString())
			h.runWorkflow(req.Id, nil)
			h.waitStatus(req.Id, owner, want)
			return pv
		}
		titles := func(pv api.UpdatePreview) []string {
			var out []string
			for _, ch := range pv.Changes {
				out = append(out, ch.Title)
			}
			return out
		}

		// The owner adds an adapter to web01: it isn't the blueprint's.
		var r api.Request
		rid := vms()[web01].Id
		h.json("POST", "/api/v1/deployments/"+depID+"/actions", owner, api.Day2Draft{Action: "nic_add", ResourceId: &rid,
			Inputs: map[string]any{"network": db}}, 202, &r, "Idempotency-Key", uuid.NewString())
		h.runWorkflow(r.Id, nil)
		h.waitStatus(r.Id, owner, api.RequestStatusSucceeded)

		// v2 adds a DHCP adapter; adding it to web02 fails, so the update
		// fails with web01's added and recorded.
		v2 := publish(farmOnNetwork(db))
		vc := h.acts.VSphere
		h.acts.VSphere = failAddNIC{vc, vms()[web02].Vm.Moref}
		update(v2, api.RequestStatusFailed)
		h.acts.VSphere = vc
		nics(map[string][]string{web01: {app + ":nic1", db + ":-", db + ":backup"}, web02: {app + ":nic1"}})
		// Made again, it adds the adapter to web02 only.
		if got := titles(update(v2, api.RequestStatusSucceeded)); !slices.Equal(got, []string{"Add a network adapter on " + db + " to " + web02}) {
			t.Fatalf("the update made again = %v", got)
		}
		nics(map[string][]string{web01: {app + ":nic1", db + ":-", db + ":backup"}, web02: {app + ":nic1", db + ":backup"}})

		// v3 moves it: the owner's adapter stays where it is.
		pv := update(publish(farmOnNetwork(app)), api.RequestStatusSucceeded)
		if got := titles(pv); !slices.Equal(got, []string{"Move Network adapter 3 of " + web01 + " to " + app, "Move Network adapter 2 of " + web02 + " to " + app}) {
			t.Fatalf("v3's changes = %v", got)
		}
		nics(map[string][]string{web01: {app + ":nic1", db + ":-", app + ":backup"}, web02: {app + ":nic1", app + ":backup"}})

		// v4 drops it: the owner's adapter stays.
		pv = update(publish(webFarm(false)), api.RequestStatusSucceeded)
		if got := titles(pv); !slices.Equal(got, []string{"Remove Network adapter 3 from " + web01, "Remove Network adapter 2 from " + web02}) {
			t.Fatalf("v4's changes = %v", got)
		}
		nics(map[string][]string{web01: {app + ":nic1", db + ":-"}, web02: {app + ":nic1"}})
	})
}
