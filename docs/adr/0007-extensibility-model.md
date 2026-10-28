# ADR-0007: Extensibility model (what replaces vRO)

- Status: accepted
- Date: 2026-10-26

## Context
In vRA, vRealize Orchestrator (vRO) does four different jobs. Replacing "vRO"
with one tool would either overload it or leave gaps, so each job gets the
replacement that fits it best.

| vRO job | Replacement | Why |
|---|---|---|
| vRA's internal engine: provisioning, day-2, approvals, waits | **Temporal**, inside rvav | Durable workflows, retries, timers and signals, as code. Built in slice 1 ([ADR-0004](0004-temporal-workflows.md)). |
| Integration library: AD, SQL, REST, PowerShell, SSH plugins | **AWX and Ansible modules** | Thousands of maintained modules, and ops teams already know Ansible. |
| Admin-written glue: event subscriptions, custom day-2 actions, XaaS catalog items | **AWX** by default, **custom Temporal workflows** for complex cases | See the three levels below. |
| Dynamic form values: vRO actions feeding dropdowns | **rvav value sources**: built-in, REST, CEL | A form lookup doesn't need a workflow engine. |

## Decision

### Three levels of extensibility

1. **Built in, no code.** Native vSphere, IPAM and DNS steps, and *composite*
   catalog items that chain native steps, AWX templates and other items in
   order, defined in YAML or in the catalog admin UI. This covers most small
   vRO glue workflows.
2. **AWX, the default for ops admins.** AWX job templates and workflow job
   templates as catalog items, custom day-2 actions and event-subscription
   targets. Most existing vRO workflows become playbooks.
3. **Custom Temporal workflows, for the platform team.** For long-running,
   multi-system processes with human steps (for example decommissioning an
   application over several days). A team writes a Temporal workflow in Go,
   Python or TypeScript, runs its own worker, and registers it in rvav as a
   catalog item, a day-2 action or a subscription target.

There is no visual workflow designer. Simple chains are composite items
(level 1); everything else is a playbook or code with review and tests.

### Contract for custom Temporal workflows (level 3)

- **Registration** (admin API and UI): name, Temporal task queue, workflow
  type including a major version (`decommission.v1`), input JSON Schema,
  timeouts, the projects allowed to use it, and where it may be used
  (catalog, day-2, subscription).
- **Invocation:** rvav starts it as a child workflow of the request's workflow,
  on the registered task queue, in the same Temporal namespace. Input is a
  typed `RvavContext` (request, deployment, resources, project, requester,
  validated inputs). Cancellation propagates from the rvav request.
- **Progress:** the workflow reports steps and log lines by calling rvav
  activities (`rvav.ReportStep`, `rvav.AppendLog`) on rvav's task queue. The
  steps show in the same live timeline and audit log as built-in steps.
- **Human steps:** `rvav.RequestApproval` creates an rvav approval and the
  workflow waits for its decision signal, so approvals keep one inbox.
- **Result:** an output object stored on the request. A failure marks the step
  failed with the workflow's error message.
- **SDK:** small helper packages for Go and Python wrap these activities, plus
  an example repository with tests.
- **Security:** workers authenticate to Temporal with mTLS. Credentials for
  external systems stay in the custom worker, never in rvav. Registration is
  limited to administrators, and every run is audited.

### As built (S10)

Levels 1 and 3 and the event subscriptions were built in S10:
[ADR-0021](0021-event-subscriptions.md) covers events and subscriptions,
[ADR-0022](0022-custom-workflows-and-composites.md) covers custom
workflows and composite items. Developers start from
[custom-workflows.md](../custom-workflows.md). Where the build differs
from the plan above:

- **Composites** chain VM, AWX and custom workflow items, AWX templates
  and custom workflows, with `when` conditions, CEL bindings to earlier
  steps' outputs, parallel groups and undo steps. They don't nest yet.
- **Subscriptions** target webhooks as well as AWX templates and custom
  workflows. Blocking ones run inside the request's workflow and may stop
  it.
- **Custom workflows** may also be composite steps. The helpers exist
  for Go (`pkg/rvavflow`) and Python (`sdk/python`), not TypeScript. The
  example (`examples/decommission`) lives in this repository rather than
  its own.
- **Approvals** a workflow asks for wait as long as the workflow decides;
  rvav gives them no deadline of their own.

### Migrating vRO workflows

Each existing vRO workflow is sorted into one group:

| Group | Typical content | Becomes |
|---|---|---|
| A. Native | Only drives vCenter (clone, reconfigure, tag, move) | Built-in rvav steps or a composite item |
| B. Ansible | Calls other systems (AD, CMDB, DNS, backup, monitoring) | An AWX job template |
| C. Temporal | Long-running, branching, human steps, compensation | A custom Temporal workflow |
| D. Drop | Unused, duplicated, or only works around vRA itself | Nothing |

The vRA importer (phase 2) produces this list from the vRO API: name, inputs,
which catalog items and subscriptions use it, and the last run date.

## Consequences
- Each job has a tool suited to it, and the tool most admins touch (AWX) is
  one they already know.
- Level 3 adds a public contract that must stay stable and versioned. It is
  built after approvals, day-2 actions and AWX integration, and starts with
  workflows written by the platform team.
- Without a visual designer, people who built vRO workflows in its GUI must
  move to YAML composites, playbooks or code. Training and examples are part
  of the plan.
