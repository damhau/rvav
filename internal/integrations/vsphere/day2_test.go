package vsphere

import (
	"context"
	"errors"
	"testing"

	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"
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
		if gone.File == "" || gone.File == kept.File {
			t.Fatalf("disks = %+v", vm.Disks)
		}
		ds := object.NewDatastore(c, mustRef(t, ctx, c, "Datastore", "vsan-gva-dev-cl01"))
		ds.InventoryPath = "vsan-gva-dev-cl01"
		var path object.DatastorePath
		path.FromString(gone.File)
		if _, err := ds.Stat(ctx, path.Path); err != nil {
			t.Fatalf("the disk's file before: %v", err)
		}

		if err := d.RemoveDisk(ctx, vm.Moref, gone.Label, gone.File); err != nil {
			t.Fatal(err)
		}
		if err := d.RemoveDisk(ctx, vm.Moref, gone.Label, gone.File); err != nil {
			t.Fatalf("again: %v", err)
		}
		if vm, err = d.GetVM(ctx, vm.Moref); err != nil {
			t.Fatal(err)
		}
		if len(vm.Disks) != 2 || vm.Disks[1].File != kept.File {
			t.Fatalf("disks after = %+v", vm.Disks)
		}
		if _, err := ds.Stat(ctx, path.Path); err == nil {
			t.Fatal("the disk's file is still on the datastore")
		}

		var refused *Refused
		if err := d.RemoveDisk(ctx, vm.Moref, boot.Label, boot.File); !errors.As(err, &refused) {
			t.Fatalf("the boot disk: %v", err)
		}
		if err := d.CreateSnapshot(ctx, vm.Moref, "before", "", false); err != nil {
			t.Fatal(err)
		}
		if err := d.RemoveDisk(ctx, vm.Moref, vm.Disks[1].Label, ""); !errors.As(err, &refused) {
			t.Fatalf("with a snapshot: %v", err)
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
