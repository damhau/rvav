# ADR-0054: Subscriptions that change the request

- Status: accepted
- Date: 2026-11-19
- Revisits the blocking subscriptions of
  [ADR-0021](0021-event-subscriptions.md), on the VM topics of
  [ADR-0049](0049-event-topics.md). E1.2 (#162), vRA parity D2.

## Context
ADR-0021 lets a blocking subscription stop a request or set the
deployment's properties, and nothing else: "not inputs, sizes, placement
or names, which policies and quotas already approved". In vRA a blocking
subscription on `compute.allocation.pre` or `network.configure` answers
with outputs that vRA applies: a CMDB names the VM, an IPAM outside vRA
gives its address, a capacity tool places it. A vRA subscription can also
name a recovery workflow, run when its workflow fails.

ADR-0049 gave rvav those topics, without blocking. So a site whose CMDB
names VMs, or whose network team hands out addresses, couldn't move to
rvav without changing that process.

## Decision

### Which topics change what
Blocking is allowed on five topics. `GET /admin/event-topics` gives each
topic's `may_block` and `may_change`:

| Topic | An answer may change |
|---|---|
| `provisioning.pre` | The deployment's `properties` (ADR-0021) |
| `compute.allocation.pre` | The VM's `name` and `placement`: cluster, resource pool, datastore, folder |
| `network.configure` | Its adapters' `networks`: each one's network and IPv4 address |
| `day2.pre`, `delete.pre` | Nothing: continue or stop |

`compute.provision.pre` doesn't block: by then the VM's address is
reserved and its DNS records are made, so a change would mean undoing
them. The size, the template and the disks don't change: the request's
approvals and quotas were decided on them.

### The answer
A webhook answers with JSON, as in ADR-0021, plus `machine`:

```json
{"outcome": "continue",
 "machine": {"name": "web-dev-app742",
             "placement": {"cluster": "GVA-DEV-CL02", "resource_pool": "rvav/dev",
                           "datastore": "nfs-gva-bronze-01", "folder": "web"}}}

{"machine": {"networks": [{"index": 0, "address": "10.31.10.50"},
                          {"index": 1, "network": "dvpg-db-dev-vl320"}]}}
```

- What `machine` doesn't name stays. `index` is the adapter, 0 for the
  first, as in the event's `machine.networks`.
- A field of `machine` rvav doesn't read, or a change the topic doesn't
  allow (a name on `network.configure`), makes the answer malformed.
- An **AWX job** answers with the `rvav_answer` artifact
  (`set_stats: {data: {rvav_answer: {...}}}`), the same object. Without
  it, the job's success continues and its failure stops, as before.
- A **custom workflow** answers with its result, the same object. Its
  context has the event's `machine`.

Each VM the request hasn't built gets its own event and its own answer.
The checks of a topic are a step of the timeline, "Allocation checks" or
"Network checks", with a child step per subscription and VM that says
what changed ("Went on, renaming it web-dev-app742, placing it on
GVA-DEV-CL01, …"). The run keeps it in `changes`. Subscriptions are asked
in priority order, each with the VM as the earlier ones left it.

### rvav checks a change again
A change goes through what checked the request when it was submitted,
before anything of the VM is built, in one transaction that locks the
request:

- **Name.** It keeps the prefix of the project's naming policy and ends in
  digits ([ADR-0046](0046-deployment-names-and-versions.md)). Nobody has
  it: no deployment, no VM in vCenter's inventory, no other VM of the
  request, no DNS record, no address reserved in EfficientIP. The prefix
  is locked as name generation locks it. A single-VM deployment named
  after its VM is renamed with it, and its FQDNs too.
- **Placement.** The environment's placement rule allows the cluster, the
  resource pool, the datastore and the folder
  ([ADR-0036](0036-placement-targets.md)), on the VM's vCenter. The
  datastore has room for its disks and the pool, or the cluster, for its
  memory, as when the request was planned. Moving to another cluster
  checks its networks, its other disks' datastores and its hosts again.
- **Network.** The rule allows it on the VM's cluster, and EfficientIP
  knows its subnet; the adapter takes the subnet's gateway, prefix and DNS
  zone. NSX segments can't be changed.
- **Address.** An IPv4 address of the adapter's subnet with EfficientIP,
  not its network, broadcast or gateway address, that nobody has: no
  other VM of the request, no deployment rvav reserved it for, nothing in
  EfficientIP. rvav reserves it when it reserves the others, with
  `ReserveAddress` instead of the next free one. One taken in between
  fails the build, outside the subscription's recovery, which is past;
  the address stays in the plan, so a Retry fails the same way, and a
  new request asks again.

Approval policies and quotas aren't asked again: they decided on the
item, its inputs, its size and its environment, which don't change.

### A refused change fails the subscription
A change rvav refuses is a failure of the subscription, as a timeout or
a malformed answer is. Its recovery target is told, then `on_failure`
decides:

- `stop_request` (the default) fails the request: "CMDB naming asked for
  a change rvav refuses: web-dev-app003 is taken." Nothing of the VM was
  built.
- `continue_request` lets it go on with the VM as it was.

Ignoring a refused change by default would build a VM other than the one
the CMDB recorded, and nobody would know. vRA also fails a request whose
subscription returns an output it can't apply.

### Recovery
A blocking subscription may name a **recovery target**: a webhook, an AWX
template or a custom workflow, as its target. When the subscription
fails, rvav tells it once, with the event and
`"recovery": {"reason": "cmdb.corp.example answered 503", "run_id": "run_…"}`,
as a run of the subscription with `recovery` true and a step "Recovery"
under the subscription's.

- If it answers with an outcome, `{"outcome": "stop", "reason": "…"}` or
  `{"outcome": "continue"}`, that decides.
- Otherwise, or if it fails too (an AWX job that fails among them),
  `on_failure` decides.
- Its `properties` and `machine` are ignored: a recovery reports and
  decides, it doesn't answer for the subscription.
- A subscription that stops the request on purpose hasn't failed: its
  recovery isn't told.

A recovery target is for blocking subscriptions only. Its timeout is the
subscription's.

### Retries
A request retried after a failure asks again only what wasn't answered:

- A VM's check step that is done is skipped. A VM's answer is found by
  its deployment, component and index, so a renamed VM isn't asked again.
- A run is unique per subscription, event and recovery: a retry reuses it.
- What a change did is in the plan, so a rollback and a retry keep the
  name, the placement and the address. A re-planned step whose
  subscriptions answered before is closed: "Its subscriptions answered
  when the request ran before: what they changed is kept."
- Workflows that started before this don't ask (`workflow.GetVersion`,
  `machine-checks`).

### rvav's own failures
A failure of rvav's while it asks (a worker restarted during a deploy,
the database out of reach, a lock timeout while it applies a change) is
not the subscription's (E1.6): Temporal tries the check again, with the
activities' retry policy, and neither the recovery target nor
`on_failure` decides.

- The run's `delivery` names the workflow activity asking. A try again
  of the same activity gets the run it finished as it was: a stop, a
  failure (then its recovery, picked up the same way) or an answer taken.
  The target isn't asked again.
- A run the activity hadn't finished is delivered again under its
  `run_id`: targets are asked at least once, and give the same answer to
  the same run.
- A run counts each call in `attempts` as the call begins, so a call whose
  answer was lost counts too (#196). Starting the run again keeps that
  call in its history as failed: "Interrupted before its answer was
  recorded." Housekeeping fails a run with that reason when it is still
  delivering an hour after its current call began, however old the run
  (#198).
- A VM's change rvav applied before the worker stopped leaves the plan
  changed and the workflow's VM not: the check fails, and a Retry of the
  request builds the VM as changed.
- An undecided recovery run keeps no `outcome`: "continue" was what
  rvav did, not what it answered.
- Workflows that started before keep failing on one
  (`workflow.GetVersion`, `check-retries`).

## Consequences
- A CMDB, an IPAM or a capacity tool outside rvav can name, address and
  place VMs, and vRA subscriptions that did so map to rvav ones.
- rvav still owns what it checks: an answer can't name a VM outside the
  project's policy, or put it where the rule doesn't allow it.
- A blocking subscription per VM topic adds its timeout, and its
  recovery's, to each VM's build.
- Not done:
  - A blueprint's constraint tags ([ADR-0035](0035-constraint-tags.md))
    aren't checked again on a move: the plan doesn't keep them.
  - The free memory of a cluster a VM moves to doesn't count the other
    VMs of the request planned there.
  - A naming prefix that ends in digits is matched by its letters: every
    prefix either name could have is locked.
