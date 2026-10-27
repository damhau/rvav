# ADR-0013: Day-2 actions are requests

- Status: accepted
- Date: 2026-10-27

## Context
S4 adds the actions users run on what they own: power, resize, disks, NICs,
snapshots, console, rename, ISO, owner or project, delete. Each needs a
timeline, may need approval, must be audited and appear in the deployment's
history, and can fail and be retried. Requests already have all of this.

## Decision

### An action run is a request
A day-2 action is a request with `kind: day2`, the `action` key, the target
`deployment_id` and, for VM actions, `resource_id`. It has the deployment's
catalog item and version, its own steps, approvals (ADR-0011), live events and
audit entries. The request workflow runs it like any request (approval,
schedule, cancel, outcome); its plan holds the action's operations instead of
VMs to build, each one a step, and each refreshes the VM from vCenter. While
it runs the deployment is `updating`; one request runs on a deployment at a
time. The deployment's history is its requests: the one that created it and
every action since. `kind: provision` is what slice 1 built.

### The actions
Native actions are code (`internal/day2`): each has a key, a name, a group,
the resource types it applies to, an availability check on the resource's
current state (no "power on" on a running VM), an optional form (JSON Schema
and UI schema, like catalog forms) and a confirmation (destructive actions ask
to type the name). `GET /deployments/{id}/actions` lists them per resource
with whether the caller may run them and why not.

### Day-2 policies
`day2_policies` decide, by priority, for an action pattern (`delete`,
`snapshot_*`, `*`), the roles it applies to and a CEL condition over the
action, resource, deployment, environment and user, an effect: `allow`,
`deny` (with a reason shown to the user) or `approval` (with levels, as
approval policies). The first matching policy wins; nothing matching denies,
and so does a condition that fails to evaluate. Roles are `owner`, the
project role (`viewer`, `member`, `project-admin`) and `admin`. Conditions
have their own CEL variables: `action`, `resource`, `deployment`, `project`,
`environment` (`key`, `name`, `production`), `user` and `inputs`. The
system user `rvav`, which applies retention and lease policies, is not
subject to them. Approvers of a day-2 action see what it will do and cannot
change its inputs. The seed allows members to operate their VMs, asks
approval to delete in production, and lets only owners and project admins
delete or hand a deployment over.

### Snapshot retention
Each environment sets how many snapshots a VM may have (`snapshot_create`
is unavailable beyond it) and after how many days rvav deletes them. A
Temporal schedule starts the short `Housekeeping` workflow every 15
minutes; it starts an ordinary `snapshot_delete` request for the oldest
expired snapshot of each VM, requested by the system user `rvav`, so the
deletion has a timeline, an audit entry and a place in the deployment's
history. Lease expiry (S5) joins the same workflow.

### Retry and roll back
A failed request can be retried from its failed step: completed steps are
skipped (activities are idempotent), the failed and later steps run again.
Rolling back undoes what a failed provisioning request created (VM, DNS
records, IP) and marks the deployment deleted.

## Consequences
- One timeline, inbox and audit model for everything users do.
- Actions are code, reviewed and tested; custom actions (AWX, custom
  workflows) arrive in S6 and S10 in the same list.
- Requests gain `kind`, `action`, `deployment_id` and `resource_id`.
