package e2e

import (
	"context"
	"slices"
	"strconv"
	"testing"

	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/types"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/inventory"
)

// TestDrift: a VM changed in vCenter outside rvav is flagged on its
// deployment with what differs (S13.2): its power, its memory, a disk
// added, an adapter moved. An action of rvav's settles only what it
// changed; a change undone in vCenter is no longer flagged; accepting the
// changes takes the VM as it is. A VM rvav built before drift was measured
// is taken as it is.
func TestDrift(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	model := simulator.VPX()
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		d := buildVM(h, model)
		sync := &inventory.Syncer{Store: h.store, VSphere: h.vc, Events: h.acts.Events, Log: h.acts.Log}
		resync := func() {
			t.Helper()
			if err := sync.SyncOnce(ctx); err != nil {
				t.Fatal(err)
			}
		}
		type vmDrift struct {
			state api.SyncState
			drift []api.VmDrift
		}
		read := func() vmDrift {
			t.Helper()
			var dep struct {
				SyncState api.SyncState `json:"sync_state"`
				Resources []struct {
					Type      string
					SyncState api.SyncState `json:"sync_state"`
					Drift     []api.VmDrift `json:"drift"`
				}
			}
			h.json("GET", "/api/v1/deployments/"+d.id, "tnguyen", nil, 200, &dep)
			for _, r := range dep.Resources {
				if r.Type == "vsphere.vm" {
					if r.SyncState != dep.SyncState {
						t.Fatalf("VM %s, deployment %s", r.SyncState, dep.SyncState)
					}
					return vmDrift{r.SyncState, r.Drift}
				}
			}
			t.Fatal("no VM")
			return vmDrift{}
		}
		describe := func(ds []api.VmDrift) []string {
			var out []string
			for _, x := range ds {
				out = append(out, x.Label+": "+deref(x.Rvav)+" → "+deref(x.Vcenter))
			}
			return out
		}
		expect := func(want ...string) {
			t.Helper()
			got := read()
			if len(want) == 0 {
				if got.state != api.InSync || len(got.drift) != 0 {
					t.Fatalf("drift = %s %v, want in sync", got.state, describe(got.drift))
				}
				return
			}
			if got.state != api.Drifted || !slices.Equal(describe(got.drift), want) {
				t.Fatalf("drift = %s %v, want %v", got.state, describe(got.drift), want)
			}
		}
		accept := func() api.Day2Action { return d.actions("tnguyen")["accept_changes"] }

		resync()
		expect()
		if a := accept(); a.Enabled || deref(a.DisabledReason) != "The VM is as rvav left it." {
			t.Fatalf("accept_changes on a VM in sync = %+v", a)
		}
		memory := d.vm().MemoryMb
		vm := object.NewVirtualMachine(c, types.ManagedObjectReference{Type: "VirtualMachine", Value: d.vm().Moref})
		wait := func(task *object.Task, err error) {
			t.Helper()
			if err == nil {
				err = task.Wait(ctx)
			}
			if err != nil {
				t.Fatal(err)
			}
		}

		// Powered off and given more memory in vCenter: flagged, with what differs.
		wait(vm.PowerOff(ctx))
		wait(vm.Reconfigure(ctx, types.VirtualMachineConfigSpec{MemoryMB: int64(memory * 2)}))
		resync()
		gb := func(mb int) string { return strconv.Itoa(mb/1024) + " GB" }
		expect("Power: on → off", "Memory: "+gb(memory)+" → "+gb(memory*2))
		if a := accept(); !a.Enabled || a.Group != api.Configuration {
			t.Fatalf("accept_changes on a drifted VM = %+v", a)
		}

		// Powered on through rvav: the power is settled, the memory isn't.
		d.succeed("tnguyen", "power_on", nil)
		expect("Memory: " + gb(memory) + " → " + gb(memory*2))
		resync()
		expect("Memory: " + gb(memory) + " → " + gb(memory*2))

		// Changed back in vCenter: no longer flagged.
		wait(vm.Reconfigure(ctx, types.VirtualMachineConfigSpec{MemoryMB: int64(memory)}))
		resync()
		expect()

		// A disk added and the adapter moved in vCenter, then accepted.
		label, err := h.vc.AddDisk(ctx, d.vm().Moref, 20)
		if err != nil {
			t.Fatal(err)
		}
		nic := deref(d.vm().Nics[0].Label)
		if err := h.vc.SetNICNetwork(ctx, d.vm().Moref, nic, "dvpg-app-tst-vl410"); err != nil {
			t.Fatal(err)
		}
		resync()
		expect(label+":  → 20 GB", nic+": dvpg-app-dev-vl310 → dvpg-app-tst-vl410")
		r := d.succeed("tnguyen", "accept_changes", nil)
		var steps struct{ Items []api.Step }
		h.json("GET", "/api/v1/requests/"+r.Id+"/steps", "tnguyen", nil, 200, &steps)
		if !hasStep(steps.Items, "Take "+r.Resource.Name+" as it is in vCenter", label+", "+nic) {
			t.Fatalf("accept steps = %+v", steps.Items)
		}
		expect()
		resync()
		expect()

		// A VM rvav built before drift was measured is taken as it is.
		if _, err := h.store.Pool.Exec(ctx, `update resources set properties = properties - 'expected' - 'drift'
			where type = 'vsphere.vm'`); err != nil {
			t.Fatal(err)
		}
		wait(vm.PowerOff(ctx))
		resync()
		expect()
	})
}

func hasStep(steps []api.Step, title, detail string) bool {
	for _, s := range steps {
		if s.Title == title && deref(s.Detail) == detail {
			return true
		}
	}
	return false
}
