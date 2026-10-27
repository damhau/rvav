package vsphere

import (
	"context"
	"errors"
	"fmt"

	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/session"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"
)

// Day-2 operations (ADR-0013). Most are safe to retry: they check the
// current state first where vCenter would otherwise fail or duplicate.
// AddDisk and AddNIC are not: a retry after a lost response adds another.

// ErrToolsNotRunning means a guest operation needs VMware Tools.
var ErrToolsNotRunning = errors.New("VMware Tools is not running in the guest")

// errVMGone is a guest operation on a VM vCenter no longer has.
var errVMGone = errors.New("the VM is no longer in vCenter")

// IsNotFound reports whether err means the VM is no longer in vCenter.
func IsNotFound(err error) bool { return errors.Is(err, errVMGone) || isNotFoundFault(err) }

// Refused is an operation that can't be done as asked; retrying won't help.
type Refused struct{ Msg string }

func (e *Refused) Error() string { return e.Msg }

// findDevice finds a device by the label vCenter shows ("Hard disk 1").
func findDevice(devices object.VirtualDeviceList, label string) types.BaseVirtualDevice {
	for _, d := range devices {
		if info := d.GetVirtualDevice().DeviceInfo; info != nil && info.GetDescription().Label == label {
			return d
		}
	}
	return devices.Find(label)
}

func deviceLabel(d types.BaseVirtualDevice) string {
	if info := d.GetVirtualDevice().DeviceInfo; info != nil {
		return info.GetDescription().Label
	}
	return ""
}

func (cl *Client) vmObject(ctx context.Context, moref string) (*object.VirtualMachine, error) {
	c, err := cl.client(ctx)
	if err != nil {
		return nil, err
	}
	return object.NewVirtualMachine(c, vmRef(moref)), nil
}

func (cl *Client) guestOp(ctx context.Context, moref string, op func(*object.VirtualMachine) error) error {
	vm, err := cl.GetVM(ctx, moref)
	if errors.Is(err, ErrNotFound) {
		return errVMGone
	}
	if err != nil {
		return err
	}
	if vm.PowerState != "on" {
		return nil
	}
	if vm.GuestState != "running" {
		return ErrToolsNotRunning
	}
	obj, err := cl.vmObject(ctx, moref)
	if err != nil {
		return err
	}
	return op(obj)
}

// ShutdownGuest asks the guest OS to shut down. It returns without waiting.
func (cl *Client) ShutdownGuest(ctx context.Context, moref string) error {
	return cl.guestOp(ctx, moref, func(vm *object.VirtualMachine) error { return vm.ShutdownGuest(ctx) })
}

// WaitPoweredOff waits until the VM is powered off.
func (cl *Client) WaitPoweredOff(ctx context.Context, moref string) error {
	vm, err := cl.vmObject(ctx, moref)
	if err != nil {
		return err
	}
	return vm.WaitForPowerState(ctx, types.VirtualMachinePowerStatePoweredOff)
}

// RebootGuest asks the guest OS to restart.
func (cl *Client) RebootGuest(ctx context.Context, moref string) error {
	return cl.guestOp(ctx, moref, func(vm *object.VirtualMachine) error { return vm.RebootGuest(ctx) })
}

// Reset is a hard reset, like pressing the reset button.
func (cl *Client) Reset(ctx context.Context, moref string) error {
	vm, err := cl.vmObject(ctx, moref)
	if err != nil {
		return err
	}
	task, err := vm.Reset(ctx)
	if err != nil {
		return err
	}
	return task.Wait(ctx)
}

// Reconfigure sets the number of CPUs and the memory.
func (cl *Client) Reconfigure(ctx context.Context, moref string, cpus, memoryMB int) error {
	vm, err := cl.vmObject(ctx, moref)
	if err != nil {
		return err
	}
	task, err := vm.Reconfigure(ctx, types.VirtualMachineConfigSpec{NumCPUs: int32(cpus), MemoryMB: int64(memoryMB)})
	if err != nil {
		return err
	}
	return task.Wait(ctx)
}

// AddDisk adds a thin disk on the VM's datastore and returns its label.
func (cl *Client) AddDisk(ctx context.Context, moref string, sizeGB int) (string, error) {
	vm, err := cl.vmObject(ctx, moref)
	if err != nil {
		return "", err
	}
	devices, err := vm.Device(ctx)
	if err != nil {
		return "", err
	}
	controller, err := devices.FindDiskController("")
	if err != nil {
		return "", err
	}
	before := map[int32]bool{}
	for _, d := range devices.SelectByType((*types.VirtualDisk)(nil)) {
		before[d.GetVirtualDevice().Key] = true
	}
	disk := devices.CreateDisk(controller, types.ManagedObjectReference{}, "")
	disk.CapacityInKB = int64(sizeGB) * 1024 * 1024
	if err := vm.AddDevice(ctx, disk); err != nil {
		return "", err
	}
	if devices, err = vm.Device(ctx); err == nil {
		for _, d := range devices.SelectByType((*types.VirtualDisk)(nil)) {
			if !before[d.GetVirtualDevice().Key] {
				return deviceLabel(d), nil
			}
		}
	}
	return "", nil
}

// ExtendDisk grows a disk. Shrinking is refused, as in vCenter.
func (cl *Client) ExtendDisk(ctx context.Context, moref, label string, sizeGB int) error {
	vm, err := cl.vmObject(ctx, moref)
	if err != nil {
		return err
	}
	devices, err := vm.Device(ctx)
	if err != nil {
		return err
	}
	disk, ok := findDevice(devices, label).(*types.VirtualDisk)
	if !ok {
		return &Refused{Msg: label + " is not on the VM."}
	}
	want := int64(sizeGB) * 1024 * 1024
	if want == disk.CapacityInKB {
		return nil
	}
	if want < disk.CapacityInKB {
		return &Refused{Msg: fmt.Sprintf("%s is %d GB; disks can only grow.", label, disk.CapacityInKB/(1024*1024))}
	}
	disk.CapacityInKB, disk.CapacityInBytes = want, want*1024
	return vm.EditDevice(ctx, disk)
}

func (cl *Client) networkBacking(ctx context.Context, network string) (types.BaseVirtualDeviceBackingInfo, error) {
	c, err := cl.client(ctx)
	if err != nil {
		return nil, err
	}
	net, err := findNetwork(ctx, c, network)
	if errors.Is(err, ErrNotFound) {
		return nil, &Refused{Msg: "Network " + network + " is not in vCenter."}
	}
	if err != nil {
		return nil, err
	}
	return net.EthernetCardBackingInfo(ctx)
}

// AddNIC connects a new vmxnet3 adapter to the network.
func (cl *Client) AddNIC(ctx context.Context, moref, network string) error {
	vm, err := cl.vmObject(ctx, moref)
	if err != nil {
		return err
	}
	backing, err := cl.networkBacking(ctx, network)
	if err != nil {
		return err
	}
	nic, err := object.EthernetCardTypes().CreateEthernetCard("vmxnet3", backing)
	if err != nil {
		return err
	}
	return vm.AddDevice(ctx, nic)
}

// findNIC finds a network adapter by its label, or by its position when
// labels repeat ("Network adapter 2" is the second; see toVM).
func (cl *Client) findNIC(ctx context.Context, vm *object.VirtualMachine, label string) (types.BaseVirtualDevice, error) {
	devices, err := vm.Device(ctx)
	if err != nil {
		return nil, err
	}
	cards := devices.SelectByType((*types.VirtualEthernetCard)(nil))
	var match []types.BaseVirtualDevice
	seen, repeated := map[string]bool{}, false
	for _, c := range cards {
		l := deviceLabel(c)
		repeated = repeated || seen[l]
		seen[l] = true
		if l == label {
			match = append(match, c)
		}
	}
	if len(match) == 1 {
		return match[0], nil
	}
	// Positions only stand in for labels that repeat, as toVM does: with
	// unique labels, a label that isn't there is an adapter already gone.
	var n int
	if _, err := fmt.Sscanf(label, "Network adapter %d", &n); repeated && err == nil && n >= 1 && n <= len(cards) {
		return cards[n-1], nil
	}
	return nil, &Refused{Msg: label + " is not on the VM."}
}

// RemoveNIC removes a network adapter. One already gone is not an error.
func (cl *Client) RemoveNIC(ctx context.Context, moref, label string) error {
	vm, err := cl.vmObject(ctx, moref)
	if err != nil {
		return err
	}
	dev, err := cl.findNIC(ctx, vm, label)
	var refused *Refused
	if errors.As(err, &refused) {
		return nil
	}
	if err != nil {
		return err
	}
	return vm.RemoveDevice(ctx, false, dev)
}

// SetNICNetwork moves a network adapter to another network.
func (cl *Client) SetNICNetwork(ctx context.Context, moref, label, network string) error {
	vm, err := cl.vmObject(ctx, moref)
	if err != nil {
		return err
	}
	dev, err := cl.findNIC(ctx, vm, label)
	if err != nil {
		return err
	}
	backing, err := cl.networkBacking(ctx, network)
	if err != nil {
		return err
	}
	dev.(types.BaseVirtualEthernetCard).GetVirtualEthernetCard().Backing = backing
	return vm.EditDevice(ctx, dev)
}

// CreateSnapshot takes a snapshot, with the memory when asked. One with
// the same name already there is taken as done (names are unique per VM in
// rvav).
func (cl *Client) CreateSnapshot(ctx context.Context, moref, name, description string, memory bool) error {
	vm, err := cl.vmObject(ctx, moref)
	if err != nil {
		return err
	}
	if found, err := snapshotExists(ctx, vm, name); err != nil || found {
		return err
	}
	task, err := vm.CreateSnapshot(ctx, name, description, memory, false)
	if err != nil {
		return err
	}
	return task.Wait(ctx)
}

// snapshotExists reports whether the VM has one snapshot with this name.
// Several (taken outside rvav) can't be told apart by name: refused.
func snapshotExists(ctx context.Context, vm *object.VirtualMachine, name string) (bool, error) {
	var o mo.VirtualMachine
	if err := vm.Properties(ctx, vm.Reference(), []string{"snapshot"}, &o); err != nil {
		return false, err
	}
	if o.Snapshot == nil {
		return false, nil
	}
	n := 0
	var walk func([]types.VirtualMachineSnapshotTree)
	walk = func(list []types.VirtualMachineSnapshotTree) {
		for _, s := range list {
			if s.Name == name {
				n++
			}
			walk(s.ChildSnapshotList)
		}
	}
	walk(o.Snapshot.RootSnapshotList)
	if n > 1 {
		return false, &Refused{Msg: fmt.Sprintf("%d snapshots are named %q; rename or delete them in vCenter.", n, name)}
	}
	return n == 1, nil
}

// RevertSnapshot goes back to a snapshot.
func (cl *Client) RevertSnapshot(ctx context.Context, moref, name string) error {
	vm, err := cl.vmObject(ctx, moref)
	if err != nil {
		return err
	}
	if found, err := snapshotExists(ctx, vm, name); err != nil {
		return err
	} else if !found {
		return &Refused{Msg: "The VM has no snapshot named " + name + "."}
	}
	task, err := vm.RevertToSnapshot(ctx, name, true)
	if err != nil {
		return err
	}
	return task.Wait(ctx)
}

// DeleteSnapshot removes a snapshot and consolidates. One already gone is not an error.
func (cl *Client) DeleteSnapshot(ctx context.Context, moref, name string) error {
	vm, err := cl.vmObject(ctx, moref)
	if err != nil {
		return err
	}
	if found, err := snapshotExists(ctx, vm, name); err != nil || !found {
		return err
	}
	consolidate := true
	task, err := vm.RemoveSnapshot(ctx, name, false, &consolidate)
	if err != nil {
		return err
	}
	return task.Wait(ctx)
}

// Rename changes the VM's name in vCenter (not the guest hostname).
func (cl *Client) Rename(ctx context.Context, moref, name string) error {
	vm, err := cl.vmObject(ctx, moref)
	if err != nil {
		return err
	}
	task, err := vm.Rename(ctx, name)
	if err != nil {
		return err
	}
	return task.Wait(ctx)
}

// MoveToFolder moves the VM into a folder (an inventory path such as
// /GVA/vm/rvav/web/dev), creating the folder when it is missing.
func (cl *Client) MoveToFolder(ctx context.Context, moref, folder string) error {
	c, err := cl.client(ctx)
	if err != nil {
		return err
	}
	f, err := ensureFolder(ctx, c, folder)
	if err != nil {
		return err
	}
	task, err := f.MoveInto(ctx, []types.ManagedObjectReference{vmRef(moref)})
	if err != nil {
		return err
	}
	return task.Wait(ctx)
}

// MountISO inserts an ISO image (`[datastore] path/file.iso`) in the first
// CD-ROM drive, adding a drive when the VM has none.
func (cl *Client) MountISO(ctx context.Context, moref, path string) error {
	vm, err := cl.vmObject(ctx, moref)
	if err != nil {
		return err
	}
	devices, err := vm.Device(ctx)
	if err != nil {
		return err
	}
	cdrom, err := devices.FindCdrom("")
	if err != nil {
		ide, err := devices.FindIDEController("")
		if err != nil {
			return err
		}
		if cdrom, err = devices.CreateCdrom(ide); err != nil {
			return err
		}
		if err := vm.AddDevice(ctx, devices.InsertIso(cdrom, path)); err != nil {
			return err
		}
		return nil
	}
	return vm.EditDevice(ctx, devices.InsertIso(cdrom, path))
}

// UnmountISO ejects the ISO image from the first CD-ROM drive.
func (cl *Client) UnmountISO(ctx context.Context, moref string) error {
	vm, err := cl.vmObject(ctx, moref)
	if err != nil {
		return err
	}
	devices, err := vm.Device(ctx)
	if err != nil {
		return err
	}
	cdrom, err := devices.FindCdrom("")
	if err != nil {
		return nil // no drive: nothing mounted
	}
	return vm.EditDevice(ctx, devices.EjectIso(cdrom))
}

// CloneTicket returns a one-time ticket for VMRC (ADR-0012).
func (cl *Client) CloneTicket(ctx context.Context) (string, error) {
	c, err := cl.client(ctx)
	if err != nil {
		return "", err
	}
	return session.NewManager(c).AcquireCloneTicket(ctx)
}

// Host returns the vCenter host name, for VMRC links.
func (cl *Client) Host() string {
	if cl.url != nil {
		return cl.url.Hostname()
	}
	if cl.c != nil {
		return cl.c.URL().Hostname()
	}
	return ""
}

// WebMKS is where the HTML console of a VM connects: a WebSocket URL on
// its ESXi host, valid once, and the host's certificate thumbprints.
type WebMKS struct {
	URL         string
	Thumbprints []string
}

// WebMKSTicket asks vCenter for a WebMKS ticket (ADR-0012).
func (cl *Client) WebMKSTicket(ctx context.Context, moref string) (*WebMKS, error) {
	vm, err := cl.vmObject(ctx, moref)
	if err != nil {
		return nil, err
	}
	t, err := vm.AcquireTicket(ctx, string(types.VirtualMachineTicketTypeWebmks))
	if err != nil {
		return nil, err
	}
	out := &WebMKS{URL: t.Url}
	host := t.Host
	if host == "" {
		host = cl.Host()
	}
	if out.URL == "" {
		port := t.Port
		if port == 0 {
			port = 443
		}
		out.URL = fmt.Sprintf("wss://%s:%d/ticket/%s", host, port, t.Ticket)
	}
	if t.SslThumbprint != "" {
		out.Thumbprints = append(out.Thumbprints, t.SslThumbprint)
	}
	for _, c := range t.CertThumbprintList {
		out.Thumbprints = append(out.Thumbprints, c.Thumbprint)
	}
	return out, nil
}
