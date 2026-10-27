package vsphere

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/vmware/govmomi/find"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/property"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"
)

// Layout is a vCenter inventory to create in vcsim for local development.
// It mirrors the seed data, so placement rules and forms show real names.
type Layout struct {
	Datacenter string
	Clusters   []ClusterSpec
	Switch     string
	Portgroups []string
	Folders    []string
	Templates  []TemplateSpec
}

type ClusterSpec struct {
	Name       string
	HostPrefix string
	Hosts      int
	Datastores []string
}

type TemplateSpec struct {
	Name      string
	GuestID   string
	Cluster   string
	Datastore string
	Network   string
	Folder    string
	VCPU      int
	MemoryMB  int
	DiskGB    int
}

// DefaultLayout is the Geneva layout used by the seed data and the mockup.
func DefaultLayout() Layout {
	return Layout{
		Datacenter: "GVA",
		Clusters: []ClusterSpec{
			{Name: "GVA-DEV-CL01", HostPrefix: "esx-gva-dev", Hosts: 2, Datastores: []string{"vsan-gva-dev-cl01", "nfs-gva-bronze-01"}},
			{Name: "GVA-TST-CL01", HostPrefix: "esx-gva-tst", Hosts: 2, Datastores: []string{"vsan-gva-tst-cl01"}},
			{Name: "GVA-PRD-CL01", HostPrefix: "esx-gva-prd1", Hosts: 3, Datastores: []string{"vsan-gva-prd-cl01", "nfs-gva-silver-02"}},
			{Name: "GVA-PRD-CL02", HostPrefix: "esx-gva-prd2", Hosts: 2, Datastores: []string{"vsan-gva-prd-cl02"}},
		},
		Switch: "GVA-DVS01",
		Portgroups: []string{
			"dvpg-app-dev-vl310", "dvpg-db-dev-vl320", "dvpg-app-tst-vl410",
			"dvpg-web-prd-vl210", "dvpg-app-prd-vl220", "dvpg-db-prd-vl230",
		},
		Folders: []string{
			"/GVA/vm/templates",
			"/GVA/vm/rvav/web/dev", "/GVA/vm/rvav/web/tst", "/GVA/vm/rvav/web/prd",
		},
		Templates: []TemplateSpec{
			{Name: "tpl-rhel9.4-2026.10", GuestID: "rhel9_64Guest", Cluster: "GVA-DEV-CL01", Datastore: "vsan-gva-dev-cl01",
				Network: "dvpg-app-dev-vl310", Folder: "/GVA/vm/templates", VCPU: 2, MemoryMB: 4096, DiskGB: 40},
			{Name: "tpl-ubuntu24.04-2026.10", GuestID: "ubuntu64Guest", Cluster: "GVA-DEV-CL01", Datastore: "vsan-gva-dev-cl01",
				Network: "dvpg-app-dev-vl310", Folder: "/GVA/vm/templates", VCPU: 2, MemoryMB: 4096, DiskGB: 40},
			{Name: "tpl-win2025-std-2026.09", GuestID: "windows2019srvNext_64Guest", Cluster: "GVA-DEV-CL01", Datastore: "vsan-gva-dev-cl01",
				Network: "dvpg-app-dev-vl310", Folder: "/GVA/vm/templates", VCPU: 2, MemoryMB: 8192, DiskGB: 80},
		},
	}
}

// Bootstrap creates the layout. It is safe to run again: existing objects are kept.
func Bootstrap(ctx context.Context, c *vim25.Client, l Layout) error {
	finder := find.NewFinder(c, false)
	root := object.NewRootFolder(c)

	dc, err := finder.Datacenter(ctx, "/"+l.Datacenter)
	if err != nil {
		if dc, err = root.CreateDatacenter(ctx, l.Datacenter); err != nil {
			return fmt.Errorf("create datacenter: %w", err)
		}
	}
	folders, err := dc.Folders(ctx)
	if err != nil {
		return err
	}
	scratch, err := scratchDatastore(ctx, c, l)
	if err != nil {
		return err
	}
	scratchDC, err := finder.Datacenter(ctx, "/"+datacenterOf(ctx, property.DefaultCollector(c), scratch.Parent))
	if err != nil {
		return fmt.Errorf("datacenter of %s: %w", scratch.Name, err)
	}

	var allHosts []types.ManagedObjectReference
	for _, cs := range l.Clusters {
		cluster, err := ensureCluster(ctx, finder, folders.HostFolder, l.Datacenter, cs.Name)
		if err != nil {
			return err
		}
		hosts, err := ensureHosts(ctx, c, cluster, cs)
		if err != nil {
			return err
		}
		allHosts = append(allHosts, hosts...)
		for _, ds := range cs.Datastores {
			if err := ensureDatastore(ctx, c, scratchDC, scratch, hosts, ds); err != nil {
				return err
			}
		}
	}

	if err := ensurePortgroups(ctx, c, folders.NetworkFolder, l.Switch, allHosts, l.Portgroups); err != nil {
		return err
	}
	for _, f := range l.Folders {
		if _, err := ensureFolder(ctx, c, f); err != nil {
			return err
		}
	}
	for _, t := range l.Templates {
		if err := ensureTemplate(ctx, c, t); err != nil {
			return fmt.Errorf("template %s: %w", t.Name, err)
		}
	}
	return nil
}

// scratchDatastore finds a datastore outside the layout. vcsim backs new
// datastores with directories, which are created on this one.
func scratchDatastore(ctx context.Context, c *vim25.Client, l Layout) (*mo.Datastore, error) {
	ours := map[string]bool{}
	for _, cs := range l.Clusters {
		for _, d := range cs.Datastores {
			ours[d] = true
		}
	}
	finder := find.NewFinder(c, false)
	list, err := finder.DatastoreList(ctx, "/*/datastore/*")
	if err != nil {
		return nil, fmt.Errorf("vcsim needs an existing datastore to hold new ones: %w", err)
	}
	for _, ds := range list {
		if ours[ds.Name()] {
			continue
		}
		var m mo.Datastore
		if err := ds.Properties(ctx, ds.Reference(), []string{"name", "info", "parent"}, &m); err != nil {
			return nil, err
		}
		return &m, nil
	}
	return nil, errors.New("no scratch datastore found")
}

func ensureCluster(ctx context.Context, finder *find.Finder, hostFolder *object.Folder, dc, name string) (*object.ClusterComputeResource, error) {
	if cl, err := finder.ClusterComputeResource(ctx, path.Join("/", dc, "host", name)); err == nil {
		return cl, nil
	}
	return hostFolder.CreateCluster(ctx, name, types.ClusterConfigSpecEx{})
}

func ensureHosts(ctx context.Context, c *vim25.Client, cluster *object.ClusterComputeResource, cs ClusterSpec) ([]types.ManagedObjectReference, error) {
	var m mo.ClusterComputeResource
	if err := cluster.Properties(ctx, cluster.Reference(), []string{"host"}, &m); err != nil {
		return nil, err
	}
	hosts := m.Host
	for i := len(hosts) + 1; i <= cs.Hosts; i++ {
		name := fmt.Sprintf("%s-%02d.corp.example", cs.HostPrefix, i)
		task, err := cluster.AddHost(ctx, types.HostConnectSpec{HostName: name, UserName: "root", Password: "vcsim", Force: true}, true, nil, nil)
		if err != nil {
			return nil, err
		}
		info, err := task.WaitForResult(ctx, nil)
		if err != nil {
			return nil, fmt.Errorf("add host %s: %w", name, err)
		}
		hosts = append(hosts, info.Result.(types.ManagedObjectReference))
	}
	return hosts, nil
}

func ensureDatastore(ctx context.Context, c *vim25.Client, dc *object.Datacenter, scratch *mo.Datastore,
	hosts []types.ManagedObjectReference, name string) error {
	info := scratch.Info.GetDatastoreInfo()
	local := strings.TrimSuffix(info.Url, "/") + "/rvav-ds/" + name
	fm := object.NewFileManager(c)
	if err := fm.MakeDirectory(ctx, fmt.Sprintf("[%s] rvav-ds/%s", scratch.Name, name), dc, true); err != nil &&
		!strings.Contains(err.Error(), "already exists") {
		return fmt.Errorf("datastore directory %s: %w", name, err)
	}
	pc := property.DefaultCollector(c)
	for _, h := range hosts {
		host := object.NewHostSystem(c, h)
		var hm mo.HostSystem
		if err := pc.RetrieveOne(ctx, h, []string{"datastore"}, &hm); err != nil {
			return err
		}
		mounted := false
		for _, ref := range hm.Datastore {
			var d mo.Datastore
			if err := pc.RetrieveOne(ctx, ref, []string{"name"}, &d); err == nil && d.Name == name {
				mounted = true
			}
		}
		if mounted {
			continue
		}
		dss, err := host.ConfigManager().DatastoreSystem(ctx)
		if err != nil {
			return err
		}
		_, err = dss.CreateNasDatastore(ctx, types.HostNasVolumeSpec{
			RemoteHost: "nas.corp.example",
			RemotePath: "/vol/" + name,
			LocalPath:  local,
			AccessMode: string(types.HostMountModeReadWrite),
			Type:       "NFS",
		})
		if err != nil {
			return fmt.Errorf("mount %s: %w", name, err)
		}
	}
	return nil
}

// ensurePortgroups creates the switch and its port groups, then joins the
// hosts, which also makes every port group available on them.
func ensurePortgroups(ctx context.Context, c *vim25.Client, netFolder *object.Folder, switchName string,
	hosts []types.ManagedObjectReference, portgroups []string) error {
	ref, err := findRef(ctx, c, "DistributedVirtualSwitch", switchName)
	if errors.Is(err, ErrNotFound) {
		task, err := netFolder.CreateDVS(ctx, types.DVSCreateSpec{
			ConfigSpec: &types.VMwareDVSConfigSpec{DVSConfigSpec: types.DVSConfigSpec{Name: switchName}},
		})
		if err != nil {
			return err
		}
		info, err := task.WaitForResult(ctx, nil)
		if err != nil {
			return fmt.Errorf("create switch: %w", err)
		}
		ref = info.Result.(types.ManagedObjectReference)
	} else if err != nil {
		return err
	}
	dvs := object.NewDistributedVirtualSwitch(c, ref)

	var specs []types.DVPortgroupConfigSpec
	for _, pg := range portgroups {
		if _, err := findRef(ctx, c, "DistributedVirtualPortgroup", pg); err == nil {
			continue
		}
		specs = append(specs, types.DVPortgroupConfigSpec{Name: pg, Type: string(types.DistributedVirtualPortgroupPortgroupTypeEarlyBinding), NumPorts: 128})
	}
	if len(specs) > 0 {
		task, err := dvs.AddPortgroup(ctx, specs)
		if err != nil {
			return err
		}
		if err := task.Wait(ctx); err != nil {
			return err
		}
	}

	var m mo.DistributedVirtualSwitch
	if err := dvs.Properties(ctx, ref, []string{"summary", "config"}, &m); err != nil {
		return err
	}
	member := map[types.ManagedObjectReference]bool{}
	for _, h := range m.Summary.HostMember {
		member[h] = true
	}
	var add []types.DistributedVirtualSwitchHostMemberConfigSpec
	for _, h := range hosts {
		if !member[h] {
			add = append(add, types.DistributedVirtualSwitchHostMemberConfigSpec{Operation: string(types.ConfigSpecOperationAdd), Host: h})
		}
	}
	if len(add) == 0 {
		return nil
	}
	task, err := dvs.Reconfigure(ctx, &types.DVSConfigSpec{ConfigVersion: m.Config.GetDVSConfigInfo().ConfigVersion, Host: add})
	if err != nil {
		return err
	}
	return task.Wait(ctx)
}

func ensureTemplate(ctx context.Context, c *vim25.Client, t TemplateSpec) error {
	if _, err := findRef(ctx, c, "VirtualMachine", t.Name); err == nil {
		return nil
	}
	clusterRef, err := findRef(ctx, c, "ClusterComputeResource", t.Cluster)
	if err != nil {
		return err
	}
	var cluster mo.ClusterComputeResource
	if err := property.DefaultCollector(c).RetrieveOne(ctx, clusterRef, []string{"resourcePool", "host"}, &cluster); err != nil {
		return err
	}
	folder, err := ensureFolder(ctx, c, t.Folder)
	if err != nil {
		return err
	}
	net, err := findNetwork(ctx, c, t.Network)
	if err != nil {
		return err
	}
	backing, err := net.EthernetCardBackingInfo(ctx)
	if err != nil {
		return err
	}
	devices := object.VirtualDeviceList{}
	scsi, err := devices.CreateSCSIController("pvscsi")
	if err != nil {
		return err
	}
	devices = append(devices, scsi)
	controller, err := devices.FindSCSIController("")
	if err != nil {
		return err
	}
	disk := devices.CreateDisk(controller, types.ManagedObjectReference{}, fmt.Sprintf("[%s] %s/%s.vmdk", t.Datastore, t.Name, t.Name))
	disk.CapacityInKB = int64(t.DiskGB) * 1024 * 1024
	nic, err := object.EthernetCardTypes().CreateEthernetCard("vmxnet3", backing)
	if err != nil {
		return err
	}

	spec := types.VirtualMachineConfigSpec{
		Name:     t.Name,
		GuestId:  t.GuestID,
		NumCPUs:  int32(t.VCPU),
		MemoryMB: int64(t.MemoryMB),
		Files:    &types.VirtualMachineFileInfo{VmPathName: fmt.Sprintf("[%s]", t.Datastore)},
		DeviceChange: []types.BaseVirtualDeviceConfigSpec{
			&types.VirtualDeviceConfigSpec{Operation: types.VirtualDeviceConfigSpecOperationAdd, Device: scsi},
			&types.VirtualDeviceConfigSpec{Operation: types.VirtualDeviceConfigSpecOperationAdd,
				FileOperation: types.VirtualDeviceConfigSpecFileOperationCreate, Device: disk},
			&types.VirtualDeviceConfigSpec{Operation: types.VirtualDeviceConfigSpecOperationAdd, Device: nic},
		},
	}
	var host *object.HostSystem
	if len(cluster.Host) > 0 {
		host = object.NewHostSystem(c, cluster.Host[0])
	}
	task, err := folder.CreateVM(ctx, spec, object.NewResourcePool(c, *cluster.ResourcePool), host)
	if err != nil {
		return err
	}
	info, err := task.WaitForResult(ctx, nil)
	if err != nil {
		return err
	}
	vm := object.NewVirtualMachine(c, info.Result.(types.ManagedObjectReference))
	return vm.MarkAsTemplate(ctx)
}
