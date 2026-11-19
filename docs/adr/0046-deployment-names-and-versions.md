# ADR-0046: Deployment names and descriptions, requestable versions

- Status: accepted
- Date: 2026-11-19
- Extends the item versions of [ADR-0019](0019-catalog-administration.md),
  which it revisits, the updates of [ADR-0042](0042-deployment-updates.md),
  the importer of [ADR-0027](0027-vra-importer.md) and the naming of
  [ADR-0020](0020-administration.md), [ADR-0024](0024-multi-vm-blueprints.md)
  and [ADR-0038](0038-names-groups-secrets-properties.md). C2.1 (#157).

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

A vRA catalog item offers every version of its template that was
released: the request form has a version list, the latest first, and
unreleasing a version takes it out. rvav's items had one published version
(ADR-0019): publishing a draft deprecated the one before, so a team still
on the earlier build lost it the moment the next one went out.

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
  given twice in one request (two steps of one item) gets ` 2`. rvav cuts
  the name before the number so that the whole stays within 80
  characters, which an edit accepts.
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
  takes a new name under the same lock, and fails if another deployment
  took it since; a name that only changes case isn't new, so a deployment
  that shares its name with another from before this keeps it.
- **Its VMs keep their names.** Renaming a VM still renames the deployment
  named after it, unless another deployment of the project holds the new
  name.
- **Moving to another project checks the name there:** a project where a
  live deployment holds it is offered disabled ("… Rename this one
  first"), and the move's last step fails if one took it since.

### Several versions requestable
This revisits ADR-0019's single published version.
- **An item may have several published versions; each is requestable.**
  The newest published (by when it was made) is the **default**:
  `catalog_items.published_version_id` holds it, recomputed after every
  publish and retirement, so what read the published version before (the
  catalog card, composites, onboarding, the item's file in Git)
  reads the default unchanged. The unique index of one published version
  per item is dropped; its down migration deprecates all but the default.
- **Publishing keeps the old rule unless asked:**
  `POST …/versions/{id}/publish` deprecates the other published versions,
  and publishing a deprecated version is still a rollback.
  `keep_published: true` publishes it beside them: a draft goes out as the
  new default with the earlier ones still offered, and a deprecated
  version is offered again. Its audit entry says `keep_published`.
- **Retiring takes one out:** `POST …/versions/{id}/deprecate` deprecates
  that published version only, and the next newest becomes the default.
  Retiring the last one withdraws the item, as before; `withdraw: true`
  retires them all at once, which the admin UI's **Withdraw** sends. A
  draft or deprecated version can't be retired (409).
- **The catalog shows the default** as `CatalogItem.version`, and
  `CatalogItem.versions` (newest first) only when more than one is
  requestable, so a catalog of single-version items reads as before.
- **The form endpoint serves any requestable version** to whoever may
  request the item, and any version to catalog admins.
- **A request names its version and keeps it**, as do its deployments
  (ADR-0019): day-2 actions, retries and approvals use it. The form shows
  a **Version** list when the item offers several, the newest preselected;
  switching versions keeps what was typed where the other version has the
  field. A saved draft or a request asked again starts on its version
  while it is still requestable, else on the default.
- **A version no longer requestable is refused as stale:** a preview or
  submit on it answers `stale_version` at `/version_id` ("Version 3.2 of
  RHEL 9 server is no longer offered: request 3.3."), and the rest is
  checked on the default, so the requester sees what the reload gives.
  An approver's edits stay checked on the request's version.
- **An update moves to any requestable version,** the newest by
  default: its page shows the same list, with the deployment's own version
  marked while it is still offered (an update on it changes only the
  inputs). A version retired since the form opened is refused with
  `stale_version`.
- **The vRA importer imports a template's latest version as the draft and
  the earlier ones deprecated** ([ADR-0027](0027-vra-importer.md)): an
  import publishes nothing.
  When vRA released several, the report says so, and the administrator
  publishes those still offered with `keep_published` once the draft is
  published.

## Consequences
- Requesters find deployments by names they chose; the lists' search
  already reads the name.
- A request that names its deployments still titles itself after its VMs
  (`RHEL 9 server · web-dev-app007`), or a multi-VM item after its
  deployment.
- The vRA importer maps a day-2 policy on `Deployment.EditDeployment` to
  `edit`, no longer to the VM's `rename`.
- Onboarding keeps the names its plans and adoptions give
  ([ADR-0026](0026-onboarding.md)), numbered when a live deployment of
  the project holds one (`erp-app02 2`), under the same lock: the VMs run
  already, so a name never refuses them.
- Until C2.4 (#160), onboarding didn't check the project's names, an
  edit of a deployment that shared its name from before this failed in
  its step, and a numbered name could be longer than 80 characters, after
  which an edit was refused unless it changed the name.
- A new build no longer has to take the old one away: teams move to it
  when they request again or update, and the administrator retires the
  old one when nobody needs it. Deployments of a retired version keep it
  and their day-2 actions; only new requests and updates leave it.
- "Published" now means requestable, and "the published version" in
  ADR-0019 and ADR-0042 reads as the default. Rollback stays one click:
  publishing a deprecated version without `keep_published` makes it the
  only one offered.
- Rules and value sources of a request on an earlier version run on that
  version's form; nothing compares versions for the requester beyond the
  list, and the admin's Compare view is where differences are read.
