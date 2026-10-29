package vsphere

import (
	"context"
	"errors"
	"testing"

	"github.com/vmware/govmomi/object"
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

// A VM from a content library's VM template, on a storage policy; a library,
// item or policy that isn't there is ErrNotFound (ADR-0024).
func TestDeployFromLibrary(t *testing.T) {
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		if err := Bootstrap(ctx, c, DefaultLayout()); err != nil {
			t.Fatal(err)
		}
		d := NewFromVim("vc01.corp.example", c)
		spec := VMSpec{Name: "web-dev-lib01", Library: "rvav-templates", Template: "rhel9-library", Cluster: "GVA-DEV-CL01",
			Datastore: "vsan-gva-dev-cl01", Network: "dvpg-db-dev-vl320", Folder: "/GVA/vm/rvav/web/dev", VCPU: 4, MemoryMB: 8192,
			DiskGB: 60, StoragePolicy: "vSAN Default Storage Policy", Disks: []DiskSpec{{SizeGB: 10}}}
		vm, err := d.CloneVM(ctx, spec)
		if err != nil {
			t.Fatal(err)
		}
		if vm.Template || vm.CPUCount != 4 || len(vm.Disks) != 2 || vm.Disks[0].CapacityGB != 60 || vm.NICs[0].Network != "dvpg-db-dev-vl320" ||
			vm.Folder != "/GVA/vm/rvav/web/dev" || vm.Cluster != "GVA-DEV-CL01" {
			t.Fatalf("deployed VM = %+v", vm)
		}
		for _, bad := range []VMSpec{{Library: "nope"}, {Template: "nope"}, {StoragePolicy: "Platinum"}} {
			s := spec
			s.Name = "web-dev-lib02"
			if bad.Library != "" {
				s.Library = bad.Library
			}
			if bad.Template != "" {
				s.Template = bad.Template
			}
			if bad.StoragePolicy != "" {
				s.StoragePolicy = bad.StoragePolicy
			}
			if _, err := d.CloneVM(ctx, s); !errors.Is(err, ErrNotFound) {
				t.Fatalf("%+v: %v", bad, err)
			}
		}
	})
}

// A VM an earlier attempt cloned but didn't finish is finished like a new
// one: its size, extra config and the disks it lacks; what its template
// gave (here two disks) is counted, not assumed (from S12's review).
func TestCloneFinishesAnEarlierAttempt(t *testing.T) {
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		if err := Bootstrap(ctx, c, DefaultLayout()); err != nil {
			t.Fatal(err)
		}
		d := NewFromVim("vc01.corp.example", c)
		spec := VMSpec{Name: "web-dev-half01", Template: "tpl-rhel9.4-2026.10", Cluster: "GVA-DEV-CL01", Datastore: "vsan-gva-dev-cl01",
			Network: "dvpg-app-dev-vl310", Folder: "/GVA/vm/rvav/web/dev", VCPU: 4, MemoryMB: 8192, DiskGB: 60,
			ExtraConfig: map[string]string{"guestinfo.rvav.deployment": "dep_half"}, Disks: []DiskSpec{{SizeGB: 10}}}
		// The earlier attempt: cloned only, from a template of two disks.
		w, err := d.where(ctx, c, spec)
		if err != nil {
			t.Fatal(err)
		}
		tpl, _ := findRef(ctx, c, "VirtualMachine", spec.Template)
		ref, err := d.cloneTemplate(ctx, c, object.NewVirtualMachine(c, tpl), spec, w.pool, w.host, w.folder, w.datastore, "")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := d.AddDisk(ctx, ref.Value, 5); err != nil {
			t.Fatal(err)
		}
		vm, err := d.CloneVM(ctx, spec)
		if err != nil {
			t.Fatal(err)
		}
		if vm.Moref != ref.Value || vm.CPUCount != 4 || vm.MemoryMB != 8192 || len(vm.Disks) != 3 || vm.Disks[0].CapacityGB != 60 ||
			vm.Disks[2].CapacityGB != 10 {
			t.Fatalf("finished VM = %+v / %+v", vm, vm.Disks)
		}
	})
}
