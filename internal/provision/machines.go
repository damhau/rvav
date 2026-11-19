package provision

import (
	"cmp"
	"context"
	"encoding/json"

	"go.temporal.io/sdk/workflow"

	"github.com/damhau/rvav/internal/blueprint"
	"github.com/damhau/rvav/internal/outbox"
	"github.com/damhau/rvav/internal/present"
	"github.com/damhau/rvav/internal/store"
)

// A VM's own events (ADR-0049): compute.allocation.pre and network.configure
// before anything of it is built, compute.provision.pre as it is about to
// be cloned, compute.provision.post once it is on, and compute.removal.pre
// and .post around its deletion.

// MachineEventsInput names the VMs a topic's events are written for.
type MachineEventsInput struct {
	RequestID string   `json:"request_id"`
	Topic     string   `json:"topic"`
	VMs       []VMUnit `json:"vms"`
}

// machineEvents writes the topic's event for each of these VMs not built
// yet; versioned, so workflows that started without them replay.
func machineEvents(ctx workflow.Context, info Info, topic string, vms []VMUnit) error {
	if len(vms) == 0 || workflow.GetVersion(ctx, "machine-events", workflow.DefaultVersion, 1) == workflow.DefaultVersion {
		return nil
	}
	var a *Activities
	return workflow.ExecuteActivity(ctx, a.MachineEvents, MachineEventsInput{RequestID: info.RequestID, Topic: topic, VMs: vms}).Get(ctx, nil)
}

// MachineEvents writes the topic's event for each VM the request hasn't
// built: a VM an earlier run built, and a retry keeps, was told of then.
func (a *Activities) MachineEvents(ctx context.Context, in MachineEventsInput) error {
	built, err := a.Store.ResourcesForRequest(ctx, in.RequestID)
	if err != nil {
		return err
	}
	for _, vm := range in.VMs {
		if hasVM(built, vm) {
			continue
		}
		if err := outbox.ForMachine(ctx, a.Store.Pool, in.Topic, in.RequestID, machineKey(ctx, in.RequestID, in.Topic, vm.Name),
			machineOf(vm, vm.chosenAddresses(), "", "")); err != nil {
			return err
		}
	}
	return nil
}

func hasVM(list []store.Resource, vm VMUnit) bool {
	for _, r := range list {
		if r.Type == "vsphere.vm" && r.DeploymentID == vm.DeploymentID && r.Name == vm.Name {
			return true
		}
	}
	return false
}

func machineKey(ctx context.Context, requestID, topic, vm string) string {
	return runKey(ctx, requestID, topic) + ":" + vm
}

// machineEvent writes one VM's compute.provision.pre or .post, with its
// addresses once reserved and its moref once cloned.
func (a *Activities) machineEvent(ctx context.Context, topic string, in VMInput) error {
	addresses := map[string]string{"": in.IP}
	for _, n := range in.VM.Addressed() {
		addresses[n.Key] = a.addressOf(ctx, in.RequestID, in.VM, n.Key)
	}
	if in.VM.Subnet6 != "" {
		addresses["ipv6"] = a.address6Of(ctx, in.RequestID, in.VM)
	}
	resourceID := ""
	if in.Moref != "" {
		if r := a.findResource(ctx, in.RequestID, in.VM.DeploymentID, "vsphere.vm", in.VM.Name); r != nil {
			resourceID = r.ID
		}
	}
	return outbox.ForMachine(ctx, a.Store.Pool, topic, in.RequestID, machineKey(ctx, in.RequestID, topic, in.VM.Name),
		machineOf(in.VM, addresses, in.Moref, resourceID))
}

// machineOf is what a VM's events say of a planned VM: addresses by
// adapter key ("" for the first, "ipv6" for its IPv6 address), nil before
// any is reserved.
func machineOf(vm VMUnit, addresses map[string]string, moref, resourceID string) map[string]any {
	ipam := blueprint.IPAMEfficientIP
	if vm.DHCP {
		ipam = blueprint.IPAMDHCP
	}
	first := map[string]any{"index": 0, "key": "", "network": vm.Network, "segment": vm.Segment, "ipam": ipam,
		"subnet": vm.Subnet, "gateway": vm.Gateway, "prefix_length": vm.PrefixLength, "subnet6": vm.Subnet6,
		"dns_zone": vm.DNSZone, "fqdn": vm.FQDN, "address": orNil(addresses[""])}
	if vm.Subnet6 != "" {
		first["address6"] = orNil(addresses["ipv6"])
	}
	networks := []any{first}
	for i, n := range vm.NICs {
		networks = append(networks, map[string]any{"index": i + 1, "key": n.Key, "network": n.Network, "segment": n.Segment,
			"ipam": n.IPAM, "subnet": n.Subnet, "gateway": "", "prefix_length": n.PrefixLength, "subnet6": "",
			"dns_zone": n.DNSZone, "fqdn": n.FQDN, "address": orNil(addresses[n.Key])})
	}
	hosts := make([]any, len(vm.Hosts))
	for i, h := range vm.Hosts {
		hosts[i] = h
	}
	tags := make([]any, len(vm.Tags))
	for i, t := range vm.Tags {
		tags[i] = t
	}
	props := map[string]any{}
	for k, v := range vm.Properties {
		props[k] = v
	}
	return map[string]any{"name": vm.Name, "fqdn": vm.FQDN, "component": vm.Component, "index": vm.Index,
		"deployment_id": vm.DeploymentID, "resource_id": orNil(resourceID), "moref": orNil(moref), "address": orNil(addresses[""]),
		"template": vm.Template, "vcpu": vm.VCPU, "memory_gb": vm.MemoryGB, "disk_gb": vm.DiskGB,
		"placement": map[string]any{"vcenter": vm.VCenter, "cluster": vm.Cluster, "resource_pool": vm.ResourcePool, "hosts": hosts,
			"datastore": vm.Datastore, "datastore_cluster": vm.DatastoreCluster, "folder": vm.Folder},
		"networks": networks, "tags": tags, "properties": props}
}

// machineOfResource is what a VM's removal events say of it, from its
// record: where it is now, which may differ from where it was built.
func machineOfResource(r store.Resource, address string) map[string]any {
	var p present.VMResourceProps
	_ = json.Unmarshal(r.Properties, &p)
	vm := p.VM
	str := func(s *string) string {
		if s == nil {
			return ""
		}
		return *s
	}
	datastore, diskGB := "", 0
	if len(vm.Disks) > 0 {
		datastore, diskGB = str(vm.Disks[0].Datastore), vm.Disks[0].CapacityGb
	}
	networks := []any{}
	for i, n := range vm.Nics {
		var ip any
		if n.IpAddresses != nil && len(*n.IpAddresses) > 0 {
			ip = (*n.IpAddresses)[0]
		}
		if i == 0 && address != "" {
			ip = address
		}
		networks = append(networks, map[string]any{"index": i, "network": n.Network, "address": ip})
	}
	tags := []any{}
	if vm.Tags != nil {
		for _, t := range *vm.Tags {
			tags = append(tags, t)
		}
	}
	props := map[string]any{}
	if vm.CustomProperties != nil {
		for k, v := range *vm.CustomProperties {
			props[k] = v
		}
	}
	host := []any{}
	if vm.Host != nil {
		host = append(host, *vm.Host)
	}
	return map[string]any{"name": r.Name, "fqdn": str(vm.Fqdn), "component": str(vm.Component), "index": nil,
		"deployment_id": r.DeploymentID, "resource_id": r.ID, "moref": orNil(vm.Moref), "address": orNil(address),
		"template": str(vm.Template), "vcpu": vm.CpuCount, "memory_gb": vm.MemoryMb / 1024, "disk_gb": diskGB,
		"placement": map[string]any{"vcenter": vm.Vcenter, "cluster": str(vm.Cluster), "resource_pool": str(vm.ResourcePool),
			"hosts": host, "datastore": datastore, "datastore_cluster": "", "folder": str(vm.Folder)},
		"networks": networks, "tags": tags, "properties": props}
}

// removing is what a VM's removal events say of it, read before it is
// deleted: its address is released after it.
func (a *Activities) removing(ctx context.Context, r store.Resource) (map[string]any, error) {
	list, err := a.Store.ListResources(ctx, r.DeploymentID)
	if err != nil {
		return nil, err
	}
	address := ""
	for _, x := range list {
		var p struct {
			Address  string `json:"address"`
			Hostname string `json:"hostname"`
			NIC      string `json:"nic"`
		}
		_ = json.Unmarshal(x.Properties, &p)
		if x.Type == "ipam.address" && p.Hostname == r.Name && p.NIC == "" && !isIPv6(p.Address) {
			address = cmp.Or(address, p.Address)
		}
	}
	return machineOfResource(r, address), nil
}

// removalEvent writes a VM's compute.removal.pre or .post for the request
// deleting it: a deletion, an update or a rollback.
func (a *Activities) removalEvent(ctx context.Context, topic, requestID string, r store.Resource, machine map[string]any) error {
	return outbox.ForMachine(ctx, a.Store.Pool, topic, requestID, machineKey(ctx, requestID, topic, r.ID), machine)
}

func orNil(s string) any {
	if s == "" {
		return nil
	}
	return s
}
