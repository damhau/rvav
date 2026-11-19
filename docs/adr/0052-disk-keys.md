# ADR-0052: Keys for a blueprint's extra disks

- Status: accepted
- Date: 2026-11-19
- Extends the deployment updates of
  [ADR-0042](0042-deployment-updates.md) and the blueprint of
  [ADR-0024](0024-multi-vm-blueprints.md). O1 (#191).

## Context
An update compares the deployment's version with the new one disk by
disk. A blueprint's extra disks had no names, so rvav matched them by
place: the n-th extra disk of the old version was the n-th of the new.
A version that removed the second of three disks and grew the third was
read as growing the second and removing the third: the wrong disk would
lose its data. rvav refused such a version, and the administrator had to
remove the disks in one version and change the others in the next.
Adapters had keys already (`nics[].key`), and an update matched them by
key.

## Decision
- **A blueprint's extra disk takes an optional `key`**, as an adapter
  does: up to 16 lower-case letters and digits, starting with a letter,
  unique among the component's disks. Saving refuses a key that doesn't
  fit (`pattern`) or one used twice (`duplicate`), at `disks/<n>/key`.
  The first disk, the template's, has none.
- **When every extra disk of both versions has a key, an update matches
  the disks by key.** A key only the old version has is a disk removed,
  a key only the new one has a disk added, wherever they are. The refusal
  of removals mixed with changes doesn't apply.
- **Otherwise the disks match by place, as before,** keys or not, and
  the refusal stays. A version that keys only some disks, or the first
  version with keys after one without, matches by place: giving the
  disks keys changes nothing on the VMs.
- **The record on a VM's disk keeps the key** (`blueprint_disk_key`,
  beside `blueprint_disk`), from the build and from an update's
  `disk_add`. Matching by key finds the VM's disk by that key; a disk
  recorded before its version had keys is found by its place in that
  version.
- **An update that succeeds records the places and keys its version
  gives the disks it kept** (`disk_marks` in its plan, written with the
  deployment's new version). Until then the VM's record follows the
  deployment's version, which a failed update leaves, so the update made
  again reads the same record. The record of a disk the update added
  carries its new place and key from the start: matching by key finds
  it, and matching by place puts it after the old version's disks.
- **The vRA importer keys each disk by its vRA resource name**
  (`resourceKey`, as segments and groups are), so imported items match
  disks by key from their first version.

## Consequences
- An administrator removes some disks and changes others in one version
  once every disk has a key. Adding keys to an existing item takes one
  version with nothing else changed on the disks.
- Removing a key from a disk, or giving a disk another key, while every
  disk of both versions has one, removes the disk and adds a new one.
  The preview marks the removal as destructive.
- A plan made before this change has no `disk_marks`: its update records
  nothing more, as before. Workflow code doesn't change.
- No migration: the record is in the VM resource's properties.
