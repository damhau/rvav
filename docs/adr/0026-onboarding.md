# ADR-0026: Onboarding existing VMs

- Status: accepted
- Date: 2026-10-29

## Context
Thousands of VMs exist that rvav didn't build: built by hand, by vRA, or
by scripts. As long as rvav doesn't know them, their owners can't run
day-2 actions on them, leases and quotas don't cover them, and AWX's
inventory from rvav lacks them. The vRA importer (P2) needs a way to
create deployments from existing VMs too.

## Decision
Administrators **adopt** existing VMs into deployments
(`POST /admin/onboarding`, the **Onboarding** page).

- **What is offered:** the inventory's VMs that no deployment has
  (`GET /admin/onboarding/vms`), with filters (name, address or host name,
  vCenter, cluster, power) and how many match. Templates aren't offered.
- **What the administrator gives:** the VMs (up to 200 at once), a project,
  an environment of it, an owner (a member of the project, not a viewer),
  a catalog item and a lease.
  - **Catalog item:** a published VM item offered in the project. It gives
    the deployments their OS family and day-2 actions, as if they had been
    requested.
  - **Lease:** without one given, the lease policy's default. Its maximum
    applies.
- **Deployments:** each VM is a deployment of its own, named after it, or
  all go into one deployment of a given name. They are ready at once, with
  no request behind them, and an audit event (`deployment.adopted`) says
  who adopted what.
- **Addresses and DNS records:** the VM is read in vCenter. EfficientIP is
  asked for the address of each of its IPv4 addresses, those addresses'
  PTR records, and the A records that point at them, of the names those
  give or of the guest's own full host name. They become the deployment's
  `ipam.address` and `dns.record` resources. Its name (FQDN) is its A
  record's, else its PTR record's, else EfficientIP's.
- **Nothing changes in vCenter or EfficientIP.** The VM keeps its name,
  folder and annotation, and the addresses and records keep their names
  and class parameters.
- **Like any deployment from then on:** the sync and drift follow it from
  how it was adopted, day-2 actions and policies apply, and it is in AWX's
  inventory. Deleting it deletes the VMs and releases the addresses and
  records it holds.
- **All or none:** if anything fails, nothing is adopted:
  - the checks fail: 422, per field and per VM;
  - vCenter or EfficientIP can't be read: 502;
  - an address or record already belongs to another deployment: 422 on
    that VM.

## Consequences
- Quotas aren't checked when adopting: the VMs exist already. They count
  from then on, so a project can end up over its quota and its next
  requests wait for room.
- The vRA importer (P2) builds on the same operation, with the item, owner
  and lease vRA had.
- Not done: adopting in the background with progress (200 VMs take a few
  seconds), and post-configuration on adopted VMs.
