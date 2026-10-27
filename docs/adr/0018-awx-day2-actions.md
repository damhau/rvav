# ADR-0018: AWX templates with the day2 label become day-2 actions

- Status: accepted
- Date: 2026-10-27

## Context
S6 ([implementation plan](../implementation-plan.md#s6-awx-integration))
asks for custom day-2 actions backed by AWX templates. They are bound to
resource types and conditions, and their form comes from the survey
([service-catalog.md](../service-catalog.md#awx-backed-custom-actions)),
for example "Install monitoring agent" on Linux VMs only. The native
actions are code ([ADR-0013](0013-day2-actions.md)). AWX templates already
reach the catalog by label ([ADR-0016](0016-awx-catalog.md)), and jobs on
rvav's VMs run on rvav's inventory ([ADR-0017](0017-awx-inventory.md)).

## Decision

### Which templates
The sync service that imports catalog templates also reads the templates
of `RVAV_AWX_ORGANIZATION` that carry one of `RVAV_AWX_ACTION_LABELS`
(`day2` by default). Each becomes the day-2 action `awx.<template name>`
(`-workflow` added for a workflow template) in the Automation group:
- Its name is made from the template's name.
- Its form comes from the survey, built as for catalog items.
- The sync keeps the form and warnings in step with AWX. It warns about
  launch settings rvav needs: limit, variables and inventory prompted on
  launch.
- A template that loses the label, or that AWX no longer has, is flagged
  gone and no longer offered.
- Putting the label on a template is the AWX admin's way of offering it.

### What administrators choose
`PATCH /api/v1/admin/awx/actions/{key}` (administrators; the Day-2
actions admin page) sets:
- the name;
- what the action runs on: VMs (one at a time) and/or whole deployments
  (every VM at once);
- a **condition**, a CEL expression over the day-2 policy variables, which
  limits where the action is offered (for example
  `resource.os_family == 'linux'`). It is checked when it is saved.
  `resource` now also has `guest_os` and `os_family`;
- whether the action is offered at all.

New actions run on VMs, everywhere, and are offered. A sync never changes
what administrators set.

### Who may run them
The day-2 policies decide, as for native actions. Their action patterns
match the keys, so `awx.*` names all AWX actions; the seeded "members
operate their VMs" (`*`) lets members run them. The condition decides
where an action is offered; the policies decide who may run it there.

### Running
The action is a day-2 request like any other, with one timeline, approval
by policy, audit and history. It has two steps:
1. Refresh the AWX inventory.
2. Run the template on rvav's inventory, with `limit` the VM (or the
   deployment's VMs), the form's inputs as extra variables, and `rvav`
   holding the context as for post-configuration jobs: request,
   deployment, project, environment, requester, and each VM's name, FQDN,
   IP and moref.

The job's tasks and output are followed as for post-configuration. The VMs
must be powered on. The form is checked against the survey's schema before
the request is made.

## Consequences
- An AWX admin can offer automation on rvav's VMs without a code change,
  and an rvav admin decides where it shows.
- AWX checks the survey answers again at launch. A survey changed after a
  request was made can still refuse it; the next sync updates the form.
- Not covered yet:
  - forms extended by admins (S8's form designer);
  - actions from custom Temporal workflows (S10);
  - per-action approval levels outside the day-2 policies.
