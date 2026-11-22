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
signals, follows in the sections below.

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
- **A custom resource's step** (ADR-0056), its type's create, update, read
  or delete, from a blueprint, a composite's resource step, a day-2 action
  or a deletion: what its type's driver is given, as a workflow or a job
  is above. A create or an update is given the inputs bound; a read or a
  delete, the resource's properties.

A VM step records none: its VMs were planned from the form at submit, and
the request's inputs show what it asked. A wait, a branch and a message
record none either: a message's text stays in the notifications of those
told (ADR-0033). `outputs` are what later steps read as `steps.<key>`, as
before; a workflow's result is now its step's outputs too, and a custom
resource's step keeps the resource's properties after it, or what its
delete gave. A step run again (a Retry) loses both, as it loses its status:
what it shows is its last run's.

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

### Recurring schedules
vRA's scheduled workflows and vRO's recurring tasks run a workflow again
and again as someone. rvav's `scheduled_at` starts one request later; a
schedule makes a request at each of its times.

- A schedule is a request's draft (item, version, project, environment,
  inputs, justification, whether warnings are accepted) with a name, an
  owner, and when it runs: five cron fields read in an IANA time zone, or
  every 5 minutes to 31 days. Its creator owns it: there is no "on behalf
  of", and an owner who leaves the project has their runs refused.
- Only an item that makes no deployment (`MakesDeployments` false: a
  custom workflow, an AWX job, a composite without a VM step) can be
  scheduled. Each run of a VM item would make another deployment, against
  quota and deployment limits, with no one to name it or let its lease
  expire.
- A schedule keeps no secret input: one with a value is refused
  (`secret_input`). A run reads secrets the item stores (ADR-0038).
- Creating one checks the draft as `POST /requests` does, strictly: an
  input, a rule or a version that would refuse the request refuses the
  schedule. Each run is then submitted as the owner, through the same
  path as a request: entitlement, sharing, policies, approvals, quota and
  deployment limits as they are at that moment. The version is pinned;
  once it isn't requestable, its runs are refused (`stale_version`).
- Temporal runs it: a Temporal schedule per rvav schedule, its action the
  `ScheduledRun` workflow, a new type, so no `workflow.GetVersion`. That
  workflow's one activity submits the run. Overlap is `SKIP`, and the
  catch-up window 15 minutes: times missed while Temporal or the workers
  were down are dropped but the last within 15 minutes. The cron
  expression is checked by a pattern, then by Temporal, whose refusal is
  a 422 on `/cron`.
- A firing is idempotent: the run is recorded in `schedule_runs` under the
  firing workflow's ID, in the request's own transaction, so a retried
  activity submits once.
- A run is `started`, `refused` (what refuses a request refused it, said
  in one line), `skipped` (paused, a request the schedule started not
  finished, approval included, or its owner not seen for too long) or
  `failed` (rvav couldn't submit it after its retries). Whether one of its
  requests is unfinished is asked in the run's own transaction, with the
  schedule's row locked, so two firings at once start one request, and a
  skipped run lets no other start while that request runs (E1.6).
- A run has the owner's groups from their last sign-in (`ForUser`): an
  owner removed from a group at the identity provider who never signs in
  again would keep its rights through their schedules. A run is skipped
  once the owner hasn't signed in or used an API token for
  `RVAV_SCHEDULE_OWNER_IDLE_DAYS` (90 unless set; making the schedule
  counts), and runs again once they do. The schedule shows when its
  owner was last seen (`owner_seen_at`). The owner isn't notified of a refusal; the schedule's
  page shows it. A scheduled run's request isn't announced as submitted to
  its owner either; its later notifications are as for any request.
- The owner and those with `request.manage` in the project pause, resume
  and delete it; those with `deployments.read_all` there see it too. There
  is no edit: a new schedule replaces it. Deleting one keeps its row, so
  the requests it started still name it. Each change is audited
  (`schedule.create`, `schedule.pause`, `schedule.resume`,
  `schedule.delete`).

The request form offers **Again and again, on a schedule** under **Start**
for those items, and **Schedules**, from the requests page, lists the
project's with their next and last runs.

### Loops, catch paths and signals
vRO's workflows loop over an array, catch an exception around a few items
or the whole schema, and wait for a signal or a custom event. ADR-0033
left these to a playbook or a custom workflow; the vRO triage counts them
in workflows that are otherwise a composite's, so a composite gets each,
narrowly.

- **A loop** is a modifier on a step: `for_each: {items, parallel}`. Its
  list is CEL, read when the step begins, of at most 100 items; up to 10
  passes run at once. Each pass is a step of its own on the timeline,
  `step:<key>/each:<n>`, and reads `each` and `index` in its `with`, its
  `when` and a REST call's URL and body. The loop's outputs are
  `{items, count}`, each pass's outputs in the list's order. A pass that
  fails stops those not begun and fails the loop. In a parallel group, a
  sibling's failure stops those not begun too: they are skipped, "Another
  step of its group failed.", and the loop is cancelled (E1.6). A Retry
  reads the list again and keeps the passes done, by position.
- Every later activity carries the steps' outputs, in Temporal payloads of
  2 MB at most. A loop's outputs are 512 KiB at most, the steps' 1.5 MiB in
  all (a REST step keeps 256 KiB of its answer, and a composite could have
  six such before loops): the step that goes over fails, saying so, with
  its outputs dropped, instead of a workflow task that fails again and
  again. Both are behind `workflow.GetVersion("flow-limits")` (E1.6).
- What can't loop, and why: a VM step (its VMs are planned at submit, so
  its item's count is the way), a custom resource step (one resource of its
  deployment, under the step's key, rolled back by its type's delete step
  as ADR-0056 has it: a loop would make several under one key), a wait of
  any kind (a pass waiting across runs of the workflow, its state in a row
  per step), a branch, and a step with an `undo` (each pass would need its
  own).
- **A catch path**, `catch: [steps]`, sits on a step, a side's step or a
  parallel group, not on a step inside a group: a group's steps stop
  together, so one path handles them. On a failure the running siblings
  stop, the path runs with `error.step` and `error.message`, and the
  composite goes on. The failed step stays failed on the timeline, with
  `{caught: true, error}` as outputs, which counts as done: later steps
  read `steps.<key>.caught`, and a Retry doesn't run it again. A path that
  fails fails the request.
- **The error handler**, the composite's `on_error`, runs on a failure
  nothing caught, before the undo steps and the rollback; the request still
  fails. It runs again on each failure, a Retry's too, since each is a new
  incident to report.
- Catch paths and the handler hold AWX jobs, workflows, REST calls and
  messages: steps that run once with nothing to wait for or undo. What
  can't have a catch path: a VM step, a custom resource step or a group
  with either, since what they built is the rollback's to delete and the
  request can't go on past it (ADR-0056 keeps a resource's undo with its
  type), and a branch, whose sides' steps take one each.
- **A wait for a signal**, `signal: {name | event, when, timeout,
  on_timeout}`, keeps its state in `signal_waits`, one row per step, as an
  ask keeps its level: the workflow waits on a channel per step
  (`rvav.signal.<key>`), reads the row on a signal and each hour, and
  continues as new on a long wait. A signal lost (the API down between
  storing it and telling the workflow) is read at the next recheck.
- A Retry that resumes keeps a wait that took its signal or event, as it
  keeps the step done. A Retry after a rollback runs everything again:
  the waits whose step read what came are dropped, so those steps wait
  again, as an ask asks again (E1.6). A signal received that no step read
  yet (the request failed elsewhere in between) stays for the step.
- `POST /requests/{id}/signal/{name}` stores the body, a JSON object of at
  most 64 KiB, in the open wait and signals the workflow; it answers 202
  with the step's key. It needs `request.manage` in the request's project,
  the role a script's API token has, and the requester lacks: a wait the
  item puts in their way is not theirs to lift. Nothing waiting is a 409,
  not a signal kept for later, so a signal sent too early is seen at once.
  Each is audited, `request.signal`.
- A wait for an event takes the first of its topic, among events after it
  began (`after_seq`) and of the request's own project, that its CEL
  `when` picks, over the event's variables and `waiting` (the request and
  its steps before the wait). The dispatcher matches it in the round that
  hands events to subscriptions, in the same transaction, and signals the
  workflows once that commits. The project bound keeps another project's
  data, which becomes the step's outputs, from its requester.
- `after_seq` is the outbox's highest `seq` when the wait opens. An event
  whose transaction took a lower `seq` but commits after the wait opened
  is not given to it: it began before the wait. A subscription saved at
  that moment draws the same line (its own `after_seq`). Telling those
  apart would need each event's commit order, which the outbox doesn't
  keep; a wait is for what happens after it opens, and an event racing
  its opening may go either way. A wait that must not miss one has a
  timeout and its `on_timeout`.
- The canvas draws a loop as lines on its step, a signal as a wait, a
  catch path as the steps after what it catches, joined by a dashed "if it
  fails" edge, and the handler after Done; the designer edits each, with
  the checks the API makes on request (ADR-0032). A catch path's step picker
  leaves out what it can't hold, and changing a step to a kind that can't
  loop drops its loop; a catch path a step can't have stays, marked, for
  the author to move. The mocks run a loop once, skip catch paths and the
  handler, and let a signal reach its timeout.
- The workflow code is behind `workflow.GetVersion("composite-flow")`, read
  only when a plan has a loop, a catch path, a handler or a signal, so a
  workflow in flight replays as before. The histories `composite-flow`
  (a loop, a caught failure and a wait that continues as new) and
  `composite-signal` (its next run, taking the signal) are replayed by
  `TestReplayHistories`.

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
- A recurring job is a request like any other: its runs are in the
  requests list, the audit and the reports, and policies and approvals
  apply to them. A schedule whose runs need approval waits on its
  approvers, and skips its times until they decide.
- Temporal holds when a schedule runs; rvav's table holds what it runs.
  A restore takes both to the same moment (they share the PostgreSQL
  server), and a Temporal schedule rvav no longer knows does nothing.
  The row and the Temporal schedule aren't written in one transaction: a
  creation Temporal refuses removes the row, and each worker's start
  makes Temporal agree with the rows (one without its Temporal schedule
  gets it, a pause or a deletion Temporal alone took is aligned), so a
  process that stopped in between leaves nothing that never runs. Only
  schedules rvav deleted are deleted in Temporal: a namespace may hold
  another rvav's.
- ADR-0033's line, a flow that needs loops or error handling is a
  playbook's or a custom workflow's, moves: a composite loops over a list
  and catches a step's failure. Paging an API, nested loops and a loop of
  waits stay a playbook's or a custom workflow's.
- A caught failure leaves a failed step in a request that succeeds: the
  timeline and the reports count it as failed, and the run data shows why.
- Another system moves a request on with a project manager's token, and
  sees nothing of it but the step's key in the answer. A wait for an event
  ties a request to what happens elsewhere in its project; an event that
  came before it waited doesn't count, so the order of two requests
  matters, and the timeout is the way out.
