# ADR-0043: The form library, and validations of the whole form

- Status: accepted
- Date: 2026-11-19
- Extends the custom forms and value sources of
  [ADR-0019](0019-catalog-administration.md), the property groups of
  [ADR-0038](0038-names-groups-secrets-properties.md) (whose versioning it
  follows) and the importer of [ADR-0027](0027-vra-importer.md). F1.5
  (#151), vRA parity D6 and A13.

## Context
In vRA, a custom form takes a field's choices or default from a vRO
action, and many forms call the same few actions (the CMDB's
applications, the open cost centres, a database's versions). An action is
written once, versioned in vRO, and a form passes it its inputs. vRA can
also check the whole form against an external service before the request
goes on (parity D6, A13).

rvav's value sources were written into each field (`rest`, `cel`,
`builtin`): the same CMDB call was copied into every item that needed it,
and changing its URL meant changing every item. Nothing checked a request
as a whole beyond the form's JSON Schema and its CEL rules. The nearest
was a blocking `provisioning.pre` subscription, which stops a request only
once it has been approved.

## Decision

### The library
- An entry has a key, a name, a description, a purpose (`source`: a
  field's choices or value; `validation`: a check of the whole form), a
  kind (`rest` or `cel`), the kind's `params`, `search` (for a source), up
  to 20 parameters and, for a validation, a refusal message. Migration
  00075 keeps them in `form_library`, one row per version.
- A version never changes: saving an entry adds the next one, as for
  property groups. The purpose doesn't change across versions, since forms
  use the entry as one or the other.
- Catalog admins (`catalog.manage`) manage it on **Form library**, under
  Catalog in the administration menu (`/admin/form-library`): a list, and a
  side panel to change an entry, test it with parameters (`POST
  /admin/form-library/test`, a saved entry or not) and see which items'
  versions name which of its versions.

### Parameters, not inputs
- An entry reads its parameters, never a form's inputs: `${params.x}` in a
  REST entry's URL and body, escaped as inputs are; `params.x` in its CEL,
  with `environment`, `project`, `user` and, for a source, `search`. So one
  entry serves forms whose inputs have other names, and an entry can be
  tested without a form.
- A form gives each parameter as a CEL expression over what its rules read
  (`inputs`, `environment`, `project`, `user`, `propgroup`, and `search`
  for a source). A parameter is `required` or not; a required parameter
  whose expression gives nothing (an input not filled in yet) keeps the
  entry from running: a source gives no options, a validation isn't run.
  An expression that fails for any other reason fails the source or the
  validation, which refuses the request (F1.6). Saving an item refuses an
  expression reading an input the form doesn't have.

### Naming an entry
- A field: `value_source: {kind: library, name: <key>, version: <n>,
  params: {<parameter>: <CEL>}}`. A validation: an entry of
  `ui_schema.validations` with `kind: library` and the same three.
- The version is required. Saving an entry never changes what a published
  form does: the form changes when a version of its item that names the
  new version is published.
- Saving an entry as a new version gives each item whose draft or
  published version names an earlier one a draft that names it: its draft
  is changed, or a new draft is made from its published version. A draft
  that wouldn't pass the designer's checks, or was changed at the same
  time, is reported on the entry's answer and left as it was.
- Saving an item checks that each entry it names exists at that version
  and is used for its purpose (a source in a field, a validation in
  `validations`), that each parameter is the entry's and its expression
  compiles. Publishing also needs every required parameter given, so a
  draft can be saved while it's being written.
- An entry is deleted with all its versions, and only while no item's
  version other than a draft names it: a published, deprecated or retired
  version may be published again. A draft naming a deleted entry is
  refused the next time it's saved.
- Inline `rest` and `cel` sources keep working; the library is the way to
  share one.

### REST and CEL, as before
- A REST entry runs as a `rest` value source does: only the hosts listed
  for REST value sources (Allowed hosts, or `RVAV_REST_SOURCE_HOSTS`),
  checked when the entry is saved and at each call; a stored secret by
  name, the project's before the global one; no redirects; 5 s and 1 MB at
  most; `cache_seconds`. A CEL entry runs in the same environment as
  form rules. No second machinery: `catalog_sources.go` and `values.go`
  run library sources, `validations.go` runs both kinds of check.

### Validations of the whole form
- `ui_schema.validations` lists up to 20 checks, in order, each `cel`,
  `rest` or `library`, with a key (for messages and the audit), a title,
  the inputs a message for no input shows on (`fields`), and a message.
- A CEL check (`params.check`) passes on `true`, `null`, `""` or `{}`;
  `false` refuses with the message; a text refuses with that text; a map
  refuses with a message per input it names (or, in a library entry, per
  parameter).
- A REST check calls its URL and reads the 2xx answer with JSONPath:
  `valid` (`$.valid`), `message` (`$.message`) and `errors` (`$.errors`, a
  map of input to message or a list of `{field, message}`). Without
  `valid`, an answer with no message and no errors passes.
- A check that can't run (another status, a timeout, a host rvav may not
  call, an answer it can't read, an expression that fails) refuses the
  request and says so: a validation never passes for want of an answer.
- A library validation's message for a parameter shows on the input that
  the parameter's expression reads alone (`inputs.x`); otherwise on the
  validation's `fields`, or on the form.
- The checks run once the inputs pass the form's own checks (schema,
  rules, pickers), so a service isn't asked about a half-filled form. They
  run on submit, before approval, and again on an approver's edits, on an
  update's request (ADR-0042) and for the items a composite's steps run.
  A preview runs them only when asked (`POST
  /requests/preview?validate=true`): the wizard asks before its review
  step, holds on the first page with a message and focuses the input
  through the form's handle. Messages for no input show above the form.
- Day-2 actions' forms (an AWX survey, a workflow's inputs) have no
  `ui_schema`, so no validations.

### Audit
- Every entry change (`form_library.create`, `form_library.update`,
  `form_library.delete`) and test (`form_library.test`) is audited, with
  the version, purpose, kind and REST host.
- Every validation run is audited (`form_validation.run`, on the item and
  project): its key and kind, the item's version, the entry and its
  version, the outcome (`ok`, `refused`, `failed`, or `not_run` when a
  required parameter had no value) and the REST host.
- A field's lookups through a library source aren't audited, as an inline
  source's aren't: the form asks on every change of the inputs it depends
  on.

### The importer
- A vRO action a custom form takes a field's choices or default from
  becomes a REST source entry when its script makes one GET to a URL the
  import can read: a literal URL, or a literal host URL followed by
  `createRequest`'s path. The URL may concatenate the action's inputs,
  which become parameters (`${params.x}`). The output type gives the
  JSONPaths: a list of text or numbers (`$`), `Array/Properties` (`$.label`
  and `$.value`), or one value for a default. The entry carries a warning
  to check its JSONPaths with Test.
- The key is the action's name as a property group key is made, numbered
  when actions share a name. The plan lists the entries (`form_library`)
  with **Create**, **Unchanged** or **Differs in rvav**; the import makes
  them after property groups and before items, audited as any entry
  change.
- A field names the entry at its version, each parameter bound to what
  vRA's form passes the action (a field, the environment, a constant), and
  depends on those fields. A text field given choices becomes a dropdown.
- No entry is made, and the field keeps its unmapped note with why, for
  an action that works its values out in JavaScript, makes several calls
  or a non-GET call, calls a vRO REST host by name (whose URL the estate
  doesn't hold), builds its URL in steps, names a host from an input or
  one rvav may not call, or that the estate lacks; and for a field whose
  form passes the action nothing for a parameter.
- The import never changes an rvav entry of the same key: when it isn't
  what the action gives, items name rvav's latest version, with a warning.

## Consequences
- A change to a shared source or check reaches its items as drafts to
  review and publish, never live, which is slower than vRA's and keeps a
  published form what was approved.
- A validation calling a service that is down blocks submits of its item
  until it is back: the outcome `failed` in the audit shows it. A form
  that must not depend on the service uses a source, which fails open.
- An entry that a form can't bind (a required parameter it doesn't give)
  is caught at publish, not at save.
- Only simple REST actions are imported. Others stay unmapped and named
  in the notes, to rewrite as a CEL entry or behind a REST service.
- vRO's JavaScript is never run by rvav.
- The mock API stores entries, gives drafts on save, and runs CEL checks
  with its small CEL evaluator, so the designer and the request form work
  without a server.
