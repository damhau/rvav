package vsphere

import (
	"cmp"
	"slices"

	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/vim25/types"
)

// How a disk is provisioned (ADR-0036), as blueprints say it.
const (
	ProvisionThin       = "thin"
	ProvisionThickLazy  = "thick_lazy_zeroed"
	ProvisionThickEager = "thick_eager_zeroed"
)

func flatBacking(d *types.VirtualDisk) *types.VirtualDiskFlatVer2BackingInfo {
	if d == nil {
		return nil
	}
	b, _ := d.Backing.(*types.VirtualDiskFlatVer2BackingInfo)
	return b
}

// provisioningOf is how a disk is provisioned; "" when it isn't a flat
// file, an RDM say.
func provisioningOf(d *types.VirtualDisk) string {
	b := flatBacking(d)
	switch {
	case b == nil:
		return ""
	case b.ThinProvisioned != nil && *b.ThinProvisioned:
		return ProvisionThin
	case b.EagerlyScrub != nil && *b.EagerlyScrub:
		return ProvisionThickEager
	}
	return ProvisionThickLazy
}

// provision sets a new disk's backing to p; "" leaves it as it is.
func provision(d *types.VirtualDisk, p string) {
	if b := flatBacking(d); b != nil && p != "" {
		b.ThinProvisioned = types.NewBool(p == ProvisionThin)
		b.EagerlyScrub = types.NewBool(p == ProvisionThickEager)
	}
}

// firstDisk is the VM's first disk, by controller and unit as vCenter
// numbers them; nil when it has none.
func firstDisk(devices object.VirtualDeviceList) *types.VirtualDisk {
	disks := devices.SelectByType((*types.VirtualDisk)(nil))
	if len(disks) == 0 {
		return nil
	}
	first := slices.MinFunc(disks, func(a, b types.BaseVirtualDevice) int {
		x, y := a.GetVirtualDevice(), b.GetVirtualDevice()
		return cmp.Or(cmp.Compare(x.ControllerKey, y.ControllerKey), cmp.Compare(deref32(x.UnitNumber), deref32(y.UnitNumber)))
	})
	return first.(*types.VirtualDisk)
}

// diskLocators convert the template's flat disks at clone, on the VM's
// datastore: a clone spec's way to change their format.
func diskLocators(devices object.VirtualDeviceList, ds types.ManagedObjectReference, p, policy string) []types.VirtualMachineRelocateSpecDiskLocator {
	var out []types.VirtualMachineRelocateSpecDiskLocator
	for _, d := range devices.SelectByType((*types.VirtualDisk)(nil)) {
		disk := d.(*types.VirtualDisk)
		b := flatBacking(disk)
		if b == nil {
			continue
		}
		out = append(out, types.VirtualMachineRelocateSpecDiskLocator{
			DiskId:    disk.Key,
			Datastore: ds,
			DiskBackingInfo: &types.VirtualDiskFlatVer2BackingInfo{
				DiskMode:        cmp.Or(b.DiskMode, string(types.VirtualDiskModePersistent)),
				ThinProvisioned: types.NewBool(p == ProvisionThin),
				EagerlyScrub:    types.NewBool(p == ProvisionThickEager),
			},
			Profile: profile(policy),
		})
	}
	return out
}

// ovfProvisioning is a content library OVF deploy's storage provisioning.
func ovfProvisioning(p string) string {
	switch p {
	case ProvisionThin:
		return "thin"
	case ProvisionThickLazy:
		return "thick"
	case ProvisionThickEager:
		return "eagerZeroedThick"
	}
	return ""
}
