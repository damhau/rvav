# ADR-0025: Drift, VMs changed outside rvav

- Status: accepted
- Date: 2026-10-29

## Context
The inventory sync follows vCenter (S13.1), and until now a VM's resource
simply took whatever vCenter said. A VM resized, moved to another network or
powered off in vCenter looked in rvav as if rvav had done it. Quotas,
approvals and costs are worked out from what rvav built, so a change made
behind its back must show. `SyncState` has had `drifted` since slice 1,
but nothing set it.

## Decision

### The VM as rvav left it
Each VM resource keeps, next to what vCenter reports, the **expected** VM:
how rvav last left it. It holds the VM's power state, its size (vCPU and
memory), its disks (size, by label) and its adapters (network, by label).
Its name, folder, snapshots, ISO image and guest are not part of it.

- Building a VM sets all of it, at the clone and again once powered on.
- A day-2 action **settles only what it changed**:
  - power actions settle the power state;
  - Resize settles the size;
  - disk actions settle the disks;
  - adapter actions settle the adapters;
  - reverting to a snapshot settles everything.

  A VM whose memory was changed in vCenter therefore stays flagged after a
  power off through rvav.
- A VM rvav built before drift was measured has no expected VM. The sync
  takes it as it is the first time it reads it.

### Drift
When the sync reads a VM (a change notification or the full read), it
compares the VM with the expected one:
- **What it lists:** each difference (`drift` on the VM resource). Each has
  a label as people call it (Memory, Hard disk 2, Network adapter 1), the
  value as rvav left it and the value in vCenter. A disk or adapter
  added outside rvav has no rvav value, and one removed has no vCenter value.
- **State:** the resource is `drifted` and so is its deployment
  (`missing` wins).
- **Clearing:** a change undone in vCenter clears the flag at the next read.
- **During rvav's own actions:** nothing is compared while the deployment is
  provisioning, updating or being deleted. The vCenter change notification
  usually arrives before the action has recorded its result, and would
  flag rvav's own change.

### Accepting the changes
A day-2 action, **Accept the changes made in vCenter**, takes the VM as it
is. It is offered only on a drifted VM, and runs as a request like any
other: audited, under the day-2 policies, in the deployment's history. Its
step lists what it accepts.

## Consequences
- The deployment page shows a "Changed outside rvav" panel: for each
  drifted VM, what differs, and the accept button.
- Quotas still count what vCenter reports, as before.
- Not done: notifying the owner when a VM drifts, and flagging other
  resources (an address or DNS record changed in EfficientIP; the
  reconcile check covers those).
