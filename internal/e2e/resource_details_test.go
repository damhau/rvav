package e2e

import (
	"context"
	"maps"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/property"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/inventory"
)

// TestResourceDetails: a deployment's VM carries what its properties panel
// shows: its resource pool, the custom properties it was built with, a
// property that reads a secret input masked, and its link into the vSphere
// Client from its vCenter's host and instance UUID, absent until the
// inventory sync has read them (X1.1).
func TestResourceDetails(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, token = "mkeller", "T0ken-0f-the-app"
		h.json("PATCH", "/api/v1/admin/projects/prj_web", admin, api.AdminProjectUpdate{
			Properties: &api.CustomProperties{"costCenter": "CC-1001"}}, 200, nil)

		def := guestItem("tpl-rhel9.4-2026.10", map[string]any{"os_family": "linux", "domain": "corp.example"})
		def.FormSchema["properties"].(map[string]any)["token"] = map[string]any{"type": "string"}
		def.UiSchema.Fields["token"] = api.UiField{Widget: api.UiWidgetSecret, Label: "Token"}
		def.UiSchema.Layout.Sections[0].Fields = append(def.UiSchema.Layout.Sections[0].Fields, "token")
		def.Blueprint["bindings"].(map[string]any)["resource_pool"] = "'rvav/dev'"
		def.Blueprint["properties"] = map[string]any{"app": "${inputs.role}", "auth": "Bearer ${inputs.token}"}
		item := publishItem(h, "details", def)

		var req api.Request
		h.json("POST", "/api/v1/requests", "tnguyen", api.RequestDraft{CatalogItemId: item.Id, VersionId: item.DraftVersion.Id,
			ProjectId: "prj_web", Environment: "dev", Inputs: map[string]any{"role": "det", "cluster": "GVA-DEV-CL01",
				"datastore": "vsan-gva-dev-cl01", "network": "dvpg-app-dev-vl310", "token": token}},
			202, &req, "Idempotency-Key", uuid.NewString())
		h.runWorkflow(req.Id, nil)
		h.waitStatus(req.Id, "tnguyen", api.RequestStatusSucceeded)
		d := &vmDeployment{h: h, id: (*req.DeploymentIds)[0], viewer: "tnguyen"}

		uuidOf := c.ServiceContent.About.InstanceUuid
		if uuidOf == "" {
			t.Fatal("vcsim gives no instance UUID")
		}
		check := func(when string) api.VmProperties {
			t.Helper()
			vm := d.vm()
			want := map[string]string{"costCenter": "CC-1001", "app": "det", "auth": "••••"}
			if vm.CustomProperties == nil || !maps.Equal(*vm.CustomProperties, want) {
				t.Fatalf("%s: custom properties = %v", when, vm.CustomProperties)
			}
			if len(vm.Nics) != 1 || vm.Nics[0].Dhcp == nil || *vm.Nics[0].Dhcp {
				t.Fatalf("%s: adapters = %+v", when, vm.Nics)
			}
			if deref(vm.ResourcePool) != "rvav/dev" || deref(vm.Cluster) != "GVA-DEV-CL01" || deref(vm.Host) == "" {
				t.Fatalf("%s: placement = %q %q %q", when, deref(vm.ResourcePool), deref(vm.Cluster), deref(vm.Host))
			}
			return vm
		}
		vm := check("built")
		link := "https://" + c.URL().Hostname() + "/ui/app/vm;nav=h/urn:vmomi:VirtualMachine:" + vm.Moref + ":" + uuidOf + "/summary"
		if deref(vm.VsphereUrl) != link {
			t.Fatalf("vsphere_url = %q, want %q", deref(vm.VsphereUrl), link)
		}

		// The secret's value is nowhere: not in the plan, the resource, nor
		// vSphere's custom attributes and notes.
		var n int
		if err := h.store.Pool.QueryRow(ctx, `select (select count(*) from requests where id = $1 and plan::text like $2)
			+ (select count(*) from resources where properties::text like $2)`, req.Id, "%"+token+"%").Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatalf("the token is in %d rows", n)
		}
		var mvm mo.VirtualMachine
		if err := property.DefaultCollector(c).RetrieveOne(ctx, vmRefByName(t, ctx, c, "web-dev-det01"),
			[]string{"config.annotation", "customValue"}, &mvm); err != nil {
			t.Fatal(err)
		}
		if notes := mvm.Config.Annotation; strings.Contains(notes, token) || !strings.Contains(notes, "auth: ••••") {
			t.Fatalf("notes = %q", notes)
		}
		for _, v := range mvm.CustomValue {
			if s, ok := v.(*types.CustomFieldStringValue); ok && strings.Contains(s.Value, token) {
				t.Fatalf("a custom attribute holds the token: %q", s.Value)
			}
		}

		// A vCenter the sync hasn't read gives no link; the next sync does.
		if _, err := h.store.Pool.Exec(ctx, `delete from inv_vcenters`); err != nil {
			t.Fatal(err)
		}
		if vm := d.vm(); vm.VsphereUrl != nil {
			t.Fatalf("an unsynced vCenter's VM links to %q", *vm.VsphereUrl)
		}
		if err := (&inventory.Syncer{Store: h.store, VSphere: h.vc, Events: h.acts.Events, Log: h.acts.Log}).SyncOnce(ctx); err != nil {
			t.Fatal(err)
		}
		if vm := check("synced"); deref(vm.VsphereUrl) != link {
			t.Fatalf("vsphere_url once synced = %q", deref(vm.VsphereUrl))
		}
		h.svc.Config.VCenter.Name, h.svc.Config.VCenter.URL = "vc01.corp.example", "https://vc01.corp.example/sdk"
		var list api.VCenterList
		h.json("GET", "/api/v1/admin/vcenters", admin, nil, 200, &list)
		if len(list.Items) == 0 || list.Items[0].Inventory == nil || deref(list.Items[0].Inventory.InstanceUuid) != uuidOf {
			t.Fatalf("vCenters = %+v", list.Items)
		}
		var dep struct {
			SyncState api.SyncState `json:"sync_state"`
		}
		h.json("GET", "/api/v1/deployments/"+d.id, "tnguyen", nil, 200, &dep)
		if dep.SyncState != api.InSync {
			t.Fatalf("the sync left the deployment %s", dep.SyncState)
		}
	})
}
