# ADR-0049: A VM's, an approval's and a project's events

- Status: accepted
- Date: 2026-11-19
- Extends the topics and the event of
  [ADR-0021](0021-event-subscriptions.md). E1.1 (#161), vRA parity D1.

## Context
ADR-0021 has nine topics, each about a request or a lease. vRA's event
broker also has a VM's own: `compute.allocation.pre` before it is placed
and named, `network.configure` before its addresses are reserved,
`compute.provision.pre` and `.post` around its cloning, and
`compute.removal.pre` and `.post` around its deletion, one event for each
VM. A vRA subscription that registers each VM in the CMDB had to become an
rvav subscription on `provisioning.post` that loops over `resources`, and
the vRO triage said so ("rvav's events are per request, vRA's compute
events per machine").

An ITSM or a CMDB also wants to hear when a request waits for someone's
approval, and when a project is made or changed. rvav has no event for
either. Whether vRA has topics for them is unsure (vra-parity.md, D1), so
rvav names its own.

## Decision

### Topics
In the order of a deployment's life, then a project's:

| Topic | Group | When | May block |
|---|---|---|---|
| `request.submitted` | request | ADR-0021 | No |
| `approval.requested` | approval | A level opens, and again once it is escalated | No |
| `approval.decided` | approval | A level is approved or rejected: by someone, at its timeout, or because nobody can decide it | No |
| `provisioning.pre` | request | ADR-0021 | Yes |
| `compute.allocation.pre` | machine | Each VM, once the request is past its checks and before anything is built: its placement and names as planned | No |
| `network.configure` | machine | Each VM, once its deployment's NSX segments have their subnets and before its addresses are reserved | No |
| `compute.provision.pre` | machine | Each VM, its addresses reserved, as it is about to be cloned | No |
| `compute.provision.post` | machine | Each VM, cloned and powered on, before its guest configuration (AWX jobs) | No |
| `provisioning.post` | request | ADR-0021 | No |
| `day2.pre`, `day2.post` | request | ADR-0021 | `day2.pre` |
| `delete.pre` | request | ADR-0021 | Yes |
| `compute.removal.pre` | machine | Each VM, as it is about to be deleted | No |
| `compute.removal.post` | machine | Each VM, deleted | No |
| `delete.post` | request | ADR-0021 | No |
| `lease.expiring`, `lease.expired` | lease | ADR-0021 | No |
| `project.created` | project | An administrator creates a project | No |
| `project.updated` | project | A project's settings change, its deployment sharing too, or the vRA import adds properties to it | No |

`GET /admin/event-topics` gives each topic's group, so the subscription
form lists them under their group. Blocking stays on `provisioning.pre`,
`day2.pre` and `delete.pre`: a subscription that changes a VM's name,
addresses or placement on its own topics is E1.2's (#162).

### A VM's events
Their data is the request's (ADR-0021), with `deployment` the VM's, and
`machine`:

```json
"machine": {
  "name": "web-dev-app001", "fqdn": "web-dev-app001.corp.example", "component": "app", "index": 0,
  "deployment_id": "dep_…", "resource_id": "res_…", "moref": "vm-1042", "address": "10.31.10.57",
  "template": "rhel9-template", "vcpu": 4, "memory_gb": 16, "disk_gb": 80,
  "placement": {"vcenter": "vc-gva-01", "cluster": "cl-gva-dev-01", "resource_pool": "", "hosts": [],
                "datastore": "ds-gva-dev-01", "datastore_cluster": "", "folder": "rvav/web"},
  "networks": [{"index": 0, "key": "", "network": "pg-web-dev", "segment": "", "ipam": "efficientip",
                "subnet": "10.31.10.0/24", "gateway": "10.31.10.1", "prefix_length": 24, "subnet6": "",
                "dns_zone": "corp.example", "fqdn": "web-dev-app001.corp.example", "address": "10.31.10.57"}],
  "tags": ["env/dev"], "properties": {"cost_center": "CC-4120"}
}
```

- `address` and each network's `address` (and `address6` with IPv6) are
  null until reserved: on `compute.allocation.pre` and `network.configure`.
  With DHCP, `compute.provision.post` has the address the guest reported.
- `moref` and `resource_id` are null until the VM is cloned: they are set
  from `compute.provision.post` on.
- The removal events describe the VM from its record: where it is now,
  which may differ from where it was built, and its primary IPv4
  address.
- An update ([ADR-0042](0042-deployment-updates.md)) that adds VMs has the allocation, network and
  provision events of each new VM; one that removes VMs has their removal
  events. A rollback after a failure has the removal events of each VM it
  deletes. A multi-VM item and a composite's VM step have them as a
  single-VM item does.

### An approval level's events
Their data is the request's, with `approval`:

```json
"approval": {
  "id": "apr_…", "name": "Production change", "level": 1, "kind": "policy", "mode": "any",
  "reasons": ["Production: every request needs approval"], "roles": [], "manager": false,
  "groups": ["rvav-infra-leads"], "approvers": [{"id": "usr_…", "name": "…", "email": "…"}],
  "escalated": false, "status": "pending", "due_at": "2026-11-21T10:00:00Z",
  "decided_at": null, "outcome": null, "decided_by": null, "comment": null
}
```

- `kind` is `policy` for an approval policy's level, `workflow` for a
  custom workflow's `RequestApproval` ([ADR-0022](0022-custom-workflows-and-composites.md)). A composite step that
  asks someone ([ADR-0033](0033-composite-step-kinds.md)) has no approval events: its step's are in the
  timeline.
- `approvers` are the people named on the level, by name; `groups` and
  `roles` are who else may decide it.
- On `approval.decided`, `status` is `approved` or `rejected`, and
  `outcome` says why. `decided_by` is the person who decided, with their
  `comment`, or null when rvav did: at the level's timeout, or because
  nobody can decide it.
- An escalated level ([ADR-0011](0011-approvals.md)) has a second
  `approval.requested`, with `escalated` true and the escalation's
  approvers.
- A level cancelled with its request, or with the workflow run that asked,
  has no event.

### A project's events
They aren't about a request: `request`, `item`, `requester`, `owner`,
`vm` and `deployment` are null, `environment` is empty, `deployments` and
`resources` are empty lists. Their data has:

```json
"project": {
  "id": "prj_…", "code": "web", "name": "Web Platform", "description": "…", "cost_center": "CC-4120",
  "environments": ["dev", "prd"], "properties": {"cmdb_ci": "CI-0042"}, "archived": false,
  "deployment_sharing": "all_members", "admins": [{"id": "usr_…", "name": "…", "email": "…"}]
},
"actor": {"id": "usr_…", "name": "Marc Keller", "email": "…"},
"changes": ["cost_center"]
```

- `changes` names the settings that changed: `name`, `description`,
  `cost_center`, `environments`, `properties`, `archived` (archiving and
  unarchiving) or `deployment_sharing`. It is empty on `project.created`.
  An update that changes nothing has no event.
- `actor` is who made the change, or null for rvav.
- Members joining or leaving don't make an event: a project's members
  come from its groups too, and change in the identity provider.

### Conditions
A subscription's condition may read `machine`, `approval`, `actor` and
`changes`. On a topic whose events don't have one, a condition that reads
it is refused when the subscription is saved ("the event has no machine:
only a VM's topics, compute.* and network.configure, have it").

### Writing them
- Each event is written in the transaction of the change it describes,
  as ADR-0021's are: the level opened or decided, the project saved.
  `store.CreateProject` and `store.UpdateProject` take the caller's
  transaction for that.
- A VM's provision and removal events are written by the activities that
  clone, power on and delete it. The allocation and network events are
  written by an activity of their own, which workflows that started before
  it existed don't run (`workflow.GetVersion(ctx, "machine-events", …)`):
  their histories replay as they ran (`TestReplayHistories`).
- Each event is written once for each run of the request's workflow: the
  dedupe key names the request, the topic, the workflow's run and the VM
  or resource. A retried request skips the allocation and network events
  of the VMs an earlier run built and the retry keeps. An approval level
  has one event for each topic, and one more `approval.requested` once
  escalated.

### The vRA import
The vRO triage ([ADR-0027](0027-vra-importer.md)) maps vRA's VM topics to rvav's of the same
name, `deployment.request.*` to `provisioning.*`, and its day-2 topics to
`day2.*`. A blocking vRA subscription on a topic rvav doesn't let block is
reported as such: its rvav subscription doesn't hold the request.

## Consequences
- A per-VM integration (CMDB, backup, monitoring) moved from vRA keeps its
  shape: one call for each VM, with that VM's placement, addresses and
  moref.
- A request has more events: two for each VM before it is built, two
  after, two at its removal. Subscriptions see only their topic's, and the
  dispatcher handles them as it does the others.
- Within `rvav.event.v1`, `machine`, `approval`, `actor` and `changes` are
  fields added to `data`: receivers that ignore them are unaffected.
- E1.2 can let a blocking subscription change a VM on its own topics. The
  `subscriptions` table allows blocking only on `*.pre` topics, so letting
  `network.configure` block takes a migration; `outbox.MayBlock` and the
  web mocks' list change with it.
