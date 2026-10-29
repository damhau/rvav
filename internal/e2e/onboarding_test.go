package e2e

import (
	"context"
	"slices"
	"testing"

	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/types"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/integrations/efficientip"
	"github.com/damhau/rvav/internal/integrations/vsphere"
	"github.com/damhau/rvav/internal/inventory"
	"github.com/damhau/rvav/internal/present"
)

// existingVM makes a VM in vCenter outside rvav, running, with VMware
// Tools reporting its address and host name.
func existingVM(t *testing.T, ctx context.Context, h *harness, c *vim25.Client, model *simulator.Model, name, ip string) string {
	t.Helper()
	vm, err := h.vc.CloneVM(ctx, vsphere.VMSpec{Name: name, Template: "tpl-rhel9.4-2026.10", Cluster: "GVA-DEV-CL01",
		Datastore: "vsan-gva-dev-cl01", Network: "dvpg-app-dev-vl310", Folder: "/GVA/vm/rvav/web/dev", VCPU: 2, MemoryMB: 4096, DiskGB: 40})
	if err != nil {
		t.Fatal(err)
	}
	ref := types.ManagedObjectReference{Type: "VirtualMachine", Value: vm.Moref}
	task, err := object.NewVirtualMachine(c, ref).PowerOn(ctx)
	if err == nil {
		err = task.Wait(ctx)
	}
	if err != nil {
		t.Fatal(err)
	}
	sim := model.Map().Get(ref).(*simulator.VirtualMachine)
	if sim.Guest == nil {
		sim.Guest = &types.GuestInfo{}
	}
	sim.Guest.IpAddress, sim.Guest.HostName = ip, name+".corp.example"
	sim.Guest.Net = []types.GuestNicInfo{{Network: "dvpg-app-dev-vl310", IpAddress: []string{ip}, Connected: true}}
	sim.Guest.ToolsRunningStatus = string(types.VirtualMachineToolsRunningStatusGuestToolsRunning)
	return vm.Moref
}

// TestOnboarding: administrators list the VMs rvav doesn't manage and
// adopt them into deployments, with an owner, project, environment, item
// and lease, and the address and DNS records EfficientIP has for them
// (S13.3). Adopted VMs are deployments like others: listed no more, day-2
// actions run on them, the sync keeps them in sync.
func TestOnboarding(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	model := simulator.VPX()
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin = "mkeller"
		built := buildVM(h, model) // rvav's own: never offered
		// EfficientIP knows erp-app01: its address and its A and PTR records.
		const fqdn = "erp-app01.corp.example"
		addr, err := h.acts.IPAM.Reserve(ctx, efficientip.Subnet{CIDR: "10.31.10.0/24"}, fqdn, "legacy")
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range []efficientip.Record{{FQDN: fqdn, Type: "A", Value: addr.IP, Zone: "corp.example"},
			{FQDN: efficientip.PTRName(addr.IP), Type: "PTR", Value: fqdn, Zone: "in-addr.arpa"}} {
			if _, err := h.acts.IPAM.CreateRecord(ctx, r); err != nil {
				t.Fatal(err)
			}
		}
		legacy1 := existingVM(t, ctx, h, c, model, "erp-app01", addr.IP)
		legacy2 := existingVM(t, ctx, h, c, model, "erp-app02", "10.31.10.202")
		legacy3 := existingVM(t, ctx, h, c, model, "erp-db01", "10.31.10.203")
		sync := &inventory.Syncer{Store: h.store, VSphere: h.vc, Events: h.acts.Events, Log: h.acts.Log}
		if err := sync.SyncOnce(ctx); err != nil {
			t.Fatal(err)
		}

		// Only administrators see them, and only those rvav doesn't manage.
		list := func(query string) api.OnboardingVmPage {
			t.Helper()
			var page api.OnboardingVmPage
			h.json("GET", "/api/v1/admin/onboarding/vms?"+query, admin, nil, 200, &page)
			return page
		}
		h.json("GET", "/api/v1/admin/onboarding/vms", "tnguyen", nil, 403, nil)
		page := list("q=erp-&limit=2")
		if page.Total != 3 || len(page.Items) != 2 || page.Items[0].Name != "erp-app01" || page.NextCursor == nil ||
			deref(page.Items[0].IpAddress) != addr.IP || page.Items[0].PowerState != api.On {
			t.Fatalf("onboarding page = %+v", page)
		}
		if next := list("q=erp-&limit=2&cursor=" + *page.NextCursor); len(next.Items) != 1 || next.Items[0].Name != "erp-db01" {
			t.Fatalf("next page = %+v", next.Items)
		}
		if all := list("limit=200"); slices.ContainsFunc(all.Items, func(v api.OnboardingVm) bool { return v.Moref == built.vm().Moref }) {
			t.Fatal("rvav's own VM is offered for onboarding")
		}
		if ip := list("q=10.31.10.20"); ip.Total != 2 {
			t.Fatalf("by address = %d", ip.Total)
		}

		var item api.CatalogItem
		h.json("GET", "/api/v1/catalog/items/rhel9-server?project_id=prj_web", "lmartin", nil, 200, &item)
		days := 30
		ref := func(moref string) struct {
			Moref   string `json:"moref"`
			Vcenter string `json:"vcenter"`
		} {
			return struct {
				Moref   string `json:"moref"`
				Vcenter string `json:"vcenter"`
			}{moref, "vc01.corp.example"}
		}
		draft := api.OnboardingDraft{ProjectId: "prj_web", Environment: "dev", OwnerId: "usr_tnguyen", CatalogItemId: item.Id,
			LeaseDays: &days}

		// What can't be adopted is said, field by field, and nothing is.
		bad := draft
		bad.OwnerId, bad.CatalogItemId, bad.LeaseDays = "usr_afavre", "postgres-cluster", present.Ptr(400)
		bad.Vms = append(bad.Vms, ref(legacy1), ref(legacy1), ref(built.vm().Moref), ref("vm-nosuch"))
		var problem struct{ Errors []api.FieldError }
		h.json("POST", "/api/v1/admin/onboarding", admin, bad, 422, &problem)
		var fields []string
		for _, e := range problem.Errors {
			fields = append(fields, e.Field+" "+deref(e.Code))
		}
		want := []string{"/owner_id invalid", "/catalog_item_id invalid", "/lease_days too_long", "/vms/1 duplicate",
			"/vms/2 not_found", "/vms/3 not_found"}
		if !slices.Equal(fields, want) {
			t.Fatalf("errors = %v, want %v", fields, want)
		}
		h.json("POST", "/api/v1/admin/onboarding", "tnguyen", draft, 403, nil)
		// None, or a name for none: refused, not a deployment of nothing.
		empty := draft
		empty.DeploymentName = present.Ptr("erp-none")
		var none struct{ Errors []api.FieldError }
		h.json("POST", "/api/v1/admin/onboarding", admin, empty, 422, &none)
		if len(none.Errors) != 1 || none.Errors[0].Field != "/vms" {
			t.Fatalf("no VMs = %+v", none.Errors)
		}

		// Two VMs, each a deployment of its own.
		draft.Vms = append(draft.Vms[:0], ref(legacy1), ref(legacy2))
		var res api.OnboardingResult
		h.json("POST", "/api/v1/admin/onboarding", admin, draft, 200, &res)
		if len(res.Deployments) != 2 || res.Deployments[0].Name != "erp-app01" || res.Deployments[0].Addresses != 1 ||
			res.Deployments[0].Records != 2 || res.Deployments[1].Addresses != 0 || res.Deployments[1].Records != 0 {
			t.Fatalf("adopted = %+v", res.Deployments)
		}
		var dep struct {
			api.DeploymentDetail
			Resources []map[string]any `json:"resources"`
		}
		h.json("GET", "/api/v1/deployments/"+res.Deployments[0].Id, "tnguyen", nil, 200, &dep)
		types := map[string]int{}
		for _, r := range dep.Resources {
			types[r["type"].(string)]++
		}
		if dep.Status != api.DeploymentStatusReady || dep.Owner.Id != "usr_tnguyen" || dep.Environment != "dev" ||
			dep.CatalogItem.Slug != "rhel9-server" || dep.Lease.ExpiresAt == nil || dep.SyncState != api.InSync ||
			types["vsphere.vm"] != 1 || types["ipam.address"] != 1 || types["dns.record"] != 2 ||
			dep.PrimaryAddress == nil || dep.PrimaryAddress.Ip != addr.IP {
			t.Fatalf("adopted deployment = %+v %v", dep.DeploymentDetail, types)
		}
		vmRes := dep.Resources[slices.IndexFunc(dep.Resources, func(r map[string]any) bool { return r["type"] == "vsphere.vm" })]
		if vm := vmRes["vm"].(map[string]any); vm["fqdn"] != fqdn || vm["power_state"] != "on" {
			t.Fatalf("adopted VM = %v", vm)
		}
		if again := list("q=erp-"); again.Total != 1 {
			t.Fatalf("still offered = %+v", again.Items)
		}
		draft.Vms = draft.Vms[:1]
		h.json("POST", "/api/v1/admin/onboarding", admin, draft, 422, nil)

		// Audited; in sync after the next read; day-2 actions run on it.
		var audit struct{ Items []api.AuditEvent }
		h.json("GET", "/api/v1/audit?action=deployment.adopted", admin, nil, 200, &audit)
		if len(audit.Items) != 2 {
			t.Fatalf("audit = %+v", audit.Items)
		}
		if err := sync.SyncOnce(ctx); err != nil {
			t.Fatal(err)
		}
		adopted := &vmDeployment{h: h, model: model, id: res.Deployments[0].Id, viewer: "tnguyen"}
		adopted.resource = *adopted.actions("tnguyen")["power_off"].ResourceId
		if adopted.vm().PowerState != api.On {
			t.Fatal("adopted VM not on")
		}
		adopted.succeed("tnguyen", "power_off", nil)
		if adopted.vm().PowerState != api.Off {
			t.Fatal("power off on the adopted VM")
		}

		// The last one in a deployment of a given name, without a lease.
		draft.Vms, draft.LeaseDays, draft.DeploymentName = draft.Vms[:0], nil, present.Ptr("erp-dev")
		draft.Vms = append(draft.Vms, ref(legacy3))
		h.json("POST", "/api/v1/admin/onboarding", admin, draft, 200, &res)
		if len(res.Deployments) != 1 || res.Deployments[0].Name != "erp-dev" || !slices.Equal(res.Deployments[0].Vms, []string{"erp-db01"}) {
			t.Fatalf("one deployment = %+v", res.Deployments)
		}

		// A VM a request is building is in vCenter before rvav records it:
		// not offered.
		building := existingVM(t, ctx, h, c, model, "web-dev-new01", "10.31.10.210")
		if err := sync.SyncOnce(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := h.store.Pool.Exec(ctx, `update requests set status = 'running',
			plan = jsonb_set(plan, '{vms,0,name}', '"web-dev-new01"') where id = $1`, built.built.Id); err != nil {
			t.Fatal(err)
		}
		if p := list("q=web-dev-new"); p.Total != 0 {
			t.Fatalf("a VM being built is offered: %+v", p.Items)
		}
		draft.Vms, draft.DeploymentName = draft.Vms[:0], nil
		draft.Vms = append(draft.Vms, ref(building))
		h.json("POST", "/api/v1/admin/onboarding", admin, draft, 422, nil)

		// Two VMs sharing an address EfficientIP has (a virtual address):
		// apart, refused; together, the address once.
		vip, err := h.acts.IPAM.Reserve(ctx, efficientip.Subnet{CIDR: "10.31.10.0/24"}, "erp-vip.corp.example", "legacy")
		if err != nil {
			t.Fatal(err)
		}
		node1 := existingVM(t, ctx, h, c, model, "erp-node01", vip.IP)
		node2 := existingVM(t, ctx, h, c, model, "erp-node02", vip.IP)
		if err := sync.SyncOnce(ctx); err != nil {
			t.Fatal(err)
		}
		draft.Vms = append(draft.Vms[:0], ref(node1), ref(node2))
		h.json("POST", "/api/v1/admin/onboarding", admin, draft, 422, &problem)
		if len(problem.Errors) != 1 || problem.Errors[0].Field != "/vms/1" || deref(problem.Errors[0].Code) != "shared" {
			t.Fatalf("a shared address apart = %+v", problem.Errors)
		}
		draft.DeploymentName = present.Ptr("erp-cluster")
		h.json("POST", "/api/v1/admin/onboarding", admin, draft, 200, &res)
		if len(res.Deployments) != 1 || res.Deployments[0].Addresses != 1 {
			t.Fatalf("a shared address together = %+v", res.Deployments)
		}
	}, model)
}
