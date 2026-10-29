// Package vsphere talks to vCenter with govmomi: cloning and customising VMs,
// power operations, and reading the inventory for the cache.
package vsphere

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"path"
	"slices"
	"strconv"
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
	// HostIndex picks the cluster's eligible hosts in turn: VMs kept apart
	// get 0, 1, 2… so they start on separate hosts (ADR-0024).
	HostIndex int
	// Disks and NICs are added after the template's (networks, by name).
	Disks []DiskSpec
	NICs  []string
	// Library, when set, is the content library Template is an item of:
	// a VM template or an OVF, deployed rather than cloned.
	Library string
	// StoragePolicy is the VM's (its home and disks) storage policy.
	StoragePolicy string
}

// DiskSpec is a disk added at provisioning, on Datastore (the VM's when
// empty), with StoragePolicy (the VM's when empty).
type DiskSpec struct {
	SizeGB        int
	Datastore     string
	StoragePolicy string
}

// Customization is the guest identity and network applied before first boot.
type Customization struct {
	OSFamily string // linux or windows
	Hostname string
	Domain   string
	// TimeZone is a tz name for Linux, a Windows time zone index for
	// Windows (110, W. Europe, when empty).
	TimeZone string
	// Spec names a vCenter customization specification used as the base
	// (ADR-0024): rvav sets the host name and adapters on it.
	Spec string
	// Windows: the local administrator's password, and the domain to join
	// (in OU) with an account allowed to.
	AdminPassword  string
	JoinDomain     string
	DomainOU       string
	DomainUser     string
	DomainPassword string
	IP             string
	PrefixLength   int
	Gateway        string
	DNSServers     []string
	// Extra are the added adapters, in order; OtherNICs the template's own
	// after its first, which keep DHCP.
	Extra     []NICSetting
	OtherNICs int
}

// NICSetting is an added adapter's address: fixed, or from DHCP. Only the
// first adapter has the gateway.
type NICSetting struct {
	DHCP         bool
	IP           string
	PrefixLength int
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
	// CPUHotAdd and MemoryHotAdd allow adding CPU or memory while running.
	CPUHotAdd    bool
	MemoryHotAdd bool
	Snapshots    []Snapshot
	// ISO is the image mounted in the first CD-ROM drive, if any.
	ISO string
}

type Snapshot struct {
	Name        string
	Description string
	Created     time.Time
}

type Disk struct {
	Label      string
	CapacityGB int
	Datastore  string
}

type NIC struct {
	Label   string
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
	SetGuestInfo(ctx context.Context, moref string, values map[string]string) error
	PowerOn(ctx context.Context, moref string) error
	PowerOff(ctx context.Context, moref string) error
	WaitForIP(ctx context.Context, moref string) (string, error)
	Destroy(ctx context.Context, moref string) error
	Inventory(ctx context.Context) (*Inventory, error)
	// Watch follows changes until ctx is done or the session breaks (watch.go).
	Watch(ctx context.Context, changed func([]Change)) error
	InventoryVM(ctx context.Context, moref string) (*InventoryVM, error)
	// AttachTags attaches vSphere tags, category/tag (tags.go).
	AttachTags(ctx context.Context, moref string, tags []string) error
	// DRS rules keeping VMs apart (rules.go).
	SetAntiAffinityRule(ctx context.Context, cluster, name string, morefs []string) error
	DeleteRule(ctx context.Context, cluster, name string) error

	// Day-2 operations (day2.go).
	ShutdownGuest(ctx context.Context, moref string) error
	WaitPoweredOff(ctx context.Context, moref string) error
	RebootGuest(ctx context.Context, moref string) error
	Reset(ctx context.Context, moref string) error
	Reconfigure(ctx context.Context, moref string, cpus, memoryMB int) error
	AddDisk(ctx context.Context, moref string, sizeGB int) (string, error)
	ExtendDisk(ctx context.Context, moref, label string, sizeGB int) error
	AddNIC(ctx context.Context, moref, network string) error
	RemoveNIC(ctx context.Context, moref, label string) error
	SetNICNetwork(ctx context.Context, moref, label, network string) error
	CreateSnapshot(ctx context.Context, moref, name, description string, memory bool) error
	RevertSnapshot(ctx context.Context, moref, name string) error
	DeleteSnapshot(ctx context.Context, moref, name string) error
	Rename(ctx context.Context, moref, name string) error
	MoveToFolder(ctx context.Context, moref, folder string) error
	MountISO(ctx context.Context, moref, path string) error
	UnmountISO(ctx context.Context, moref string) error
	CloneTicket(ctx context.Context) (string, error)
	Host() string
	WebMKSTicket(ctx context.Context, moref string) (*WebMKS, error)
}

// Client implements Driver for one vCenter.
type Client struct {
	name string
	url  *url.URL

	insecure bool
	mu       sync.Mutex
	c        *govmomi.Client
	// login signs in to vCenter's REST API (the tagging service).
	login *url.Userinfo
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
	cl := &Client{name: name, url: u, insecure: insecure, login: u.User}
	if _, err := cl.client(ctx); err != nil {
		return nil, err
	}
	return cl, nil
}

// NewFromVim wraps an existing session, as used by tests against vcsim
// (whose default account the tagging service signs in with).
func NewFromVim(name string, c *vim25.Client) *Client {
	return &Client{name: name, c: &govmomi.Client{Client: c}, login: vcsimLogin}
}

func (cl *Client) Name() string { return cl.name }

// Vim returns the logged-in vim25 client, for helpers such as Bootstrap.
func (cl *Client) Vim(ctx context.Context) (*vim25.Client, error) { return cl.client(ctx) }

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

// InventoryVM reads one VM as the inventory has it, without quick stats
// (the inventory sync's full read has them).
func (cl *Client) InventoryVM(ctx context.Context, moref string) (*InventoryVM, error) {
	c, err := cl.client(ctx)
	if err != nil {
		return nil, err
	}
	vm, err := cl.vmInfo(ctx, c, vmRef(moref))
	if err != nil {
		return nil, err
	}
	iv := &InventoryVM{VM: *vm}
	var m mo.VirtualMachine
	if err := property.DefaultCollector(c).RetrieveOne(ctx, vmRef(moref), []string{"snapshot"}, &m); err == nil && m.Snapshot != nil {
		iv.SnapshotCount = countSnapshots(m.Snapshot.RootSnapshotList)
	}
	return iv, nil
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
		vm.CPUHotAdd = m.Config.CpuHotAddEnabled != nil && *m.Config.CpuHotAddEnabled
		vm.MemoryHotAdd = m.Config.MemoryHotAddEnabled != nil && *m.Config.MemoryHotAddEnabled
		// Disks in controller and unit order, as vCenter numbers them;
		// adapters by key, the first adapter first; other devices in the
		// order vCenter lists them.
		var disks, cards, others []types.BaseVirtualDevice
		for _, d := range m.Config.Hardware.Device {
			switch d.(type) {
			case *types.VirtualDisk:
				disks = append(disks, d)
			case types.BaseVirtualEthernetCard:
				cards = append(cards, d)
			default:
				others = append(others, d)
			}
		}
		slices.SortStableFunc(disks, func(a, b types.BaseVirtualDevice) int {
			x, y := a.GetVirtualDevice(), b.GetVirtualDevice()
			if x.ControllerKey != y.ControllerKey {
				return int(x.ControllerKey - y.ControllerKey)
			}
			return int(deref32(x.UnitNumber) - deref32(y.UnitNumber))
		})
		slices.SortStableFunc(cards, func(a, b types.BaseVirtualDevice) int {
			return int(a.GetVirtualDevice().Key - b.GetVirtualDevice().Key)
		})
		devices := slices.Concat(disks, cards, others)
		for _, d := range devices {
			switch dev := d.(type) {
			case *types.VirtualDisk:
				label := ""
				if dev.DeviceInfo != nil {
					label = dev.DeviceInfo.GetDescription().Label
				}
				gb := int(dev.CapacityInKB / (1024 * 1024))
				// Its datastore, from its file ("[vsan-01] vm/vm_1.vmdk").
				ds := ""
				if b, ok := dev.Backing.(types.BaseVirtualDeviceFileBackingInfo); ok {
					var p object.DatastorePath
					if p.FromString(b.GetVirtualDeviceFileBackingInfo().FileName) {
						ds = p.Datastore
					}
				}
				vm.Disks = append(vm.Disks, Disk{Label: label, CapacityGB: gb, Datastore: ds})
				vm.DiskGB += gb
			case types.BaseVirtualEthernetCard:
				card := dev.GetVirtualEthernetCard()
				label := ""
				if card.DeviceInfo != nil {
					label = card.DeviceInfo.GetDescription().Label
				}
				vm.NICs = append(vm.NICs, NIC{Label: label, MAC: card.MacAddress, Network: backingName(card.Backing)})
			case *types.VirtualCdrom:
				if iso, ok := dev.Backing.(*types.VirtualCdromIsoBackingInfo); ok && vm.ISO == "" {
					vm.ISO = iso.FileName
				}
			}
		}
	}
	// Labels name adapters in actions; if they repeat (vcsim), use positions.
	seen := map[string]bool{}
	for _, n := range vm.NICs {
		if seen[n.Label] {
			for i := range vm.NICs {
				vm.NICs[i].Label = fmt.Sprintf("Network adapter %d", i+1)
			}
			break
		}
		seen[n.Label] = true
	}
	if m.Snapshot != nil {
		var walk func([]types.VirtualMachineSnapshotTree)
		walk = func(list []types.VirtualMachineSnapshotTree) {
			for _, s := range list {
				vm.Snapshots = append(vm.Snapshots, Snapshot{Name: s.Name, Description: s.Description, Created: s.CreateTime})
				walk(s.ChildSnapshotList)
			}
		}
		walk(m.Snapshot.RootSnapshotList)
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

func deref32(p *int32) int32 {
	if p == nil {
		return 0
	}
	return *p
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
	existing, err := cl.FindVM(ctx, spec.Name)
	if err != nil {
		return nil, err
	}
	// A VM an earlier attempt made and booted is as it should be.
	if existing != nil && existing.PowerState == "on" {
		return existing, nil
	}
	policy, err := policyID(ctx, c, spec.StoragePolicy)
	if err != nil {
		return nil, err
	}
	w, err := cl.where(ctx, c, spec)
	if err != nil {
		return nil, err
	}
	var ref types.ManagedObjectReference
	switch {
	case existing != nil:
		// An earlier attempt made it: it is finished below, as a new one is.
		ref = vmRef(existing.Moref)
	case spec.Library != "":
		if ref, err = cl.deployFromLibrary(ctx, c, spec, w.pool, w.host, w.folder.Reference(), w.datastore, policy); err != nil {
			return nil, err
		}
	default:
		tplRef, err := findRef(ctx, c, "VirtualMachine", spec.Template)
		if err != nil {
			return nil, fmt.Errorf("template: %w", err)
		}
		if ref, err = cl.cloneTemplate(ctx, c, object.NewVirtualMachine(c, tplRef), spec, w.pool, w.host, w.folder, w.datastore, policy); err != nil {
			return nil, err
		}
	}
	if err := cl.finish(ctx, c, object.NewVirtualMachine(c, ref), spec, policy, w); err != nil {
		return nil, fmt.Errorf("%s: %w", spec.Name, err)
	}
	return cl.vmInfo(ctx, c, ref)
}

// placed is where a VM goes: the cluster's resource pool, a host, the
// datastore and the folder.
type placed struct {
	pool, host, datastore types.ManagedObjectReference
	folder                *object.Folder
}

// where places a VM: on a connected host of the cluster that mounts the
// datastore, so it never lands outside the cluster; VMs kept apart take the
// eligible hosts in turn.
func (cl *Client) where(ctx context.Context, c *vim25.Client, spec VMSpec) (placed, error) {
	var w placed
	clusterRef, err := findRef(ctx, c, "ClusterComputeResource", spec.Cluster)
	if err != nil {
		return w, err
	}
	pc := property.DefaultCollector(c)
	var cluster mo.ClusterComputeResource
	if err := pc.RetrieveOne(ctx, clusterRef, []string{"resourcePool", "host"}, &cluster); err != nil {
		return w, err
	}
	if cluster.ResourcePool == nil {
		return w, fmt.Errorf("cluster %q has no resource pool", spec.Cluster)
	}
	if w.datastore, err = findRef(ctx, c, "Datastore", spec.Datastore); err != nil {
		return w, err
	}
	var ds mo.Datastore
	if err := pc.RetrieveOne(ctx, w.datastore, []string{"host"}, &ds); err != nil {
		return w, err
	}
	var eligible []types.ManagedObjectReference
	for _, h := range cluster.Host {
		for _, m := range ds.Host {
			if m.Key == h {
				var hm mo.HostSystem
				if err := pc.RetrieveOne(ctx, h, []string{"runtime.connectionState", "runtime.inMaintenanceMode"}, &hm); err == nil &&
					hm.Runtime.ConnectionState == types.HostSystemConnectionStateConnected && !hm.Runtime.InMaintenanceMode {
					eligible = append(eligible, h)
				}
				break
			}
		}
	}
	if len(eligible) == 0 {
		return w, fmt.Errorf("no connected host in %s mounts datastore %s: %w", spec.Cluster, spec.Datastore, ErrNotFound)
	}
	w.pool, w.host = *cluster.ResourcePool, eligible[spec.HostIndex%len(eligible)]
	w.folder, err = ensureFolder(ctx, c, spec.Folder)
	return w, err
}

// Extra config keeping how many disks and adapters the VM had before rvav
// added its own (ADR-0024).
const baseDisksKey, baseNICsKey = "rvav.base.disks", "rvav.base.nics"

// finish makes a VM just cloned or deployed, or left by an earlier attempt,
// what the spec says; every part is safe to run again. Some vCenter
// versions (and vcsim) ignore the clone spec's hardware settings, so size
// and extra config are applied here.
func (cl *Client) finish(ctx context.Context, c *vim25.Client, vm *object.VirtualMachine, spec VMSpec, policy string, w placed) error {
	// vcsim deploys a library VM template as a template: make it a VM.
	if tpl, err := vm.IsTemplate(ctx); err != nil {
		return err
	} else if tpl {
		if err := vm.MarkAsVirtualMachine(ctx, *object.NewResourcePool(c, w.pool), object.NewHostSystem(c, w.host)); err != nil {
			return err
		}
	}
	if change, err := firstNIC(ctx, c, vm, spec); err != nil {
		return err
	} else if change != nil {
		if err := vm.EditDevice(ctx, change.Device); err != nil {
			return fmt.Errorf("connect to %s: %w", spec.Network, err)
		}
	}
	// What the template gave is counted once, before anything is added, and
	// kept on the VM: a retry adds only what is still missing.
	disks, nics, known, err := baseDevices(ctx, vm)
	if err != nil {
		return err
	}
	extra := map[string]string{}
	for k, v := range spec.ExtraConfig {
		extra[k] = v
	}
	if !known {
		extra[baseDisksKey], extra[baseNICsKey] = strconv.Itoa(disks), strconv.Itoa(nics)
	}
	s := spec
	s.ExtraConfig = extra
	if err := reconfigure(ctx, vm, s, policy); err != nil {
		return fmt.Errorf("reconfigure: %w", err)
	}
	if err := cl.addHardware(ctx, vm, spec, disks, nics); err != nil {
		return fmt.Errorf("add disks and adapters: %w", err)
	}
	return nil
}

// baseDevices are how many disks and adapters the VM had before rvav added
// its own: as kept on the VM (known), or as it has now.
func baseDevices(ctx context.Context, vm *object.VirtualMachine) (disks, nics int, known bool, err error) {
	var m mo.VirtualMachine
	if err := vm.Properties(ctx, vm.Reference(), []string{"config.extraConfig", "config.hardware.device"}, &m); err != nil {
		return 0, 0, false, err
	}
	kept := map[string]string{}
	if m.Config != nil {
		for _, o := range m.Config.ExtraConfig {
			if v, ok := o.GetOptionValue().Value.(string); ok {
				kept[o.GetOptionValue().Key] = v
			}
		}
	}
	d, errD := strconv.Atoi(kept[baseDisksKey])
	n, errN := strconv.Atoi(kept[baseNICsKey])
	if errD == nil && errN == nil {
		return d, n, true, nil
	}
	if m.Config != nil {
		devices := object.VirtualDeviceList(m.Config.Hardware.Device)
		disks = len(devices.SelectByType((*types.VirtualDisk)(nil)))
		nics = len(devices.SelectByType((*types.VirtualEthernetCard)(nil)))
	}
	return disks, nics, false, nil
}

// firstNIC is the change connecting the VM's (or template's) first adapter
// to spec.Network (nil without one).
func firstNIC(ctx context.Context, c *vim25.Client, vm *object.VirtualMachine, spec VMSpec) (*types.VirtualDeviceConfigSpec, error) {
	if spec.Network == "" {
		return nil, nil
	}
	devices, err := vm.Device(ctx)
	if err != nil {
		return nil, err
	}
	nics := devices.SelectByType((*types.VirtualEthernetCard)(nil))
	if len(nics) == 0 {
		return nil, fmt.Errorf("template %q has no network adapter", spec.Template)
	}
	// The first is the lowest key: an edited device may move in the list.
	first := slices.MinFunc(nics, func(a, b types.BaseVirtualDevice) int {
		return int(a.GetVirtualDevice().Key - b.GetVirtualDevice().Key)
	})
	net, err := findNetwork(ctx, c, spec.Network)
	if err != nil {
		return nil, err
	}
	card := first.(types.BaseVirtualEthernetCard).GetVirtualEthernetCard()
	if on := backingName(card.Backing); on == spec.Network || on == net.Reference().Value {
		return nil, nil // already there
	}
	backing, err := net.EthernetCardBackingInfo(ctx)
	if err != nil {
		return nil, err
	}
	card.Backing = backing
	return &types.VirtualDeviceConfigSpec{Operation: types.VirtualDeviceConfigSpecOperationEdit, Device: first}, nil
}

// cloneTemplate clones the VM template, its first adapter on the network.
func (cl *Client) cloneTemplate(ctx context.Context, c *vim25.Client, tpl *object.VirtualMachine, spec VMSpec,
	pool, host types.ManagedObjectReference, folder *object.Folder, dsRef types.ManagedObjectReference, policy string) (types.ManagedObjectReference, error) {
	var none types.ManagedObjectReference
	var changes []types.BaseVirtualDeviceConfigSpec
	change, err := firstNIC(ctx, c, tpl, spec)
	if err != nil {
		return none, err
	}
	if change != nil {
		changes = append(changes, change)
	}
	cs := types.VirtualMachineCloneSpec{
		Location: types.VirtualMachineRelocateSpec{
			Pool:         &pool,
			Host:         &host,
			Datastore:    &dsRef,
			DeviceChange: changes,
			Profile:      profile(policy),
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
		return none, err
	}
	info, err := task.WaitForResult(ctx, nil)
	if err != nil {
		return none, fmt.Errorf("clone %s: %w", spec.Name, err)
	}
	ref, ok := info.Result.(types.ManagedObjectReference)
	if !ok {
		return none, fmt.Errorf("clone %s: unexpected task result %T", spec.Name, info.Result)
	}
	return ref, nil
}

// addHardware adds the spec's disks and adapters the VM doesn't have yet,
// counting from the template's (baseDisks, baseNICs): it is safe to run
// again.
func (cl *Client) addHardware(ctx context.Context, vm *object.VirtualMachine, spec VMSpec, baseDisks, baseNICs int) error {
	if len(spec.Disks)+len(spec.NICs) == 0 {
		return nil
	}
	devices, err := vm.Device(ctx)
	if err != nil {
		return err
	}
	var add []types.BaseVirtualDevice
	nics := len(devices.SelectByType((*types.VirtualEthernetCard)(nil)))
	for _, network := range spec.NICs[min(max(nics-baseNICs, 0), len(spec.NICs)):] {
		backing, err := cl.networkBacking(ctx, network)
		if err != nil {
			return err
		}
		nic, err := object.EthernetCardTypes().CreateEthernetCard("vmxnet3", backing)
		if err != nil {
			return err
		}
		add = append(add, nic)
	}
	disks := len(devices.SelectByType((*types.VirtualDisk)(nil)))
	if missing := spec.Disks[min(max(disks-baseDisks, 0), len(spec.Disks)):]; len(missing) > 0 {
		c, err := cl.client(ctx)
		if err != nil {
			return err
		}
		controller, err := devices.FindDiskController("")
		if err != nil {
			return err
		}
		for _, d := range missing {
			var ds types.ManagedObjectReference
			if d.Datastore != "" {
				if ds, err = findRef(ctx, c, "Datastore", d.Datastore); err != nil {
					return err
				}
			}
			disk := devices.CreateDisk(controller, ds, "")
			if d.Datastore != "" {
				// "[datastore]" alone: vCenter names the file, in the VM's
				// folder on that datastore (CreateDisk would make it
				// "[datastore].vmdk", one file for every VM).
				disk.Backing.(*types.VirtualDiskFlatVer2BackingInfo).FileName = "[" + d.Datastore + "]"
			}
			disk.CapacityInKB = int64(d.SizeGB) * 1024 * 1024
			id, err := policyID(ctx, c, cmp.Or(d.StoragePolicy, spec.StoragePolicy))
			if err != nil {
				return err
			}
			// A disk at a time: each one's file is named when it is made.
			if err := vm.AddDeviceWithProfile(ctx, profile(id), disk); err != nil {
				return err
			}
			// The next one takes the next unit number on the controller.
			devices = append(devices, disk)
		}
	}
	if len(add) == 0 {
		return nil
	}
	return vm.AddDevice(ctx, add...)
}

// reconfigure sets CPU, memory, annotation and extra config, grows the
// first disk to spec.DiskGB (disks are never shrunk), and puts the VM's
// home and first disk on the storage policy.
func reconfigure(ctx context.Context, vm *object.VirtualMachine, spec VMSpec, policy string) error {
	cfg := types.VirtualMachineConfigSpec{
		NumCPUs:    int32(spec.VCPU),
		MemoryMB:   int64(spec.MemoryMB),
		Annotation: spec.Annotation,
		VmProfile:  profile(policy),
	}
	for k, v := range spec.ExtraConfig {
		cfg.ExtraConfig = append(cfg.ExtraConfig, &types.OptionValue{Key: k, Value: v})
	}
	if spec.DiskGB > 0 || policy != "" {
		devices, err := vm.Device(ctx)
		if err != nil {
			return err
		}
		if disks := devices.SelectByType((*types.VirtualDisk)(nil)); len(disks) > 0 {
			disk := disks[0].(*types.VirtualDisk)
			want := int64(spec.DiskGB) * 1024 * 1024
			if grow := disk.CapacityInKB < want; grow || policy != "" {
				if grow {
					disk.CapacityInKB = want
					disk.CapacityInBytes = want * 1024
				}
				cfg.DeviceChange = append(cfg.DeviceChange, &types.VirtualDeviceConfigSpec{
					Operation: types.VirtualDeviceConfigSpecOperationEdit,
					Device:    disk,
					Profile:   profile(policy),
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
	if err := property.DefaultCollector(c).RetrieveOne(ctx, vm.Reference(), []string{"config.tools", "config.hardware.device",
		"runtime.powerState"}, &mvm); err != nil {
		return err
	}
	// Every adapter needs its setting: the template's others keep DHCP.
	if mvm.Config != nil {
		n := len(object.VirtualDeviceList(mvm.Config.Hardware.Device).SelectByType((*types.VirtualEthernetCard)(nil)))
		cu.OtherNICs = max(n-1-len(cu.Extra), 0)
	}
	if mvm.Config != nil && mvm.Config.Tools != nil && mvm.Config.Tools.PendingCustomization != "" {
		return nil // already applied by an earlier attempt
	}
	if mvm.Runtime.PowerState == types.VirtualMachinePowerStatePoweredOn {
		return nil // customization already ran on first boot
	}
	var base *types.CustomizationSpec
	if cu.Spec != "" {
		item, err := object.NewCustomizationSpecManager(c).GetCustomizationSpec(ctx, cu.Spec)
		if err != nil {
			if isNotFoundFault(err) || strings.Contains(err.Error(), "NotFound") {
				return fmt.Errorf("customization specification %q: %w", cu.Spec, ErrNotFound)
			}
			return err
		}
		base = &item.Spec
	}
	task, err := vm.Customize(ctx, CustomizationSpec(cu, base))
	if err != nil {
		return err
	}
	return task.Wait(ctx)
}

// CustomizationSpec is the specification applied: base (a vCenter
// specification, or nil) with the host name, adapters, and for Windows the
// domain join and administrator password, set from cu.
func CustomizationSpec(cu Customization, base *types.CustomizationSpec) types.CustomizationSpec {
	var spec types.CustomizationSpec
	if base != nil {
		spec = *base
	}
	spec.GlobalIPSettings.DnsServerList = cu.DNSServers
	if cu.Domain != "" {
		spec.GlobalIPSettings.DnsSuffixList = []string{cu.Domain}
	}
	spec.NicSettingMap = []types.CustomizationAdapterMapping{{
		Adapter: types.CustomizationIPSettings{
			Ip:            &types.CustomizationFixedIp{IpAddress: cu.IP},
			SubnetMask:    prefixToMask(cu.PrefixLength),
			Gateway:       []string{cu.Gateway},
			DnsDomain:     cu.Domain,
			DnsServerList: cu.DNSServers,
		},
	}}
	for range cu.OtherNICs {
		spec.NicSettingMap = append(spec.NicSettingMap, types.CustomizationAdapterMapping{
			Adapter: types.CustomizationIPSettings{Ip: &types.CustomizationDhcpIpGenerator{}}})
	}
	for _, n := range cu.Extra {
		a := types.CustomizationIPSettings{Ip: &types.CustomizationDhcpIpGenerator{}}
		if !n.DHCP {
			a = types.CustomizationIPSettings{Ip: &types.CustomizationFixedIp{IpAddress: n.IP},
				SubnetMask: prefixToMask(n.PrefixLength), DnsServerList: n.DNSServers}
		}
		spec.NicSettingMap = append(spec.NicSettingMap, types.CustomizationAdapterMapping{Adapter: a})
	}
	if cu.OSFamily == "windows" {
		sp, ok := spec.Identity.(*types.CustomizationSysprep)
		if !ok {
			sp = &types.CustomizationSysprep{
				UserData:       types.CustomizationUserData{FullName: "rvav", OrgName: "rvav"},
				GuiUnattended:  types.CustomizationGuiUnattended{TimeZone: 110},
				Identification: types.CustomizationIdentification{JoinWorkgroup: "WORKGROUP"},
			}
		} else {
			copied := *sp
			sp = &copied
		}
		sp.UserData.ComputerName = &types.CustomizationFixedName{Name: cu.Hostname}
		if tz, err := strconv.Atoi(cu.TimeZone); err == nil {
			sp.GuiUnattended.TimeZone = int32(tz)
		}
		if cu.AdminPassword != "" {
			sp.GuiUnattended.Password = &types.CustomizationPassword{Value: cu.AdminPassword, PlainText: true}
		}
		if cu.JoinDomain != "" {
			sp.Identification = types.CustomizationIdentification{JoinDomain: cu.JoinDomain, DomainAdmin: cu.DomainUser,
				DomainAdminPassword: &types.CustomizationPassword{Value: cu.DomainPassword, PlainText: true}, DomainOU: cu.DomainOU}
		}
		spec.Identity = sp
		return spec
	}
	lp, ok := spec.Identity.(*types.CustomizationLinuxPrep)
	if !ok {
		lp = &types.CustomizationLinuxPrep{TimeZone: orDefault(cu.TimeZone, "Europe/Zurich"), HwClockUTC: types.NewBool(true)}
	} else {
		copied := *lp
		lp = &copied
		if cu.TimeZone != "" {
			lp.TimeZone = cu.TimeZone
		}
	}
	lp.HostName = &types.CustomizationFixedName{Name: cu.Hostname}
	if cu.Domain != "" {
		lp.Domain = cu.Domain
	}
	spec.Identity = lp
	return spec
}

// SetGuestInfo writes guestinfo keys (cloud-init's VMware data source
// reads its metadata and user data there; ADR-0024).
func (cl *Client) SetGuestInfo(ctx context.Context, moref string, values map[string]string) error {
	c, err := cl.client(ctx)
	if err != nil {
		return err
	}
	var cfg types.VirtualMachineConfigSpec
	for _, k := range slices.Sorted(maps.Keys(values)) {
		cfg.ExtraConfig = append(cfg.ExtraConfig, &types.OptionValue{Key: k, Value: values[k]})
	}
	task, err := object.NewVirtualMachine(c, vmRef(moref)).Reconfigure(ctx, cfg)
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
