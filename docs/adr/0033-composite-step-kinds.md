# ADR-0033: Composite step kinds

## Status
Accepted (2026-11-07). Extends [ADR-0022](0022-custom-workflows-and-composites.md)'s
composites, within what [ADR-0007](0007-extensibility-model.md) and
[ADR-0032](0032-one-canvas.md) set. Each D2 issue adds its kind here.

## Context
D2 ([implementation plan](../implementation-plan.md#d2-composite-step-kinds-after-n1))
gives composites the steps vRO workflows are made of, sized by
`rvav vro-triage -standard`: a REST call in 5 of 12 typical workflows, wait
for a person in 3, wait for a time in 3, a branch in 2, a notification in 1.
A composite step could run an item, an AWX template or a custom workflow, so
a workflow calling a CMDB needed a playbook for one HTTP request.

## Decision

### What a step kind is
- **One key per kind.** A step runs one of `item`, `awx`, `workflow`, `rest`,
  `ask`, `wait`, `poll`, `branch`, `notify`, each with its own fields, beside what every step has (`key`, `title`,
  `when`, and `undo` when it has something to undo). A later kind adds a key:
  nothing moves in existing definitions. A step with none of them, or more
  than one, is refused when saved.
- **The logic is CEL.** No scripting language and no code node. Work that
  needs one is an AWX playbook or a custom workflow.
- **It is everywhere a step is.** A kind is done when it has: the check on
  save and on request, its run in `internal/provision`, its part of
  `blueprint.Topology` (with the mocks' `topologyOf` and the shared fixture),
  its checks in `lib/composite.ts`, its badge and lines in `canvas/blueprint.ts`
  (form, designer, run, list), its fields in the designer (`composite/edits.ts`,
  `composite/scope.ts`), the mocks' run, and a browser spec. A new
  `request_steps.system` value needs a migration.

### A REST call (D2.2)
```yaml
- key: cmdb
  title: Register in the CMDB
  rest:
    method: POST
    url: https://cmdb.corp.example/api/ci/${steps.vm.name}?env=${environment}
    body: "{'name': steps.vm.fqdn, 'ip': steps.vm.ip, 'owner': inputs.owner}"
    secret: cmdb-token
    accept: [200, 201]   # default: any 2xx
    keep: $.result       # JSONPath; default: the whole answer
  undo:
    rest:
      method: DELETE
      url: https://cmdb.corp.example/api/ci/${steps.cmdb.json.id}
      secret: cmdb-token
      accept: [200, 204, 404]
```

- **Shared with value sources.** `internal/restcall` holds what REST value
  sources did: the allowed hosts (`RVAV_REST_SOURCE_HOSTS`), no redirects,
  1 MB, stored secrets as bearer, basic or a header. A value source keeps
  its 5 s, since someone is waiting at a form; a step has 30 s.
- **The host is fixed.** Expressions fill the path and query, URL-escaped,
  never the scheme, host or port. An empty value, `.`, `..`, a map or a list
  fails the step without calling. Hosts and secrets are checked on save and
  again on request, since either may have changed.
- **A step has no `with`:** its URL and body read `inputs`, `steps`,
  `environment` and `project` themselves. An undo that is a call may read its
  own step's outputs (`steps.cmdb.json.id`). Any step that can have an undo
  may have a call as its undo; a VM step has none (a failure deletes its VMs).
- **Outputs:** `steps.<key>.status` and `steps.<key>.json`, what `keep` picks.
  A path naming one thing gives it or null; one with a wildcard or a filter
  always gives a list. An answer that isn't JSON gives null. More than 256 KiB
  kept fails the step ("pick less with keep"): outputs travel through
  Temporal's history to every later step. Numbers are JSON numbers, so an ID
  over 2^53 loses precision; such an ID is better read as a string.
- **Retries follow the method.** GET, PUT and DELETE are retried like other
  activities on network errors, 5xx, 429 and 408. A POST or PATCH is made
  once, even if the worker dies during it: what it did is unknown, so the step
  fails and a person decides. A person's Retry repeats any call, knowing that.

### No secret stays behind a call
A stored secret or a secret input may be sent, never kept:
- **Temporal holds names and expressions.** The URL, the body and the
  secrets are resolved in the activity (`RunREST`), so none reaches the
  workflow's input or history.
- **Answers are scrubbed** of each secret used and every secret input, as
  they are, JSON- and URL-escaped and base64, in values and keys (`Scrubber`):
  an API that echoes its request or its headers leaves `Bearer [removed]`.
  Answer headers are never kept. A number or boolean holding a value becomes
  the mark.
- **Errors say little.** A requester reads them: the method, the host and the
  status or network failure, not the URL, the answer or the server's status
  text. A CEL problem names the expression with its literals masked, and says
  why in rvav's words (`what it reads isn't there`, `it can't be read`, `it
  isn't true or false`, `it isn't valid CEL`), never cel-go's, which quote a key
  a map lacks, a pattern's text or the whole expression. Every step's CEL goes
  through one evaluator for this (`stepEval`): a `when`, a step's inputs, a URL,
  a body, a case, a message, a time, a poll's condition.
- **A secret input can't go in a URL,** where proxies and access logs would
  have it: it stays a marker, a map, which a URL refuses. It may go in a body.

### A wait for a person (D2.3)
```yaml
- key: owner_ok
  title: The owner's decision
  ask:
    group: vm-owners            # an OIDC group, as an approval level
    approvers: [usr_mkeller]    # and users, by their ID
    owner: true                 # and the request's owner; requester: true too
    message: Check it is unused first.
    form:                       # none: they approve or reject
      - key: decision
        label: Keep or delete?
        type: choice            # string, number, boolean or choice
        options: [keep, delete]
        required: true
        default: keep
    timeout: 48h
    on_timeout: escalate        # fail (the default), continue or escalate
    escalate_to:
      group: vm-managers
```

- **It is an approval level.** The wait is a row of `approvals`, with its step,
  form and answer (`migrations/00051`). Those it names are told as for an
  approval: the notification centre, mail, Teams, Slack. A card can approve or
  reject; a form is only linked to, and answered on the request's page.
- **It waits as an approval does.** `awaitLevel`, `waitRound` and `waitEnd` in
  `internal/provision/workflow.go` wait on a signal and a timer, look again
  each hour for a lost signal and continue as new on a long wait. So a wait
  outlives restarts of the API and the worker, and a timeout that came due
  with no worker is acted on when one returns. A later wait kind reuses them.
  A signal per row (`rvav.ask.<id>`) lets steps of a group wait side by side.
- **Who answers.** Those it names. As for an approval, nobody answers their own
  request by being in the group or `approvers`: only `requester: true`, or
  `owner: true` when they own it, lets them. A step that would then leave
  nobody is refused on request. Administrators follow ADR-0011: they read
  everything and answer only if named, like anyone else (403). It can't be
  delegated (403): the author named who answers. Someone who can't read the
  request gets 404.
- **What they answer.** Checked on the server against the form: the type, a
  choice among its options, what is required, nothing else, 4096 characters a
  value. One answer: a second gets 409. A form is answered, not rejected; a
  step with no form is approved or rejected, and a reject needs a comment,
  which the requester reads. `message` is fixed text, not an expression.
- **Outputs,** for later steps' CEL:

  | Output | What it is |
  |---|---|
  | `steps.<key>.answered` | True when a person answered |
  | `steps.<key>.by` | Their email; empty if nobody did |
  | `steps.<key>.comment` | Their comment; may be empty |
  | `steps.<key>.answer.<field>` | The field as given; left blank, its default; with no default, null |

  **A timeout that goes on** gives `answered: false`, an empty `by` and
  `comment`, and each field's default or null. A later step tells "nobody
  answered" by `answered`, not by the field's value. There is no `decision`
  output: a reject fails the step, so a later step only ever runs after a yes
  or a timeout that goes on.
- **A timeout.** `fail`, the default, fails the step. `continue` goes on, as
  above. `escalate` adds `escalate_to` for one more timeout, and then the step
  fails (ADR-0011's level does the same). With no timeout it waits until it is
  answered or the request is cancelled.
- **A failure, a cancel, a Retry.** A wait has nothing to undo itself, so it
  has no `undo` and no `with`. A reject fails the step with `Rejected by <name>:
  <comment>`, and the undos of earlier steps run as for any failure. Cancelling
  the request closes the question at once. A failing sibling stops a wait of its
  group: the row and step are cancelled. A Retry asks again any wait that failed
  or was stopped, on a new row, the old one staying as history, and keeps an
  answer that let its step finish. After a finished rollback, which undoes
  everything, it asks everything again.

### No secret in a person's answer
An answer goes through Temporal's history to every later step, and their logs
and events may read it. So a form may not have a secret field: `secret: true`
or the type `secret` or `password` is refused on save. Keep secrets in the
request's form or a stored secret. The answer is kept on its row and, for the
steps after it, in the step's stored outputs and Temporal's history, which no
API serves. The audit, events and deliveries don't have it: they say a level was
decided. A comment is free text, and read by the requester: it is no place for
a secret either.

### A wait for a time, and a poll (D2.4)
```yaml
- key: keep_snapshot
  title: Keep the snapshot three days
  wait:
    for: 72h                # or until: a CEL expression giving a time
- key: change_ok
  title: The change is approved
  poll:
    rest:                   # as a REST call step's, with GET, PUT or DELETE
      method: GET
      url: https://servicenow.corp.example/api/now/table/change_request/${steps.change.json.sys_id}
      secret: servicenow-token
      keep: $.result.state
    until: "steps.change_ok.json == 'approved'"
    every: 10m              # required, at least 1m
    timeout: 72h            # required, at least every
    on_timeout: fail        # fail (the default) or continue
```

- **Two keys.** A wait for a time has one of two fields and needs no answer. A
  poll has a call, a condition, an interval, a deadline and outputs. One key
  would leave most of its fields' combinations to refuse. Both are `system: wait`
  on a step's row, and both have a due time, so they read alike on a timeline.
- **There is no condition without a call.** Nothing else can change during the
  wait, so a condition that doesn't read `steps.<key>`, its own answer, is
  refused on save.
- **They wait as a person's wait does,** on `waitRound` (a timer and a stop),
  continuing as new when Temporal suggests it. Their state is in the database,
  not in the history: `request_steps.due_at` (`migrations/00052`), set once, and
  a poll's last answer, as the step's outputs. So a run continued as new or a
  worker back after a while finds them, and a time that came due with no worker
  is acted on when one returns. A poll adds a few events to the history at each
  check, and continues as new only when a check is due, so that continuing never
  checks early and the new run's first act is that check.
- **A time.** `for` is a duration of at least 1 s. `until` is CEL over `inputs`,
  `steps`, `environment` and `project`, giving a CEL timestamp or a text: RFC
  3339, a date and time, or a date; with no zone, it is read as UTC. A time
  already passed goes on at once, and says so on the timeline. An expression
  that doesn't give a time fails the step, its literals masked (`Its time can't
  be read: inputs.window + '…' doesn't give a time`), and quotes no value.
  Outputs: `steps.<key>.until`, the time it waited for, in UTC.
- **A check.** The first is at once, the others each `every`, and one more at the
  deadline. Its call is a REST call step's (`callREST`, `internal/restcall`):
  the allowed hosts, a stored secret, a URL's fixed host, 1 MB, 30 s, an
  answer scrubbed, resolved in the activity. So everything in "No secret stays
  behind a call" holds, each check, and the history holds only names and
  expressions. `every` is at least 1m, since each check calls that API, and a
  deadline is required, since otherwise a change never approved holds its run for
  ever.
- **Only what can be repeated.** POST and PATCH are refused on save, in those
  words: above they are made once, since they may do something each time, and a
  poll repeats its call.
- **What a failed check does.** It stays simple, along the line retries already
  draw. Each check is tried once: the next check is its retry.

  | A check gets | What happens |
  |---|---|
  | A status it accepts (`accept`; empty: any 2xx) | Its condition is read on the answer |
  | No answer, a 5xx, a 429 or a 408, or the worker went away | The step says so, and waits for the next check: only the deadline ends these |
  | Another status (a 401, a 403, a 404) | The step fails: a next check wouldn't change it. List it in `accept` to read it as an answer, a 404 for "not yet" |
  | A host no longer allowed, a secret gone, a URL it can't fill, more than it may keep | The step fails |
  | A condition it can't read, or that doesn't give true or false | The step fails, its literals masked. A field the answer may not have needs `has()` |

- **Outputs,** for later steps' CEL:

  | Output | What it is |
  |---|---|
  | `steps.<key>.status` | What the last check that got an answer answered; null if none did |
  | `steps.<key>.json` | Its answer, what `keep` picks, scrubbed as a REST step's; null if none did |
  | `steps.<key>.met` | True when the condition held |

- **A deadline.** It counts from when the step begins. At it, one last check, then
  `on_timeout`: `fail`, the default, fails the step (`Its condition wasn't met
  within 72h.`); `continue` goes on, saying so. There is no `escalate`: nobody is
  asked. **A later step reads, when it went on at the deadline,** `met: false`
  with the last answer's `status` and `json`, each null if no check got one. It
  tells that the condition never held by `met`, not by reading `json` again.
- **A failure, a cancel, a Retry.** Like a person's wait, these have nothing to
  undo themselves, so they have no `undo` and no `with`. Cancelling the request
  stops them at once. A failing sibling stops them: the step is cancelled. **A
  Retry starts again** any that failed or were stopped: `for` waits its whole
  duration, `until` reads its expression again, a poll checks at once and has its
  whole deadline. A wait that finished stays done, as an answer does.
- **What it says.** The timeline says what it waits for and `Until <time>`, in the
  viewer's time format; a run's graph shows the node Waiting. The poll's last
  check is its step's detail: `Checks every 10m. Last check: GET answered 200, not
  yet.`

### A branch (D2.5)
```yaml
- key: change
  title: Is the change approved
  branch:
    cases:
      - title: Refused        # said on the graph and the timeline
        when: "steps.ask.answer.ok == false"
        steps:                # may be empty: it goes on
          - key: told
            notify: {requester: true, message: "Refused."}
      - when: "inputs.disk_gb > 500"
        steps:
          - key: manager
            ask: {group: managers}
    else:                     # optional, what runs when no case holds
      - key: open
        rest: {method: POST, url: "https://itsm.corp.example/changes"}
```

- **One key for an if and a switch.** Its cases are read in order and the first
  whose condition holds is taken. A case is CEL, as a `when` is: no comparison to a
  bare value, which would be a new literal for a topology to mask. A step's `when`
  stays as it is, for one step. A branch has none itself, nor `with` or `undo`.
- **Nothing nests.** A branch can't be in a parallel group, a parallel group can't
  be on a side, and a branch can't be on a side: all refused on save. The canvas
  draws a side as a lane, a plain sequence, and a frame as steps with no edge between
  them: neither holds the other well. Cases already say what a nested condition
  would, and a refusal can be relaxed later, where an accepted shape can't be taken
  back. A VM step can't be on a side either: its VMs are planned when the request is
  submitted, before a branch chooses (refused on request, and said in the designer).
- **Keys.** Keys stay unique across the composite, whichever side they are on. A
  side's steps are `request_steps` rows like any step's, `step:<key>`, in order: the
  branch's row (`system: branch`), each side's rows, then what comes next. So the
  graph, the timeline and `earlierOutputs` find them as they do others. A key has no
  `/`, which keeps meaning a child of a step.
- **What a step can read.** A later step reads a side's step as one `when` skipped:
  it is there only if it ran, so `has(steps.x) && steps.x.ok`. A side reads its
  branch's outputs and the earlier steps of its own side. Another side's steps are
  refused on save, by the server and by `composite/scope.ts`, and aren't offered. A
  case reads what came before the branch, not the branch or its sides.
- **The decision stays decided.** `DecideBranch` stores the side once, in the row's
  outputs, with a coalesce: `{case: n}`, 1 for the first case, 0 for otherwise or
  none. An activity retried finds it, and a run that continues as new, or a worker
  back after a restart, reads it from the done row and evaluates nothing, even if
  what the condition reads has changed since. **A Retry keeps it:** a done branch
  stays done, and only its side's failed and undone steps run again. A rollback
  that finished deleted what was built, and everything runs again, so only then does
  it choose again. That needs a VM to delete: with nothing to delete there is no
  rollback row, so the branch keeps its choice. A condition it can't read fails the
  branch, its literals masked, and a Retry reads it again.
- **The sides not taken** are skipped, not failed: `Its side of the branch wasn't
  taken.` The branch says `Took: <title>`, `Took: case 2` or `Took: otherwise`, and
  `No case held: nothing ran here.` when nothing did. A run's graph marks each way
  `taken` or `not taken`, in words as well as a line, and the size of a node or the
  room of a label doesn't depend on the run, so nothing moves.
- **Undo.** Only steps that ran are undone, in reverse, as anywhere: a side not taken
  has none. A branch has nothing of its own to undo.
- **Runs in flight** take the code they took: a definition with no branch makes the
  same Temporal commands, and `replay_test.go` holds to that.

### A step that sends a message (D2.5)
```yaml
- key: told
  title: Tell the owner
  notify:
    owner: true             # requester, owner, groups and users, at least one
    groups: [dba]
    message: "${inputs.name} is ready at ${steps.vm.fqdn}."
```

- **Through the notifications' channels** (ADR-0023): the notification centre,
  then mail, Teams and Slack as each person chose, under a kind of its own,
  `step_message`. Everyone gets the channels of their choice, and only them.
- **Its text** has the `${...}` of a REST call's URL over `inputs`, `steps`,
  `environment` and `project`: no new template language. It is at most 2000
  characters as written and 4000 once filled, cut with an ellipsis. Each `${...}`
  must give one value (a text, a number, true or false; null is empty). A list or a
  map fails the step, its expression masked: `The message wasn't sent: its
  ${steps.x.items}: it isn't a single value.`
- **No secret.** A template reads inputs and outputs, and never a stored secret. A
  `secret` input it names, by name or index, is refused on save. One reached another
  way (`inputs['a' + 'b']`) reads as a marker, not its value, and fails the step with
  nobody told. A REST answer is already scrubbed, a person's form can't ask for a
  secret, and the filled text is scrubbed of the request's secret inputs all the
  same, since an API or a person may repeat one.
- **Values from a requester reach other people,** so they are data. Mail is
  `html/template`, with its subject on one line. A Teams card's markdown is escaped.
  Slack gets this kind as `plain_text`, with no mention or link, and other kinds
  keep their markup. `TestStepMessageEscapes` holds them to it.
- **Sent once.** A dedupe key, `step_message:<request>:<step>`, makes a retried
  activity or a run continued as new tell nobody twice. **A Retry doesn't send it
  again:** a done step stays done. After a rollback that finished, everything runs
  again, this message too, and the keys are freed for it.
- **What fails it.** A template it can't fill, masked, with nobody told, and the
  database failing (the activity retries). A channel that fails doesn't: the notifier
  retries it, as for any notification (ADR-0023). Nobody to tell doesn't either. An
  unknown user is refused on save, but a group may have no known member yet, or
  someone may have left, so at run time it goes on and says `Nobody to tell: it
  went on.`, without naming them. Cancelling the request tells nobody.
- **What it leaves.** No `undo`: a message can't be unsent. Its one output is
  `steps.<key>.told`, how many it told, so that its text stays out of later steps,
  of Temporal's history and of the timeline, which says `Message sent.` The text is
  in the notifications of those it told. A requester reads it only if they are one.

### What a requester sees
`blueprint.Topology` keeps a REST call's method, and its URL and body masked
as CEL literals are (`policy.MaskLiterals`, ADR-0032), for the step and its
undo. A URL's fixed text is literal too: `https://cmdb.corp.example/ci/${inputs.name}`
reads `…${inputs.name}`. Its secret, accepted statuses and `keep` stay out.

A wait for a person keeps that it asks the requester or the owner, its timeout
and what that does, and its form's keys: `Asks: the requester`, or `Asks: a
person`, `Wants: 2 answers`, `Timeout: 48h, then it goes on`. Its group, users,
message, labels, options, defaults and escalation stay out: a form shows that it
waits and for how long, not who decides or what they are asked. The keys stay
because a later step's masked CEL names them anyway.

A wait for a time keeps its duration, or its expression masked: `For: 72h`,
`Until: inputs.window`. A poll keeps its method, its URL and body masked like a
REST call's, its condition masked, how often it checks, its deadline and what
that does: `Calls: GET …${steps.change.json.sys_id}`, `Until: steps.change_ok.json
== '…'`, `Every: 10m`, `Timeout: 72h, then the step fails`. Its secret, accepted
statuses and `keep` stay out.

A branch keeps each case's title and its condition masked, and its sides' steps get
the same projection as any step: `Refused: steps.ask.answer.ok == '…'`. A message
keeps only whether the requester or the owner is told: `Tells: the requester`, or
`Tells: other people`. Who else it names and what it says stay out.

A topology is masked, so it carries no problems: a step's masked URL read as a
mistake, and a requester saw "1 problem" on a REST step.

On a request's page, its readers see a wait's name, who it waits for, its status
and due date, and the outcome and comment once there is one, as for an approval
(ADR-0011). Only those it names, and those who see everything, read its message,
form and answer. The inbox lists the row and sends its answerer to the page.

### The triage
`rvav vro-triage` maps a workflow that reaches only REST APIs, with or without
vCenter, to a composite item with REST call steps (group A), unless a
subscription or a day-2 action runs it: those run a job template, a webhook or
a custom workflow, not a composite, so it stays in group B. Others keep their
group, with a note that as a composite their REST calls would be REST call
steps.

A workflow with a user interaction or a timer maps to a composite item with steps
that wait for a person or for a time (group A), its REST calls being REST call
steps and any other system an AWX step; a timer around a REST call is a step that
polls it until a condition holds. Two stay in group C, with a custom workflow
waiting and asking through `rvav.RequestApproval`: one that also waits for an
event, until its step kind exists, and one a subscription or a day-2 action runs,
for the reason above.

Mail no longer needs a playbook: a workflow whose only other system is mail, with
or without REST calls, is group A, a composite item with a step that sends a message
(the standard `Tell the owner the VM is ready`), unless a subscription or a day-2
action runs it. Mail to an address that isn't a user stays a playbook's, so the note
says so. A decision no longer moves a workflow on its own: a branch covers it, and the
note warns only of decisions that nest beyond what cases say, or that compensate.

### How this sits with ADR-0007, ADR-0011, ADR-0022 and ADR-0032
- **ADR-0007:** the integration library stays AWX and Ansible, and level 2
  stays the default for anything with logic. A REST call is a level-1 step
  for what is one request and its answer. A flow of calls that needs loops,
  paging or error handling is a playbook or a custom workflow.
- **ADR-0011:** a wait reuses a level's row, its timeout and its channels. It
  differs where it answers another question: a level asks whether a request may
  run, a wait asks something in the middle of its run. So it has no approval
  rules, delegation, editable inputs or all-of, and stays out of the approval
  reports (the backlog metric keeps it: someone is waiting).
- **ADR-0022:** a custom workflow stays the way for days of back and forth,
  compensation, or decisions that nest beyond what a branch's cases say. A step
  reading an answer and a branch now cover plain branching on answers.
  `rvav.RequestApproval` is unchanged. A wait for an event, or a condition that takes
  more than one call and CEL on its answer, is one too.
- **ADR-0023:** a message is a notification like any other, with its dedupe key, its
  deliveries and each person's channels. Its own kind lets someone turn it down
  without losing the rest. It differs in that an author writes it, so its values are
  escaped harder (Slack's plain text).
- **ADR-0032:** the YAML stays the source of truth, and the canvas draws these
  steps as it draws others. Topology masking extends to URLs and bodies. The
  "no scripting language" rule is the one above.

## Consequences
- Most small vRO glue that only calls an API no longer needs a playbook.
- A REST call's retry and undo are the author's to get right: `accept` lists
  what counts as done, and an undo that may find nothing should accept 404.
- The Allowed hosts list now covers two uses under one setting: a host allowed
  for a value source can be called by a step. Both are catalog admins' work,
  and `RVAV_REST_SOURCE_HOSTS` keeps its name.
- A `/undo` child shows its parent's system on the timeline: an AWX step
  undone by a REST call reads AWX.
- `approvals` holds two kinds of rows now. What reads them says which it wants:
  the reports and the request's levels leave out those with a step.
- A wait with no timeout holds its workflow open until someone answers or
  cancels. A step naming a person who has left stalls its request, and an
  administrator can't answer for them: the request is cancelled and asked for
  again once the item is fixed. A timeout, or a group, avoids this.
- A message can't quote the request, since it is fixed text. The page it is
  answered on shows the request beside it.
- A wait or a poll holds its workflow open, and `until` has no limit: an item can
  wait for a date a year away. It costs little, since it only wakes each hour to
  look again, and continues as new when asked.
- Each check calls the API it names, and nothing shares them between requests: a
  thousand requests waiting on one ticket system at `every: 1m` are a thousand
  calls a minute. An author picks a sensible interval, and 1m is only a floor.
- In a parallel group, a wait or a poll that has left to continue as new, on a
  long history, waits for its siblings' next round or end before it goes on. A
  sibling long at work (a VM build) can make it late, never early. One that must
  keep its pace has a group of its own.
- A branch can't hold a VM step, as a VM is planned at submission: an item that
  builds a VM on one side only is two items, or a custom workflow.
- A side can't run steps at the same time, nor hold a branch. Where that is really
  needed, a custom workflow does it.
- Retry keeps a branch's choice, even when what it read has changed since, so
  correcting that means a new request. Only a rollback that finished chooses again.
- A message to a group reaches the members rvav has seen sign in: someone who never
  has isn't told. Mail to a list beyond those is a playbook's.
