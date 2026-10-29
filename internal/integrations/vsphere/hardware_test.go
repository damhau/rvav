package vsphere

import (
	"context"
	"testing"

	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"
)

// Disks and adapters added at provisioning (ADR-0024): two VMs, each with a
// disk on another datastore, once however often the clone is retried.
func TestCloneAddsDisksAndAdapters(t *testing.T) {
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		if err := Bootstrap(ctx, c, DefaultLayout()); err != nil {
			t.Fatal(err)
		}
		d := NewFromVim("vc01.corp.example", c)
		for _, name := range []string{"web-dev-app01", "web-dev-app02"} {
			spec := VMSpec{Name: name, Template: "tpl-rhel9.4-2026.10", Cluster: "GVA-DEV-CL01", Datastore: "nfs-gva-bronze-01",
				Network: "dvpg-app-dev-vl310", Folder: "/GVA/vm/rvav/web/dev", VCPU: 2, MemoryMB: 2048, DiskGB: 50,
				Disks: []DiskSpec{{SizeGB: 20, Datastore: "vsan-gva-dev-cl01"}, {SizeGB: 5}}, NICs: []string{"dvpg-db-dev-vl320"}}
			vm, err := d.CloneVM(ctx, spec)
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			// A retry finds the VM and adds nothing more.
			if vm, err = d.CloneVM(ctx, spec); err != nil {
				t.Fatalf("%s again: %v", name, err)
			}
			if len(vm.Disks) != 3 || vm.Disks[1].CapacityGB != 20 || vm.Disks[2].CapacityGB != 5 ||
				len(vm.NICs) != 2 || vm.NICs[1].Network != "dvpg-db-dev-vl320" {
				t.Fatalf("%s: disks %+v, adapters %+v", name, vm.Disks, vm.NICs)
			}
		}
	})
}
