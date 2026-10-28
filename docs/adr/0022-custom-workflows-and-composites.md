# ADR-0022: Custom workflows and composite items

- Status: accepted
- Date: 2026-10-28

## Context
[ADR-0007](0007-extensibility-model.md) sets three levels of
extensibility. S10 builds level 1's **composite items**, chaining what rvav
already runs, and level 3's **custom Temporal workflows**, written by the
platform team for long processes with human steps. Both run inside a
request, in its timeline. The workflow contract is public: teams build on
it, so it must be clear what may change and how.

## Decision

### Registering a custom workflow
Administrators register a workflow (`/admin/workflows`):

- **name** (`decommission`), **title** and **description**;
- the Temporal **workflow type**, ending with its major version
  (`decommission.v1`), and the **task queue** its worker listens on;
- an **input schema** (JSON Schema): its form when people start it;
- an **execution timeout** (default 30 days) and the **projects** that may
  use it (none listed: all);
- where it may be used: as a **catalog item** (an item of type `workflow`),
  as a **day-2 action** (below), as a **subscription target**
  ([ADR-0021](0021-event-subscriptions.md)), or as a **step of composite
  items**.

A registration in use (by an item, an action, a subscription or a run not
ended) can't be deleted, only disabled.

### Running it
rvav starts the workflow as a **child workflow** of the request's
workflow, on the registered task queue, in rvav's namespace, with the ID
`<request workflow ID>/<step key>`. Cancelling the request cancels it. Its
single argument is the **context**:

```json
{
  "contract": "rvav.v1",
  "rvav_task_queue": "rvav-provision",
  "request": {"id": "req_…", "number": "REQ-10077", "action": "decommission"},
  "project": {"id": "prj_web", "code": "web", "name": "Web Platform"},
  "environment": "prd", "production": true,
  "requester": {"id": "usr_…", "name": "…", "email": "…"},
  "deployment": {"id": "dep_…", "name": "…", "properties": {}},
  "resources": [{"id": "res_…", "type": "vsphere.vm", "name": "…", "fqdn": "…", "ip": "…", "moref": "…"}],
  "inputs": {},
  "steps": {},
  "event": null
}
```

`inputs` are the validated form; `steps` the outputs of earlier steps of a
composite item; `event` the event of a subscription. The workflow's result
(any JSON object) is stored on its step and, for a blocking subscription,
read as `{outcome, reason, properties}`. A failure fails the step with the
workflow's error message.

### What it can ask of rvav
rvav's worker serves three activities on its own task queue (named in the
context). A workflow calls them with `rvav_task_queue` as the task queue:

| Activity | Input | Does |
|---|---|---|
| `rvav.ReportStep` | `{key, title, status, detail}`, status `running`, `done`, `failed` or `skipped` | Adds or updates a step under the workflow's step in the timeline |
| `rvav.AppendLog` | `{step_key, lines}` | Adds lines to a step's log (the workflow's own step without `step_key`) |
| `rvav.RequestApproval` | `{title, reason, group, approvers, mode}` | Opens an approval on the request, in the same inbox; returns its ID |

When an approval is decided, rvav sends the workflow the signal
`rvav.approval` with `{approval_id, outcome, comment}` (`outcome` is
`approved` or `rejected`). An approval has no deadline of its own: a
workflow that can't wait forever waits with its own timer and decides what
happens (the helpers' `Wait`), and the approval stays open in the inbox
until decided or the run ends (its open approvals are then cancelled).
Approvals a workflow asks for are not policy levels: the request's
approval chain ignores them, and an approver can't change the request's
inputs in them. rvav finds the request and step from the calling
workflow's ID: an activity called by a workflow rvav didn't start, or by
one whose run rvav saw end, is refused.

The Go package `pkg/rvavflow` and the Python package `rvav-workflows`
(`sdk/python`) wrap these calls and the wait for the signal. An example
decommission workflow with its tests is in `examples/decommission`.

### Versions
- **rvav's side is `rvav.v1`.** Within it, the context and the activities'
  inputs and outputs only gain optional fields. A change that would break
  a workflow is `rvav.v2`, with new activity names (`rvav.v2.ReportStep`);
  rvav then serves both for at least a release, and the context says which
  one it was started with.
- **The workflow's side is its type's major version.** A change that
  breaks running workflows (Temporal replays history) or changes inputs
  is a new type, `decommission.v2`, registered alongside `v1`; runs in
  flight finish on `v1`, whose worker stays until none is left (the
  registration says how many run). Smaller changes use Temporal's own
  versioning (`workflow.GetVersion`, `workflow.patched`) in the same type.
- **Security.** Workers authenticate to Temporal with mTLS in production.
  Credentials for other systems stay in the custom worker, never in rvav.
  Only administrators register workflows; every run is a request step,
  audited.

### Composite items
A composite item's blueprint lists **steps**, run in order; a `parallel`
group runs its steps at the same time:

```yaml
kind: composite
rollback_on_failure: true
steps:
  - key: vm
    item: rhel9-server          # another item: its published version
    with: {role: inputs.role, size: inputs.size, post_config: ["linux-baseline"]}
  - parallel:
      - key: db_user
        awx: {job_template: pg-create-role, title: Create the database user}
        with: {database: inputs.database, role_name: inputs.role_name, host: steps.vm.fqdn}
        undo: {job_template: pg-drop-role, with: {role_name: inputs.role_name}}
      - key: monitoring
        workflow: register-monitoring     # a registered custom workflow
        with: {fqdn: steps.vm.fqdn}
```

- A step is **another item** (`item`: a VM, AWX or custom workflow item),
  an **AWX template** (`awx`) or a **custom workflow** (`workflow`). A step
  may have a `when` condition. Composites don't nest yet: one composite
  can't be a step of another (step keys and bindings would need a
  namespace each).
- **`with`** binds a step's inputs with CEL over `inputs` and, except for
  VM steps, `steps.<key>` (the outputs of earlier steps: a VM's `name`,
  `fqdn`, `ip` and `moref`, or `vms` when there are several; an AWX job's
  `job_id` and its `set_stats` artifacts; a workflow's result).
- **VM steps are planned when the request is submitted**, as one request:
  names, placement and quota are checked for all of them together, and
  approval policies see the whole. So their inputs come from the form
  only, and they have no `when`. The other steps' inputs are bound when
  they run. Approvers can't change a composite's inputs.
- **One timeline:** each step is a step of the request, with its own steps
  under it. A step's outputs are kept on it, so a request retried from a
  failed step binds later steps to what earlier ones gave.
- **Failure:** with `rollback_on_failure`, what was built is undone in
  reverse order: VMs, addresses and records are deleted as today, AWX and
  workflow steps run their `undo` when they have one (a step its `when`
  skipped has nothing to undo). A step undone is marked cancelled, so a
  retry runs it again; a VM step retried keeps its own steps that were
  done. Without rollback, everything is kept to be looked at.
- Composites are written in YAML (the item's Definition tab), checked when
  saved: steps, references, bindings, cycles and depth.

### Day-2 actions from custom workflows
A workflow registered for day-2 use becomes the action `wf.<name>`, as AWX
templates become `awx.<name>` ([ADR-0018](0018-awx-day2-actions.md)): an
administrator sets its name, what it runs on (VMs or deployments), a CEL
condition and whether it is offered; its form is the input schema; the
day-2 policies decide who may run it; its context has the deployment and
the resource it runs on.

## Consequences
- Simple chains need no code (composites), and processes that need code
  run in rvav's timeline and inbox rather than beside them.
- Teams owning custom workflows own their workers, their deployment and
  their versioning; rvav's side changes only as the contract allows.
- A composite's VM steps can't depend on the outputs of earlier steps: a
  VM whose size depends on an AWX job's result isn't possible. None of the
  vRA blueprints reviewed needs it.
