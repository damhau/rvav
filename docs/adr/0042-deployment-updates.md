# ADR-0042: Updating a deployment to new inputs or a new version

- Status: accepted
- Date: 2026-11-19
- Extends the day-2 actions of [ADR-0013](0013-day2-actions.md), the
  multi-VM blueprints of [ADR-0024](0024-multi-vm-blueprints.md) and the
  leases and quotas of [ADR-0014](0014-leases-and-quotas.md). X1.4 (#144),
  vRA parity B15, B16 and A19.

## Context
vRA updates a deployment in place: its owner changes its inputs, or moves
it to a new version of its cloud template, and vRA shows a plan of what
changes before running it. Scaling a cluster of VMs out or in is the same
update, with a new count. A failed or partly built deployment is updated
the same way, to build what is missing. rvav could only resize, add disks
and adapters one VM at a time, and a deployment kept the version and
inputs it was requested with: a new version of its item never reached it,
and a deployment that failed half-way was retried or deleted.

## Decision

### An update is a request
- **A request of kind `update`** (migration 00074), action `update`
  (Update, lifecycle group), on one deployment of a VM item. Its title is
  "Update · *name*", its version the one it moves to, its inputs the new
  ones. It has a timeline, a history entry and an audit trail as a day-2
  action has.
- **It moves to a version the item offers.** The form is that version's,
  as for a new request. Until C2.1 it was always the published version,
  and an update sent with another was refused with `stale_version`; since
  then it moves to any version the item still offers, the newest by default
  ([ADR-0046](0046-deployment-names-and-versions.md#several-versions-requestable)). A deployment whose item is no longer
  offered in its project can't be updated.
- `POST /deployments/{id}/update/preview` returns the plan;
  `POST /deployments/{id}/update` submits it, with an idempotency key. The
  submit plans again and refuses what the preview would show as not
  allowed or refused. It also refuses when the deployment's version or
  inputs changed since it was planned, and when another request runs on
  it.
- **Inputs follow F1.4's rule:** an input left out takes its field's
  default, `null` means it was emptied. The page sends every key the form
  has. Secret inputs are never shown again: the requester gives them
  again, or the field's default applies.
- **Computed fields (REST and CEL value sources, rules) are computed
  again** on preview and submit, from the new inputs, as a new request's
  are. Keeping the values computed when the deployment was built would
  carry stale lookups into the new plan, and a value the new version
  computes differently would never reach the deployment.
- The form's validations (ADR-0043, F1.5) run on submit, as for a new
  request.

### What it compares
- rvav renders the deployment's version with its recorded inputs, and the
  published version with the new inputs, through the same rules, and
  compares the two, component by component and VM by VM. The result is a
  list of changes, in the order they run:
  - `disk_extend`, `disk_add`, `disk_remove`: through the day-2 disk
    operations;
  - `resize`: CPU and memory, through Resize;
  - `nic_add`, `nic_remove`, `nic_move`: DHCP adapters only;
  - `vm_add`, then `vm_remove`;
  - `job`: the AWX jobs the new version adds run on the VMs kept, and each
    VM added runs all its component's jobs as it is built.
- **rvav records which of its blueprint's disks each disk of a VM is**
  (`blueprint_disk`, kept by vCenter's device key) when it builds the VM
  and when an update adds a disk (X1.6). An update grows and removes only
  those, wherever day-2 actions added disks between them, and adds a
  version's disk only to the VMs that don't have it: an update made again
  after one failed part-way doesn't add it twice. When every extra disk
  of both versions has a `key`, the disks match by key, and the record
  keeps the key too (ADR-0052, #191). Otherwise the n-th extra disk of
  the old version is the n-th of the new, and a version that removes
  extra disks while it changes others is refused, as rvav can't tell
  which ones go. An update that succeeds records, on the VMs it kept,
  the places and keys of their disks in its version. A VM without the
  record (onboarded, or built before X1.6) refuses growing or removing
  its disks, rather than guessing from their places; a disk the new
  version adds is added to it.
  A disk no longer on the VM when its step runs fails the step.
- **rvav records which of its blueprint's adapters each adapter of a VM
  is** (`blueprint_nic`: `nic1` for the first, else the adapter's `key`,
  kept by vCenter's device key) when it builds the VM and when an update
  adds an adapter (#192). An update adds a version's DHCP adapter only to
  the VMs that have none recorded as it, and moves or removes the one
  recorded, not one a day-2 action added, which it never touches; the
  steps name that adapter by its device key, as vCenter numbers the
  labels again once one is gone. An adapter already where the new version
  puts it isn't moved. A VM without the record (its first adapter isn't
  marked: onboarded, or built before #192) keeps the adapters' places, as
  before: the n-th the old version adds is `Network adapter n+1`.
- **Changes that delete something are marked `destructive`:** a disk and
  its data, an adapter, a VM.
- **A change rvav can't make in place is refused, with its reason,** and
  the update can't be submitted until it is changed back: the template,
  the cluster, resource pool, host, folder or datastore, the first
  adapter's network, an adapter with an EfficientIP address added, moved
  or removed, the storage policy or provisioning, DHCP or fixed
  addresses, NSX security groups, custom properties, vSphere tags, guest
  settings, a disk that shrinks, a VM split into components or the
  reverse. A VM's state can refuse too: suspended, snapshots under a disk
  that changes, a linked clone's disk from its template that grows, a VM
  protected from deletion that a scale in removes. rvav doesn't recreate
  a VM to apply a change, as vRA does: the user deletes the deployment
  and requests it again, knowing what that loses.
- **Unchanged** (`unchanged` in the preview): the version and the inputs,
  as the rules leave them, are the deployment's, and it isn't failed.
  Submitting it is refused with `no_change`. A new version that changes
  nothing on the VMs is not unchanged: the update runs, and the deployment
  records the new version and inputs.

### Scale out and in
- **A component's count is an input like the others.** Scale out adds VMs
  at the end: they are named from the naming policy's counter, which goes
  on from the last name it gave, and addressed, recorded in DNS and built
  as a new request's VMs are, with the index after the existing ones
  (`self.index`). Names are given when the update is submitted, not in
  the preview.
- **Scale in removes the VMs adopted into the component first, then
  those with the highest indices,** with their addresses and DNS records,
  as Delete does for each. A VM protected from deletion refuses it.
- The component's DRS rule is set again for its new members. VMs can't be
  added to NSX segments made for the deployment.

### Failed and partly built deployments
- An update of a failed deployment builds what is missing (VMs a count
  asks for that don't exist) and runs all the jobs again on the VMs kept,
  not only those the new version adds: a failed deployment's jobs may not
  have run. With the deployment's own inputs, it is a repair: the preview
  is not `unchanged`, and it can be submitted.
- While it runs, the deployment is `updating`. When it succeeds, the
  deployment is ready, of the new version, with the new inputs. When it
  fails, it goes back to the status it had (a failed deployment stays
  failed) and keeps its version and inputs; the request can be retried
  from its failed step, not rolled back.

### Quota and approval
- **Quota checks the net change** of vCPU, memory, storage and VM count:
  a hard limit refuses an update that would go over it, a soft one asks
  for approval, as for a new request. The request reserves what it adds
  while it runs; what it frees is free once the VMs are changed, as the
  quota used follows the VMs.
- **Approval policies apply to updates,** and read what they change:
  `request.kind` (`provision` or `update`), and `update.version_changed`,
  `update.from_version`, `update.to_version`, `update.vcpu`,
  `update.memory_gb`, `update.storage_gb`, `update.vm_count` (the net
  changes, negative when they free), `update.added`, `update.removed`,
  `update.changes` (the kinds of change) and `update.destructive`. On a
  new request, `update` holds zeros and an empty list, so existing
  conditions keep their meaning. A policy that should ask only for new
  requests says `request.kind == "provision"`.
- **Day-2 policies apply to the action `update`** as to any other: who
  may run it, where, and whether it needs approval. Both approvals are
  asked when both apply.
- **Approvers can't change an update's inputs.** Its plan is what the
  requester saw before asking; an approver who disagrees rejects it, and
  the requester asks again.

### The page
- A deployment's **Actions** menu offers **Update**, which opens a page of
  its own: the item's form, started from the deployment's inputs, beside a
  summary that previews, as the inputs change, the changes, those
  refused, the quota after the update and the approval. **Review the
  update** asks in a confirmation that names what it deletes.

## Consequences
- A deployment follows its item: publishing a version no longer leaves
  older deployments behind it, and a cluster is resized by changing a
  number.
- Changes that need a VM rebuilt stay out of updates. They can be added
  later, one at a time, as an explicit `recreate` change marked
  destructive.
- An update's plan is fixed when it is submitted: an approval that takes a
  day doesn't make it change what it does. So VMs can't join a deployment
  while a request on it waits, and an update whose deployment no longer
  has the VMs it was planned on fails before it starts, changing nothing:
  the owner updates it again.
- While an update that adds VMs runs, a VM already built is counted both
  as used and as reserved until the request ends, which errs towards
  refusing another request.
- An onboarded or adopted deployment may have no inputs recorded: an
  update then compares from its version's initial values, so only what
  the user changes from them reaches its VMs. A placement input it never
  recorded (cluster, datastore, network) isn't refused where its VMs
  already are. Its first plan is worth reading against the VMs: rvav
  doesn't know which of their disks are the blueprint's, so it refuses
  to grow or remove them.
- The importer maps vRA's `Deployment.Update` to `update` in day-2
  policies, and says that an approval policy for `Deployment.Create` now
  covers updates too, unless its condition adds
  `request.kind == "provision"`.
- The mock API plans updates as the server does, but asks approval only
  when the update adds VMs, vCPU or memory.
