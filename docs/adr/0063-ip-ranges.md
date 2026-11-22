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

A range is saved as written (I1.6). A DNS domain with its final dot, or an
exclusion not written as rvav writes it (`a` for one address, `a-b` with
no spaces), is refused with the code `not_canonical` and the value to
write, so that a client that compares what it sent with what it reads
back, as Terraform does, finds them equal. A DNS server the range would
give is refused (`in_range`): exclude it, or keep it out of the span.

The ranges of one network agree on subnet, gateway, DNS servers, search
domains and domain: they are the network's, and VMs get them whichever
range gives their address. Ranges of one vCenter don't overlap. Each is
refused with the other range's name. The saves of one vCenter's ranges
take turns (a transaction advisory lock on the vCenter's name), so two
overlapping ranges saved at once aren't both accepted. A range from which VMs hold
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

### Addresses VMs hold otherwise
A range never gives an address a VM holds that the range didn't give it
(I1.6): the address of an `ipam.address` resource of a deployment not
deleted, whatever its source (EfficientIP's, an adopted VM's, or another
range's of the same vCenter), the address vCenter reports for a VM of the
range's vCenter, and a DNS server of the range (one saved before the
`in_range` check). These are the range's `taken` addresses: skipped when an
address is given, refused when a subscription asks for one, and left out
of `free` and of the free addresses a request is evaluated against.

A save that would make such an address one the range gives is refused
(422 on `/excluded`, code `held`, naming the addresses and their VMs), for
a new range as for a changed one: the administrator excludes them or keeps
the range apart from them. Refused rather than saved with a warning, since
a warning on an API save is easy to miss and the range would otherwise
list as free an address a VM uses. Addresses held after the range was
saved, as when EfficientIP gives one later or a VM changes its address,
don't stop the range from saving again: they are skipped.

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
The reconcile report, which needed EfficientIP, also runs without it
(`efficientip: false`), and compares the ranges with what VMs hold:

- An orphan allocation: no deployment has the VM any more, and the request
  that took the address has ended. Unregistering a VM leaves its address
  held, since the VM still uses it; this is how it shows.
- An unallocated VM: a VM of the inventory whose address, as vCenter
  reports it, is in a range of its vCenter, not excluded, and given to no
  VM or to another (`held_by`).

An administrator gives an orphan back with `DELETE
/admin/ip-ranges/{id}/allocations/{address}`, audited; it is refused (409)
for an address a deployment's VM holds, and while vCenter still reports a
VM of the range's vCenter on the address (as the inventory last saw it),
so that the address isn't given to a new VM while the old one uses it. rvav gives addresses back itself in
every other case, so this is the only manual release. An unallocated VM is
fixed by excluding its address, or by moving the VM.

### Clients
Ranges are configuration an administrator writes, not inventory rvav reads
from vCenter, so they depart from ADR-0053's "infrastructure stays in the
web app": Terraform's `rvav_ip_range` creates, changes and deletes them,
and imports one by its ID. The user clients of ADR-0051 and ADR-0053 read
them, to answer "who has this address": `rvavctl ip-ranges list|show`, and
PowerShell's `Get-RvavIPRange` and `Get-RvavIPAllocation`.
`rvavctl ip-ranges release` gives an orphan back, the one manual release;
PowerShell does it with `Invoke-RvavApi`.

## Consequences
- An administrator manages ranges in Infrastructure › IP ranges and with
  Terraform's `rvav_ip_range`; `rvavctl ip-ranges` and PowerShell read
  them, and `rvavctl` gives orphans back.
- A network with ranges and an EfficientIP subnet uses the ranges; the
  EfficientIP subnet sync goes on, unused for that vCenter.
- Moving a network from EfficientIP to ranges doesn't move the addresses
  EfficientIP gave: they stay there until their VMs go, and the ranges
  don't give them meanwhile.
- Not done: the vRA importer reads no network profile, so vRA's internal
  IP ranges aren't imported; DNS registration for ranges; IPv6 ranges.
