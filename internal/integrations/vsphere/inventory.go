package vsphere

import (
	"context"
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
}

type Datastore struct {
	Moref      string
	Name       string
	CapacityGB int64
	FreeGB     int64
	Clusters   []string
}

type Network struct {
	Moref    string
	Name     string
	Kind     string
	Clusters []string
}

type Template struct {
	Moref   string
	Name    string
	GuestOS string
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
	if err := retrieve([]string{"HostSystem"}, []string{"name", "parent", "summary.hardware", "summary.quickStats"}, &hosts); err != nil {
		return nil, err
	}
	var clusters []mo.ClusterComputeResource
	if err := retrieve([]string{"ClusterComputeResource"}, []string{"name", "parent", "host"}, &clusters); err != nil {
		return nil, err
	}
	var datacenters []mo.Datacenter
	if err := retrieve([]string{"Datacenter"}, []string{"name", "hostFolder"}, &datacenters); err != nil {
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
		cluster.CPUTotalMHz += int64(h.Summary.Hardware.CpuMhz) * int64(h.Summary.Hardware.NumCpuCores)
		cluster.MemoryTotalMB += h.Summary.Hardware.MemorySize / (1024 * 1024)
		cluster.CPUUsedMHz += int64(h.Summary.QuickStats.OverallCpuUsage)
		cluster.MemoryUsedMB += int64(h.Summary.QuickStats.OverallMemoryUsage)
	}
	for _, d := range datastores {
		ds := Datastore{Moref: d.Self.Value, Name: d.Name,
			CapacityGB: d.Summary.Capacity / (1 << 30), FreeGB: d.Summary.FreeSpace / (1 << 30)}
		for _, mount := range d.Host {
			ds.Clusters = appendUnique(ds.Clusters, hostCluster[mount.Key])
		}
		inv.Datastores = append(inv.Datastores, ds)
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
	_ = datacenters
	return inv, nil
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
