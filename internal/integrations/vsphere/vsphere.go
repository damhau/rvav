// Package vsphere talks to vCenter with govmomi: cloning and customising VMs,
// power operations, and reading the inventory for the cache.
package vsphere

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/vmware/govmomi"
	"github.com/vmware/govmomi/fault"
	"github.com/vmware/govmomi/find"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/property"
	"github.com/vmware/govmomi/view"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"
)

// ErrNotFound is returned when a named inventory object does not exist.
var ErrNotFound = errors.New("not found in vCenter")

// VMSpec describes a VM to clone from a template.
type VMSpec struct {
	Name      string
	Template  string
	Cluster   string
	Datastore string
	Network   string
	// Folder is an inventory path such as /GVA/vm/rvav/web/dev. Missing
	// folders are created.
	Folder     string
	VCPU       int
	MemoryMB   int
	DiskGB     int
	Annotation string
	// ExtraConfig is written to the VM, for example rvav.deployment.
	ExtraConfig map[string]string
}

// Customization is the guest identity and network applied before first boot.
type Customization struct {
	OSFamily     string // linux or windows
	Hostname     string
	Domain       string
	TimeZone     string
	IP           string
	PrefixLength int
	Gateway      string
	DNSServers   []string
}

type VM struct {
	Moref        string
	InstanceUUID string
	Name         string
	PowerState   string // on, off, suspended
	GuestState   string // running, not_running, unknown
	IP           string
	Hostname     string
	GuestOS      string
	CPUCount     int
	MemoryMB     int
	DiskGB       int
	Disks        []Disk
	NICs         []NIC
	Cluster      string
	Host         string
	Folder       string
	Datastore    string
	Template     bool
}

type Disk struct {
	Label      string
	CapacityGB int
	Datastore  string
}

type NIC struct {
	Network string
	MAC     string
	IPs     []string
}

// Driver is what the workflow and the sync loop need from vCenter.
type Driver interface {
	// Name identifies the vCenter in the API, for example vc01.corp.example.
	Name() string
	FindVM(ctx context.Context, name string) (*VM, error)
	GetVM(ctx context.Context, moref string) (*VM, error)
	CloneVM(ctx context.Context, spec VMSpec) (*VM, error)
	Customize(ctx context.Context, moref string, c Customization) error
	PowerOn(ctx context.Context, moref string) error
	PowerOff(ctx context.Context, moref string) error
	WaitForIP(ctx context.Context, moref string) (string, error)
	Destroy(ctx context.Context, moref string) error
	Inventory(ctx context.Context) (*Inventory, error)
}

// Client implements Driver for one vCenter.
type Client struct {
	name string
	url  *url.URL

	insecure bool
	mu       sync.Mutex
	c        *govmomi.Client
}

// New logs in to vCenter. rawURL is the SDK endpoint, for example
// https://vc01.corp.example/sdk.
func New(ctx context.Context, name, rawURL, username, password string, insecure bool) (*Client, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("vcenter url: %w", err)
	}
	if !strings.HasSuffix(u.Path, "/sdk") {
		u.Path = strings.TrimSuffix(u.Path, "/") + "/sdk"
	}
	if username != "" {
		u.User = url.UserPassword(username, password)
	}
	cl := &Client{name: name, url: u, insecure: insecure}
	if _, err := cl.client(ctx); err != nil {
		return nil, err
	}
	return cl, nil
}

// NewFromVim wraps an existing session, as used by tests against vcsim.
func NewFromVim(name string, c *vim25.Client) *Client {
	return &Client{name: name, c: &govmomi.Client{Client: c}}
}

func (cl *Client) Name() string { return cl.name }

// client returns a logged-in session, logging in again if it expired.
func (cl *Client) client(ctx context.Context) (*vim25.Client, error) {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	if cl.c != nil {
		if cl.url == nil {
			return cl.c.Client, nil
		}
		if ok, err := cl.c.SessionManager.SessionIsActive(ctx); err == nil && ok {
			return cl.c.Client, nil
		}
	}
	c, err := govmomi.NewClient(ctx, cl.url, cl.insecure)
	if err != nil {
		return nil, fmt.Errorf("log in to vCenter %s: %w", cl.url.Host, err)
	}
	cl.c = c
	return c.Client, nil
}

// findRef looks up a managed object of kind by its name anywhere in the inventory.
func findRef(ctx context.Context, c *vim25.Client, kind, name string) (types.ManagedObjectReference, error) {
	m := view.NewManager(c)
	v, err := m.CreateContainerView(ctx, c.ServiceContent.RootFolder, []string{kind}, true)
	if err != nil {
		return types.ManagedObjectReference{}, err
	}
	defer v.Destroy(context.Background())
	refs, err := v.Find(ctx, []string{kind}, property.Match{"name": name})
	if err != nil {
		return types.ManagedObjectReference{}, err
	}
	if len(refs) == 0 {
		return types.ManagedObjectReference{}, fmt.Errorf("%s %q: %w", kind, name, ErrNotFound)
	}
	return refs[0], nil
}

func findNetwork(ctx context.Context, c *vim25.Client, name string) (object.NetworkReference, error) {
	for _, kind := range []string{"DistributedVirtualPortgroup", "Network", "OpaqueNetwork"} {
		ref, err := findRef(ctx, c, kind, name)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		nr, ok := object.NewReference(c, ref).(object.NetworkReference)
		if !ok {
			return nil, fmt.Errorf("network %q is not usable as a NIC backing", name)
		}
		return nr, nil
	}
	return nil, fmt.Errorf("network %q: %w", name, ErrNotFound)
}

// ensureFolder returns the folder at an inventory path, creating missing parts.
func ensureFolder(ctx context.Context, c *vim25.Client, p string) (*object.Folder, error) {
	finder := find.NewFinder(c, false)
	if f, err := finder.Folder(ctx, p); err == nil {
		return f, nil
	}
	parent := path.Dir(p)
	if parent == p || parent == "/" || parent == "." {
		return nil, fmt.Errorf("folder %q: %w", p, ErrNotFound)
	}
	pf, err := ensureFolder(ctx, c, parent)
	if err != nil {
		return nil, err
	}
	f, err := pf.CreateFolder(ctx, path.Base(p))
	if err != nil {
		// Another worker may have created it in the meantime.
		if f2, err2 := finder.Folder(ctx, p); err2 == nil {
			return f2, nil
		}
		return nil, fmt.Errorf("create folder %q: %w", p, err)
	}
	return f, nil
}

func (cl *Client) FindVM(ctx context.Context, name string) (*VM, error) {
	c, err := cl.client(ctx)
	if err != nil {
		return nil, err
	}
	ref, err := findRef(ctx, c, "VirtualMachine", name)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return cl.vmInfo(ctx, c, ref)
}

func (cl *Client) GetVM(ctx context.Context, moref string) (*VM, error) {
	c, err := cl.client(ctx)
	if err != nil {
		return nil, err
	}
	return cl.vmInfo(ctx, c, vmRef(moref))
}

func vmRef(moref string) types.ManagedObjectReference {
	return types.ManagedObjectReference{Type: "VirtualMachine", Value: moref}
}

var vmProps = []string{"name", "config", "runtime.powerState", "runtime.host", "guest", "summary.quickStats",
	"parent", "datastore", "network", "snapshot"}

func (cl *Client) vmInfo(ctx context.Context, c *vim25.Client, ref types.ManagedObjectReference) (*VM, error) {
	var m mo.VirtualMachine
	pc := property.DefaultCollector(c)
	if err := pc.RetrieveOne(ctx, ref, vmProps, &m); err != nil {
		if isNotFoundFault(err) {
			return nil, fmt.Errorf("vm %s: %w", ref.Value, ErrNotFound)
		}
		return nil, err
	}
	vm := toVM(m)
	if m.Runtime.Host != nil {
		var h mo.HostSystem
		if err := pc.RetrieveOne(ctx, *m.Runtime.Host, []string{"name", "parent"}, &h); err == nil {
			vm.Host = h.Name
			if h.Parent != nil && h.Parent.Type == "ClusterComputeResource" {
				var cc mo.ClusterComputeResource
				if err := pc.RetrieveOne(ctx, *h.Parent, []string{"name"}, &cc); err == nil {
					vm.Cluster = cc.Name
				}
			}
		}
	}
	names := map[types.ManagedObjectReference]string{}
	var dss []mo.Datastore
	if len(m.Datastore) > 0 {
		if err := pc.Retrieve(ctx, m.Datastore, []string{"name"}, &dss); err == nil {
			for _, d := range dss {
				names[d.Self] = d.Name
			}
		}
	}
	if len(m.Datastore) > 0 {
		vm.Datastore = names[m.Datastore[0]]
	}
	for i := range vm.Disks {
		if vm.Disks[i].Datastore == "" {
			vm.Disks[i].Datastore = vm.Datastore
		}
	}
	// Distributed port group backings carry the port group key, which is
	// the port group's moref; show its name instead.
	if len(m.Network) > 0 {
		netNames := map[string]string{}
		for _, ref := range m.Network {
			var n mo.Network
			if err := pc.RetrieveOne(ctx, ref, []string{"name"}, &n); err == nil {
				netNames[ref.Value] = n.Name
			}
		}
		for i := range vm.NICs {
			if name, ok := netNames[vm.NICs[i].Network]; ok {
				vm.NICs[i].Network = name
			}
		}
	}
	if m.Parent != nil {
		vm.Folder = inventoryPath(ctx, pc, *m.Parent)
	}
	return &vm, nil
}

func toVM(m mo.VirtualMachine) VM {
	vm := VM{Moref: m.Self.Value, Name: m.Name, PowerState: powerState(m.Runtime.PowerState), GuestState: "unknown"}
	if m.Config != nil {
		vm.InstanceUUID = m.Config.InstanceUuid
		vm.GuestOS = m.Config.GuestFullName
		vm.CPUCount = int(m.Config.Hardware.NumCPU)
		vm.MemoryMB = int(m.Config.Hardware.MemoryMB)
		vm.Template = m.Config.Template
		for _, d := range m.Config.Hardware.Device {
			switch dev := d.(type) {
			case *types.VirtualDisk:
				label := ""
				if dev.DeviceInfo != nil {
					label = dev.DeviceInfo.GetDescription().Label
				}
				gb := int(dev.CapacityInKB / (1024 * 1024))
				vm.Disks = append(vm.Disks, Disk{Label: label, CapacityGB: gb})
				vm.DiskGB += gb
			case types.BaseVirtualEthernetCard:
				card := dev.GetVirtualEthernetCard()
				vm.NICs = append(vm.NICs, NIC{MAC: card.MacAddress, Network: backingName(card.Backing)})
			}
		}
	}
	if m.Guest != nil {
		switch m.Guest.ToolsRunningStatus {
		case string(types.VirtualMachineToolsRunningStatusGuestToolsRunning):
			vm.GuestState = "running"
		case string(types.VirtualMachineToolsRunningStatusGuestToolsNotRunning):
			vm.GuestState = "not_running"
		}
		vm.IP = m.Guest.IpAddress
		vm.Hostname = m.Guest.HostName
		for i, n := range m.Guest.Net {
			if i < len(vm.NICs) {
				vm.NICs[i].IPs = n.IpAddress
				if vm.NICs[i].Network == "" {
					vm.NICs[i].Network = n.Network
				}
			}
		}
	}
	return vm
}

func backingName(b types.BaseVirtualDeviceBackingInfo) string {
	switch bk := b.(type) {
	case *types.VirtualEthernetCardNetworkBackingInfo:
		return bk.DeviceName
	case *types.VirtualEthernetCardDistributedVirtualPortBackingInfo:
		return bk.Port.PortgroupKey
	}
	return ""
}

func powerState(s types.VirtualMachinePowerState) string {
	switch s {
	case types.VirtualMachinePowerStatePoweredOn:
		return "on"
	case types.VirtualMachinePowerStateSuspended:
		return "suspended"
	}
	return "off"
}

// CloneVM clones the template into the folder, cluster and datastore of
// spec, connects the first NIC to the network and sets CPU, memory, disk
// size and extra config. The VM stays powered off. If a VM with the same name
// already exists it is returned, so retries do not create duplicates.
func (cl *Client) CloneVM(ctx context.Context, spec VMSpec) (*VM, error) {
	c, err := cl.client(ctx)
	if err != nil {
		return nil, err
	}
	if existing, err := cl.FindVM(ctx, spec.Name); err != nil {
		return nil, err
	} else if existing != nil {
		return existing, nil
	}

	tplRef, err := findRef(ctx, c, "VirtualMachine", spec.Template)
	if err != nil {
		return nil, fmt.Errorf("template: %w", err)
	}
	tpl := object.NewVirtualMachine(c, tplRef)
	clusterRef, err := findRef(ctx, c, "ClusterComputeResource", spec.Cluster)
	if err != nil {
		return nil, err
	}
	var cluster mo.ClusterComputeResource
	if err := property.DefaultCollector(c).RetrieveOne(ctx, clusterRef, []string{"resourcePool"}, &cluster); err != nil {
		return nil, err
	}
	if cluster.ResourcePool == nil {
		return nil, fmt.Errorf("cluster %q has no resource pool", spec.Cluster)
	}
	dsRef, err := findRef(ctx, c, "Datastore", spec.Datastore)
	if err != nil {
		return nil, err
	}
	folder, err := ensureFolder(ctx, c, spec.Folder)
	if err != nil {
		return nil, err
	}

	devices, err := tpl.Device(ctx)
	if err != nil {
		return nil, err
	}
	var changes []types.BaseVirtualDeviceConfigSpec
	if spec.Network != "" {
		nics := devices.SelectByType((*types.VirtualEthernetCard)(nil))
		if len(nics) == 0 {
			return nil, fmt.Errorf("template %q has no network adapter", spec.Template)
		}
		net, err := findNetwork(ctx, c, spec.Network)
		if err != nil {
			return nil, err
		}
		backing, err := net.EthernetCardBackingInfo(ctx)
		if err != nil {
			return nil, err
		}
		card := nics[0].(types.BaseVirtualEthernetCard).GetVirtualEthernetCard()
		card.Backing = backing
		changes = append(changes, &types.VirtualDeviceConfigSpec{
			Operation: types.VirtualDeviceConfigSpecOperationEdit,
			Device:    nics[0],
		})
	}

	pool := *cluster.ResourcePool
	cs := types.VirtualMachineCloneSpec{
		Location: types.VirtualMachineRelocateSpec{
			Pool:         &pool,
			Datastore:    &dsRef,
			DeviceChange: changes,
		},
		Config: &types.VirtualMachineConfigSpec{
			NumCPUs:    int32(spec.VCPU),
			MemoryMB:   int64(spec.MemoryMB),
			Annotation: spec.Annotation,
		},
	}
	for k, v := range spec.ExtraConfig {
		cs.Config.ExtraConfig = append(cs.Config.ExtraConfig, &types.OptionValue{Key: k, Value: v})
	}
	task, err := tpl.Clone(ctx, folder, spec.Name, cs)
	if err != nil {
		return nil, err
	}
	info, err := task.WaitForResult(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("clone %s: %w", spec.Name, err)
	}
	ref, ok := info.Result.(types.ManagedObjectReference)
	if !ok {
		return nil, fmt.Errorf("clone %s: unexpected task result %T", spec.Name, info.Result)
	}
	// Some vCenter versions (and vcsim) ignore the clone spec's hardware
	// settings, so apply size and extra config explicitly.
	if err := reconfigure(ctx, object.NewVirtualMachine(c, ref), spec); err != nil {
		return nil, fmt.Errorf("reconfigure %s: %w", spec.Name, err)
	}
	return cl.vmInfo(ctx, c, ref)
}

// reconfigure sets CPU, memory, annotation and extra config, and grows the
// first disk to spec.DiskGB (disks are never shrunk).
func reconfigure(ctx context.Context, vm *object.VirtualMachine, spec VMSpec) error {
	cfg := types.VirtualMachineConfigSpec{
		NumCPUs:    int32(spec.VCPU),
		MemoryMB:   int64(spec.MemoryMB),
		Annotation: spec.Annotation,
	}
	for k, v := range spec.ExtraConfig {
		cfg.ExtraConfig = append(cfg.ExtraConfig, &types.OptionValue{Key: k, Value: v})
	}
	if spec.DiskGB > 0 {
		devices, err := vm.Device(ctx)
		if err != nil {
			return err
		}
		if disks := devices.SelectByType((*types.VirtualDisk)(nil)); len(disks) > 0 {
			disk := disks[0].(*types.VirtualDisk)
			want := int64(spec.DiskGB) * 1024 * 1024
			if disk.CapacityInKB < want {
				disk.CapacityInKB = want
				disk.CapacityInBytes = want * 1024
				cfg.DeviceChange = append(cfg.DeviceChange, &types.VirtualDeviceConfigSpec{
					Operation: types.VirtualDeviceConfigSpecOperationEdit,
					Device:    disk,
				})
			}
		}
	}
	task, err := vm.Reconfigure(ctx, cfg)
	if err != nil {
		return err
	}
	return task.Wait(ctx)
}

// Customize applies a guest customization spec with a fixed IP. It takes
// effect at the next power-on.
func (cl *Client) Customize(ctx context.Context, moref string, cu Customization) error {
	c, err := cl.client(ctx)
	if err != nil {
		return err
	}
	vm := object.NewVirtualMachine(c, vmRef(moref))
	var mvm mo.VirtualMachine
	if err := property.DefaultCollector(c).RetrieveOne(ctx, vm.Reference(), []string{"config.tools", "runtime.powerState"}, &mvm); err != nil {
		return err
	}
	if mvm.Config != nil && mvm.Config.Tools != nil && mvm.Config.Tools.PendingCustomization != "" {
		return nil // already applied by an earlier attempt
	}
	if mvm.Runtime.PowerState == types.VirtualMachinePowerStatePoweredOn {
		return nil // customization already ran on first boot
	}

	spec := types.CustomizationSpec{
		GlobalIPSettings: types.CustomizationGlobalIPSettings{
			DnsServerList: cu.DNSServers,
			DnsSuffixList: []string{cu.Domain},
		},
		NicSettingMap: []types.CustomizationAdapterMapping{{
			Adapter: types.CustomizationIPSettings{
				Ip:            &types.CustomizationFixedIp{IpAddress: cu.IP},
				SubnetMask:    prefixToMask(cu.PrefixLength),
				Gateway:       []string{cu.Gateway},
				DnsDomain:     cu.Domain,
				DnsServerList: cu.DNSServers,
			},
		}},
	}
	if cu.OSFamily == "windows" {
		spec.Identity = &types.CustomizationSysprep{
			UserData: types.CustomizationUserData{
				FullName:     "rvav",
				OrgName:      "rvav",
				ComputerName: &types.CustomizationFixedName{Name: cu.Hostname},
			},
			GuiUnattended:  types.CustomizationGuiUnattended{TimeZone: 110},
			Identification: types.CustomizationIdentification{JoinWorkgroup: "WORKGROUP"},
		}
	} else {
		spec.Identity = &types.CustomizationLinuxPrep{
			HostName:   &types.CustomizationFixedName{Name: cu.Hostname},
			Domain:     cu.Domain,
			TimeZone:   orDefault(cu.TimeZone, "Europe/Zurich"),
			HwClockUTC: types.NewBool(true),
		}
	}
	task, err := vm.Customize(ctx, spec)
	if err != nil {
		return err
	}
	return task.Wait(ctx)
}

func (cl *Client) PowerOn(ctx context.Context, moref string) error {
	c, err := cl.client(ctx)
	if err != nil {
		return err
	}
	vm := object.NewVirtualMachine(c, vmRef(moref))
	state, err := vm.PowerState(ctx)
	if err != nil {
		return err
	}
	if state == types.VirtualMachinePowerStatePoweredOn {
		return nil
	}
	task, err := vm.PowerOn(ctx)
	if err != nil {
		return err
	}
	return task.Wait(ctx)
}

func (cl *Client) PowerOff(ctx context.Context, moref string) error {
	c, err := cl.client(ctx)
	if err != nil {
		return err
	}
	vm := object.NewVirtualMachine(c, vmRef(moref))
	state, err := vm.PowerState(ctx)
	if err != nil {
		return err
	}
	if state == types.VirtualMachinePowerStatePoweredOff {
		return nil
	}
	task, err := vm.PowerOff(ctx)
	if err != nil {
		return err
	}
	return task.Wait(ctx)
}

// WaitForIP waits until VMware Tools reports a guest IP address.
func (cl *Client) WaitForIP(ctx context.Context, moref string) (string, error) {
	c, err := cl.client(ctx)
	if err != nil {
		return "", err
	}
	return object.NewVirtualMachine(c, vmRef(moref)).WaitForIP(ctx, true)
}

// Destroy powers off and deletes the VM. A VM that no longer exists is not an error.
func (cl *Client) Destroy(ctx context.Context, moref string) error {
	c, err := cl.client(ctx)
	if err != nil {
		return err
	}
	if err := cl.PowerOff(ctx, moref); err != nil {
		if isNotFoundFault(err) {
			return nil
		}
		return err
	}
	task, err := object.NewVirtualMachine(c, vmRef(moref)).Destroy(ctx)
	if err != nil {
		if isNotFoundFault(err) {
			return nil
		}
		return err
	}
	return task.Wait(ctx)
}

func isNotFoundFault(err error) bool {
	if err == nil {
		return false
	}
	if fault.Is(err, &types.ManagedObjectNotFound{}) {
		return true
	}
	return strings.Contains(err.Error(), "ManagedObjectNotFound") || strings.Contains(err.Error(), "has already been deleted")
}

func prefixToMask(bits int) string {
	if bits <= 0 || bits > 32 {
		bits = 24
	}
	m := uint32(0xffffffff) << (32 - bits)
	return fmt.Sprintf("%d.%d.%d.%d", m>>24, (m>>16)&0xff, (m>>8)&0xff, m&0xff)
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// Sleep waits for d or until ctx is done.
func Sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
