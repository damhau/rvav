package vsphere

import (
	"context"
	"slices"
	"strings"

	"github.com/vmware/govmomi/property"
	"github.com/vmware/govmomi/view"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"
)

// Inventory is a snapshot of what forms, placement and sync need.
type Inventory struct {
	Clusters   []Cluster
	Datastores []Datastore
	Networks   []Network
	Templates  []Template
	VMs        []InventoryVM
	// Hosts, ResourcePools and DatastoreClusters are what placement rules
	// may name besides clusters and datastores (ADR-0036).
	Hosts             []Host
	ResourcePools     []ResourcePool
	DatastoreClusters []DatastoreCluster
	// Folders are the VM folders, for forms to offer (vsphere.folders).
	Folders []Folder
	// TagsError says why clusters, datastores and networks have no tags:
	// the tagging service couldn't be read. The rest of the inventory is
	// fine.
	TagsError error
	// StoragePolicies are the vCenter's storage policies (ADR-0024).
	StoragePolicies []StoragePolicy
	// LibrariesError and PoliciesError say why content library items or
	// storage policies are missing: they couldn't be read.
	LibrariesError, PoliciesError error
}

type Cluster struct {
	Moref         string
	Name          string
	Datacenter    string
	HostCount     int
	CPUTotalMHz   int64
	CPUUsedMHz    int64
	MemoryTotalMB int64
	MemoryUsedMB  int64
	Tags          []string // category/tag
}

type Datastore struct {
	Moref      string
	Name       string
	CapacityGB int64
	FreeGB     int64
	Clusters   []string
	Tags       []string
}

type Network struct {
	Moref    string
	Name     string
	Kind     string
	Clusters []string
	Tags     []string
}

// Host is a cluster's ESXi host.
type Host struct {
	Moref         string
	Name          string
	Cluster       string
	State         string // HostConnected, HostMaintenance or HostDisconnected
	CPUTotalMHz   int64
	CPUUsedMHz    int64
	MemoryTotalMB int64
	MemoryUsedMB  int64
	Tags          []string
}

const (
	HostConnected    = "connected"
	HostMaintenance  = "maintenance"
	HostDisconnected = "disconnected"
)

// ResourcePool is a cluster's resource pool below its root one; Path is
// where it is under the root, such as rvav/dev. A limit of -1 is none.
type ResourcePool struct {
	Moref         string
	Name          string
	Cluster       string
	Path          string
	CPULimitMHz   int64
	MemoryLimitMB int64
	Tags          []string
}

// Folder is a folder VMs can be in: Path is from the root, such as
// /GVA/vm/rvav/web, and Datacenter the one it is in.
type Folder struct {
	Moref      string
	Path       string
	Datacenter string
}

// DatastoreCluster is a storage pod: its member datastores, and whether
// Storage DRS is on.
type DatastoreCluster struct {
	Moref      string
	Name       string
	CapacityGB int64
	FreeGB     int64
	Datastores []string
	SDRS       bool
	Tags       []string
}

type Template struct {
	Moref   string
	Name    string
	GuestOS string
	// Library is a content library item's library; Moref is its ID then.
	Library string
}

type InventoryVM struct {
	VM
	CPUPct        *float32
	MemoryPct     *float32
	SnapshotCount int
}

// Inventory reads clusters, datastores, networks, templates and VMs with a
// few property collector calls.
func (cl *Client) Inventory(ctx context.Context) (*Inventory, error) {
	c, err := cl.client(ctx)
	if err != nil {
		return nil, err
	}
	m := view.NewManager(c)
	root := c.ServiceContent.RootFolder
	retrieve := func(kinds []string, props []string, dst any) error {
		v, err := m.CreateContainerView(ctx, root, kinds, true)
		if err != nil {
			return err
		}
		defer v.Destroy(context.Background())
		return v.Retrieve(ctx, kinds, props, dst)
	}

	var hosts []mo.HostSystem
	if err := retrieve([]string{"HostSystem"}, []string{"name", "parent", "summary.hardware", "summary.quickStats",
		"runtime.connectionState", "runtime.inMaintenanceMode"}, &hosts); err != nil {
		return nil, err
	}
	var clusters []mo.ClusterComputeResource
	if err := retrieve([]string{"ClusterComputeResource"}, []string{"name", "parent", "host"}, &clusters); err != nil {
		return nil, err
	}
	var datacenters []mo.Datacenter
	if err := retrieve([]string{"Datacenter"}, []string{"name", "parent"}, &datacenters); err != nil {
		return nil, err
	}
	var folders []mo.Folder
	if err := retrieve([]string{"Folder"}, []string{"name", "parent", "childType"}, &folders); err != nil {
		return nil, err
	}
	var datastores []mo.Datastore
	if err := retrieve([]string{"Datastore"}, []string{"name", "summary", "host"}, &datastores); err != nil {
		return nil, err
	}
	var networks []mo.Network
	if err := retrieve([]string{"Network"}, []string{"name", "host"}, &networks); err != nil {
		return nil, err
	}
	var pools []mo.ResourcePool
	if err := retrieve([]string{"ResourcePool"}, []string{"name", "parent", "owner", "config"}, &pools); err != nil {
		return nil, err
	}
	var pods []mo.StoragePod
	if err := retrieve([]string{"StoragePod"}, []string{"name", "summary", "childEntity", "podStorageDrsEntry"}, &pods); err != nil {
		return nil, err
	}
	var vms []mo.VirtualMachine
	if err := retrieve([]string{"VirtualMachine"}, []string{"name", "config", "runtime.powerState", "runtime.host",
		"runtime.maxCpuUsage", "guest", "summary.quickStats", "network", "snapshot"}, &vms); err != nil {
		return nil, err
	}

	pc := property.DefaultCollector(c)
	hostCluster := map[types.ManagedObjectReference]string{}
	hostName := map[types.ManagedObjectReference]string{}
	clusterByRef := map[types.ManagedObjectReference]*Cluster{}
	inv := &Inventory{}
	for _, cc := range clusters {
		cluster := Cluster{Moref: cc.Self.Value, Name: cc.Name, HostCount: len(cc.Host), Datacenter: datacenterOf(ctx, pc, cc.Parent)}
		inv.Clusters = append(inv.Clusters, cluster)
		for _, h := range cc.Host {
			hostCluster[h] = cc.Name
		}
	}
	for i := range inv.Clusters {
		clusterByRef[types.ManagedObjectReference{Type: "ClusterComputeResource", Value: inv.Clusters[i].Moref}] = &inv.Clusters[i]
	}
	for _, h := range hosts {
		hostName[h.Self] = h.Name
		if h.Parent == nil {
			continue
		}
		cluster := clusterByRef[*h.Parent]
		if cluster == nil || h.Summary.Hardware == nil {
			continue
		}
		host := Host{Moref: h.Self.Value, Name: h.Name, Cluster: cluster.Name, State: HostConnected,
			CPUTotalMHz:   int64(h.Summary.Hardware.CpuMhz) * int64(h.Summary.Hardware.NumCpuCores),
			MemoryTotalMB: h.Summary.Hardware.MemorySize / (1024 * 1024),
			CPUUsedMHz:    int64(h.Summary.QuickStats.OverallCpuUsage),
			MemoryUsedMB:  int64(h.Summary.QuickStats.OverallMemoryUsage)}
		switch {
		case h.Runtime.ConnectionState != "" && h.Runtime.ConnectionState != types.HostSystemConnectionStateConnected:
			host.State = HostDisconnected
		case h.Runtime.InMaintenanceMode:
			host.State = HostMaintenance
		}
		inv.Hosts = append(inv.Hosts, host)
		cluster.CPUTotalMHz += host.CPUTotalMHz
		cluster.MemoryTotalMB += host.MemoryTotalMB
		cluster.CPUUsedMHz += host.CPUUsedMHz
		cluster.MemoryUsedMB += host.MemoryUsedMB
	}
	inv.ResourcePools = resourcePools(pools, clusterByRef)
	for _, d := range datastores {
		ds := Datastore{Moref: d.Self.Value, Name: d.Name,
			CapacityGB: d.Summary.Capacity / (1 << 30), FreeGB: d.Summary.FreeSpace / (1 << 30)}
		for _, mount := range d.Host {
			ds.Clusters = appendUnique(ds.Clusters, hostCluster[mount.Key])
		}
		inv.Datastores = append(inv.Datastores, ds)
	}
	dsName := map[types.ManagedObjectReference]string{}
	for _, d := range datastores {
		dsName[d.Self] = d.Name
	}
	for _, p := range pods {
		dc := DatastoreCluster{Moref: p.Self.Value, Name: p.Name}
		if p.Summary != nil {
			dc.CapacityGB, dc.FreeGB = p.Summary.Capacity/(1<<30), p.Summary.FreeSpace/(1<<30)
		}
		for _, ref := range p.ChildEntity {
			if name, ok := dsName[ref]; ok {
				dc.Datastores = append(dc.Datastores, name)
			}
		}
		if e := p.PodStorageDrsEntry; e != nil {
			dc.SDRS = e.StorageDrsConfig.PodConfig.Enabled
		}
		inv.DatastoreClusters = append(inv.DatastoreClusters, dc)
	}
	for _, n := range networks {
		nw := Network{Moref: n.Self.Value, Name: n.Name, Kind: n.Self.Type}
		for _, h := range n.Host {
			nw.Clusters = appendUnique(nw.Clusters, hostCluster[h])
		}
		inv.Networks = append(inv.Networks, nw)
	}
	netName := map[string]string{}
	for _, n := range networks {
		netName[n.Self.Value] = n.Name
	}
	for _, v := range vms {
		vm := toVM(v)
		for i := range vm.NICs {
			if name, ok := netName[vm.NICs[i].Network]; ok {
				vm.NICs[i].Network = name
			}
		}
		if vm.Template {
			inv.Templates = append(inv.Templates, Template{Moref: vm.Moref, Name: vm.Name, GuestOS: vm.GuestOS})
			continue
		}
		iv := InventoryVM{VM: vm}
		if v.Runtime.Host != nil {
			iv.Host = hostName[*v.Runtime.Host]
			iv.Cluster = hostCluster[*v.Runtime.Host]
		}
		if vm.PowerState == "on" {
			qs := v.Summary.QuickStats
			if v.Runtime.MaxCpuUsage > 0 {
				p := float32(qs.OverallCpuUsage) / float32(v.Runtime.MaxCpuUsage) * 100
				iv.CPUPct = &p
			}
			if vm.MemoryMB > 0 {
				p := float32(qs.GuestMemoryUsage) / float32(vm.MemoryMB) * 100
				iv.MemoryPct = &p
			}
		}
		if v.Snapshot != nil {
			iv.SnapshotCount = countSnapshots(v.Snapshot.RootSnapshotList)
		}
		inv.VMs = append(inv.VMs, iv)
	}
	inv.Folders = vmFolders(folders, datacenters)
	inv.TagsError = cl.readTags(ctx, c, inv)
	inv.LibrariesError = cl.readLibraries(ctx, c, inv)
	inv.PoliciesError = readStoragePolicies(ctx, c, inv)
	return inv, nil
}

// vmFolders are the folders that hold VMs, with their paths worked out from
// what was read rather than one call per folder.
func vmFolders(folders []mo.Folder, datacenters []mo.Datacenter) []Folder {
	type node struct {
		name, kind string
		parent     *types.ManagedObjectReference
	}
	nodes := map[types.ManagedObjectReference]node{}
	for _, f := range folders {
		nodes[f.Self] = node{name: f.Name, kind: "Folder", parent: f.Parent}
	}
	for _, d := range datacenters {
		nodes[d.Self] = node{name: d.Name, kind: "Datacenter", parent: d.Parent}
	}
	var out []Folder
	for _, f := range folders {
		if !slices.Contains(f.ChildType, "VirtualMachine") {
			continue
		}
		var parts []string
		dc := ""
		for ref, i := f.Self, 0; i < 30; i++ {
			n, ok := nodes[ref]
			if !ok || n.parent == nil {
				break // the root folder has no name in paths
			}
			if n.kind == "Datacenter" && dc == "" {
				dc = n.name
			}
			parts = append([]string{n.name}, parts...)
			ref = *n.parent
		}
		if dc != "" {
			out = append(out, Folder{Moref: f.Self.Value, Path: "/" + strings.Join(parts, "/"), Datacenter: dc})
		}
	}
	slices.SortFunc(out, func(a, b Folder) int { return strings.Compare(a.Path, b.Path) })
	return out
}

// resourcePools are the clusters' pools below their root ones, vApps left
// out, each with its path under the root.
func resourcePools(pools []mo.ResourcePool, clusters map[types.ManagedObjectReference]*Cluster) []ResourcePool {
	byRef := map[types.ManagedObjectReference]*mo.ResourcePool{}
	for i := range pools {
		byRef[pools[i].Self] = &pools[i]
	}
	var out []ResourcePool
	for _, p := range pools {
		cluster := clusters[p.Owner]
		if p.Self.Type != "ResourcePool" || cluster == nil || p.Parent == nil || p.Parent.Type != "ResourcePool" {
			continue
		}
		parts := []string{p.Name}
		for up := byRef[*p.Parent]; up != nil && up.Parent != nil && up.Parent.Type == "ResourcePool"; up = byRef[*up.Parent] {
			parts = append([]string{up.Name}, parts...)
		}
		rp := ResourcePool{Moref: p.Self.Value, Name: p.Name, Cluster: cluster.Name, Path: strings.Join(parts, "/"),
			CPULimitMHz: -1, MemoryLimitMB: -1}
		if l := p.Config.CpuAllocation.Limit; l != nil {
			rp.CPULimitMHz = *l
		}
		if l := p.Config.MemoryAllocation.Limit; l != nil {
			rp.MemoryLimitMB = *l
		}
		out = append(out, rp)
	}
	return out
}

func countSnapshots(list []types.VirtualMachineSnapshotTree) int {
	n := 0
	for _, s := range list {
		n += 1 + countSnapshots(s.ChildSnapshotList)
	}
	return n
}

func appendUnique(list []string, s string) []string {
	if s == "" {
		return list
	}
	for _, x := range list {
		if x == s {
			return list
		}
	}
	return append(list, s)
}

// datacenterOf walks up from ref to the enclosing datacenter's name.
func datacenterOf(ctx context.Context, pc *property.Collector, ref *types.ManagedObjectReference) string {
	for i := 0; ref != nil && i < 20; i++ {
		var e mo.ManagedEntity
		if err := pc.RetrieveOne(ctx, *ref, []string{"name", "parent"}, &e); err != nil {
			return ""
		}
		if ref.Type == "Datacenter" {
			return e.Name
		}
		ref = e.Parent
	}
	return ""
}

// inventoryPath returns the path of a folder such as /GVA/vm/rvav/web/dev.
func inventoryPath(ctx context.Context, pc *property.Collector, ref types.ManagedObjectReference) string {
	var parts []string
	cur := &ref
	for i := 0; cur != nil && i < 30; i++ {
		var e mo.ManagedEntity
		if err := pc.RetrieveOne(ctx, *cur, []string{"name", "parent"}, &e); err != nil {
			break
		}
		if e.Parent == nil {
			break // the root folder has no name in paths
		}
		parts = append([]string{e.Name}, parts...)
		cur = e.Parent
	}
	return "/" + strings.Join(parts, "/")
}
