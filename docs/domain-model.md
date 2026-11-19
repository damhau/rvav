# Domain model

Single organisation (see [ADR-0002](adr/0002-single-organisation.md)). **Projects**
are the isolation boundary for access, quotas and placement.

## Entity overview

```
User ──< ProjectMember >── Project ──< Deployment ──< Resource
                              │            │
                              │            └──< Request ──< RequestEvent
                              │                    └──< Approval
                              ├──< PlacementRule
                              ├──< Quota
                              └──< Entitlement >── CatalogItem ──< CatalogItemVersion
                                                       │
PolicyDefinition (approval | lease | day2 | quota |   ├── ContentSource (native | awx)
                  naming)                              └── Form (JSON Schema + UI schema)
  └── scoped to: global | project | catalog item

Day2Action ── (native | awx job template) ── bound to ResourceType
Subscription (event → awx template | webhook)
```

## Core entities

### Identity and access
- **User**: subject from OIDC, display name, email, manager, groups (synced from token claims).
- **Group mapping**: OIDC group → global role or project role.
- **Global roles**: `admin`, `catalog-admin`, `auditor`, `user`.
- **Project roles**: `project-admin`, `member`, `viewer`, `supervisor` (approves and
  sees, never requests; ADR-0041).
- **ApiToken**: user-owned, scoped to roles, with expiry, stored hashed.

### Environment
- `key` (`dev`, `tst`, `prd`, …), `name`, `production` flag, display order.
- Defined by administrators. Requests and deployments always target one
  environment; placement rules, quotas and policies can depend on it.

### Project
- `code` (short, used in names), `name`, `description`, `cost_center` (metadata only, passed to AWX and webhooks), `tags`, allowed environments.
- `deployment_sharing`: `all_members` (the default) or `owner_only`, where
  members see only the deployments they own and holders of
  `deployments.read_all` see all of them
  ([ADR-0045](adr/0045-deployment-sharing-and-project-policies.md)).
- **PlacementRule**: allowed vCenter, datacenter, cluster or resource pool, VM
  folder, datastores or datastore cluster, networks and their linked EfficientIP
  subnets. Supports tag-based selection and priority.
- **Quota**: limits on vCPU, RAM, storage and VM count, optionally per environment.

### Catalog
- **ContentSource**: where items come from.
  - `native`: blueprints authored in the app.
  - `awx`: an AWX connection plus a filter (organisation, labels, name pattern).
    Synced on a schedule; detects drift when a survey or template changes.
- **CatalogItem**: `name`, `slug`, `icon`, `category`, `tags`, `description`
  (Markdown), `type` (`vm_blueprint` | `awx_job_template` | `awx_workflow` |
  `composite`), owner, `featured`.
- **CatalogItemVersion**: immutable once published. Holds the form, blueprint or
  step definition, and changelog. States: `draft` → `published` → `deprecated`.
  Several versions of an item may be published, each requestable; the
  newest is the default (ADR-0046). Requests always point to an exact version.
- **Entitlement**: catalog item × project (or every project), with who may
  request it there: all the project's members, or chosen users, OIDC groups
  and project roles. Grants "can request" to those who also hold
  `request.submit` in the project (ADR-0019, Sharing).
- **Form**: JSON Schema (data) plus UI schema (layout, widgets, conditions,
  dynamic value sources). See [service catalog](service-catalog.md#custom-forms).

### Blueprint (for `vm_blueprint` items)
- One or more VM components, each with: a count, source (vSphere template or
  content library item), size presets or custom CPU/RAM, disks, NICs (network +
  IPAM mode: `efficientip` | `dhcp`), vSphere tags, anti-affinity, guest
  customization (customization spec or cloud-init; Windows domain join), and
  AWX post-config steps (ordered job templates with an extra_vars mapping).
  A blueprint with components builds one deployment
  ([ADR-0024](adr/0024-multi-vm-blueprints.md)).
- Expressions (CEL) bind form inputs to component properties.

### Request and execution
- **Request**: who, on behalf of whom, project, catalog item version, inputs
  (JSONB, secrets encrypted), kind (`provision` | `day2` | `update`, the
  last moving a deployment to a version and inputs,
  [ADR-0042](adr/0042-deployment-updates.md)), status (`pending_approval` | `approved` |
  `rejected` | `running` | `succeeded` | `failed` | `cancelled`), scheduled
  time, Temporal workflow ID.
- **RequestEvent**: timeline entries (step key, parent step, status, message,
  started/finished, external reference such as an AWX job ID).
- **Approval**: the policy that triggered it, level, approvers (users, groups,
  project roles resolved when the level opens, or the requester's manager),
  decision, comment, decided_by, due date.

### Deployment and resources
- **Deployment**: logical group of resources created by one request. Has owner,
  project, catalog item version, inputs, lease expiry, and status. Its
  version and inputs are those of the request that created it, or of its
  last update that succeeded. Its name is unique among the project's live
  deployments, whatever the case, and its description says what it is for
  ([ADR-0046](adr/0046-deployment-names-and-versions.md)).
- **Resource**: `type` (`vsphere.vm` | `ipam.address` | `dns.record` |
  `vsphere.drs_rule`), external ID (VM MoRef plus instance UUID, IP ID, ...),
  properties (JSONB), sync state (`in_sync` | `drifted` | `missing`). A VM's
  properties keep it as rvav last left it, and how it differs now (drift,
  [ADR-0025](adr/0025-drift.md)).
- **InventoryVM**: cached vCenter inventory maintained by `rvav-sync`, including
  VMs not managed by the app, with their folder and tags. Administrators
  adopt those into deployments with no request behind them
  ([ADR-0026](adr/0026-onboarding.md)).
- **OnboardingPlan**: a saved filter of the inventory's VMs (name or
  address, folder, tag, cluster, vCenter, power) and where what it
  matches goes (each its own deployment, one named deployment, or an
  existing one, with a component and recorded inputs), with an optional
  interval in hours. **OnboardingRun**: one run of a plan (manual or
  scheduled), its status, and what it did with each VM it matched:
  adopted, skipped or failed, with a code and why
  ([ADR-0044](adr/0044-onboarding-plans.md)).

### Day-2
- **Day2Action**: key, name, icon, applicable resource types, condition (CEL,
  for example `resource.guestOS.startsWith("linux")`), implementation (`native:<op>`
  or `awx:<template id>`), optional form, whether it needs confirmation.
- **Day2Run**: execution record linked to its own timeline events.

### Policies
- **PolicyDefinition**: `type`, `scope` (global | project | catalog item),
  `condition` (CEL), `priority`, `enforcement` (`hard` | `soft`), `spec` (JSONB,
  depends on type). A project's policies are written by its project admins
  (`project.policies.manage`), except those an administrator wrote last
  (`set_by_admin`); a hard global one still wins. Soft lease and
  day-2 policies warn, and a request that goes on keeps its warnings
  ([ADR-0045](adr/0045-deployment-sharing-and-project-policies.md)). Types:
  - `approval`: levels, approvers, auto-approve conditions, timeout behaviour.
  - `lease`: default, max, grace period, max extensions, expiry action.
  - `day2`: which roles can run which actions under which conditions.
  - `naming`: template and counter scope.
  - `quota`: see Quota above (stored as a policy for consistent evaluation).

### Extensibility
- **Subscription**: event topic (for example `deployment.provision.post`,
  `resource.day2.pre`, `lease.expiring`), condition (CEL), target
  (`awx:<template>` | `webhook:<url>`), `blocking`, `timeout`, priority.
- **OutboxEvent**: reliable delivery for webhooks and notifications.

### Audit
- **AuditEvent**: actor, action, target, before and after values (diff), IP
  address, user agent, timestamp. Append-only.
