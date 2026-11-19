package e2e

import (
	"context"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/integrations/efficientip"
	"github.com/damhau/rvav/internal/inventory"
	"github.com/damhau/rvav/internal/present"
)

// TestUnregister: administrators release a VM, or a whole deployment, from
// rvav (X1.3). Nothing changes in vCenter or EfficientIP: the VM keeps
// running with its address and DNS records, onboarding offers it again,
// and adopting it finds them again. Members and project admins, who don't
// onboard, aren't offered it.
func TestUnregister(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	model := simulator.VPX()
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, lead = "mkeller", "lmartin"
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
		app01 := existingVM(t, ctx, h, c, model, "erp-app01", addr.IP)
		app02 := existingVM(t, ctx, h, c, model, "erp-app02", "10.31.10.202")
		sync := &inventory.Syncer{Store: h.store, VSphere: h.vc, Events: h.acts.Events, Log: h.acts.Log}
		if err := sync.SyncOnce(ctx); err != nil {
			t.Fatal(err)
		}
		type ref = struct {
			Moref   string `json:"moref"`
			Vcenter string `json:"vcenter"`
		}
		var item api.CatalogItem
		h.json("GET", "/api/v1/catalog/items/rhel9-server?project_id=prj_web", lead, nil, 200, &item)
		onboard := func(name *string, morefs ...string) api.OnboardingResult {
			t.Helper()
			draft := api.OnboardingDraft{ProjectId: "prj_web", Environment: "dev", OwnerId: "usr_tnguyen", CatalogItemId: item.Id,
				DeploymentName: name}
			for _, m := range morefs {
				draft.Vms = append(draft.Vms, ref{m, "vc01.corp.example"})
			}
			var res api.OnboardingResult
			h.json("POST", "/api/v1/admin/onboarding", admin, draft, 200, &res)
			return res
		}
		offered := func(name string) bool {
			t.Helper()
			var page api.OnboardingVmPage
			h.json("GET", "/api/v1/admin/onboarding/vms?q="+name, admin, nil, 200, &page)
			return page.Total == 1
		}
		dep := onboard(present.Ptr("erp-dev"), app01, app02).Deployments[0]
		if dep.Addresses != 1 || dep.Records != 2 {
			t.Fatalf("adopted = %+v", dep)
		}

		actions := func(user string) map[string]api.Day2Action {
			t.Helper()
			var out struct{ Items []api.Day2Action }
			h.json("GET", "/api/v1/deployments/"+dep.Id+"/actions", user, nil, 200, &out)
			m := map[string]api.Day2Action{}
			for _, a := range out.Items {
				m[a.Key+"@"+deref(a.ResourceName)] = a
			}
			return m
		}
		resources := func() []string {
			t.Helper()
			var d struct {
				Resources []struct{ Type, Name string }
			}
			h.json("GET", "/api/v1/deployments/"+dep.Id, admin, nil, 200, &d)
			var out []string
			for _, r := range d.Resources {
				out = append(out, r.Type+" "+r.Name)
			}
			return out
		}
		run := func(user, action string, resource *string, want int) api.Request {
			t.Helper()
			var r api.Request
			var out any
			if want == 202 {
				out = &r
			}
			h.json("POST", "/api/v1/deployments/"+dep.Id+"/actions", user, api.Day2Draft{Action: action, ResourceId: resource,
				Inputs: map[string]any{}}, want, out, "Idempotency-Key", uuid.NewString())
			if want != 202 {
				return r
			}
			h.runWorkflow(r.Id, nil)
			return h.waitStatus(r.Id, user, api.RequestStatusSucceeded)
		}
		stepDetail := func(r api.Request) string {
			t.Helper()
			var steps struct{ Items []api.Step }
			h.json("GET", "/api/v1/requests/"+r.Id+"/steps", admin, nil, 200, &steps)
			i := slices.IndexFunc(steps.Items, func(s api.Step) bool { return s.Key == "rvav.unregister" })
			if i < 0 {
				t.Fatalf("steps = %+v", steps.Items)
			}
			return deref(steps.Items[i].Detail)
		}

		for _, user := range []string{"tnguyen", lead} {
			got := actions(user)
			if _, ok := got["vm_unregister@erp-app01"]; ok {
				t.Fatalf("vm_unregister offered to %s", user)
			}
			if _, ok := got["unregister@erp-dev"]; ok {
				t.Fatalf("unregister offered to %s", user)
			}
		}
		a := actions(admin)
		vm := a["vm_unregister@erp-app01"]
		if !vm.Enabled || vm.ResourceId == nil || vm.Confirm == nil || vm.Destructive {
			t.Fatalf("vm_unregister = %+v", vm)
		}
		run(lead, "vm_unregister", vm.ResourceId, 422)

		// One VM, with its address and records; they stay in EfficientIP.
		r := run(admin, "vm_unregister", vm.ResourceId, 202)
		if got := stepDetail(r); got != "Left in vCenter: erp-app01" {
			t.Fatalf("step detail = %q", got)
		}
		if got := resources(); !slices.Equal(got, []string{"vsphere.vm erp-app02"}) {
			t.Fatalf("resources after unregistering erp-app01 = %v", got)
		}
		if addresses, records, err := h.acts.IPAM.Find(ctx, []string{addr.IP}, []string{fqdn}); err != nil || len(addresses) != 1 ||
			len(records) != 2 {
			t.Fatalf("EfficientIP after unregistering = %v %v %v", addresses, records, err)
		}
		if _, err := h.vc.GetVM(ctx, app01); err != nil {
			t.Fatalf("erp-app01 in vCenter: %v", err)
		}
		if !offered("erp-app01") {
			t.Fatal("erp-app01 is not offered for onboarding again")
		}
		if again := onboard(nil, app01).Deployments[0]; again.Addresses != 1 || again.Records != 2 {
			t.Fatalf("adopted again = %+v", again)
		}

		// Its last VM goes with the deployment, unregistered whole and
		// deleted from rvav.
		a = actions(admin)
		if last, ok := a["vm_unregister@erp-app02"]; ok {
			t.Fatalf("vm_unregister of the last VM = %+v", last)
		}
		run(admin, "vm_unregister", a["power_off@erp-app02"].ResourceId, 422)
		whole := a["unregister@erp-dev"]
		if !whole.Enabled || whole.ResourceId != nil || whole.Confirm == nil {
			t.Fatalf("unregister = %+v", whole)
		}
		r = run(admin, "unregister", nil, 202)
		if got := stepDetail(r); got != "Left in vCenter: erp-app02" {
			t.Fatalf("step detail = %q", got)
		}
		var d api.DeploymentDetail
		h.json("GET", "/api/v1/deployments/"+dep.Id, admin, nil, 200, &d)
		if d.Status != api.DeploymentStatusDeleted || len(resources()) != 0 {
			t.Fatalf("after unregistering: %s %v", d.Status, resources())
		}
		if _, err := h.vc.GetVM(ctx, app02); err != nil || !offered("erp-app02") {
			t.Fatalf("erp-app02 after unregistering: %v, offered %v", err, offered("erp-app02"))
		}

		var audit struct{ Items []api.AuditEvent }
		h.json("GET", "/api/v1/audit?action=action.run", admin, nil, 200, &audit)
		var runs []string
		for _, e := range audit.Items {
			if e.TargetId == dep.Id {
				runs = append(runs, e.Detail["action"].(string))
			}
		}
		slices.Sort(runs)
		if !slices.Equal(runs, []string{"unregister", "vm_unregister"}) {
			t.Fatalf("audited = %v", runs)
		}
	}, model)
}
