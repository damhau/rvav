# ADR-0055: The API fills in the inputs a request leaves out

- Status: accepted
- Date: 2026-11-19
- Builds on the form engine of [ADR-0019](0019-catalog-administration.md),
  the designed day-2 forms of [ADR-0050](0050-designed-day2-forms.md) and
  the command line of [ADR-0051](0051-command-line.md). O1 (#194).

## Context
The web app starts a request form with the version's initial values and
shows each field's default; it sends every value, and `null` for a field
with a default the requester emptied. The server filled in only the
defaults of `default` rules and value sources: a request through the API
without `size` or `post_config` failed, though the form gives both.
Every client had to do what the web app does: rvavctl merged
`initial_values` itself, and the Terraform provider and the PowerShell
module (#168) would have had to. No client but the web app worked out a
designed day-2 form's rules on its target. vRA's API gives a blueprint
input its default when a request leaves it out.

## Decision

### What a form starts with
A form's start values are its initial values and, for an input without
one, the `default` its JSON Schema property gives. The server serves them
as `initial_values`: `getRequestForm`'s `RequestForm`, a day-2 action's
`ActionForm` and the designer's previews. A schema `default` so shows in
the web app as an initial value does.

### Who gets them
- **A new request:** submit, `previewRequest` (the dry run) and the
  wizard's review step give each input left out its start value, before
  the rules run; rules and value sources then give the defaults of the
  fields still without an input, as before. `resolveFieldValues` fills
  in the inputs it is given alike, so that a source reads a start value.
- **A day-2 run:** `runDeploymentAction`, `previewDeploymentAction` and
  `resolveDeploymentActionValues` give each input left out the start
  value of the action's form on its target: a resize's the VM's size, a
  survey's its defaults. A designed form then works its rules out on the
  target, as its preview does.
- **Not an approver's edit or an update:** they evaluate the inputs a
  request or a deployment has. A field missing there was emptied, or left
  out before this decision, and it stays empty. An update sends every
  input (`UpdateDraft.inputs`), as the update form shows them.

### An input given stays as given
An input sent with a value keeps it. `null` for an input with a start
value or a default means the requester emptied it: it gets neither, and
it is dropped before the schema check, so that an optional field stays
empty and a required one is refused as required, not as the wrong type.
The web app sends a field emptied of its start value as `null`, as it did
for a field with a default.

### Clients
rvavctl no longer merges `initial_values`: it sends the inputs given. The
Terraform provider and the PowerShell module send the inputs a
configuration gives.

## Consequences
- A script requests an item, or runs an action, with the inputs it
  chooses; the request records every input it got, as one from the web
  app does.
- The web app's preview and the server agree on what a request gets, a
  schema `default` included.
- No workflow changes: the inputs are filled in before the request
  exists, and a request's plan, the job's extra variables and the
  workflow's input are made from them as before.
