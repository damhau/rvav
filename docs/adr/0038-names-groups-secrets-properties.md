# ADR-0038: Name counters, property groups, secrets in templates, properties

- Status: accepted
- Date: 2026-11-18
- Extends the naming policies of [ADR-0020](0020-administration.md), the
  secrets of [ADR-0019](0019-catalog-administration.md), the blueprints of
  [ADR-0024](0024-multi-vm-blueprints.md) and the importer of
  [ADR-0027](0027-vra-importer.md). T1.5 (#138).

## Context
vRA's custom naming has a counter that starts and moves as the
administrator says; rvav's always started at 1 and moved by 1 (parity C7).
vRA property groups let several cloud templates share inputs and
constants (C25). vRA projects carry custom properties that reach every machine (C6), and a
cloud template's machine can say `preventDelete`, `folderName` and its
own `properties` (C30).

## Decision

### Name counters
- A naming policy has `start` (0 or more, 1 by default) and `increment`
  (1 to 1000, 1 by default). Migration 00058 adds `counter_start` and
  `counter_step` to `naming_policies`; existing policies keep 1 and 1. A
  blueprint's own template, without a policy, counts from 1 by 1.
- Counters stay one row per prefix or per policy in `name_counters`
  (`last_value`, the last number given). The next number is
  `greatest(last_value + increment, start)`: a new counter starts at
  `start`, one in use goes on from its last number, and jumps to `start`
  when an administrator raises it past that number. Lowering the start
  never goes back to numbers given before. The upsert is the same single
  statement as before, so names stay unique under concurrent requests;
  the names passed over (taken in vCenter, DNS or rvav) are skipped by
  `increment` too.
- The policy's preview and a request's look-ahead (the DNS prefetch) count
  the same way.
- A number wider than `{seq:NN}`'s padding is printed whole (`web1000`).

### Property groups
- A property group is a key, a name, up to 50 inputs and a map of
  constants, in `property_groups` (migration 00061), one row per version.
  A version never changes: saving a group adds the next one, so an item
  stays as it was published until its own new version names the new one.
  Catalog admins manage groups on **Property groups**, under Catalog in
  the administration menu (`/admin/property-groups`, `catalog.manage`).
- An input has a name, a label, a type (`string`, `integer`, `number`,
  `boolean`), and may be required, secret (text without options or
  default), have a default, a help text and, as text, options, and be
  exposed to the AWX inventory (`expose_to_inventory`, never with
  `secret`), as a form field may. Constants are scalars
  (text, number, yes or no): a binding reads `propgroup.<key>.<name>`
  whole, and a nested value would read as JSON text in a vSphere
  attribute.
- A blueprint names groups at a version: `property_groups: [{key: ops,
  version: 2}]`. The version is required: an item's form and bindings
  don't change under it when an administrator saves the group, as vRA's
  do. Saving the item checks that the group and version exist and that no
  input is both in the form and in a group, or in two groups.
- The store gives a version two ways. `GetVersionAsWritten` is the
  definition as saved: the item's admin pages, YAML export and Git read
  it. `GetVersion`, which everything a requester reaches reads (the
  request form, preview, submit, drafts, approvals, secret masking,
  bindings), adds the groups' inputs to the form schema, UI schema and
  initial values, each group's in a section of its own
  (`propgroup-<key>`, titled with its name), and their constants to
  `GroupConstants`. So no requester path needs to know about groups, and
  a secret group input is sealed and masked like any other.
- What reads a request's or deployment's inputs outside the API, the
  integration events (`outbox.RequestData`, `DeploymentData`) and the AWX
  inventory (`store.InventoryHosts`), merges the groups' UI fields the
  same way (`store.UISchemaWithGroups`). The events leave out a group's
  secret inputs as the form's, and the inventory has a group input when
  it's exposed. An AWX item's launch already had them all: it gets the
  submitted inputs, a secret one opened at launch.
- Form rules, bindings, properties, folders and composite steps read the
  constants as `propgroup.<key>.<name>`; the worker reads them from the
  version when it works out a composite step.
- A group is deleted with all its versions, and only when no version of an
  item other than a draft names it: published and retired versions may be
  rolled back to. A draft naming a deleted group is refused when saved
  next. The delete and a publish hold the group with a transaction
  advisory lock (`store.LockPropertyGroup`): a publish checks again,
  under the lock, that each group version it names is there, so a group
  deleted meanwhile gives a 409 rather than a published version whose
  form silently lost the group's inputs.

### Custom properties and folders
- A project has `properties`, a map of up to 50 keys (letters, digits,
  `.`, `_`, `-`, starting with a letter) to text of up to 512 characters,
  in `projects.properties` (migration 00059), set on the project's
  **Details** tab. A blueprint and its components have `properties` too:
  text with `${CEL}` over the inputs and the project, worked out when the
  request is made; a component's go over the blueprint's key by key, and
  both over the project's.
- The VM gets them as vSphere custom attributes, set once it is cloned
  (rvav defines for VMs the attributes vCenter doesn't have yet; a
  concurrent clone that defined it first is fine), and in its notes after
  `Managed by rvav. Deployment <id>`, a `key: value` line each. vcsim
  supports custom attributes, so guestinfo isn't used for them.
- The merged properties are copied into the plan (`VMUnit.properties`,
  omitempty): a project's later change doesn't touch VMs built before.
- A property that reads a secret input is `••••` everywhere (X1.1): in
  the plan, the custom attribute, the notes and the VM resource. It is
  found by working the property out again with the secret inputs replaced
  by two stand-ins: it changes or fails with either. A composite's step
  given a secret through a binding its item's form doesn't mark secret
  has a property holding the sealed value masked too. Custom attributes
  are readable by anyone who sees the VM in vCenter, so a secret has no
  place there.
- The VM resource carries the properties it was built with
  (`vm.custom_properties`, kept by the inventory sync like its FQDN); VMs
  built before X1.1 have none.
- Bindings, form rules, approval conditions and composite steps read
  `project.properties.<key>` (`project` has `id`, `code`, `name` and
  `properties`). Saving an approval policy accepts a property its sample
  project lacks, as it does an input. The blueprint carries the
  project as a context its bindings read besides the inputs.
- `folder` on a blueprint or component replaces the placement rule's
  folder: from the root as it is, else below the VM folder of the rule
  folder's datacenter (`/GVA/vm`), or below the rule's folder when it
  isn't in a datacenter's VM folder. `${CEL}` is allowed; `..` is not,
  and neither is a secret input, the form's or a group's (T1.7): a
  folder's name is in vCenter and the plan, and masking it would put the
  VMs in a folder named `••••`. Saving refuses it, and the vRA importer
  leaves out a folder or a custom property that reads an encrypted input,
  since a masked property is no use to the VM.
  A folder from the root in another datacenter than the cluster placement
  chose is refused when the request is evaluated, on the input the folder
  reads, else on the form (T1.7): vCenter clones only into the cluster's
  datacenter, and failing at the clone would leave its addresses, DNS
  records and folder made first.
  Missing folders are created, as for the rule's. **Move to another
  project** still moves VMs to the new project's rule folder.

### Keeping VMs from deletion
- `prevent_delete: true` on a blueprint or component is copied to the
  plan and to the VM's resource (`resources.prevent_delete`, migration
  00059) when the VM is recorded.
- The deployment's **Delete** is unavailable while one of its VMs is kept,
  and says which: the action list shows the reason and a request for it
  is refused (409). A lease's end doesn't delete such a deployment
  either: rvav checks first, cancels none of the requests waiting on it,
  and tells the owner once per lease (T1.7). Rolling back a failed request still removes
  the VMs it built: they never made a working deployment.
- **Allow deletion**, a deployment action, clears the flag on all its VMs.
  It is offered only to those holding `project.update` in the project
  (administrators), whatever the day-2 policies allow: owners and project
  admins, who may delete, can't lift the protection themselves. A
  separate step, recorded as a request, is the simplest way that keeps an
  accidental deletion from going through; a per-VM switch would need a
  VM-level delete, which rvav doesn't have.

### Secrets in templates
- vRA writes `${secret.name}` in a cloud template. rvav reads stored
  secrets in the two places a value leaves rvav at build time, and only
  there:
  - cloud-init user data, a Go template, calls `{{ secret "name" }}`
    (`secret.name` isn't a template field: names have dashes). The name is
    a quoted literal: `{{ secret .inputs.x }}`, a pipe into `secret` or a
    `call` of it is refused on save, so a requester never picks which
    secret is opened. The value is printed as it is; `| json` quotes it
    for YAML. The item's and the template's user data both may.
  - an AWX step's `with` value, or its undo's, that is exactly
    `${secret.name}`: kept out of CEL, it is set aside by name
    (`Resolved.secrets`, then `JobInput.secrets`, both omitempty) and
    opened by `RunJob` just before the launch, as secret inputs are.
- So the plan, the step's resolved values, the activities' inputs and
  results, and so Temporal's history, hold the name only; the timeline,
  logs, events and audit never see the value. `TestSecretsInTemplates`
  records every payload the workflow's data converter makes and finds the
  name, not the value.
- `${secret.` anywhere else in a blueprint (properties, folders, bindings
  worked out at submit, messages, REST bodies, an item step's or a
  workflow's inputs) is refused on save: its value would be worked out with
  the request and kept in the plan. A REST call names its secret in
  `secret`, as before.
- Saving checks that each named secret exists: in user data, the error
  says the line (`Line 3: there is no secret "nope".`). A template's user
  data is checked when the template is saved.
- A secret's **Used by** lists the items whose versions name it anywhere
  (REST sources and calls, Windows guests, user data, AWX bindings), and
  `template:<key>` for templates whose user data reads it; it can't be
  removed while one does. Versions are found with `strpos` on the
  blueprint's text, then read.
- AWX shows a job's extra variables to those who see the job, and vCenter
  users read guestinfo: the same limits as secret inputs (S14.6).

### Project secrets
- A secret may belong to a project (`secrets.project_id`, migration
  00060; a name is unique per scope). That project's requests open it
  rather than the global secret of the same name; other projects' never
  do, whatever their items name. The worker looks it up by the request's
  project (`project_id` is in every activity input that opens one), as
  value sources do by the form's project.
- Its value is sealed with `project:<id>:<name>` as associated data, as
  request inputs and vCenter passwords have their own prefixes: a row
  copied into another scope doesn't open. Rotation reseals each under its
  own.
- Saving an item checks that a secret of each name exists in some scope.
  A request checks that its project has every secret its guests, user
  data and AWX steps open, and says which is missing; a REST call's is
  checked with its step, as before. A REST value source's cached answers
  are kept per secret opened, so one project's credentials never answer
  another's form.
- Catalog admins manage project secrets on the same **Secrets** tab, with
  a **Project** column; the list carries the projects to pick from, as
  entitlements do, so `project-names` stays for the pages it serves.
  Project admins don't manage them: a secret is a credential rvav holds,
  not a project setting.
- A project's own secret can be removed while items use it if a global
  one of its name remains: its requests open that one instead. Webhook
  signing secrets are global only.

### The importer
- vRA's custom names (`/iaas/api/naming`, read with the estate; none
  before vRA 8.3) name a project's machines before its
  `machineNamingTemplate`, its own before the organisation's default.
  The pattern translates as the template did. An item's naming has no
  counter settings, so a start or a step other than 1 is unmapped, with
  the naming policy to make instead: the counter belongs to the policy.
- `preventDelete: true` is the component's `prevent_delete`. The other
  resource flags are unmapped; timeouts are left out with an info note,
  rvav's own apply, and vRA's canvas `metadata` is ignored.
- `folderName` is the component's `folder`. A machine's properties
  outside vRA's vSphere machine schema are its custom properties: text
  and numbers, `${input.x}` as `${inputs.x}`, checked as an item's are.
  Lists, maps and values that read a machine are unmapped, since rvav
  works properties out when the request is made, before any VM exists.
- `${secret.x}` in `cloudConfig` is `{{ secret "x" }}`, its name lower
  case with dashes. vRA's API never returns a secret's value, so the
  import can't carry it: the plan puts the item in conflict until a
  secret of each name it opens exists in rvav, in any scope, rather than
  importing an item the YAML import would refuse. A secret anywhere else
  is unmapped.
- A vRA project's custom properties go to the rvav project it maps to,
  when the catalog is imported (`properties_add` in the plan,
  `projects_updated` in the result, audited as `project.update`). They
  are only added: a key the rvav project has keeps its rvav value, and
  keys rvav refuses, or past its limit, are listed as unmapped.
- vRA's property groups (`/properties/api/property-groups`) become rvav
  property groups before the items, keyed by their name (a project's
  first, then the organisation's; `_2` on a clash). A `$ref` input is
  the group named in `property_groups`, `${input.g.p}` is `inputs.p`,
  `${propgroup.g.p}` is `propgroup.<key>.p`. In cloud-init the
  constant's value is written in: the user data is a Go template, which
  has no `propgroup`. The import makes a group once and never changes
  it: when rvav's differs, items name rvav's latest version with a
  warning, as an administrator may have changed it on purpose.

## Consequences
- Names, deletion protection, folders and custom properties carry over
  from vRA without hand edits; the notes say what didn't.
- An item that opens a secret can't be imported before the secret is
  stored: the plan names each one, and importing again picks the item
  up.
- A machine property rvav doesn't know becomes a custom attribute. One
  that was a vSphere setting outside the list rvav keeps would show up
  as an attribute: the notes list every property made, to check.
