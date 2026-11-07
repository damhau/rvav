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
  `ask`, each with its own fields, beside what every step has (`key`, `title`,
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
  text. A CEL problem names the expression with its literals masked, or says
  only that it isn't valid CEL, since a compile error quotes its text.
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

A workflow with a user interaction maps to a composite item with a step that
waits for a person (group A), its REST calls being REST call steps and any other
system an AWX step. Two stay in group C, with a custom workflow asking through
`rvav.RequestApproval`: one that also waits for a time or an event, until its
step kind exists, and one a subscription or a day-2 action runs, for the reason
above. A workflow that waits only for a time stays in group C until then too.

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
- **ADR-0022:** a custom workflow stays the way for branching on answers, days
  of back and forth or compensation. `rvav.RequestApproval` is unchanged.
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
