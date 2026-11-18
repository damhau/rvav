# ADR-0035: Capability and constraint tags

- Status: accepted
- Date: 2026-11-18
- Extends the placement of [ADR-0024](0024-multi-vm-blueprints.md) and the
  placement rules of [ADR-0020](0020-administration.md). T1.2 (#135).

## Context
Placement rules (ADR-0020) say which clusters, datastores and networks a
project may use in an environment, by name pattern or by vSphere tag. When
a blueprint doesn't bind its cluster or datastore, rvav picks the allowed
cluster with the most free memory and its roomiest datastore (ADR-0024).
A blueprint can't say what its VMs need from those: a database on gold
storage, a web server outside the PCI zone, a network in the DMZ. vRA says
it with constraint tags on machines, disks and networks
(`constraints: [{tag: 'env:prod'}]`), matched against capability tags on
compute, storage and networks. The vRA importer (ADR-0027) turned them into
form inputs, which put the choice on the requester.

## Decision

### Capability tags
- **The tags are vSphere's.** The inventory sync reads the tags of
  clusters, datastores and networks (port groups and NSX segments), as
  `category/tag`, through the tagging service. rvav keeps no tags of its
  own: an administrator tags vCenter objects in the vSphere Client. Host
  tags aren't read: hosts aren't in the inventory, and placing on a host is
  T1.3's (#136). [ADR-0036](0036-placement-targets.md) ("Host tags") has
  since put hosts in the inventory with their tags: a compute constraint is
  met by a host carrying the tag or by its cluster.
- Administration › Templates and networks › vCenter objects lists each
  cluster, datastore and network with its tags (`GET /admin/inventory`).

### Constraints in a blueprint
```yaml
constraints:
  compute: [rvav-env/prod, "!rvav-zone/pci"]
  storage: ["rvav-backup/${inputs.backup}", rvav-tier/platinum:soft]
  network: ["!rvav-zone/dmz"]
```
- **An entry is `[!]category/tag[:hard|:soft]`**: the tag as the inventory
  writes it, as placement rules' tags are. `!` rules out what carries the
  tag. Hard is the default. `category:tag`, vRA's way, is refused at save
  with a message saying rvav's, so that a key-value pair isn't read as a
  category named after the key.
- **`compute`** chooses the cluster, **`storage`** the datastore, and
  **`network`** the network of the first adapter.
- **Where:** the blueprint's apply to every component; a component's own
  replace them kind by kind (`storage: []` removes the blueprint's). An
  added disk's `constraints: [...]` are its own storage constraints.
- **CEL:** an entry may hold `${...}`, worked out at submit with the
  bindings' variables (`inputs`, `sizes`, `segments`) plus `environment`
  (its key) and `project`. An entry that is only `${...}` may give a list
  of entries; an empty string gives none. A constraint that can't be
  worked out from the inputs is an error at submit, not in a preview while
  inputs are missing.

### Matching
Constraints filter inside what placement rules allow: the rules stay the
outer bound, and a constraint never reaches a cluster, datastore or network
they don't allow.

- **Choosing a cluster** (none bound): among the allowed clusters with the
  template, hard compute constraints keep those that meet them, in order.
  Then those with an allowed datastore meeting the hard storage constraints
  (the VMs' and each disk's that rvav places), and, when rvav chooses the
  network, an allowed network with a subnet meeting the hard network
  constraints. Then room, hosts and the bound network, as before. Soft
  compute constraints keep those meeting them when any does, and are let
  go otherwise. The most free memory wins.
- **Choosing a datastore** (none bound): the allowed datastores of the
  cluster with room, kept by the hard constraints, narrowed by the soft
  ones when any meets them; the roomiest wins.
- **Choosing a network**: only when the first adapter's network isn't
  bound and network constraints exist. The first allowed network, by name,
  with an EfficientIP subnet that meets them. A segment made for the
  deployment (ADR-0028) has no tags: network constraints don't apply to it.
- **A disk with its own constraints** and no datastore goes to the
  datastore they choose; the other disks stay on the VM's datastore, which
  is then sized without it.
- **What the blueprint or the form binds** is checked: a hard constraint it
  doesn't meet is an error on its field ("Cluster GVA-PRD-CL02 is tagged
  rvav-zone/pci, which this item rules out."); a soft one is noted.
- **A hard constraint nothing allowed meets** fails the request at submit,
  before anything is made, saying which constraint ("This item needs a
  datastore tagged rvav-backup/weekly, and no cluster allowed here has
  one."). The preview says it first.
- Without constraints, placement is what it was.

### What the requester and the timeline see
- The preview's `components[].placement.constraints` lists, per
  constraint, its kind, tag, negation, softness, the object placed on,
  whether it meets it, and the allowed objects it ruled out (or passed
  over). The request form's summary turns each into a line.
- The plan keeps a note per component ("Placed by rvav-env/prod,
  !rvav-zone/pci; soft constraint rvav-tier/platinum not met: placed on
  nfs-gva-silver-02 without it"). The timeline shows it as a done step,
  "Placed by constraint tags", after "Submitted". An approver's edit that
  places the VMs again rewrites it.

### Saving a blueprint
- Entries are checked at save: the syntax, `${...}` CEL that compiles.
- **A tag no object of the inventory carries is a warning, not an error**
  (`warnings`, code `unknown_tag`, on the version): the tag may not be
  made yet, or the inventory not synced. A kind the inventory has nothing
  of isn't checked, and entries holding CEL are worked out per request.

### The vRA importer
vRA's constraints on machines, disks and networks become rvav's:
`key:value` becomes `key/value`, `:hard`, `:soft` and `!` are kept, and
`${input.x}` becomes `${inputs.x}` (`${input.environment}`,
`${environment}`). A machine's are its `compute` constraints, its
`storage.constraints` its `storage` ones, an attached disk's the disk's
own, and its first adapter's existing network's its `network` ones.

- A machine with constraints is placed by rvav: the item no longer asks
  for its cluster, datastore and network, as the importer did before.
- When its first adapter is on an existing network without constraints,
  rvav can't choose that network. The item keeps asking for all three,
  checked against the constraints, and the import says so (a warning).
- An on-demand network's constraints chose vRA's network profile; rvav's
  placement rule says where its segment goes, so they aren't mapped.
- A key-only tag, a tag that is one expression (vRA's `key:value` at
  request time), and a tag reading a machine are listed as not mapped.

## Consequences
- Blueprints say what their VMs need, and administrators say what vCenter
  offers, with vSphere's own tags; a new cluster tagged right is used
  without editing blueprints or rules.
- Placement is still deterministic: the same inventory and inputs give the
  same placement.
- A tag removed in vCenter makes requests with a hard constraint on it
  fail with a message naming the tag; saved blueprints aren't refused.
- Hosts, resource pools, datastore clusters and placement strategies are
  T1.3's; constraint tags per image, T1.4's.
