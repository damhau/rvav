package vsphere

import (
	"context"
	"fmt"

	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"
)

// linkedCloneBase is the template's current snapshot, which a linked
// clone's disks are children of (ADR-0037). A child disk can't be grown, so
// the VM's first disk must fit the template's.
func linkedCloneBase(ctx context.Context, tpl *object.VirtualMachine, spec VMSpec) (*types.ManagedObjectReference, error) {
	var m mo.VirtualMachine
	if err := tpl.Properties(ctx, tpl.Reference(), []string{"snapshot", "config.hardware.device"}, &m); err != nil {
		return nil, err
	}
	if m.Snapshot == nil || m.Snapshot.CurrentSnapshot == nil {
		return nil, fmt.Errorf("%w: template %s has no snapshot to clone from. Take one in vCenter, or turn linked clones off",
			ErrLinkedClone, spec.Template)
	}
	if m.Config != nil && spec.DiskGB > 0 {
		if disk := firstDisk(object.VirtualDeviceList(m.Config.Hardware.Device)); disk != nil && disk.CapacityInKB < int64(spec.DiskGB)*1024*1024 {
			return nil, fmt.Errorf("%w: a linked clone keeps the %d GB disk of template %s, %d GB were asked for. Ask for %d GB at most, or turn linked clones off",
				ErrLinkedClone, disk.CapacityInKB/(1024*1024), spec.Template, spec.DiskGB, disk.CapacityInKB/(1024*1024))
		}
	}
	return m.Snapshot.CurrentSnapshot, nil
}
