package e2e

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/types"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/integrations/vsphere"
	"github.com/damhau/rvav/internal/inventory"
)

// TestInventoryAndReportingAcceptance is the acceptance scenario of slice
// 13 (S13.6), on vcsim: an administrator adopts the 50 VMs vCenter has that
// rvav didn't build into Web Platform's production, in one operation, each
// a deployment of its own owned by Léa; the sync follows vCenter, so one of
// them powered off in vCenter is off in rvav within seconds, and another
// given more memory there is flagged as changed outside rvav; the reports
// count them. What vcsim can't show (a real vCenter's change notifications
// at scale, EfficientIP's records for real VMs) is Lab acceptance.
func TestInventoryAndReportingAcceptance(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	model := simulator.VPX()
	model.Machine = 25 // two hosts' worth: 50 VMs nobody built with rvav
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin = "mkeller"

		// The sync as deployed, following vCenter. After its first full read,
		// none: what changes comes only from VM change notifications.
		u := *c.URL()
		u.User = nil
		pass, _ := simulator.DefaultLogin.Password()
		vc, err := vsphere.New(ctx, "vc01.corp.example", u.String(), simulator.DefaultLogin.Username(), pass, true)
		if err != nil {
			t.Fatal(err)
		}
		sync := &inventory.Syncer{Store: h.store, VSphere: vc, Events: h.acts.Events, Log: h.acts.Log,
			Interval: time.Hour, Settle: time.Hour}
		runCtx, stop := context.WithCancel(ctx)
		stopped := make(chan struct{})
		go func() { _ = sync.Run(runCtx); close(stopped) }()
		// Stopped before vcsim is: its watchers release their collectors.
		defer func() { stop(); <-stopped }()
		within := func(what string, ok func() bool) time.Duration {
			t.Helper()
			start := time.Now()
			for !ok() {
				if time.Since(start) > 10*time.Second {
					t.Fatalf("%s: not seen within 10s", what)
				}
				time.Sleep(50 * time.Millisecond)
			}
			return time.Since(start)
		}

		// The administrator finds the 50 VMs rvav doesn't manage.
		var page api.OnboardingVmPage
		within("the sync's first read", func() bool {
			h.json("GET", "/api/v1/admin/onboarding/vms?limit=200", admin, nil, 200, &page)
			return page.Total == 50
		})
		var item api.CatalogItem
		h.json("GET", "/api/v1/catalog/items/rhel9-server?project_id=prj_web", "lmartin", nil, 200, &item)
		draft := api.OnboardingDraft{ProjectId: "prj_web", Environment: "prd", OwnerId: "usr_lmartin", CatalogItemId: item.Id}
		for _, v := range page.Items {
			draft.Vms = append(draft.Vms, struct {
				Moref   string `json:"moref"`
				Vcenter string `json:"vcenter"`
			}{v.Moref, v.Vcenter})
		}

		// One operation adopts them all, each into its own deployment.
		start := time.Now()
		var res api.OnboardingResult
		h.json("POST", "/api/v1/admin/onboarding", admin, draft, 200, &res)
		t.Logf("50 VMs adopted in %s", time.Since(start).Round(time.Millisecond))
		if len(res.Deployments) != 50 {
			t.Fatalf("%d deployments", len(res.Deployments))
		}
		h.json("GET", "/api/v1/admin/onboarding/vms", admin, nil, 200, &page)
		if page.Total != 0 {
			t.Fatalf("%d VMs still offered", page.Total)
		}
		var mine api.DeploymentPage
		h.json("GET", "/api/v1/deployments?project_id=prj_web&limit=200", "lmartin", nil, 200, &mine)
		adopted := 0
		for _, d := range mine.Items {
			if d.Environment == "prd" && d.Owner.Id == "usr_lmartin" && d.CatalogItem.Slug == "rhel9-server" && d.Lease.ExpiresAt == nil {
				adopted++
			}
		}
		if adopted != 50 {
			t.Fatalf("Léa sees %d adopted deployments", adopted)
		}

		// Powered off in vCenter: off in rvav within seconds.
		first := &vmDeployment{h: h, model: model, id: res.Deployments[0].Id, viewer: "lmartin"}
		ref := types.ManagedObjectReference{Type: "VirtualMachine", Value: first.vm().Moref}
		if first.vm().PowerState != api.On {
			t.Fatalf("%s is %s", res.Deployments[0].Name, first.vm().PowerState)
		}
		task, err := object.NewVirtualMachine(c, ref).PowerOff(ctx)
		if err == nil {
			err = task.Wait(ctx)
		}
		if err != nil {
			t.Fatal(err)
		}
		took := within("powered off", func() bool { return first.vm().PowerState == api.Off })
		t.Logf("powered off in vCenter, off in rvav %s later", took.Round(time.Millisecond))

		// More memory in vCenter: flagged on its deployment, with what differs.
		second := res.Deployments[1].Id
		var dep api.DeploymentDetail
		h.json("GET", "/api/v1/deployments/"+second, "lmartin", nil, 200, &dep)
		vm, _ := dep.Resources[0].AsVmResource()
		task, err = object.NewVirtualMachine(c, types.ManagedObjectReference{Type: "VirtualMachine", Value: vm.Vm.Moref}).
			Reconfigure(ctx, types.VirtualMachineConfigSpec{MemoryMB: int64(vm.Vm.MemoryMb * 2)})
		if err == nil {
			err = task.Wait(ctx)
		}
		if err != nil {
			t.Fatal(err)
		}
		within("changed outside rvav", func() bool {
			h.json("GET", "/api/v1/deployments/"+second, "lmartin", nil, 200, &dep)
			vm, _ = dep.Resources[0].AsVmResource()
			return dep.SyncState == api.Drifted && vm.Drift != nil && len(*vm.Drift) == 1 && (*vm.Drift)[0].Label == "Memory"
		})
		// The first is flagged too: it was powered off outside rvav.
		h.json("GET", "/api/v1/deployments/"+res.Deployments[0].Id, "lmartin", nil, 200, &dep)
		if dep.SyncState != api.Drifted {
			t.Fatalf("the VM powered off in vCenter is %s", dep.SyncState)
		}

		// The reports count them: 50 more VMs used in Web Platform, none of
		// them with a lease ending.
		var r api.Report
		h.json("GET", "/api/v1/reports?project_id=prj_web", "lmartin", nil, 200, &r)
		vms := slices.IndexFunc(r.Quota, func(l api.ReportQuotaLine) bool { return l.Environment == nil && l.Resource == api.VmCount })
		if vms < 0 || r.Quota[vms].Used != 50 || len(r.Leases) != 0 {
			t.Fatalf("report quota %+v, leases %+v", r.Quota, r.Leases)
		}
		var audit struct{ Items []api.AuditEvent }
		h.json("GET", "/api/v1/audit?action=deployment.adopted&limit=200", admin, nil, 200, &audit)
		if len(audit.Items) != 50 {
			t.Fatalf("%d adoptions audited", len(audit.Items))
		}
	}, model)
}
