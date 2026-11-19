package e2e

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/integrations/efficientip"
	"github.com/damhau/rvav/internal/integrations/vsphere"
	"github.com/damhau/rvav/internal/inventory"
	"github.com/damhau/rvav/internal/present"
)

type refuseDestroy struct {
	vsphere.Driver
	moref string
}

func (r refuseDestroy) Destroy(ctx context.Context, moref string) error {
	if moref == r.moref {
		return errors.New("the VM is locked by a backup")
	}
	return r.Driver.Destroy(ctx, moref)
}

type refuseRelease struct {
	efficientip.Client
	ip string
}

func (r refuseRelease) Release(ctx context.Context, a efficientip.Address) error {
	if a.IP == r.ip {
		return errors.New("the address is locked")
	}
	return r.Client.Release(ctx, a)
}

// TestDeleteGoesOnPastFailures: a deletion stops at its first failed step,
// and the deployment is failed; deleting it again with the option to go on
// past failures deletes what it can, lists what was left and forgets it,
// and the deployment is deleted (B18). What was left stays where it is: the
// VM can be onboarded, the address shows in the EfficientIP reconciliation.
// The VM left keeps its address, which isn't released under it (X1.6).
func TestDeleteGoesOnPastFailures(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	model := simulator.VPX()
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin, owner = "mkeller", "tnguyen"
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
		addr2, err := h.acts.IPAM.Reserve(ctx, efficientip.Subnet{CIDR: "10.31.10.0/24"}, "erp-app02.corp.example", "legacy")
		if err != nil {
			t.Fatal(err)
		}
		app01 := existingVM(t, ctx, h, c, model, "erp-app01", addr.IP)
		app02 := existingVM(t, ctx, h, c, model, "erp-app02", addr2.IP)
		sync := &inventory.Syncer{Store: h.store, VSphere: h.vc, Events: h.acts.Events, Log: h.acts.Log}
		if err := sync.SyncOnce(ctx); err != nil {
			t.Fatal(err)
		}
		var item api.CatalogItem
		h.json("GET", "/api/v1/catalog/items/rhel9-server?project_id=prj_web", admin, nil, 200, &item)
		draft := api.OnboardingDraft{ProjectId: "prj_web", Environment: "dev", OwnerId: "usr_tnguyen", CatalogItemId: item.Id,
			DeploymentName: present.Ptr("erp-dev")}
		for _, m := range []string{app01, app02} {
			draft.Vms = append(draft.Vms, struct {
				Moref   string `json:"moref"`
				Vcenter string `json:"vcenter"`
			}{m, "vc01.corp.example"})
		}
		var adopted api.OnboardingResult
		h.json("POST", "/api/v1/admin/onboarding", admin, draft, 200, &adopted)
		dep := adopted.Deployments[0]
		if dep.Addresses != 2 || dep.Records != 2 {
			t.Fatalf("adopted = %+v", dep)
		}

		vc, ipam := h.acts.VSphere, h.acts.IPAM
		h.acts.VSphere, h.acts.IPAM = refuseDestroy{vc, app02}, refuseRelease{ipam, addr.IP}
		t.Cleanup(func() { h.acts.VSphere, h.acts.IPAM = vc, ipam })

		var listed struct{ Items []api.Day2Action }
		h.json("GET", "/api/v1/deployments/"+dep.Id+"/actions", owner, nil, 200, &listed)
		i := slices.IndexFunc(listed.Items, func(a api.Day2Action) bool { return a.Key == "delete" })
		if i < 0 || listed.Items[i].Form == nil {
			t.Fatalf("delete = %+v", listed.Items)
		}
		del := func(inputs map[string]any, want api.RequestStatus) map[string]api.Step {
			t.Helper()
			var r api.Request
			h.json("POST", "/api/v1/deployments/"+dep.Id+"/actions", owner, api.Day2Draft{Action: "delete", Inputs: inputs}, 202, &r,
				"Idempotency-Key", uuid.NewString())
			h.runWorkflow(r.Id, nil)
			h.waitStatus(r.Id, owner, want)
			var steps struct{ Items []api.Step }
			h.json("GET", "/api/v1/requests/"+r.Id+"/steps", owner, nil, 200, &steps)
			out := map[string]api.Step{}
			for _, s := range steps.Items {
				out[s.Key] = s
			}
			return out
		}
		status := func() api.DeploymentStatus {
			t.Helper()
			var d api.DeploymentDetail
			h.json("GET", "/api/v1/deployments/"+dep.Id, owner, nil, 200, &d)
			return d.Status
		}
		orphan := func() bool {
			t.Helper()
			var report api.IpamReconcile
			h.json("GET", "/api/v1/admin/ipam/reconcile", admin, nil, 200, &report)
			return slices.ContainsFunc(report.OrphanAddresses, func(o api.IpamOrphanAddress) bool { return o.Ip == addr.IP })
		}

		// Without the option, the deletion stops at the VM it can't delete.
		steps := del(map[string]any{}, api.RequestStatusFailed)
		if s := steps["vsphere.destroy@erp-app02"]; s.Status != api.StepStatusFailed || steps["dns.delete"].Status != api.StepStatusPending {
			t.Fatalf("steps = %+v", steps)
		}
		if _, ok := steps["rvav.left"]; ok || status() != api.DeploymentStatusFailed {
			t.Fatalf("after a failed deletion: %s %+v", status(), steps["rvav.left"])
		}
		if _, err := vc.GetVM(ctx, app01); !errors.Is(err, vsphere.ErrNotFound) {
			t.Fatalf("erp-app01 after the failed deletion: %v", err)
		}

		// With it, the deletion goes on, within a step too, and says what
		// was left.
		steps = del(map[string]any{"ignore_failures": true}, api.RequestStatusSucceeded)
		for key, want := range map[string]api.StepStatus{"vsphere.destroy@erp-app02": api.StepStatusFailed, "dns.delete": api.StepStatusDone,
			"ipam.release": api.StepStatusFailed, "rvav.left": api.StepStatusDone} {
			if steps[key].Status != want {
				t.Fatalf("step %s = %+v, want %s", key, steps[key], want)
			}
		}
		if got, want := deref(steps["rvav.left"].Detail), "Left: VM erp-app02, IP address "+addr.IP+", IP address "+addr2.IP; got != want {
			t.Fatalf("left = %q, want %q", got, want)
		}
		var d api.DeploymentDetail
		h.json("GET", "/api/v1/deployments/"+dep.Id, owner, nil, 200, &d)
		if d.Status != api.DeploymentStatusDeleted || len(d.Resources) != 0 {
			t.Fatalf("deployment = %s with %d resources", d.Status, len(d.Resources))
		}
		if _, err := vc.GetVM(ctx, app02); err != nil {
			t.Fatalf("erp-app02 after the deletion: %v", err)
		}
		var page api.OnboardingVmPage
		h.json("GET", "/api/v1/admin/onboarding/vms?q=erp-app02", admin, nil, 200, &page)
		if page.Total != 1 {
			t.Fatal("erp-app02 is not offered for onboarding")
		}
		if addresses, records, err := ipam.Find(ctx, []string{addr.IP}, []string{fqdn}); err != nil || len(addresses) != 1 || len(records) != 0 {
			t.Fatalf("EfficientIP after the deletion = %v %v %v", addresses, records, err)
		}
		if !orphan() {
			t.Fatal("the address left is not in the reconciliation")
		}
		if addresses, _, err := ipam.Find(ctx, []string{addr2.IP}, nil); err != nil || len(addresses) != 1 {
			t.Fatalf("erp-app02's address after the deletion = %v %v", addresses, err)
		}
	}, model)
}
