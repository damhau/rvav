# ADR-0020: Administration: projects, placement, governance and settings

- Status: accepted; how saved settings take effect revised by
  [ADR-0030](0030-settings-and-local-administrator.md)
- Date: 2026-10-28

## Context
S9 ([milestone](https://github.com/damhau/rvav/milestone/2)) moves what
the seed and environment variables set until now into the application:
projects and their members, OIDC group mappings, environments, placement,
templates and sizes, naming, quotas and the lease, approval and day-2
policies, the vCenter, AWX and EfficientIP connections, API tokens and the
audit log. Most tables exist since slice 1; what was missing is who may
change them, how, and how settings stored in the database relate to the
environment variables rvav has been configured with.

## Decision

### Who
- **Administrators** (global `admin`) manage everything in this ADR.
- **Project admins** manage the direct members of their own projects, as
  vRA project administrators do, and see their projects' settings and audit
  events. They change nothing else here.
- **Users** create, see and revoke their own API tokens; administrators see
  and revoke everyone's.
- **Auditors** read the whole audit log.

Every change is audited, the action saying what changed: `project.*`,
`member.*`, `group_mapping.*`, `environment.*`, `placement.*`,
`network.mapping.*`, `template.*`, `size.*`, `naming.*`, `policy.*`
(quota, lease, approval, day-2), `settings.*` (integrations and their
connection tests) and `token.*`. The audit log filters by these.

### Projects, members and groups
- A project's code is fixed once it exists: it is in VM names and in their
  counters. A project is **archived**, not deleted: it can't request
  anything new, and its deployments, leases and day-2 actions go on.
- Members are direct (a role in the project) or come from **OIDC group
  mappings**, as of their last sign-in. A member through a group is changed
  through the mapping, not one by one.
- A project always keeps a project admin: removing or demoting the last
  one is refused unless a group mapping gives the role. Administrators act
  as project admins everywhere.
- A group mapping gives a project role in a project, or a global role
  (`admin`, `catalog-admin`, `auditor`) without one. The last way anyone is
  an administrator can't be removed.
- Environments keep their key; one is removed only when nothing uses it.

### Placement, templates, sizes and naming
- **Placement rules** stay per project and environment, lower priority
  first, with `*` patterns for clusters (or resource pools), datastores and
  networks, and a folder. Clusters and datastores can also be chosen by
  vSphere tags (`category/tag`, all of them), read by the inventory sync. A
  preview shows what a project and environment can use now and what is
  missing (a network without a subnet).
- **Network mappings** set by hand win over the ones synced from
  EfficientIP: the sync never overwrites them, and removing one lets the
  sync bring EfficientIP's back.
- **Templates and size presets** (vRA image and flavour mappings) are
  offered to projects and environments; an offer with no project or no
  environment means all of them. VM forms choose from what is offered
  through the `rvav.templates` and `rvav.sizes` value sources; a blueprint
  that names its own template or sizes keeps them.
- **Naming policies** are offered the same way, and the most specific
  wins: project and environment, then project, then environment, then all,
  then the blueprint's own naming. A counter is per name prefix (as
  before) or one for the whole policy. Counters never go back, and a name
  is checked against vCenter, DNS and every deployment rvav ever made, so
  changing a policy never gives a name again.

### Policies
Quotas and the lease, approval and day-2 policies are listed, created,
edited and ordered from the UI. Their CEL is compiled and evaluated on
sample variables when saved, so a typo or a type error is caught then, not
at the next request. An **explain** query says, for a project, environment,
item or action, which policy decides, which others were considered and
why.

### Settings and environment variables
The vCenter, AWX and EfficientIP connections are stored in the database
(`integration_settings`), their password or token in the secret store of
[ADR-0019](0019-catalog-administration.md).

- **An environment variable wins.** When one is set, its value is used
  and the field is shown locked in the UI. Deployments managed with Helm or
  GitOps stay declarative; the UI serves the lab and the pilot.
- **Saved settings take effect when rvav's processes restart.** Each
  process records the settings revision it started with, and the UI says
  when a restart is needed. Clients hold sessions, and Temporal workers and
  the sync loops are built at start: swapping them under running activities
  is a risk for something that rarely changes. *(Revised by ADR-0030:
  they apply without a restart by hand, on next use or as each process
  drains and starts again.)*
- **Test connection** uses the settings given, saved or not, and says
  which checks pass (reach the server, sign in, see what rvav needs).
- **A password or token goes only where it was given for.** Testing or
  saving another URL, or turning TLS checks off, needs it typed again; one
  set by an environment variable goes only to the URL set with it. So an
  administrator's session or token can't send the saved credentials to a
  host of its choosing (found by the S9 review).

### Secrets
Secrets stay AES-256-GCM in PostgreSQL (Vault later). Each records the ID
of the key that encrypted it. `RVAV_SECRETS_KEY` is the current key and
`RVAV_SECRETS_OLD_KEYS` lists earlier ones that still decrypt; `rvav
secrets rotate` re-encrypts everything with the current key, after which
the old keys can go. Processes configured with both keys keep working
throughout: no downtime. A secret replaced while a rotation runs is left
as replaced.

### EfficientIP instance
`RVAV_INSTANCE_ID` (default `rvav`) is written on everything rvav creates
in EfficientIP, next to `rvav_managed`. The reconcile report counts only
its own instance's objects (those without an instance came before it, and
count as its own), so two rvav instances sharing a SOLIDserver don't report
each other's objects as orphans.

### API tokens and the audit log
Tokens are created from the UI as from `rvav api-token`: a scope (`all`,
or `inventory` for the Ansible inventory only), an expiry, and the value
shown once; when each was last used is shown. The audit log is searched by
who, what, when and project, and exported as CSV.

## Consequences
- Administrators set up a project end to end without SQL or YAML.
- Two places can hold a setting; the environment wins, and the UI shows
  which one is used.
- A connection change needs a restart, which the UI says; on Kubernetes a
  rolling restart.
- Templates, sizes and naming move from blueprints to policies that can be
  shared; blueprints that define their own keep working.
- The development seed fills what administrators manage (environments,
  projects, members, group mappings, quotas, placement, network mappings,
  policies) and keeps it current only until an administrator changes that
  kind of thing: from the first audited change, it is theirs.
