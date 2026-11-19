# ADR-0050: Designed forms for custom day-2 actions

- Status: accepted
- Date: 2026-11-19
- Extends the AWX day-2 actions of [ADR-0018](0018-awx-day2-actions.md),
  the workflow actions of [ADR-0022](0022-custom-workflows-and-composites.md), and the
  form designer, rules and value sources of
  [ADR-0019](0019-catalog-administration.md) and
  [ADR-0043](0043-form-library.md). E1.3 (#163), vRA parity B19.

## Context
A custom day-2 action, an AWX template labelled `day2` or a registered
workflow offered on deployments, asks for the form its AWX survey or its
input schema gives: one field per question, in one section, with the
survey's defaults. vRA lets an administrator lay out a resource action's
form with its custom forms designer: sections, dynamic choices, visibility
rules, and defaults read from the resource the action runs on.

rvav already has that designer for catalog items, and a form engine that
works its rules and value sources out on the server (ADR-0019, F1).

## Decision

### One designed form per action
- An administrator (`extensibility.manage`) may design the form of any
  `awx.*` or `wf.*` action, offered or not. Migration 00088 keeps it in
  `day2_forms`, one row per action: its JSON Schema, UI schema and initial
  values, whether it is active, and a revision.
- While inactive, the form is a draft: it is checked as a catalog item's
  draft is, and the action keeps asking for its survey's or schema's form.
  Turning it on checks it as a published version is; from the next run,
  the action asks for it. Dropping it gives the survey's or schema's form
  back.
- A form isn't versioned: a run keeps the inputs it was made with, and
  nothing reads the form again once the run is a request. A save names the
  revision it was made on; another save since is refused (409), as the
  catalog designer's drafts are.
- The form shows in the action's dialog, so it may not be a wizard, take a
  file (a run has no version to attach one to) or take a secret (a run
  keeps its inputs with the request as they are).
- What the survey or input schema asks for and the form doesn't give, or
  gives as another type, is a warning on the form, not a refusal: the
  template's survey and the workflow's schema stay the contract, which AWX
  or the workflow enforces.

### The same engine, on the target
- The form's rules, value sources (inline and from the library) and
  validations work as a request form's. Its rules and sources also read the
  target: `resource` (the VM or the deployment acted on: its name, type,
  power state, size, guest OS, addresses, cluster, folder, tags and custom
  properties; a deployment's VMs) and `deployment` (its ID, name,
  description, owner and item). Request forms read them as empty maps.
- `POST /deployments/{id}/actions/preview` gives the field states and
  errors for the inputs so far, as `previewRequest` does; `POST
  /deployments/{id}/actions/values` resolves a field's source, as
  `resolveFieldValues` does. Both are for whoever may run the action on the
  target. A run applies the states as a request's submit does (defaults,
  hidden fields dropped, computed values) and checks the schema, the
  values picked from built-in sources and the validations; the job or the
  workflow gets the inputs that result.
- `ActionForm.designed` tells the dialog to use those two endpoints. The
  other actions' forms are checked against their schema as before.

### The designer
- **Day-2 actions** in the administration lists the AWX and workflow
  actions with their form's state; **Design the form** opens the catalog
  designer on it (`/admin/day2-actions/<key>/form`): the same field list,
  properties and preview, saved as the user edits. The preview runs on a
  deployment, or one of its VMs, the administrator picks, as the
  administrator.

## Consequences
- An administrator can lay out an action's form, default its fields from
  the VM, offer choices from the CMDB or the library, and hide what doesn't
  apply, without changing the template or the workflow.
- A survey changed in AWX doesn't change a designed form: the warnings say
  where they differ.
- A form's validations and REST sources run as the user who runs the
  action, with the deployment's project's secrets, as a request form's do.
