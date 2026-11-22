# ADR-0063: IP ranges rvav manages, for networks without EfficientIP

- Status: accepted
- Date: 2026-11-22
- Builds on the EfficientIP client of [ADR-0015](0015-efficientip-client.md),
  the NSX segments of [ADR-0028](0028-nsx-networking.md), the adapters of
  [ADR-0039](0039-guest-access-and-networking.md), the updates of
  [ADR-0042](0042-deployment-updates.md) and the subscriptions that change
  a request of [ADR-0054](0054-subscriptions-that-change-the-request.md).
  I1.2 (#175), C11 of the parity programme.

## Context
Every static address rvav gives comes from EfficientIP: a network's subnet
is mapped by the subnet sync or by hand, and the address is reserved in
EfficientIP with its A and PTR records. A site without EfficientIP (a lab,
an isolated network, a customer who runs another IPAM) gets nothing but
DHCP. vRA has its internal IPAM for these: IP ranges in a network profile,
CIDR, start and end, gateway, DNS servers and search domains, handed out in
order and given back when the machine goes. It writes no DNS record.

## Decision

### One source per network, in one vCenter
An IP range belongs to a network (a port group, or an NSX segment's port
group) of one vCenter: port groups of two vCenters may share a name and not
a subnet. A network with at least one range takes its VMs' addresses from
its ranges; every other network keeps EfficientIP, the default, mappings
set by hand included. There is no switch to set: adding the first range of
a network moves its new VMs to rvav, removing its last moves them back.
VMs already built keep their addresses. rvav has no site object; the
network is what vRA's network profile ties a range to, and the vCenter is
the closest rvav has to a site.

The network mappings list shows a network with ranges once more, for its
vCenter, with source `rvav`.

### What a range says
A range has a name, the vCenter and network, the network's IPv4 subnet,
its start and end, the gateway, DNS servers, DNS search domains, an
optional DNS domain, and excluded addresses (one address or a span
`a-b`). It gives every address from start to end but the excluded ones,
the gateway, and the subnet's network and broadcast addresses.

The ranges of one network agree on subnet, gateway, DNS servers, search
domains and domain: they are the network's, and VMs get them whichever
range gives their address. Ranges of one vCenter don't overlap. Each is
refused with the other range's name. A range from which VMs hold
addresses keeps its vCenter, network, subnet and gateway, and keeps those
addresses in, and can't be deleted (409).

IPv4 only: a blueprint asking for IPv6 on a network with ranges is refused
when the request is evaluated.

### Allocation
`ip_allocations` holds one row per address given, with the request, the
deployment, the VM's name and its adapter's key (`""` for its first). Two
unique keys carry the guarantees: (range, address), so that two requests
never get one address, and (request, VM, adapter), so that an activity run
twice gets the address it got the first time. The address is taken in a
transaction that locks the network's ranges (`for update`) and gives the
lowest free address of the first range, in the order the ranges were
added, that has one.

The reserve step records the address as the VM's `ipam.address` resource,
as an EfficientIP address is, with `source: rvav`, the range and the
allocation. What gives an EfficientIP address back gives this one back:
deleting the deployment or removing the VM, a rollback. Two more give it
back too: a request that fails before its VM exists (the address is free
for a retry, which takes one again), and a day-2 change that moves or
removes the adapter. A day-2 move to a network with ranges gives the
adapter no address, as a move between EfficientIP networks doesn't.
Renaming a VM whose addresses all come from ranges renames their
allocations and asks EfficientIP nothing.

A static address a subscription asks for (ADR-0054) must be one the
network's ranges give and no other VM holds. A request is refused when it
is evaluated if the network's ranges don't have the free addresses its VMs
need, with the code `no_free_address`.

### No DNS
rvav writes no DNS record for an address from a range: the plan has no DNS
step for a VM whose addresses all come from ranges, and its reserve step's
system is rvav. A VM is named in the range's DNS domain, or the
blueprint's guest domain when the range has none; no DNS check is made of
the name. The guest gets the DNS servers and the search domains, after its
own domain, through its customization or cloud-init.

### Reconcile
The reconcile report, which needed EfficientIP, also runs without it, and
compares the ranges with what VMs hold: an address given to a VM the
deployment no longer has, and a VM in the inventory whose address is in a
range but was never given by it.

## Consequences
- An administrator manages ranges in Infrastructure › IP ranges, from
  `rvavctl ip-ranges`, Terraform's `rvav_ip_range` and PowerShell.
- A network with ranges and an EfficientIP subnet uses the ranges; the
  EfficientIP subnet sync goes on, unused for that vCenter.
- Moving a network from EfficientIP to ranges doesn't move the addresses
  EfficientIP gave: they stay there until their VMs go.
- Not done: the vRA importer reads no network profile, so vRA's internal
  IP ranges aren't imported; DNS registration for ranges; IPv6 ranges.
