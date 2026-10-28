# Service catalog specification

The goal is parity with vRA Service Broker, plus the UX it never delivered.

## 1. Content sources and item types

| Type | Source | What it does |
|---|---|---|
| `vm_blueprint` | Authored in the app | Provisions one or more VMs natively, with optional AWX post-config steps |
| `awx_job_template` | Imported from AWX | Launches a job template with inputs from the form |
| `awx_workflow` | Imported from AWX | Launches a workflow job template |
| `composite` | Authored in the app | Ordered or parallel steps combining other items (VM, AWX, custom workflow), AWX templates and custom workflows, inputs bound to earlier steps' outputs ([ADR-0022](adr/0022-custom-workflows-and-composites.md)) |
| `workflow` | Authored in the app | Starts a registered custom Temporal workflow with the form's inputs ([ADR-0022](adr/0022-custom-workflows-and-composites.md)) |

A composite's blueprint, in its Definition tab:

```yaml
kind: composite
rollback_on_failure: true
steps:
  - key: vm
    item: rhel9-server
    with: {role: inputs.role, size: inputs.size, cluster: inputs.cluster,
           datastore: inputs.datastore, network: inputs.network, post_config: ["linux-baseline"]}
  - parallel:
      - key: db_user
        item: db-user
        with: {database: inputs.database, role_name: inputs.role_name, privileges: '"read"', host: steps.vm.fqdn}
        undo: {job_template: pg-drop-role, with: {role_name: inputs.role_name}}
      - key: monitoring
        awx: {job_template: monitoring-agent, title: Monitoring}
        with: {target: steps.vm.fqdn}
  - key: dba_notice
    awx: {job_template: dba-notice}
    when: inputs.database == "ledger"
```

`with` values that are strings are CEL (a literal string is quoted:
`'"read"'`); a VM step's come from the form only (and it has no `when`:
its VMs are planned at submit), the others may use
`steps.<key>`: a VM's `name`, `fqdn`, `ip`, `moref` (and `vms`), an AWX
job's `job_id`, a workflow's result.

### AWX import
- A content source connects to AWX and filters templates by organisation, label or
  name pattern.
- The **survey spec is converted into a form automatically**:
  - `text`, `textarea`, `password` → string fields (`password` → secret widget)
  - `integer`, `float` → number fields with min and max
  - `multiplechoice`, `multiselect` → dropdown and multi-select
  - `required`, `default`, `min`, `max` and the help text carry over
- A catalog admin can then **enhance** the generated form (dynamic values,
  conditional fields, layout). The enhancements are kept on top of the survey
  mapping.
- Periodic resync detects survey or template changes and flags the item as
  "source changed", with a diff and a one-click update to a new draft version.
- The template's launch prompts (`ask_*_on_launch`) are read so the app knows
  which overrides are allowed (limit, inventory, credentials, variables).

## 2. Catalog experience

- Home page with featured items, favourites, recently requested and categories.
- Full-text search with filters (category, tag, type) plus ⌘K.
- Item cards: icon, name, short description, typical duration, "needs approval"
  badge.
- Item detail page: Markdown description, what will be created, approval and lease
  summary, a request button, and version history.
- **Entitlements**: items or categories shared with projects, groups or users. Users
  only see what they can request.
- **Versioning**: draft, published and deprecated states; preview the draft as a
  user; compare versions; roll back; existing deployments keep their version.
- Import and export of catalog items as YAML (GitOps-friendly), with an optional
  Git sync later.

**Built (S8, [ADR-0019](adr/0019-catalog-administration.md)):** catalog
admins and administrators create items, open them to every project (those
made later too) or to chosen projects, and manage categories and the icon
(from rvav's own set, no upload). Versions are draft, published or
deprecated: one draft and one published version per item at most;
publishing a deprecated version again rolls back; withdrawing the published
version takes the item out of the catalog; deployments and requests keep
their version. Two versions compare change by change. Items export to YAML
(one version or all) and import as a new item or a new draft, never
published by the import, with problems on their YAML line. Not built yet:
entitlements to groups or users rather than projects, and a Git sync.

## 3. Custom forms

A form is a **JSON Schema** (data contract) plus a **UI schema** (presentation and
behaviour). The designer edits both visually; a code view shows the YAML.

### Field types
text, textarea, number, slider, toggle, dropdown, multi-select, radio cards,
date/time, tags, key-value, datagrid (rows of sub-fields), secret, file upload,
Markdown or info block, and resource pickers:
- vSphere: cluster, datastore, network, folder, template, existing VM
- EfficientIP: subnet, free IP, DNS zone
- AWX: inventory, credential, host
- App: project, user, group, deployment

### Behaviour
- **Dynamic values**: options, defaults or validation results from a *value source*:
  - built-in sources (vSphere inventory cache, EfficientIP, AWX, app data),
  - REST source (URL template, auth, JSONPath mapping, caching),
  - CEL expression over other fields.
- **Cascading dependencies**: a field declares `dependsOn` and re-resolves when
  those fields change (cluster → datastores → networks → subnets).
- **Conditional rules** (CEL): visible, required, read-only, value.
- **Validation**: JSON Schema constraints, regex, CEL cross-field rules, async
  server-side validation (for example "hostname free in DNS").
- **Layout**: sections, columns, tabs, multi-step wizard with a review step.
- **Computed fields**: for example the VM name preview from the naming policy.
- Every form is validated again on the server. The client is only for UX.

**Built (S8):**
- The form designer: fields by section, a live preview as a chosen project
  member, and the selected field's properties; every change saves the draft
  and the server's problems show where they are
  ([frontend.md](frontend.md#catalog-administration-and-the-form-designer)).
  The YAML view edits anything the designer doesn't.
- Value sources: the built-in ones, `rest` (a URL template with escaped
  inputs, GET or POST, a stored secret as bearer, basic or header, JSONPath
  mapping, cached; only hosts in `RVAV_REST_SOURCE_HOSTS`, no redirects,
  5 s and 1 MB at most) and `cel` (options or a value over the inputs,
  project, user and environment). Dependencies re-resolve a field when the
  inputs it depends on change.
- Rules are evaluated on the server, in the preview and again on submit:
  a computed value becomes the input, and a field a rule makes required is
  checked.
- For AWX items, the form can choose the launch's inventory and credentials
  (inputs picked from `awx.inventories` and `awx.credentials`), which the
  template must prompt for.

Not built yet: tabs and wizard steps (forms render as sections), file upload, async
server-side validation rules, and the group and AWX host pickers.

## 4. Requesting

- Live validation, quota usage preview ("this uses 8 of your 40 remaining vCPU"),
  and approval preview ("needs approval from the Infra leads").
- Request for yourself or **on behalf** of another user.
- **Count**: request N identical deployments.
- **Scheduled** requests ("deploy on Monday 08:00").
- Save as draft; **re-request** from history with prefilled inputs.
- Cancel while pending or, where possible, while running.

## 5. Policies

All policies have a scope (global, project or item), a CEL condition, and a priority.

### Approval
- Multi-level (sequential), with any-of or all-of approvers per level.
- Approvers: users, groups, the project admin, the requester's manager (from an
  OIDC claim).
- Conditions, for example `inputs.memoryGB > 32 || inputs.environment == "prod"`.
- Auto-approve rules, delegation (out of office), timeout → auto-approve,
  auto-reject or escalate.
- Approve or reject from the web inbox, email links, or Slack/Teams buttons.
- Approvers can edit allowed inputs before approving (for example reduce RAM),
  and this is audited.

### Lease
- Default and maximum lease; "never expires" only where the policy allows it.
- Reminders before expiry (for example 7 days and 1 day).
- Self-service extension within limits, or extension through approval.
- Expiry action: power off → grace period → delete (configurable).

### Day-2 entitlement
- Which roles can run which actions, on which resource types, under which
  conditions (for example "members can snapshot but not delete prod VMs").

### Quota
- Per project and optionally per environment: vCPU, RAM, storage, VM count.
- Hard (block) or soft (warn, or require approval).

### Placement
- Project → allowed compute, storage and network targets.
- Strategy: first fit, most free capacity, or round robin; tag-based constraints
  (for example `env:prod`, `tier:gold`).
- The form can let the user choose among allowed targets, or hide them entirely.

### Naming
- Template such as `{project.code}-{inputs.env}-{inputs.role}{seq:03}`.
- Counter scope (global, project, template), uniqueness checked in vCenter and DNS.

## 6. Deployments

- List with filters (project, owner, status, item, lease expiring), saved views,
  bulk actions.
- Detail page: summary, resources (VMs with live power state, IPs, DNS names),
  topology diagram, inputs, **timeline** of all requests and day-2 runs, lease,
  audit.
- Change owner or project (with quota checks).
- Onboarding: adopt existing vCenter VMs into a deployment.

## 7. Day-2 actions

### Native (vSphere)
Power on, off, shut down guest, reset, restart guest · reconfigure CPU and RAM
(hot-add aware) · add or extend disk · add or remove NIC · change network ·
snapshot create, revert, delete (with a retention policy) · web console (VMRC/HTML
console ticket) · rename (vCenter + DNS) · mount or unmount ISO · change owner or
project · extend lease · delete (VM + IPAM/DNS release).

### AWX-backed custom actions
- Any job template can become a day-2 action bound to resource types and
  conditions, for example "Install monitoring agent" (Linux VMs only) or
  "Patch now".
- Form generated from the survey; the target VM is passed as `limit` plus
  `extra_vars.rvav` context (deployment, resource, project, requester).

### Common to all
- Confirmation dialogs with a clear preview of what will change.
- Optional approval through the approval policy.
- Bulk execution across selected resources.
- Each run has a live timeline and goes into the deployment history.

## 8. Extensibility (event subscriptions)

| Topic | When |
|---|---|
| `request.submitted`, `request.approved`, `request.rejected` | Request lifecycle |
| `deployment.provision.pre`, `.post` | Around provisioning |
| `resource.day2.pre`, `.post` | Around each day-2 action |
| `deployment.delete.pre`, `.post` | Around deletion |
| `lease.expiring`, `lease.expired` | Lease lifecycle |

Targets: an AWX job template or a webhook (HMAC-signed). **Blocking**
subscriptions can stop the flow or modify allowed properties (for example a
CMDB check that returns a cost center). Non-blocking ones run asynchronously.

## 9. Notifications

Email, Slack and Microsoft Teams: request submitted, approval needed, approved or
rejected, succeeded or failed, lease expiring or expired. Users choose their
channels in preferences.

## 10. Reporting

Dashboards for requests over time, success rate, mean provisioning time, top
items, resource usage per project versus quota, upcoming lease expiries,
and approval lead time.

> Cost estimation and showback are out of scope for now (see the
> [roadmap](roadmap.md#out-of-scope-for-now)).
