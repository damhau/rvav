package vsphere

import (
	"context"
	"testing"

	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/types"
)

// Disks added at clone and by day 2 are provisioned as they say, else as the
// VM's first disk (ADR-0036). vcsim keeps an added disk's backing.
func TestDiskProvisioning(t *testing.T) {
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		if err := Bootstrap(ctx, c, DefaultLayout()); err != nil {
			t.Fatal(err)
		}
		d := NewFromVim("vc01.corp.example", c)
		spec := VMSpec{Name: "web-dev-disks01", Template: "tpl-rhel9.4-2026.10", Cluster: "GVA-DEV-CL01", Datastore: "vsan-gva-dev-cl01",
			Network: "dvpg-app-dev-vl310", Folder: "/GVA/vm/rvav/web/dev", VCPU: 2, MemoryMB: 2048, DiskGB: 50, Provisioning: ProvisionThin,
			Disks: []DiskSpec{{SizeGB: 10, Provisioning: ProvisionThin}, {SizeGB: 5, Provisioning: ProvisionThickEager}, {SizeGB: 3}}}
		vm, err := d.CloneVM(ctx, spec)
		if err != nil {
			t.Fatal(err)
		}
		first := vm.Disks[0].Provisioning
		if len(vm.Disks) != 4 || first == "" || vm.Disks[1].Provisioning != ProvisionThin ||
			vm.Disks[2].Provisioning != ProvisionThickEager || vm.Disks[3].Provisioning != first {
			t.Fatalf("disks = %+v", vm.Disks)
		}
		if _, err := d.AddDisk(ctx, vm.Moref, DiskSpec{SizeGB: 4, Provisioning: ProvisionThickLazy}); err != nil {
			t.Fatal(err)
		}
		if _, err := d.AddDisk(ctx, vm.Moref, DiskSpec{SizeGB: 2}); err != nil {
			t.Fatal(err)
		}
		if vm, err = d.GetVM(ctx, vm.Moref); err != nil {
			t.Fatal(err)
		}
		if len(vm.Disks) != 6 || vm.Disks[4].Provisioning != ProvisionThickLazy || vm.Disks[5].Provisioning != first {
			t.Fatalf("disks after day 2 = %+v", vm.Disks)
		}
	})
}

// A disk whose backing has a parent is a linked clone's template disk;
// vcsim makes none, so the backing is built here (T1.7).
func TestLinkedDisk(t *testing.T) {
	flat := &types.VirtualDiskFlatVer2BackingInfo{}
	child := &types.VirtualDiskFlatVer2BackingInfo{Parent: &types.VirtualDiskFlatVer2BackingInfo{}}
	for _, c := range []struct {
		disk *types.VirtualDisk
		want bool
	}{
		{&types.VirtualDisk{VirtualDevice: types.VirtualDevice{Backing: flat}}, false},
		{&types.VirtualDisk{VirtualDevice: types.VirtualDevice{Backing: child}}, true},
		{&types.VirtualDisk{VirtualDevice: types.VirtualDevice{Backing: &types.VirtualDiskRawDiskMappingVer1BackingInfo{}}}, false},
		{nil, false},
	} {
		if got := linkedDisk(c.disk); got != c.want {
			t.Errorf("linkedDisk(%+v) = %v", c.disk, got)
		}
	}
}

// A custom attribute is set by the VMs' definition of its name, or the
// global one, never a host's of the same name (T1.7).
func TestVMFieldKey(t *testing.T) {
	fields := []types.CustomFieldDef{{Key: 1, Name: "owner", ManagedObjectType: "HostSystem"}, {Key: 2, Name: "owner", ManagedObjectType: "VirtualMachine"},
		{Key: 3, Name: "site"}, {Key: 4, Name: "rack", ManagedObjectType: "HostSystem"}}
	for name, want := range map[string]int32{"owner": 2, "site": 3, "rack": 0, "app": 0} {
		if got, ok := vmFieldKey(fields, name); got != want || ok != (want != 0) {
			t.Errorf("vmFieldKey(%q) = %d, %v", name, got, ok)
		}
	}
}

// A clone converts the template's flat disks with disk locators, which vcsim
// ignores: the clone spec is checked here.
func TestDiskLocators(t *testing.T) {
	unit, next := int32(0), int32(1)
	flat := &types.VirtualDisk{VirtualDevice: types.VirtualDevice{Key: 2000, ControllerKey: 1000, UnitNumber: &unit,
		Backing: &types.VirtualDiskFlatVer2BackingInfo{DiskMode: "independent_persistent", ThinProvisioned: types.NewBool(true)}}}
	rdm := &types.VirtualDisk{VirtualDevice: types.VirtualDevice{Key: 2001, ControllerKey: 1000, UnitNumber: &next,
		Backing: &types.VirtualDiskRawDiskMappingVer1BackingInfo{}}}
	ds := types.ManagedObjectReference{Type: "Datastore", Value: "datastore-1"}
	got := diskLocators(object.VirtualDeviceList{rdm, flat}, ds, ProvisionThickEager, "policy-1")
	if len(got) != 1 || got[0].DiskId != 2000 || got[0].Datastore != ds || len(got[0].Profile) != 1 {
		t.Fatalf("locators = %+v", got)
	}
	b := got[0].DiskBackingInfo.(*types.VirtualDiskFlatVer2BackingInfo)
	if b.DiskMode != "independent_persistent" || *b.ThinProvisioned || !*b.EagerlyScrub {
		t.Fatalf("backing = %+v", b)
	}
	if p := provisioningOf(flat); p != ProvisionThin {
		t.Fatalf("flat disk is %q", p)
	}
	if p := provisioningOf(rdm); p != "" {
		t.Fatalf("RDM is %q", p)
	}
	if firstDisk(object.VirtualDeviceList{rdm, flat}) != flat {
		t.Fatal("the first disk is the one of the lowest unit")
	}
	for p, want := range map[string]string{ProvisionThin: "thin", ProvisionThickLazy: "thick", ProvisionThickEager: "eagerZeroedThick", "": ""} {
		if got := ovfProvisioning(p); got != want {
			t.Fatalf("ovfProvisioning(%q) = %q", p, got)
		}
	}
}
