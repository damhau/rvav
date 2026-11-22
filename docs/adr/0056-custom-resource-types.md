# ADR-0056: Custom resource types

- Status: accepted
- Date: 2026-11-19
- Builds on the custom workflows and composites of
  [ADR-0022](0022-custom-workflows-and-composites.md), the multi-VM
  blueprints of [ADR-0024](0024-multi-vm-blueprints.md), the updates of
  [ADR-0042](0042-deployment-updates.md) and the event topics of
  [ADR-0049](0049-event-topics.md). E1.4 (#164), vRA parity C27.

## Context
vRA's custom resource types put what vRA doesn't build itself, such as a
load balancer, a DNS alias or a database, in a deployment beside its
machines: typed, with their own properties and day-2 actions, made by a
vRO workflow or an ABX action and deleted by another with the
deployment. rvav ran custom workflows and AWX templates as catalog items,
day-2 actions, subscription targets and composite steps with an undo,
but a deployment's resources were rvav's own (VMs, addresses, DNS
records, NSX objects, DRS rules). What a job made elsewhere left no trace
in the deployment, nothing deleted it with the deployment, and the vRA
importer left `Custom.*` resources out.

## Decision

### A resource type
Administrators define resource types under **Administration › Resource
types** (`/api/v1/admin/resource-types`):

| Field | What it is |
|---|---|
| `key` | Its name in blueprints, `^[a-z][a-z0-9-]{1,40}$`, set once |
| `title`, `description` | What people see |
| `create`, `delete` | Required: what makes a resource and what deletes it |
| `update`, `read` | Optional: what changes a resource's inputs in place, and what reads its properties again |
| `property_schema` | A JSON Schema of an object: the properties `create` returns. Its titles label them on the deployment's page |
| `actions` | Day-2 actions on its resources: `wf.<key>` or `awx.<template>` |
| `enabled` | Off, it can't make or update resources |

Each step is a driver, `{kind, name}`: a custom workflow registered with
the usage `resource_type` (`kind: workflow`), or a job template AWX has
(`kind: awx`). One type may mix them. rvav checks the names when the type
is saved: the workflow must be registered for that use and on, the
template known to AWX when rvav has an AWX connection.

Reading types needs `extensibility.read` (administrators and auditors),
changing them `extensibility.manage` (administrators). Changes are
audited (`resource_type.create`, `resource_type.update`,
`resource_type.delete`). A type that deployments have resources of, or
that a draft or published version names, can't be removed: the 409 says
to turn it off instead. A changed type applies to the resources made
before: they keep their properties, and its workflows and templates as
they are now update, read and delete them. A change replaces the type:
a field left out takes its default, `enabled` too, which is true (to keep
a type off, send `false`), and a field the type doesn't have is refused
(E1.6).

A type turned off refuses the requests and the updates of the items
that use it ("Load balancer is turned off: ask an administrator to turn
it on."), and publishing a version that names it. So does a `create`
workflow that is off, no longer registered for resource types, or
offered to other projects only ("The custom workflow lb-create isn't
offered to this project."). A create step that reaches any of these
after its request was accepted fails; `update`, `read` and `delete`
still run, so what was made can be deleted. Its resources keep
their day-2 actions and are deleted as before. A draft may name a type
that doesn't exist yet; publishing needs it to exist and be on
(`unknown_resource_type`, `disabled`).

### In a blueprint: `resources`
A VM blueprint with components lists the custom resources of its
deployment:

```yaml
components:
  - key: web
    count: 2
resources:
  - key: lb
    title: Load balancer
    type: load-balancer
    with:
      members: components.web.addresses
      port: 443
```

- `key` is a component key (up to 16 lower-case letters and digits,
  starting with a letter), unique among components and resources: it
  goes into the resource's name. `title` defaults to the type's.
- `with` binds the inputs of the type's `create`. A string is CEL over
  `inputs`, `environment`, `project`, `propgroup`, `deployment` (`id`,
  `name`), `components` and `resources`; another value is taken as it is.
- `components.<key>` is what that component built, as for references
  between components: `count`, `names`, `fqdns`, `addresses` and `vms`
  (`name`, `fqdn`, `address`, `index`). `resources.<key>` is the
  properties of a resource listed before it.
- The resources are made once every VM is built, one at a time in list
  order, each a step `resource:<key>` of the request ("Custom resource ·
  load-balancer"). Their inputs are worked out when their step runs, so
  they read the VMs' addresses.
- Only a blueprint with components has them: it makes one deployment,
  where a blueprint without components makes one per VM when a request
  asks for several. A composite's `resource` step covers the others.
- A resource has no `when` and no `count`: each entry is one resource,
  always made.

Saving refuses resources in a blueprint without components, a key
already taken, a missing type, CEL that doesn't compile, and a reference
to a component that doesn't exist or to a resource not listed before.

### In a composite: the `resource` step
A composite step of kind `resource` makes a custom resource:

```yaml
  - key: lb
    resource: {type: load-balancer, deployment: web}
    with: {members: "[steps.web.ip]", port: 443}
```

- Its `with` is any composite step's: CEL over `inputs` and earlier
  steps' outputs. Its title defaults to the type's.
- It joins the deployment of a VM step in an earlier group: the one
  `resource.deployment` names, else the only one. A request is refused
  when there is none, when several come before it and it names none, or
  when it names a step that isn't a VM step before it.
- It has no `undo`: a custom resource is undone by its type's delete.
  When the request fails, the rollback deletes it.
- It may have a `when`, and may sit in a parallel group or a branch.
- Later steps read its properties as `steps.<key>.<property>`.
- An update of the deployment it joined leaves it as it is: the VM
  item's blueprint doesn't list it. Deleting that deployment deletes it.

### What the drivers get and give
A step of a resource's life is a step of the request that runs it: a
new request, an update, a day-2 action, a deletion or a rollback.

- **Inputs:** `create` and `update` get the inputs `with` binds. `delete`
  and `read` get the resource's properties.
- **A create that fails part way:** the resource is recorded, marked
  `creating`, before its create runs, and the mark goes once create
  returns. One whose create failed keeps it: what it made, if anything,
  isn't known. Its `delete` then gets `resource.creating` true, no
  properties and the create's inputs as `resource.inputs`, and must
  delete what create may have made from those, succeeding when there is
  nothing (E1.6). A retried create reuses the resource's ID.
- **A custom workflow** starts as a child workflow, as any custom
  workflow step does. Its context has `inputs` and `resource`: `id`,
  `type` (`key`, `title`), `key`, `name`, `deployment_id`, `phase`
  (`create`, `update`, `read` or `delete`), `inputs` and `properties`.
  The Go SDK has it as `rvavflow.Context.Resource`, the Python SDK as
  `Context.resource`. Its result, a JSON object, is the resource's
  properties.
- **An AWX template** is launched with the inputs as extra variables,
  and `rvav`: `request_id`, `request_number`, `project`, `environment`,
  `requester`, `deployment_id`, `deployment` and `resource`, as above.
  It runs on the template's own inventory, with no limit: the resource
  isn't a host of rvav's inventory. The job's artifacts, what its
  playbook gave with `set_stats`, are the resource's properties.
- **Its name** is the result's `name`, else the `name` input, else
  `<deployment>-<key>`.
- An `update` whose inputs come out as they were doesn't run ("Its
  inputs are the same."). A type without the step skips it ("Load
  balancer has no read step.").

rvav keeps the resource with the type `custom`, beside the deployment's
VMs: its type's key and title, its key, what made it (the blueprint or
a composite), the inputs its create or last update got, and its
properties with their titles.

### Events: `custom.create.*` and `custom.delete.*`
Four topics, in the group `custom`, one event per resource:

| Topic | When |
|---|---|
| `custom.create.pre` | A custom resource is about to be made, its inputs worked out |
| `custom.create.post` | It is made, with the properties its type's create gave |
| `custom.delete.pre` | It is about to be deleted: by a deletion, an update that removes it, or a failed request's rollback |
| `custom.delete.post` | It is deleted |

The event is a request's, as for the other topics, with the resource in
`data.custom_resource` (as its drivers see it) and its deployment in
`data.deployment`. A subscription condition that reads `custom_resource`
on another topic is refused.

These are new topics rather than E1.1's VM topics (`compute.provision.*`,
`compute.removal.*`, [ADR-0049](0049-event-topics.md)): those events are
a VM, `data.machine`, with its placement, networks and addresses, which
subscriptions written for VMs read, and which a blocking answer on
`compute.allocation.pre` or `network.configure` changes
([ADR-0054](0054-subscriptions-that-change-the-request.md)). A custom
resource has none of that, and a subscription on `compute.provision.post`
must not start getting load balancers. A subscriber picks custom
resources by topic, and a type by condition
(`custom_resource.type.key == "load-balancer"`).

They don't block: a subscription that must hold a request does so on
the request's `provisioning.pre`, `day2.pre` or `delete.pre`. `update`
and `read` have no topic. They run in a day-2 request or a deployment's
update, whose `day2.pre` and `day2.post` events a subscriber already
gets, with the deployment and the resource a day-2 action is on; a
resource's properties after are read from the API. E1.6 left it so: `custom.update.*` and `custom.read.post`
would be new topics of the catalogue (ADR-0049), its enum and the
designer's lists, for what no subscriber has asked for yet.

### Day-2 actions
The actions a type lists are offered on each of its resources:
- An action offered elsewhere, on VMs or deployments, is offered on the
  resources too.
- A custom workflow registered and on as a day-2 action, but not
  offered, is offered only on them. An AWX action is offered on the VMs
  or deployments it is set for as well; one that is off, or whose
  template AWX no longer has, is offered nowhere.
- A workflow action gets the resource in its context's `resource`, an
  AWX action in `rvav.resource`, with the `phase` `day2` (its
  `rvav.resources`, the VMs, is empty). An AWX action on a custom resource runs
  on its template's own inventory with no limit, and without refreshing
  rvav's inventory: there is no VM to target or to be powered on.
- When the type has `read`, it runs after each action
  (`custom.read@<id>`): what it returns replaces the properties.

Day-2 policies decide who runs them, as for any action. The deployment's
page lists the custom resources on its Overview, with their type, key
and titled properties, and their actions under **Custom resources** in
the **Actions** menu and in each resource's menu.

### Deletion and rollback
- Deleting a deployment deletes its custom resources first, after the
  lease check and before the DRS rules and the VMs, which they may use.
  Each is a step `custom.delete@<id>` that runs its type's `delete`.
- When a request fails and rolls back, its custom resources are deleted
  first, the last made first, then the rest of the rollback runs. Each is
  a step `rollback:resource:<id>`, "Delete <name>". A composite without
  `rollback_on_failure` keeps them, as it keeps its VMs, until someone
  rolls the request back.
- A delete that fails in a rollback stops it before the VMs, which the
  resource may use: the request's `rollback` step is "Roll back failed"
  with each resource and why, the deployment stays `failed`, and its
  owner isn't told it was rolled back. Another rollback, or the
  deployment's deletion, runs the deletes again; a rollback that then
  finishes writes the step again as "Rolled back" (E1.6, behind
  `rollback-stops`).

### Updates
An update ([ADR-0042](0042-deployment-updates.md)) compares the
blueprint's resources with those the deployment has, by key:

| Change | Kind | What runs |
|---|---|---|
| A resource the new version adds | `resource_add` | Its type's `create` |
| A kept resource whose inputs may change | `resource_update` | Its type's `update` |
| A resource the new version drops | `resource_remove` (destructive) | Its type's `delete` |

- A kept resource's inputs may change when its `with` changes, an input
  it reads changes, it reads `components` and VMs are added or removed,
  or a resource it reads is added or updated. Otherwise its `update`
  doesn't run.
- A resource whose inputs may change, of a type without `update`, refuses
  the update: "lb's inputs change, but Load balancer has no update step:
  ask an administrator to give it one, or request a new deployment."
- A resource whose type changes is removed and added: the new one is
  made before the old one is deleted.
- Order: the changes to existing VMs, the VMs added, the resources made
  or updated, then the removals, custom resources before VMs, then the
  jobs.
- Approval policies read the new kinds in `update.changes`, and
  `update.destructive` is true when a custom resource is deleted.

### Designer and topology
- `GET /admin/catalog/composite-steps` lists each type that is on as a
  target of kind `resource`: its inputs from its create workflow's input
  schema or its template's survey, its outputs from the property
  schema's properties, and open outputs, since a create may return more.
- The composite designer offers them under **Custom resources**, with no
  undo editor for such a step, and a **Deployment** field naming the VM
  step it joins when several come before it. The canvas draws a
  resource step with a **Resource** badge.
- A requester's topology of a version keeps its resources' key, title
  and type, and of their `with` only the `components.<key>` and
  `resources.<key>` references, so it shows what each is made after
  without the expressions; of a composite's resource step, its type and
  deployment.
- A deployment's **Topology** draws each custom resource as a node,
  made after the components and resources its inputs read.

### The vRA import
A cloud template's `Custom.*` resource is reported as unmapped: "… is a
vRA custom resource: it needs an rvav resource type (Administration >
Resource types) with the custom workflows or AWX templates that make and
delete it. Add one, then add the resource to the blueprint's
resources." Its vRO workflows and ABX actions aren't imported.

### Workflow versioning
The request workflow's new commands, making a blueprint's resources, a
composite's resource step and the rollback's deletes, sit behind
`workflow.GetVersion(ctx, "custom-resources", DefaultVersion, 1)`.
Requests started before replay as they ran; `TestReplayHistories`
replays them.

## Consequences
- A deployment holds what its custom workflows and AWX templates made,
  typed and with properties, and deletes it with itself, by the type's
  own delete.
- One type serves blueprints, composites and day-2 actions: the
  workflows and templates are registered once, and a change to them
  reaches every resource of the type.
- vRA's custom resources have a target: a type with the same workflows
  rewritten for rvav, or AWX templates.
- Limitations:
  - Only a blueprint with components lists resources; one resource per
    entry, with no `when` and no `count`.
  - The `custom.*` topics don't block, and `update` and `read` send no
    event.
  - rvav doesn't check a resource's inputs against its workflow's input
    schema: they are worked out when the step runs. The workflow, or
    AWX's survey, checks them.
  - An update leaves the resources a composite made as they are.
  - An AWX driver is a job template, not a workflow job template.
  - `read` runs only after a day-2 action; nothing reads a resource on a
    schedule, and rvav doesn't report its drift.
