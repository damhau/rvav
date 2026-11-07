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
  each with its own fields, beside what every step has (`key`, `title`,
  `when`, `undo`). A later kind adds a key: nothing moves in existing
  definitions. A step with none of them, or more than one, is refused when
  saved.
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

### What a requester sees
`blueprint.Topology` keeps a REST call's method, and its URL and body masked
as CEL literals are (`policy.MaskLiterals`, ADR-0032), for the step and its
undo. A URL's fixed text is literal too: `https://cmdb.corp.example/ci/${inputs.name}`
reads `…${inputs.name}`. Its secret, accepted statuses and `keep` stay out.

### The triage
`rvav vro-triage` maps a workflow that reaches only REST APIs, with or without
vCenter, to a composite item with REST call steps (group A), unless a
subscription or a day-2 action runs it: those run a job template, a webhook or
a custom workflow, not a composite, so it stays in group B. A workflow that
waits stays in group C until its step kind exists. Others keep their group,
with a note that as a composite their REST calls would be REST call steps.

### How this sits with ADR-0007 and ADR-0032
- **ADR-0007:** the integration library stays AWX and Ansible, and level 2
  stays the default for anything with logic. A REST call is a level-1 step
  for what is one request and its answer. A flow of calls that needs loops,
  paging or error handling is a playbook or a custom workflow.
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
