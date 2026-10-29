package e2e

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/vmware/govmomi/find"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/property"
	"github.com/vmware/govmomi/session"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/integrations/vsphere"
	"github.com/damhau/rvav/internal/inventory"
	"github.com/damhau/rvav/internal/store"
)

// TestInventoryFollowsVCenter: the sync follows vCenter's change
// notifications (S13.1). A VM rvav built, powered off in vCenter, is off in
// rvav within seconds, with no full read to wait for; a port group made in
// vCenter is in the inventory; after its session is cut, the sync signs in
// and follows again; a VM deleted in vCenter is missing on its deployment.
func TestInventoryFollowsVCenter(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	model := simulator.VPX()
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		d := buildVM(h, model)

		// The sync as deployed: signed in with an address and an account, so
		// that it can sign in again. No full read during the test.
		u := *c.URL()
		u.User = nil
		user := simulator.DefaultLogin.Username()
		pass, _ := simulator.DefaultLogin.Password()
		vc, err := vsphere.New(ctx, "vc01.corp.example", u.String(), user, pass, true)
		if err != nil {
			t.Fatal(err)
		}
		sync := &inventory.Syncer{Store: h.store, VSphere: vc, Events: h.acts.Events, Log: h.acts.Log,
			Interval: time.Hour, Settle: 200 * time.Millisecond}
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
		power := func(on bool) {
			t.Helper()
			vm := object.NewVirtualMachine(c, types.ManagedObjectReference{Type: "VirtualMachine", Value: d.vm().Moref})
			task, err := vm.PowerOff(ctx)
			if on {
				task, err = vm.PowerOn(ctx)
			}
			if err == nil {
				err = task.Wait(ctx)
			}
			if err != nil {
				t.Fatal(err)
			}
		}
		inventoryPower := func() string {
			var s string
			_ = h.store.Pool.QueryRow(ctx, `select power_state from inv_vms where moref = $1`, d.vm().Moref).Scan(&s)
			return s
		}
		// Following: what the sync read when it started is in.
		within("the sync started", func() bool { return inventoryPower() == "on" })

		// Powered off in vCenter: off in rvav, the deployment and the cache.
		power(false)
		took := within("powered off", func() bool { return d.vm().PowerState == api.Off && inventoryPower() == "off" })
		t.Logf("powered off in vCenter, off in rvav %s later", took)

		// A port group made in vCenter: in the inventory, for forms and placement.
		dvs, err := find.NewFinder(c).Network(ctx, "/GVA/network/GVA-DVS01")
		if err != nil {
			t.Fatal(err)
		}
		task, err := dvs.(*object.DistributedVirtualSwitch).AddPortgroup(ctx,
			[]types.DVPortgroupConfigSpec{{Name: "dvpg-new-dev-vl390", Type: "earlyBinding", NumPorts: 8}})
		if err == nil {
			err = task.Wait(ctx)
		}
		if err != nil {
			t.Fatal(err)
		}
		within("a new port group", func() bool {
			nets, _ := h.store.InventoryNetworks(ctx)
			return slices.ContainsFunc(nets, func(n store.InvNetwork) bool { return n.Name == "dvpg-new-dev-vl390" })
		})

		// The sync's session cut (vCenter restarted, or its session timed
		// out): it signs in and follows again.
		var sm mo.SessionManager
		if err := property.DefaultCollector(c).RetrieveOne(ctx, *c.ServiceContent.SessionManager, []string{"sessionList", "currentSession"}, &sm); err != nil {
			t.Fatal(err)
		}
		var others []string
		for _, s := range sm.SessionList {
			if s.Key != sm.CurrentSession.Key {
				others = append(others, s.Key)
			}
		}
		if len(others) == 0 {
			t.Fatal("no session of the sync's")
		}
		if err := session.NewManager(c).TerminateSession(ctx, others); err != nil {
			t.Fatal(err)
		}
		power(true) // still told through the old wait
		within("powered on", func() bool { return d.vm().PowerState == api.On })
		power(false) // only through a new session
		within("powered off after signing in again", func() bool { return d.vm().PowerState == api.Off })

		// Deleted in vCenter: missing on its deployment.
		vm := object.NewVirtualMachine(c, types.ManagedObjectReference{Type: "VirtualMachine", Value: d.vm().Moref})
		if task, err := vm.Destroy(ctx); err != nil || task.Wait(ctx) != nil {
			t.Fatalf("destroy: %v", err)
		}
		within("deleted", func() bool {
			var dep api.Deployment
			h.json("GET", "/api/v1/deployments/"+d.id, "tnguyen", nil, 200, &dep)
			return dep.SyncState == api.Missing && inventoryPower() == ""
		})
	})
}
