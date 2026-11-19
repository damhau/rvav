package vsphere

import (
	"context"
	"errors"
	"testing"

	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/methods"
	"github.com/vmware/govmomi/vim25/soap"
	"github.com/vmware/govmomi/vim25/types"
)

// A removed disk goes with its file, named by it, so that a retry after
// vCenter numbered the labels again removes nothing more (X1.3).
func TestRemoveDisk(t *testing.T) {
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		if err := Bootstrap(ctx, c, DefaultLayout()); err != nil {
			t.Fatal(err)
		}
		d := NewFromVim("vc01.corp.example", c)
		spec := VMSpec{Name: "web-dev-rmdisk01", Template: "tpl-rhel9.4-2026.10", Cluster: "GVA-DEV-CL01", Datastore: "vsan-gva-dev-cl01",
			Network: "dvpg-app-dev-vl310", Folder: "/GVA/vm/rvav/web/dev", VCPU: 2, MemoryMB: 2048, DiskGB: 50,
			Disks: []DiskSpec{{SizeGB: 10}, {SizeGB: 5}}}
		vm, err := d.CloneVM(ctx, spec)
		if err != nil {
			t.Fatal(err)
		}
		boot, gone, kept := vm.Disks[0], vm.Disks[1], vm.Disks[2]
		if gone.File == "" || gone.File == kept.File || gone.Key == 0 || gone.Key == kept.Key || vm.BaseDisks != 1 {
			t.Fatalf("disks = %+v, %d from the template", vm.Disks, vm.BaseDisks)
		}
		ds := object.NewDatastore(c, mustRef(t, ctx, c, "Datastore", "vsan-gva-dev-cl01"))
		ds.InventoryPath = "vsan-gva-dev-cl01"
		var path object.DatastorePath
		path.FromString(gone.File)
		if _, err := ds.Stat(ctx, path.Path); err != nil {
			t.Fatalf("the disk's file before: %v", err)
		}

		// Another disk at its key or with its file is not it.
		if err := d.RemoveDisk(ctx, vm.Moref, DiskRef{Key: gone.Key, File: kept.File}); !errors.Is(err, ErrNoDisk) {
			t.Fatalf("its key with another's file: %v", err)
		}
		ref := DiskRef{Key: gone.Key, File: gone.File, Label: gone.Label}
		if err := d.RemoveDisk(ctx, vm.Moref, ref); err != nil {
			t.Fatal(err)
		}
		if err := d.RemoveDisk(ctx, vm.Moref, ref); !errors.Is(err, ErrNoDisk) {
			t.Fatalf("again: %v", err)
		}
		if err := d.ExtendDisk(ctx, vm.Moref, ref, 50); !errors.Is(err, ErrNoDisk) {
			t.Fatalf("extending it: %v", err)
		}
		if vm, err = d.GetVM(ctx, vm.Moref); err != nil {
			t.Fatal(err)
		}
		if len(vm.Disks) != 2 || vm.Disks[1].File != kept.File || vm.Disks[1].Key != kept.Key {
			t.Fatalf("disks after = %+v", vm.Disks)
		}
		if err := d.ExtendDisk(ctx, vm.Moref, DiskRef{Key: kept.Key}, 8); err != nil {
			t.Fatal(err)
		}
		if vm, err = d.GetVM(ctx, vm.Moref); err != nil || vm.Disks[1].CapacityGB != 8 {
			t.Fatalf("extended by its key: %+v %v", vm.Disks, err)
		}
		if _, err := ds.Stat(ctx, path.Path); err == nil {
			t.Fatal("the disk's file is still on the datastore")
		}

		var refused *Refused
		if err := d.RemoveDisk(ctx, vm.Moref, DiskRef{Key: boot.Key, File: boot.File}); !errors.As(err, &refused) {
			t.Fatalf("the boot disk: %v", err)
		}
		if err := d.CreateSnapshot(ctx, vm.Moref, "before", "", false); err != nil {
			t.Fatal(err)
		}
		if err := d.RemoveDisk(ctx, vm.Moref, DiskRef{Label: vm.Disks[1].Label}); !errors.As(err, &refused) {
			t.Fatalf("with a snapshot: %v", err)
		}
	})
}

// reconfigSpy keeps the reconfigure specs sent to vCenter: vcsim ignores a
// disk's storage policy.
type reconfigSpy struct {
	soap.RoundTripper
	specs []types.VirtualMachineConfigSpec
}

func (s *reconfigSpy) RoundTrip(ctx context.Context, req, res soap.HasFault) error {
	if b, ok := req.(*methods.ReconfigVM_TaskBody); ok {
		s.specs = append(s.specs, b.Req.Spec)
	}
	return s.RoundTripper.RoundTrip(ctx, req, res)
}

// A day-2 disk goes on the datastore and under the storage policy chosen;
// one vCenter lacks is refused before anything changes (X1.3).
func TestAddDiskOn(t *testing.T) {
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		if err := Bootstrap(ctx, c, DefaultLayout()); err != nil {
			t.Fatal(err)
		}
		d := NewFromVim("vc01.corp.example", c)
		spec := VMSpec{Name: "web-dev-adddisk01", Template: "tpl-rhel9.4-2026.10", Cluster: "GVA-DEV-CL01", Datastore: "vsan-gva-dev-cl01",
			Network: "dvpg-app-dev-vl310", Folder: "/GVA/vm/rvav/web/dev", VCPU: 2, MemoryMB: 2048, DiskGB: 50}
		vm, err := d.CloneVM(ctx, spec)
		if err != nil {
			t.Fatal(err)
		}
		spy := &reconfigSpy{RoundTripper: c.RoundTripper}
		c.RoundTripper = spy
		label, err := d.AddDisk(ctx, vm.Moref, DiskSpec{SizeGB: 20, Datastore: "nfs-gva-bronze-01", StoragePolicy: "vSAN Default Storage Policy"})
		if err != nil {
			t.Fatal(err)
		}
		if vm, err = d.GetVM(ctx, vm.Moref); err != nil {
			t.Fatal(err)
		}
		if len(vm.Disks) != 2 || vm.Disks[1].Label != label || vm.Disks[1].Datastore != "nfs-gva-bronze-01" || vm.Disks[1].CapacityGB != 20 {
			t.Fatalf("disks = %+v, added %q", vm.Disks, label)
		}
		if len(spy.specs) != 1 || len(spy.specs[0].DeviceChange) != 1 {
			t.Fatalf("reconfigure specs = %+v", spy.specs)
		}
		if p := spy.specs[0].DeviceChange[0].GetVirtualDeviceConfigSpec().Profile; len(p) != 1 {
			t.Fatalf("the disk's profile = %+v", p)
		}

		var refused *Refused
		for _, bad := range []DiskSpec{{SizeGB: 5, Datastore: "nope"}, {SizeGB: 5, StoragePolicy: "Platinum"}} {
			if _, err := d.AddDisk(ctx, vm.Moref, bad); !errors.As(err, &refused) {
				t.Fatalf("%+v: %v", bad, err)
			}
		}
		if len(spy.specs) != 1 {
			t.Fatalf("reconfigured anyway: %+v", spy.specs[1:])
		}
	})
}

func mustRef(t *testing.T, ctx context.Context, c *vim25.Client, kind, name string) types.ManagedObjectReference {
	t.Helper()
	ref, err := findRef(ctx, c, kind, name)
	if err != nil {
		t.Fatal(err)
	}
	return ref
}
