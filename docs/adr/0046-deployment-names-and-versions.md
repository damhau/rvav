# ADR-0046: Deployment names and descriptions, requestable versions

- Status: accepted
- Date: 2026-11-19
- Extends the item versions of [ADR-0019](0019-catalog-administration.md)
  and the naming of [ADR-0020](0020-administration.md),
  [ADR-0024](0024-multi-vm-blueprints.md) and
  [ADR-0038](0038-names-groups-secrets-properties.md). C2.1 (#157).

## Context
A vRA request takes a deployment name and a description. vRA refuses a
name already in use (`GET /deployment/api/deployments/names?name=` checks
one, across the organization), and the deployment's owner edits both
later with the day-2 action **Edit deployment**.

rvav named deployments itself: a single VM's after its VM, a multi-VM
item's by the blueprint's `deployment_name`, else the item's name and the
request number. Nothing kept two names apart: two requests with one
`deployment_name` template made two deployments of one name. The only text
a request carried was its reason, which approvers read.

## Decision

### A request names its deployments
- **`RequestDraft` takes `deployment_name` and `deployment_description`,**
  both optional. `RequestForm.names_deployments` says the item makes
  deployments: a VM item, or a composite with a VM step. An item that makes
  none refuses either (422, `no_deployment`), and the form doesn't show
  them.
- **Without a name, the deployment is named as before.**
- **The name is trimmed, one line, at most 80 characters** (`too_long`,
  `invalid`).
- **Several deployments are numbered:** a request for three single-VM
  deployments named `Payroll` makes `Payroll 1`, `Payroll 2` and
  `Payroll 3`, as do a composite's VM steps. The preview lists them
  (`RequestPreview.deployment_names`).
- **The description is at most 2000 characters,** stored on each
  deployment of the request and shown on its page. It isn't the reason: the
  reason stays the request's, for its approvers, and the description stays
  the deployment's.

### Unique per project
- **A name is unique among the project's deployments that aren't deleted,
  whatever its case.** vRA checks across the organization; rvav checks per
  project, where deployments are listed and searched. An organization-wide
  check would also tell a requester the names of deployments in projects
  they can't see. A deleted deployment's name is free again.
- **The check runs in the preview and again at submit,** inside the
  request's transaction, under a transaction advisory lock per project and
  lower-cased name (`rvav.deployment:<project>:<name>`), so two requests
  can't both take a name. A requested name taken in the meantime is refused
  (`taken`).
- **A name rvav gives is made unique too:** a blueprint's
  `deployment_name` already taken gets the request number after it
  (`erp-prd REQ-10492`), then a number if that is taken as well; a name
  given twice in one request (two steps of one item) gets ` 2`.
- **No unique index:** deployments made before this may share a name. An
  index on `(project_id, lower(name))` of the live ones serves the check.

### Checked against the naming policy
A single-VM deployment carries its VM's name, and rvav never gives a VM
name twice (ADR-0020): a deployment named like a VM would collide with
the VMs to come. A requested name that the request's VM naming gives (its
prefix and a number, such as `web-dev-app007` for `web-dev-app{seq:03}`) is
refused (`naming`), for each VM step of a composite too.

### Edited with a day-2 action
- **Edit deployment (`edit`) is a day-2 action on the deployment,** as in
  vRA, its form holding the name and description. rvav's comparable
  changes (change owner, move to another project, rename a VM) are day-2
  actions: each runs as a request, under the day-2 policies, with its
  step in the deployment's history and its inputs in the audit log. A plain
  `PATCH` would skip the policies and the history, so an edit goes the same
  way. It is in the **Configuration** group.
- **Who may:** as for any action, the day-2 policies, else the roles'
  `day2.configuration` permissions. The demo's policies let the owner and
  the project admins edit, with delete and hand over; other members are
  refused.
- **Checked as a request's name:** trimmed, one line, at most 80
  characters (the form's schema bounds both fields), not one another live
  deployment of the project holds, and not one its VMs' naming gives (a VM
  name without its number, then a number) unless it is one of its VMs'
  names. An edit that changes nothing is refused (`no_change`). Its step
  takes the name under the same lock, and fails if another deployment took
  it since.
- **Its VMs keep their names.** Renaming a VM still renames the deployment
  named after it, unless another deployment of the project holds the new
  name.
- **Moving to another project checks the name there:** a project where a
  live deployment holds it is offered disabled ("… Rename this one
  first"), and the move's last step fails if one took it since.

## Consequences
- Requesters find deployments by names they chose; the lists' search
  already reads the name.
- A request that names its deployments still titles itself after its VMs
  (`RHEL 9 server · web-dev-app007`), or a multi-VM item after its
  deployment.
- The vRA importer maps a day-2 policy on `Deployment.EditDeployment` to
  `edit`, no longer to the VM's `rename`.
- Onboarding keeps the names its plans and adoptions give
  ([ADR-0026](0026-onboarding.md)): an administrator's, not checked
  against the project's names.
