# ADR-0036: Placement targets, strategies and disk provisioning

- Status: accepted
- Date: 2026-11-18
- Extends the placement rules of [ADR-0020](0020-administration.md), the
  placement of [ADR-0024](0024-multi-vm-blueprints.md) and the constraint
  tags of [ADR-0035](0035-constraint-tags.md). T1.3 (#136); reservations
  across requests, I1 (#189).

## Context
A placement rule names clusters, datastores and networks, and rvav clones
into the cluster's root resource pool, on a host it takes in turn among
those that mount the datastore. When it chooses the cluster, the one with
the most free memory wins. vRA places on resource pools, hosts and
datastore clusters, with a placement policy per project or cloud zone
(default, binpack, spread), and a blueprint says how each disk is
provisioned. The API's description of a rule's clusters said they could
be resource pools, which wasn't true.

## Decision

### Inventory
- The inventory sync reads **hosts** (cluster, connection and maintenance
  state, CPU and memory), **resource pools** below each cluster's root one
  (by path, such as `rvav/dev`, with their CPU and memory limits; vApps
  left out) and **datastore clusters** (storage pods: their datastores,
  free space and whether Storage DRS is on), each with its vSphere tags.
  Tags that couldn't be read keep their last values, as for clusters.
- Administration › Templates and networks › vCenter objects lists them
  with the clusters, datastores and networks (`GET /admin/inventory`).
- The vcsim layout has pools `rvav` and `rvav/dev` on GVA-DEV-CL01 and
  `rvav` and `rvav/prd` on GVA-PRD-CL01, and the datastore cluster
  `dsc-gva-dev-01` of two datastores mounted by GVA-DEV-CL01, which no
  seeded rule allows.

### Where a rule puts VMs
A placement rule may also name:
- **`resource_pool`**: a pool's path below the root pool of each cluster
  it allows. A cluster without the pool isn't chosen, and a bound cluster
  without it is an error. Empty: the root pool, as before.
- **`hosts`**: host name patterns (`esx-gva-dev-0[12]*`). VMs start on the
  connected hosts out of maintenance that match, in turn. Empty: any of
  the cluster's, as before.
- **`datastore_cluster`**: a datastore cluster whose datastores become the
  rule's, narrowed by its datastore patterns and tags when it has any. A
  rule names datastores, datastore tags or a datastore cluster.
- **`strategy`**: how rvav chooses among the clusters left once rules and
  constraints have filtered them, when the blueprint doesn't bind one.

A blueprint or a component may bind `resource_pool`, `host` and
`datastore_cluster` too. The rule stays the outer bound: a bound pool is
the rule's or one below it, a bound host matches the rule's patterns, and
a bound datastore cluster gives only the datastores the rule allows. A
rule's pool and patterns are checked when it is saved: a pool path is
`a/b`, without empty parts.

The administration's placement preview (`PlacementPreview`) says, per
cluster, the pool and strategy, counts only the hosts the rule allows,
and lists as a problem a cluster without the rule's pool, a datastore
cluster vCenter doesn't have, and a cluster none of whose allowed hosts
takes new VMs.

### Strategies
- **`most_free_memory`** (the default): the cluster with the most free
  memory, as before.
- **`binpack`**: the cluster with the least free memory that still has
  room for the component's VMs (their memory), so clusters fill one after
  the other. When none has room, the most free memory.
- **`spread`**: the cluster with the fewest VMs of rvav's deployments not
  deleted, counting the VMs other requests reserved there (#189) and those
  of the request's earlier components; ties go to the most free memory.
- A cluster's free memory is that of its hosts in service (not in
  maintenance or disconnected) that the rule's host patterns allow, when
  the inventory has their memory, else the cluster's, less what other
  requests reserved there; at most the memory limit of the resource pool
  the VMs go in, less what other requests reserved in that pool and those
  below it (#189); less the memory of the VMs the request's earlier
  components placed there (T1.7).
- The strategy is the rule's that allows the candidate clusters; when
  rules of different strategies allow them, the first by priority wins.
- The preview's `placement.strategy` says which strategy chose the
  cluster, and the request form's summary names a strategy other than the
  default. A strategy other than the default adds a note to the timeline's
  placement step ("Binpack chose GVA-DEV-CL01: the least free memory with
  room for 2 VMs of 8 GB"), titled "Placed by rules" alone or "Placed by
  rules and constraint tags". The default adds none, so that requests
  placed the usual way keep their timelines.

### Reservations (#189)
The inventory's free memory doesn't know the VMs of requests not built
yet. So that requests placed at once don't all choose the same cluster,
each VM a request places holds its memory and vCPU on its cluster and
resource pool: a row of `placement_reservations` per VM, by request.
- **Made** in the transaction that records the request, the update
  adding VMs, an approver's edit placing them again, a retry, or a
  subscription's answer moving a VM (ADR-0054); and again, with a fresh
  expiry, when the build starts (`MarkRunning`), after an approval or a
  schedule. Plans without a vCenter in their VMs reserve nothing.
- **Counted** while its request waits (`pending_approval`, `scheduled`,
  `queued`) or runs, until it expires, and until its VM exists: in the
  inventory (`inv_vms`, from the full read or a change notification), or
  recorded as its deployment's VM resource right after the clone. So a
  build that fails, is rolled back, is cancelled or is rejected stops
  counting at once, and a retry counts again. Whether a row counts is
  decided when it is read, so no path that ends a build has to release
  it. Housekeeping deletes the rows that no longer count, every round.
- **Expires** six hours after it was made or its build started: the
  longest a single step may run (an AWX job's timeout). A build still
  running has cloned its VMs well within that; one whose worker or
  workflow is gone stops counting within the day. A request approved
  later than that builds where it was placed and counts again from its
  start.
- **Concurrency**, as quotas (ADR-0047): placement reads the reservations
  without a lock and records, for each cluster a strategy chose, what
  others held there. The transaction then takes a transaction advisory
  lock per cluster its VMs go to (`hashtext('rvav.placement')`, keys in
  order, the last locks it takes), reads them again and, when others hold
  more on a chosen cluster, gives up. The request is then placed again,
  one at a time behind a session lock (`LockPlacement`), so that each
  counts what those before it reserved; after five tries it is a 409.
  More reserved elsewhere doesn't make a chosen cluster overfill, so only
  the chosen clusters are checked; a bound cluster is locked, not
  checked.
- **Seen** in Administration › Templates and networks › vCenter objects:
  a cluster's memory free, and how much of it is held for VMs not built
  yet (`memory_reserved_gb`).

### Host tags
A compute constraint (ADR-0035) is met by a host carrying the tag, or its
cluster: a host's tags are its own and its cluster's. A cluster meets a
compute constraint when one of its usable hosts does, and the VMs start
only on the hosts that meet the component's compute constraints. When
those are fewer than the cluster's usable hosts, the plan names them
(`hosts`), and anti-affinity needs as many of them as VMs. The plan also
names them when a host disconnected or in maintenance at plan time is
one the rule's patterns, a bound host or a constraint leaves out (T1.7):
a plan without `hosts` lets the clone use any host in service by then. Clusters
without hosts in the inventory are matched by their own tags, as before.

### Datastore clusters
- A datastore in a datastore cluster carries the cluster's tags too, for
  storage constraints.
- rvav chooses among the datastore cluster's allowed datastores as among
  any: the roomiest meeting the constraints, which the preview shows.
- **At clone, when Storage DRS is on, rvav asks it**
  (`RecommendDatastores`, a clone placement into the datastore cluster),
  and clones onto the first recommended datastore that is one of the
  allowed datastores meeting the constraints. rvav doesn't call
  `ApplyStorageDrsRecommendation_Task`: it clones as it always does, onto
  the recommended datastore, so the clone keeps its pool, host, folder,
  storage policy and disks. vcsim recommends every datastore of the
  cluster, in order, and has no way to apply a recommendation.
- **When Storage DRS is off, recommends nothing allowed, or fails**, and
  for a content library deploy, rvav clones onto its own choice. The
  clone's log line says which chose.

### Disk provisioning
- `provisioning:` on a blueprint or a component is how its VMs' disks are
  provisioned, the template's included: `thin`, `thick_lazy_zeroed` or
  `thick_eager_zeroed`. A disk's own `provisioning:` wins for that disk.
  Without any, the template's disks stay as they are and an added disk is
  provisioned as the VM's first disk. Until now added disks were always
  thin: from a thick template, they are now thick.
- At clone, the template's flat disks are converted with the relocate
  spec's disk locators (their backing's `thinProvisioned` and
  `eagerlyScrub`, on the VM's datastore and storage policy); added disks
  are created with that backing. vcsim keeps the backing of added disks
  and ignores disk locators, so the conversion of the template's disks is
  checked by a unit test of the locators only.
- A content library OVF is deployed with the matching
  `storage_provisioning` (`thin`, `thick`, `eagerZeroedThick`). A library
  VM template's deploy can't convert disks: its disks stay as they are,
  and only added disks follow `provisioning`.
- The day-2 action Add a disk asks for the provisioning, **As the VM's
  first disk** by default; its op carries `provisioning` only when another
  is chosen.
- A VM's disks (`VmDisk`) say their `provisioning`, read from vCenter.

### The vRA importer
- The import writes no placement rule: rvav's rules name vCenter objects
  vRA's projects don't. The import plan gives each project's
  `placementPolicy` and the strategy that places as it does, for the
  administrator to set on the rvav project's rules: DEFAULT and
  SPREAD_MEMORY are most free memory (the nearest: vRA's lowest memory
  use), SPREAD is spread, and a cloud zone's BINPACK binpack. vRA's
  SPREAD counts machines per zone, rvav's VMs per cluster.
- A disk's `provisioningType` (`thin`, `thick`, `eagerZeroedThick`)
  becomes its `provisioning` (`thin`, `thick_lazy_zeroed`,
  `thick_eager_zeroed`). Another value, or an expression, is reported as
  unmapped: the disk is provisioned as the template's first disk.

## Consequences
- Administrators can carve a cluster into pools per project or
  environment, pin VMs to licensed hosts, use Storage DRS, and fill or
  spread clusters, without blueprint changes.
- Without the new fields, placement is what it was: the root pool, today's
  host choice, the most free memory, and the template's disk format;
  only added disks follow the template's first disk rather than being thin.
- Requests placed before keep their plans: the new plan fields are left
  out when empty, and the workflows read them only when present.
- The preview shows rvav's datastore; with Storage DRS on, the VM may end
  up on another datastore of the same datastore cluster.
- Requests placed at once count each other's VMs (#189): they spread over
  the clusters with room instead of all choosing the one that looked
  freest. Memory still never refuses a placement, it ranks clusters; only
  a subscription moving a VM is refused when the cluster has no room.
- From a VM's clone to the inventory's next full read, at most one sync
  interval, its memory counts neither as reserved nor as used by vCenter;
  and vCenter's used memory is what VMs consume, not their size.
- A pool's limit is compared with what requests reserve and place in it,
  not with what the pool already runs (the inventory reads its limit, not
  its use).
- A reservation left by a build that is gone steers placement for at most
  six hours; of placements, it can refuse only a subscription's move of a
  VM onto its cluster.
