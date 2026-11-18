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
vRA projects carry custom properties that reach every machine (C6), and a
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
- Bindings and form rules read `project.properties.<key>` (`project` has
  `id`, `code`, `name` and `properties`). The blueprint carries the
  project as a context its bindings read besides the inputs.
- `folder` on a blueprint or component replaces the placement rule's
  folder: from the root as it is, else below the VM folder of the rule
  folder's datacenter (`/GVA/vm`), or below the rule's folder when it
  isn't in a datacenter's VM folder. `${CEL}` is allowed; `..` is not.
  Missing folders are created, as for the rule's. **Move to another
  project** still moves VMs to the new project's rule folder.

### Keeping VMs from deletion
- `prevent_delete: true` on a blueprint or component is copied to the
  plan and to the VM's resource (`resources.prevent_delete`, migration
  00059) when the VM is recorded.
- The deployment's **Delete** is unavailable while one of its VMs is kept,
  and says which: the action list shows the reason and a request for it
  is refused (409). A lease's end tries **Delete** too, so it doesn't
  delete such a deployment. Rolling back a failed request still removes
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
