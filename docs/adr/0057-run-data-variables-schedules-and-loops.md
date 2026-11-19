# ADR-0057: Run data, shared variables, schedules and loops

- Status: accepted
- Date: 2026-11-19
- Builds on the composites of [ADR-0022](0022-custom-workflows-and-composites.md),
  their step kinds of [ADR-0033](0033-composite-step-kinds.md), the run on
  its graph of [ADR-0032](0032-one-canvas.md) and the secrets of
  [ADR-0038](0038-names-groups-secrets-properties.md). E1.5 (#165).

## Context
vRO keeps each workflow run's variables: what every item was given and
what it gave, readable in the run's log when something went wrong. rvav
kept a composite step's outputs, for later steps' `steps.<key>`, but not
what the step was given, and showed neither: a failed REST call's body, or
the extra variables a job got, were in Temporal's history at best, with
the secrets an operator must not read. The rest of E1.5, vRO's
configuration elements, recurring schedules, loops, error handling and
signals, follows in the sections below as each part lands.

## Decision

### What a run keeps of a step
A step's `inputs` are what it ran with, recorded by the activity that runs
it, so no workflow changes and no replay concerns:

- **An AWX job**, wherever it runs (a composite's step or item, a VM's
  post-configuration, a day-2 action, an undo): its extra variables as
  bound, rvav's own `rvav` context left out, a stored secret as its
  `${secret.<name>}` binding.
- **A custom workflow** (a workflow step or item): its `inputs`.
- **A REST call** (a step, a poll's check, an undo): its method, URL, the
  secret's name and its body as evaluated, before the secret inputs in it
  are opened.

A VM step records none: its VMs were planned from the form at submit, and
the request's inputs show what it asked. A wait, a branch and a message
record none either: a message's text stays in the notifications of those
told (ADR-0033). `outputs` are what later steps read as `steps.<key>`, as
before; a workflow's result is now its step's outputs too. A step run again
(a Retry) loses both, as it loses its status: what it shows is its last
run's.

### Who reads them, and how
`GET /requests/{id}/steps/{stepId}/data` serves both; `Step.has_data` says
there is something to read, so the steps list and the live events stay as
light as before. A step's data holds its blueprint's literals, a URL's
fixed text, a body, a template's variables, that a requester's view masks
(ADR-0033), so it needs `catalog.manage` for all of rvav: those who build
the items debug their runs. A requester reads the step's status and error,
as before.

Secrets are masked when read, never stored in clear:

- a secret input stays its marker, and reads `••••`;
- a stored secret stays its name, `${secret.<name>}`;
- a secret input's value inside a text, a REST answer echoing it, reads
  `[removed]` (the scrubber of REST calls).

A wait for a person's answer stays with those it asks and those who read
every approval, as its question does on the request's page: anyone else
gets `outputs_hidden`.

### In the web app
A step with data has **Show data** on its timeline, in the list view and in
the selected node's details. The **Step data** panel shows the step chosen,
else the selected node's own step: its inputs and its outputs as a list of
names and values, a masked secret read out as such, and the
`steps.<key>.<name>` later steps read. The mocks work a step's data out
from its bindings when it runs.

### Shared variables
vRO's configuration elements are values workflows share: a site's DNS
servers, a CMDB's site code, a backup bucket. rvav's property groups
(ADR-0038) give an item constants too, but each item names a version of the
group and keeps it: a new value reaches an item only once it is saved and
published again. A shared variable is read as it is when the step runs.

- A variable is a group and a name, `[a-z][a-z0-9_]*` each, and a JSON
  value: text, a number, true or false, a list or an object, 16 KiB at
  most. Composite steps and conditions read it as `vars.<group>.<name>`:
  `with`, `when`, a REST call's URL and body, a wait's or a poll's `until`,
  a branch's cases and a message's text. The VM steps planned at submit read
  it then.
- Each read takes the value of the moment: the `ResolveStep` activity, and
  each activity that evaluates a step's CEL, loads the variables. A change
  reaches the steps that haven't run yet, in requests already running too.
  The workflow code is unchanged, so nothing needs `workflow.GetVersion`.
- They aren't secret, and nothing masks them: they show in the run data
  and in the audit log, with their values. A group or a name that says it
  holds a password, a secret or a key is refused; those are stored as
  secrets, which stay masked.
- `catalog.manage` for all of rvav reads and changes them, as it does
  property groups, under `/admin/variables`. Each change is audited with
  its value, and an update with the value it replaced.
- Publishing a version whose steps read a variable that doesn't exist is
  refused (`unknown_variable`), as a resource type that doesn't exist is;
  a draft may name one not added yet. A variable a draft or a published
  version reads isn't removed, so a published item never reads one that
  is gone.

The **Variables** page, under Catalog in the administration menu, lists
them with their values and the items that read them. The composite
designer offers them among what an input can be bound to.

## Consequences
- An author sees why a step failed from what it was given, without reading
  Temporal's history or the worker's logs.
- A value several items share is changed in one place, without a new
  version of each; an item's steps can read a different value from one
  request to the next, so the run data is where to see what each read.
- `request_steps.inputs` grows a run's rows by what its steps take, the
  size of their outputs; nothing new reaches Temporal's history.
- A requester's view is unchanged: a blueprint's literals stay masked for
  them.
