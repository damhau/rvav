package core

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/damhau/rvav/internal/blueprint"
	"github.com/damhau/rvav/internal/naming"
	"github.com/damhau/rvav/internal/provision"
	"github.com/damhau/rvav/internal/store"
)

// ChangeMachine checks what a blocking subscription's answer changes on a
// VM the request hasn't built, as the request was checked when submitted,
// and applies it to the request's plan, steps and deployment (ADR-0054): a
// name of the project's naming policy nobody has, a placement its rules
// allow with room for the VM, a network of its cluster, a free address of
// the network's subnet. It refuses a change with a *provision.RefusedChange.
func (s *Service) ChangeMachine(ctx context.Context, ch provision.MachineChange) (provision.VMUnit, []string, error) {
	vm := ch.VM
	var did []string
	err := s.Store.Tx(ctx, func(tx pgx.Tx) error {
		if err := s.Store.LockDeployment(ctx, tx, vm.DeploymentID); err != nil {
			return err
		}
		if _, err := s.Store.LockRequest(ctx, tx, ch.RequestID); err != nil {
			return err
		}
		req, err := s.Store.GetRequestIn(ctx, tx, ch.RequestID)
		if err != nil {
			return err
		}
		var plan provision.Plan
		if err := json.Unmarshal(req.Plan, &plan); err != nil {
			return err
		}
		list := plan.VMs
		if ch.Step != "" {
			i := slices.IndexFunc(plan.Composite, func(c provision.CompositeStep) bool { return c.StepKey() == ch.Step })
			if i < 0 {
				return fmt.Errorf("request %s has no step %s", req.Number, ch.Step)
			}
			list = plan.Composite[i].VMs
		}
		at := slices.IndexFunc(list, func(v provision.VMUnit) bool { return v.DeploymentID == vm.DeploymentID && v.Name == vm.Name })
		if at < 0 {
			return fmt.Errorf("request %s doesn't build %s", req.Number, vm.Name)
		}
		before := plan.Steps(req.Environment)
		c := &machineChange{s: s, tx: tx, req: req, plan: plan, vms: []*provision.VMUnit{&vm, &list[at]}}
		if err := c.apply(ctx, ch.Answer); err != nil {
			return err
		}
		if did = c.did; len(did) == 0 {
			return nil
		}
		// Its steps not started yet are named and described after it again.
		if after := plan.Steps(req.Environment); len(after) == len(before) {
			for i, b := range before {
				if a := after[i]; a.Key != b.Key || a.Title != b.Title || a.Detail != b.Detail {
					if err := s.Store.ReplanPendingStep(ctx, tx, req.ID, b.Key, a.Key, a.Title, a.Detail); err != nil {
						return err
					}
				}
			}
		}
		raw, err := json.Marshal(plan)
		if err != nil {
			return err
		}
		if err := s.Store.SetRequestPlan(ctx, tx, req.ID, raw); err != nil {
			return err
		}
		// Its VMs hold their room where they go now (ADR-0036).
		return s.holdPlacement(ctx, tx, nil, req.ID, plan.Reservations())
	})
	return vm, did, err
}

// machineChange checks and applies one answer: to the VM as the workflow
// has it (vms[0], which a segment made for its deployment gave a subnet)
// and as the request's plan does.
type machineChange struct {
	s    *Service
	tx   pgx.Tx
	req  store.RequestRow
	plan provision.Plan
	vms  []*provision.VMUnit
	pl   *placement
	did  []string
}

func refuse(format string, args ...any) error {
	return &provision.RefusedChange{Reason: fmt.Sprintf(format, args...)}
}

func (c *machineChange) vm() provision.VMUnit { return *c.vms[0] }

func (c *machineChange) set(fn func(*provision.VMUnit)) {
	for _, v := range c.vms {
		fn(v)
	}
}

func (c *machineChange) placement(ctx context.Context) (*placement, error) {
	if c.pl != nil {
		return c.pl, nil
	}
	pl, err := c.s.placement(ctx, c.req.ProjectID, c.req.Environment)
	if err != nil {
		return nil, err
	}
	// Other requests' VMs not built yet count; this request's don't, so
	// that the VM moved doesn't count twice.
	if err := c.s.withReservations(ctx, pl, c.req.ID); err != nil {
		return nil, err
	}
	c.pl = pl
	return pl, nil
}

func (c *machineChange) envName(ctx context.Context) string {
	if env, err := c.s.Store.GetEnvironment(ctx, c.req.Environment); err == nil {
		return env.Name
	}
	return c.req.Environment
}

func (c *machineChange) apply(ctx context.Context, a provision.MachineAnswer) error {
	if a.Name != nil {
		if err := c.rename(ctx, strings.TrimSpace(*a.Name)); err != nil {
			return err
		}
	}
	if a.Placement != nil {
		if err := c.place(ctx, *a.Placement); err != nil {
			return err
		}
	}
	for _, n := range a.Networks {
		if err := c.network(ctx, n); err != nil {
			return err
		}
	}
	return nil
}

// others are the request's other VMs.
func (c *machineChange) others() []provision.VMUnit {
	all := slices.Clone(c.plan.VMs)
	for _, st := range c.plan.Composite {
		all = append(all, st.VMs...)
	}
	vm := c.vm()
	return slices.DeleteFunc(all, func(v provision.VMUnit) bool { return v.DeploymentID == vm.DeploymentID && v.Name == vm.Name })
}

const decimalDigits = "0123456789"

// rename gives the VM a name of its naming policy's prefix nobody has: no
// deployment or VM of rvav, no VM of vCenter, no host name in DNS.
func (c *machineChange) rename(ctx context.Context, name string) error {
	vm := c.vm()
	old := vm.Name
	if name == old {
		return nil
	}
	if !naming.Valid(name) {
		return refuse("%s is not a VM name: lower-case letters, digits and hyphens, starting with a letter, 63 at most.", name)
	}
	prefix := strings.TrimRight(old, decimalDigits)
	if strings.TrimRight(name, decimalDigits) != prefix || name == prefix {
		return refuse("%s doesn't follow the project's naming policy: these VMs are named %s followed by a number.", name, prefix)
	}
	// The policy's prefix may end in digits too: every prefix either name
	// could have been given from is locked, in the order requests lock them.
	var prefixes []string
	for _, n := range []string{old, name} {
		for i := len(prefix); i < len(n); i++ {
			prefixes = append(prefixes, n[:i])
		}
	}
	slices.Sort(prefixes)
	for _, p := range slices.Compact(prefixes) {
		if err := c.s.Store.LockNamePrefix(ctx, c.tx, p); err != nil {
			return err
		}
	}
	ours, err := c.s.Store.DeploymentNames(ctx, c.tx, name)
	if err != nil {
		return err
	}
	vcenter, err := c.s.Store.InventoryVMNames(ctx, c.tx, name)
	if err != nil {
		return err
	}
	if ours[name] || vcenter[name] || slices.ContainsFunc(c.others(), func(v provision.VMUnit) bool { return v.Name == name }) {
		return refuse("%s is taken.", name)
	}
	resources, err := c.s.Store.ResourcesForRequest(ctx, c.req.ID)
	if err != nil {
		return err
	}
	if slices.ContainsFunc(resources, func(r store.Resource) bool {
		var p struct {
			Hostname string `json:"hostname"`
		}
		_ = json.Unmarshal(r.Properties, &p)
		return r.DeploymentID == vm.DeploymentID && r.Type == "ipam.address" && p.Hostname == old
	}) {
		return refuse("%s already has an address reserved under its name.", old)
	}
	renamed := vm
	renamed.NICs = slices.Clone(vm.NICs)
	renameVM(&renamed, old, name)
	hostnames := []string{renamed.FQDN}
	for _, n := range renamed.Addressed() {
		hostnames = append(hostnames, n.FQDN)
	}
	for _, h := range hostnames {
		if err := c.freeInDNS(ctx, h); err != nil {
			return err
		}
	}
	if err := c.s.Store.LockDeploymentNames(ctx, c.tx, c.req.ProjectID, []string{name}); err != nil {
		return err
	}
	if taken, err := c.s.Store.TakenDeploymentNames(ctx, c.tx, c.req.ProjectID, vm.DeploymentID, []string{name}); err != nil {
		return err
	} else if len(taken) > 0 {
		return refuse("A deployment of %s is named %s.", c.req.ProjectName, taken[0])
	}
	if _, err := c.s.Store.RenameRequestDeployment(ctx, c.tx, vm.DeploymentID, c.req.ID, old, name); err != nil {
		return err
	}
	c.set(func(v *provision.VMUnit) { renameVM(v, old, name) })
	c.did = append(c.did, "renaming it "+name)
	return nil
}

// freeInDNS refuses a host name DNS or IPAM has.
func (c *machineChange) freeInDNS(ctx context.Context, fqdn string) error {
	if !strings.Contains(fqdn, ".") || c.s.IPAM == nil {
		return nil
	}
	exists, err := c.s.IPAM.NameExists(ctx, fqdn)
	if err != nil {
		return fmt.Errorf("EfficientIP: %w", err)
	}
	if exists {
		return refuse("%s is taken in DNS.", fqdn)
	}
	return nil
}

// renameVM names a VM and its adapters' host names after name.
func renameVM(v *provision.VMUnit, old, name string) {
	v.Name = name
	if v.FQDN == old {
		v.FQDN = name
	} else if rest, ok := strings.CutPrefix(v.FQDN, old+"."); ok {
		v.FQDN = name + "." + rest
	}
	for i, n := range v.NICs {
		if rest, ok := strings.CutPrefix(n.FQDN, old+"-"); ok {
			v.NICs[i].FQDN = name + "-" + rest
		}
	}
}

// place moves the VM to a cluster, resource pool, datastore or folder its
// project's placement rules allow, with room for it.
func (c *machineChange) place(ctx context.Context, p provision.PlacementAnswer) error {
	pl, err := c.placement(ctx)
	if err != nil {
		return err
	}
	vm := c.vm()
	trim := func(s *string, def string) string {
		if s == nil {
			return def
		}
		return strings.TrimSpace(*s)
	}
	cluster := trim(p.Cluster, vm.Cluster)
	cl := pl.cluster(cluster)
	if cl == nil {
		return refuse("Cluster %s is not available to %s in %s.", cluster, c.req.ProjectName, c.envName(ctx))
	}
	if vm.VCenter != "" && cl.VCenter != vm.VCenter {
		return refuse("Cluster %s is in vCenter %s; the VM's template is in %s.", cluster, cl.VCenter, vm.VCenter)
	}
	rule := pl.rule(cluster)
	moved := cluster != vm.Cluster
	pool := vm.ResourcePool
	if moved {
		pool = rule.ResourcePool
	}
	if p.ResourcePool != nil {
		pool = strings.Trim(strings.TrimSpace(*p.ResourcePool), "/")
	}
	if rp := rule.ResourcePool; rp != "" && pool != rp && !strings.HasPrefix(pool, rp+"/") {
		return refuse("Resource pool %s is not within %s, where VMs go on %s.", pool, rp, cluster)
	}
	if pool != "" && pl.pool(*cl, pool) == nil {
		return refuse("Cluster %s has no resource pool %s.", cluster, pool)
	}
	datastore := trim(p.Datastore, vm.Datastore)
	allowed := pl.datastoresFor(cluster)
	if moved || datastore != vm.Datastore {
		i := slices.IndexFunc(allowed, func(d store.InvDatastore) bool { return d.Name == datastore })
		if i < 0 {
			return refuse("Datastore %s cannot be used on %s.", datastore, cluster)
		}
		need := vm.DiskGB
		for _, d := range vm.Disks {
			if cmp.Or(d.Datastore, vm.Datastore) == vm.Datastore {
				need += d.SizeGB
			}
		}
		if free := allowed[i].FreeGB; free < int64(need) {
			return refuse("Datastore %s has %d GB free; the VM needs %d GB.", datastore, free, need)
		}
	}
	if moved || pool != vm.ResourcePool {
		need := int64(vm.MemoryGB) * 1024
		if free := pl.freeOn(cl, pool); free < need {
			where := cluster
			if pool != "" {
				where = "Resource pool " + pool + " of " + cluster
			}
			return refuse("%s has %d MB of memory free; the VM needs %d MB.", where, max(free, 0), need)
		}
	}
	folder := vm.Folder
	if p.Folder != nil {
		folder = blueprint.FolderUnder(rule.Folder, *p.Folder)
	} else if dc := blueprint.FolderDatacenter(folder); moved && dc != "" && dc != cl.Datacenter {
		folder = rule.Folder
	}
	if dc, at := blueprint.FolderDatacenter(folder), cl.Datacenter; dc != "" && at != "" && dc != at {
		return refuse("Folder %s is in datacenter %s, and cluster %s in %s.", folder, dc, cluster, at)
	}
	var hosts []string
	if moved {
		networks := []string{}
		if vm.Segment == "" {
			networks = append(networks, vm.Network)
		}
		for _, n := range vm.NICs {
			if n.Segment == "" {
				networks = append(networks, n.Network)
			}
		}
		for _, n := range networks {
			if !slices.ContainsFunc(pl.networksFor(cluster), func(x store.InvNetwork) bool { return x.Name == n }) {
				return refuse("Network %s cannot be used on %s.", n, cluster)
			}
		}
		for _, d := range vm.Disks {
			if d.Datastore != "" && d.Datastore != vm.Datastore &&
				!slices.ContainsFunc(allowed, func(x store.InvDatastore) bool { return x.Name == d.Datastore }) {
				return refuse("Datastore %s cannot be used on %s.", d.Datastore, cluster)
			}
		}
		if len(rule.HostPatterns) > 0 {
			for _, h := range pl.hostsFor(cl, "") {
				hosts = append(hosts, h.Name)
			}
		}
	}
	if !moved && pool == vm.ResourcePool && datastore == vm.Datastore && folder == vm.Folder {
		return nil
	}
	pod := pl.podOf(cluster, "", datastore)
	c.set(func(v *provision.VMUnit) {
		if datastore != v.Datastore {
			for i, d := range v.Disks {
				if d.Datastore == v.Datastore {
					v.Disks[i].Datastore = datastore
				}
			}
			v.DatastoreCluster, v.PodDatastores = pod, nil
		}
		if moved {
			v.Hosts = hosts
		}
		v.Cluster, v.ResourcePool, v.Datastore, v.Folder = cluster, pool, datastore, folder
	})
	where := []string{cluster}
	if pool != "" {
		where = append(where, "resource pool "+pool)
	}
	where = append(where, "datastore "+datastore, "folder "+folder)
	c.did = append(c.did, "placing it on "+strings.Join(where, ", "))
	return nil
}

// network changes one adapter's network or address: 0 is the first.
func (c *machineChange) network(ctx context.Context, n provision.NetworkAnswer) error {
	vm := c.vm()
	idx := *n.Index
	if idx < 0 || idx > len(vm.NICs) {
		return refuse("The VM has no adapter %d: it has %d, from 0.", idx, len(vm.NICs)+1)
	}
	current := vm.Network
	if idx > 0 {
		current = vm.NICs[idx-1].Network
	}
	if n.Network != nil && strings.TrimSpace(*n.Network) != current {
		if err := c.renetwork(ctx, idx, strings.TrimSpace(*n.Network)); err != nil {
			return err
		}
	}
	if n.Address != nil && strings.TrimSpace(*n.Address) != "" {
		return c.readdress(ctx, idx, strings.TrimSpace(*n.Address))
	}
	return nil
}

// renetwork puts an adapter on another network of the VM's cluster, in
// that network's subnet.
func (c *machineChange) renetwork(ctx context.Context, idx int, network string) error {
	pl, err := c.placement(ctx)
	if err != nil {
		return err
	}
	vm := c.vm()
	segment, addressed := vm.Segment, !vm.DHCP
	if idx > 0 {
		segment, addressed = vm.NICs[idx-1].Segment, vm.NICs[idx-1].IPAM == blueprint.IPAMEfficientIP
	}
	if segment != "" {
		return refuse("Adapter %d is on the NSX segment made for the deployment: its network can't change.", idx)
	}
	if !slices.ContainsFunc(pl.networksFor(vm.Cluster), func(x store.InvNetwork) bool { return x.Name == network }) {
		return refuse("Network %s cannot be used on %s.", network, vm.Cluster)
	}
	if !addressed {
		c.set(func(v *provision.VMUnit) {
			if idx == 0 {
				v.Network = network
			} else {
				v.NICs[idx-1].Network = network
			}
		})
		c.did = append(c.did, fmt.Sprintf("moving adapter %d to %s", idx, network))
		return nil
	}
	sn, ok := pl.subnets[network]
	if !ok {
		return refuse("No EfficientIP subnet is linked to %s. Ask an administrator to link one.", network)
	}
	if idx == 0 && vm.Subnet6 != "" && sn.Subnet6 == "" {
		return refuse("No EfficientIP IPv6 subnet is linked to %s. Ask an administrator to link one.", network)
	}
	fqdn := vm.Name + "." + sn.DNSZone
	if idx > 0 {
		fqdn = vm.Name + "-" + vm.NICs[idx-1].Key + "." + sn.DNSZone
	}
	if err := c.freeInDNS(ctx, fqdn); err != nil {
		return err
	}
	c.set(func(v *provision.VMUnit) {
		if idx == 0 {
			v.Network, v.FQDN, v.Address = network, fqdn, ""
			v.Subnet, v.SubnetID, v.Gateway = sn.Subnet, sn.EfficientIPSubnetID, sn.Gateway
			v.PrefixLength, v.DNSZone, v.DNSServers = prefixLength(sn.Subnet), sn.DNSZone, sn.DNSServers
			if v.Subnet6 != "" {
				v.Subnet6, v.Subnet6ID, v.Gateway6, v.PrefixLength6 = sn.Subnet6, sn.EfficientIPSubnet6ID, sn.Gateway6, prefixLength(sn.Subnet6)
			}
			return
		}
		nic := &v.NICs[idx-1]
		nic.Network, nic.FQDN, nic.Address = network, fqdn, ""
		nic.Subnet, nic.SubnetID, nic.PrefixLength = sn.Subnet, sn.EfficientIPSubnetID, prefixLength(sn.Subnet)
		nic.DNSZone, nic.DNSServers = sn.DNSZone, sn.DNSServers
	})
	c.did = append(c.did, fmt.Sprintf("moving adapter %d to %s (%s)", idx, network, sn.Subnet))
	return nil
}

// readdress gives an adapter an address of its subnet nobody has: not the
// subnet's own, not another of the request's, none rvav or EfficientIP
// holds. It is reserved when the VM's addresses are.
func (c *machineChange) readdress(ctx context.Context, idx int, ip string) error {
	vm := c.vm()
	subnet, gateway, network := vm.Subnet, vm.Gateway, vm.Network
	switch {
	case idx == 0 && vm.DHCP:
		return refuse("Adapter 0 gets its address from DHCP: it takes none.")
	case idx > 0 && vm.NICs[idx-1].IPAM != blueprint.IPAMEfficientIP:
		return refuse("Adapter %d gets no address from EfficientIP: it takes none.", idx)
	case idx > 0:
		subnet, gateway, network = vm.NICs[idx-1].Subnet, "", vm.NICs[idx-1].Network
	}
	addr, err := netip.ParseAddr(ip)
	if err != nil || !addr.Is4() {
		return refuse("%s is not an IPv4 address.", ip)
	}
	prefix, err := netip.ParsePrefix(subnet)
	if err != nil {
		return refuse("Adapter %d has no subnet yet to take an address in.", idx)
	}
	prefix = prefix.Masked()
	if !prefix.Contains(addr) {
		return refuse("%s is not in %s, the subnet of %s.", ip, prefix, network)
	}
	if addr == prefix.Addr() || addr == lastAddress(prefix) || ip == gateway {
		return refuse("%s is the network, broadcast or gateway address of %s.", ip, prefix)
	}
	for _, v := range append(c.others(), vm) {
		if v.Address == ip && !(v.Name == vm.Name && idx == 0) {
			return refuse("%s is already %s's in this request.", ip, v.Name)
		}
		for i, nic := range v.NICs {
			if nic.Address == ip && !(v.Name == vm.Name && idx == i+1) {
				return refuse("%s is already %s's in this request.", ip, v.Name)
			}
		}
	}
	inUse, err := c.s.Store.IPAddressesInUse(ctx, prefix.String())
	if err != nil {
		return err
	}
	if slices.Contains(inUse, ip) {
		return refuse("%s is taken: rvav reserved it for another deployment.", ip)
	}
	if c.s.IPAM != nil {
		addresses, records, err := c.s.IPAM.Find(ctx, []string{ip}, nil)
		if err != nil {
			return fmt.Errorf("EfficientIP: %w", err)
		}
		if len(addresses) > 0 {
			return refuse("%s is taken in EfficientIP (%s).", ip, cmp.Or(addresses[0].Hostname, "without a name"))
		}
		if len(records) > 0 {
			return refuse("%s has DNS records in EfficientIP (%s %s).", ip, records[0].Type, records[0].FQDN)
		}
	}
	c.set(func(v *provision.VMUnit) {
		if idx == 0 {
			v.Address = ip
		} else {
			v.NICs[idx-1].Address = ip
		}
	})
	c.did = append(c.did, fmt.Sprintf("giving adapter %d the address %s", idx, ip))
	return nil
}

// lastAddress is an IPv4 prefix's broadcast address.
func lastAddress(p netip.Prefix) netip.Addr {
	a := p.Addr().As4()
	host := uint32(1)<<(32-p.Bits()) - 1
	n := (uint32(a[0])<<24 | uint32(a[1])<<16 | uint32(a[2])<<8 | uint32(a[3])) | host
	return netip.AddrFrom4([4]byte{byte(n >> 24), byte(n >> 16), byte(n >> 8), byte(n)})
}
