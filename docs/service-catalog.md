# Service catalog specification

The goal is parity with vRA Service Broker, plus the UX it never delivered.

## 1. Content sources and item types

| Type | Source | What it does |
|---|---|---|
| `vm_blueprint` | Authored in the app | Provisions one or more VMs natively, with optional AWX post-config steps |
| `awx_job_template` | Imported from AWX | Launches a job template with inputs from the form |
| `awx_workflow` | Imported from AWX | Launches a workflow job template |
| `composite` | Authored in the app | Ordered or parallel steps combining other items (VM, AWX, custom workflow), AWX templates, custom workflows, custom resources, REST calls and waits for a person, a time or a condition, inputs bound to earlier steps' outputs ([ADR-0022](adr/0022-custom-workflows-and-composites.md), [ADR-0033](adr/0033-composite-step-kinds.md), [ADR-0056](adr/0056-custom-resource-types.md)) |
| `workflow` | Authored in the app | Starts a registered custom Temporal workflow with the form's inputs ([ADR-0022](adr/0022-custom-workflows-and-composites.md)) |

A composite's blueprint, in its Definition tab:

```yaml
kind: composite
rollback_on_failure: true
steps:
  - key: vm
    item: rhel9-server
    with: {role: inputs.role, size: inputs.size, cluster: inputs.cluster,
           datastore: inputs.datastore, network: inputs.network, post_config: ["linux-baseline"]}
  - parallel:
      - key: db_user
        item: db-user
        with: {database: inputs.database, role_name: inputs.role_name, privileges: '"read"', host: steps.vm.fqdn}
        undo: {job_template: pg-drop-role, with: {role_name: inputs.role_name}}
      - key: monitoring
        awx: {job_template: monitoring-agent, title: Monitoring}
        with: {target: steps.vm.fqdn}
  - key: dba_notice
    awx: {job_template: dba-notice}
    when: inputs.database == "ledger"
```

`with` values that are strings are CEL (a literal string is quoted:
`'"read"'`); a VM step's come from the form only (and it has no `when`:
its VMs are planned at submit), the others may use
`steps.<key>`: a VM's `name`, `fqdn`, `ip`, `moref` (and `vms`), an AWX
job's `job_id`, a workflow's result, a REST call's `status` and `json`, a
person's `answered`, `by`, `comment` and `answer`, a wait's `until`, a poll's
`status`, `json` and `met`, a branch's `case`, a message's `told`, a custom
resource's properties, what a signal or an event brought, a loop's `items`
and `count`, and a caught step's `caught` and `error`.

An AWX step's `with` value, or its undo's, can be a stored secret:
`with: {password: "${secret.db-admin}"}`, the whole value. The job gets the
secret's value as that extra variable; the request, its plan and Temporal's
history keep only the name, and the worker opens it when it launches the job
([ADR-0038](adr/0038-names-groups-secrets-properties.md)). Mark the
playbook's tasks that use it `no_log`. `${secret.name}` anywhere else (an item
step, a workflow, a message, a property) is refused on save, as is a secret
that doesn't exist.

#### A REST call step
A step can call a REST API instead of running something
([ADR-0033](adr/0033-composite-step-kinds.md)). Any step with an undo can
have a call as its undo (a VM step has none):

```yaml
  - key: cmdb
    title: Register in the CMDB
    rest:
      method: POST
      url: https://cmdb.corp.example/api/ci/${steps.vm.name}?env=${environment}
      body: "{'name': steps.vm.fqdn, 'ip': steps.vm.ip, 'owner': inputs.owner}"
      secret: cmdb-token
      accept: [200, 201]
      keep: $.result
    undo:
      rest:
        method: DELETE
        url: https://cmdb.corp.example/api/ci/${steps.cmdb.json.id}
        secret: cmdb-token
        accept: [200, 204, 404]
```

| Field | What it is |
|---|---|
| `method` | GET, POST, PUT, PATCH or DELETE |
| `url` | On a host in Administration › Integrations › Allowed hosts (or `RVAV_REST_SOURCE_HOSTS`). `${...}` is CEL over `inputs`, `steps`, `environment` and `project`, URL-escaped, in the path and query only. An empty value, `.`, `..`, a map or a list fails the step without calling |
| `body` | A CEL expression, sent as JSON; a GET has none. It may hold a secret input; a URL may not |
| `secret` | A stored secret, sent as a bearer token, basic credentials or a header, as stored |
| `accept` | The statuses that count as success; any 2xx by default. Others fail the step. A redirect isn't followed: it is an answer like any other |
| `keep` | A JSONPath into the answer; all of it by default. A path naming one thing gives it or null; a wildcard or a filter always gives a list |

A step has no `with`: its URL and body read what they need. Later steps read
`steps.<key>.status` and `steps.<key>.json`; an undo may read its own step's.
An answer that isn't JSON gives null, and its headers are never kept. An ID
over 2^53 loses precision as a JSON number: have the API send a string.

A call has 30 s and 1 MB, and more than 256 KiB kept fails the step. GET, PUT
and DELETE are retried on network errors, 5xx, 429 and 408; a POST or a PATCH
is made once, and if it fails a person decides. **Retry** makes any call
again. An undo that might find nothing left should accept 404.

#### A step that waits for a person
A step can stop and ask someone, with a form or to approve or reject, and go on
with their answer ([ADR-0033](adr/0033-composite-step-kinds.md)). It has no
`with` and no `undo`:

```yaml
  - key: owner_ok
    title: The owner's decision
    ask:
      group: vm-owners
      approvers: [usr_mkeller]
      owner: true
      message: Check it is unused first.
      form:
        - key: decision
          label: Keep or delete?
          type: choice
          options: [keep, delete]
          required: true
          default: keep
      timeout: 48h
      on_timeout: escalate
      escalate_to:
        group: vm-managers
```

| Field | What it is |
|---|---|
| `group`, `approvers` | An OIDC group and users by ID, as for an approval level. Nobody answers their own request through these. At least one of these, `requester` or `owner` |
| `requester`, `owner` | `true`: the request's requester or owner is asked too, and may answer even though it is their request |
| `message` | Fixed text for those asked, up to 2000 characters. It is not an expression |
| `form` | Up to 20 questions. With none, they approve or reject, and a reject fails the step |
| `form[].key`, `label` | What later steps read, and what the question says |
| `form[].type` | `string`, `number`, `boolean` or `choice`, which needs `options`. A secret is refused: an answer travels through Temporal's history to every later step |
| `form[].required`, `default` | A required field must be answered. A default is what it reads if left blank or if nobody answers |
| `timeout` | A duration such as `48h`; with none, it waits until answered or cancelled |
| `on_timeout` | `fail`, the default; `continue`, which goes on with no answer; `escalate`, which adds `escalate_to` for one more timeout and then fails |
| `escalate_to` | `group` and `approvers`, for `escalate` |

Later steps read `steps.<key>.answered`, `by` (an email), `comment` and
`answer.<field>`: as given, or left blank its default, or else null. When a
timeout goes on, `answered` is false, `by` and `comment` are empty, and each
field is its default or null. A reject fails the step, with the undos of the
steps before it, so nothing later reads it.

Those it names are told as for an approval (the notification centre, mail,
Teams, Slack) and answer on the request's page. A step that asks only the person
requesting it is refused when requested: nobody could answer. Cancelling the
request closes the question, and **Retry** asks again if the step failed or was
stopped. A requester's form shows that it waits, whether for them or the owner,
its timeout and how many answers: not who else, or what it asks.

#### A step that waits for a time or a condition
A step can wait for a time, with `wait`, or call a REST API until a condition
holds, with `poll` ([ADR-0033](adr/0033-composite-step-kinds.md)). Neither has a
`with` or an `undo`:

```yaml
  - key: keep_snapshot
    title: Keep the snapshot three days
    wait:
      for: 72h
  - key: change_ok
    title: The change is approved
    poll:
      rest:
        method: GET
        url: https://servicenow.corp.example/api/now/table/change_request/${steps.change.json.sys_id}
        secret: servicenow-token
        keep: $.result.state
      until: "steps.change_ok.json == 'approved'"
      every: 10m
      timeout: 72h
      on_timeout: fail
```

| Field | What it is |
|---|---|
| `wait.for` | A duration such as `72h`, at least 1 s |
| `wait.until` | Instead of `for`: CEL over `inputs`, `steps`, `environment` and `project` giving a time: a timestamp, or a text in RFC 3339, a date and time or a date. With no zone it is UTC |
| `poll.rest` | The call it repeats, as a [REST call step](#a-rest-call-step)'s, with its allowed hosts, secret, `accept` and `keep`. Only `GET`, `PUT` or `DELETE`: a POST or a PATCH may do something each time, so they are refused on save |
| `poll.until` | CEL giving true or false, which must read the step's own answer, `steps.<key>.json` or `.status`: nothing else changes during the wait |
| `poll.every` | How often it checks, a duration of at least `1m`. Required. The first check is at once |
| `poll.timeout` | Its deadline from when it begins, at least `every`. Required. At the deadline, one last check |
| `poll.on_timeout` | `fail`, the default, or `continue`, which goes on |

Later steps read a wait's `steps.<key>.until`, the time it waited for, in UTC. They
read a poll's `status` and `json`, from the last check that got an answer, scrubbed
and picked by `keep` as a REST step's, and `met`, true when the condition held. When
a deadline goes on, `met` is false and `status` and `json` are the last answer's,
or null if no check got one.

A time already passed goes on at once, and says so on the timeline. An expression
that doesn't give a time fails the step.

No answer, a 5xx, a 429 or a 408 only waits for the next check, and only the
deadline ends those. Another status it doesn't accept fails the step, since a next
check wouldn't change it: list a 404 in `accept` if it means "not yet". So does a
host no longer allowed, a secret gone, and a condition it can't read, on a field the
answer doesn't have, say (use `has()` if it may not).

Cancelling the request stops it at once. **Retry** starts it again if it failed or
was stopped: the whole duration, or the whole deadline. A requester's form shows
that it waits and for how long, and a poll's method and how often: its URL, body and
conditions masked, its secret, `accept` and `keep` left out.

#### A branch
Steps that run on one side of a condition, or the cases of a switch
([ADR-0033](adr/0033-composite-step-kinds.md)). A step's `when` stays the way to skip
one step:

```yaml
  - key: change
    title: Is the change approved
    branch:
      cases:
        - title: Refused
          when: "steps.ask.answer.ok == false"
          steps:
            - key: refused
              notify: {requester: true, message: "Your change was refused."}
        - title: A big disk
          when: "inputs.disk_gb > 500"
          steps:
            - key: manager
              ask: {group: managers}
      else:
        - key: open
          rest: {method: POST, url: "https://itsm.corp.example/changes"}
```

| Field | What it is |
|---|---|
| `branch.cases` | At least one, in order. The first whose condition holds is taken |
| `cases[].title` | Optional: what the graph and the timeline call it |
| `cases[].when` | CEL giving true or false, over `inputs`, `steps` before the branch, `environment` and `project` |
| `cases[].steps` | What runs on that side, in order. Empty: it goes on |
| `branch.else` | Optional: what runs when no case holds |

A branch has no `when`, `with` or `undo`. Nothing nests: a branch can't be in a
parallel group or on a side, nor a parallel group on a side, and a VM step can't be
on a side, since its VMs are planned when the request is submitted. Keys are unique
across the composite.

Later steps read `steps.<key>.case`: 1 for the first case, 0 for otherwise or none.
They read a side's step as one `when` skipped, there only if it ran:
`has(steps.open) && steps.open.status == 201`. A side reads its branch and the steps
before it on that side, not another side's, which is refused on save.

It chooses once. A Retry keeps its choice and runs only what failed or was undone on
its side, unless a rollback that finished deleted what was built. A condition it
can't read fails the branch, masked. Steps on the sides not taken are skipped, and
never undone. A requester's form shows its cases' titles and their conditions masked,
and its steps as any step.

#### A step that sends a message
Through rvav's notifications ([ADR-0033](adr/0033-composite-step-kinds.md)): the
notification centre, then mail, Teams and Slack as each person chose:

```yaml
  - key: told
    title: Tell the owner
    notify:
      owner: true
      groups: [dba]
      message: "${inputs.name} is ready at ${steps.vm.fqdn}."
```

| Field | What it is |
|---|---|
| `notify.requester`, `notify.owner` | True to tell them |
| `notify.groups` | OIDC groups: their members rvav has seen sign in |
| `notify.users` | User IDs: one unknown is refused on save |
| `notify.message` | At most 2000 characters, with `${...}` as a REST call's URL has it. Required |

At least one person or group is required. It has no `with` or `undo`, and may have a
`when`. Each `${...}` must give one value, a text, a number, true or false, and null
is empty. A secret input is refused on save, and fails the step if reached some other
way. What it fills in is data: each channel escapes it, and the filled text is scrubbed
of the request's secret inputs.

It is sent once, whatever is retried: a **Retry** doesn't send it again, unless a
rollback that finished deleted what was built. A template it can't fill fails the step,
masked, with nobody told. A channel that fails doesn't: the notifier retries it. If
there's nobody to tell when it runs, it goes on and says so. Later steps read
`steps.<key>.told`, how many it told, never what it said. The timeline says `Message
sent.`, and a requester's form only whether the requester or the owner is told.

#### A custom resource step
Makes a resource of a resource type, with the type's create step, in the
deployment of a VM step before it
([ADR-0056](adr/0056-custom-resource-types.md)). After the `vm` step of
the composite above:

```yaml
  - key: lb
    resource: {type: load-balancer, deployment: vm}
    with: {members: "[steps.vm.ip]", port: 443}
```

| Field | What it is |
|---|---|
| `resource.type` | The resource type's key. Required. Publishing needs it to exist and be on |
| `resource.deployment` | The key of the VM step whose deployment the resource joins. Optional when only one VM step comes before it |
| `with` | The create step's inputs, CEL over `inputs` and `steps.<key>` as in any step. Saving refuses one the type's create workflow doesn't take, as for a blueprint's resources |
| `title` | Its title on the timeline: the type's by default |

It may have a `when`, and may sit in a parallel group or a branch, after
the VM step it joins. It has no `undo`: a custom resource is undone by its
type's delete, and a rollback deletes it. Later steps read its properties
as `steps.<key>.<property>`. The resource belongs to the VM step's
deployment: it shows there, its type's actions run on it, and deleting
the deployment deletes it. An update of that deployment leaves it as it
is.

In the designer, the resource types that are on are under **Custom
resources**, with the inputs of their create step and the properties of
their property schema. Such a step has no undo, and its **Deployment**
names the VM step it joins when several come before it. The canvas marks
it **Resource**.

#### A step that waits for a signal
Stops the run until another system says it may go on: a change board
approves, a backup finishes
([ADR-0057](adr/0057-run-data-variables-schedules-and-loops.md)). It
waits for a signal sent to the request by name, or for one of rvav's
[events](#8-extensibility-event-subscriptions):

```yaml
  - key: cab
    title: The change board approves
    signal:
      name: cab-approved
      timeout: 72h
  - key: backup
    title: Wait for the backup host
    signal:
      event: provisioning.post
      when: "item.slug == 'backup-host' && environment == 'prod'"
      timeout: 4h
      on_timeout: continue
```

| Field | What it is |
|---|---|
| `signal.name` | The signal to wait for: lower-case letters, digits, dots, dashes and underscores. Unique in the composite |
| `signal.event` | Instead of `name`: the topic of an event to wait for |
| `signal.when` | For an event only: CEL giving true or false over the event's variables, as a subscription's condition reads them, and `waiting`: the request that waits (`waiting.request.id`, `.number`) and its steps before this one (`waiting.steps`). Optional: without it, the first event of the topic is taken |
| `signal.timeout` | How long it waits, from a minute to 90 days. Required |
| `signal.on_timeout` | `fail`, the default, or `continue`, which goes on |

Those with `request.manage` in the request's project send a signal, from
a script or another system with their API token:

```sh
curl -X POST -H "Authorization: Bearer $RVAV_TOKEN" -H "Content-Type: application/json" \
  -d '{"by": "the change board", "change": "CHG0042"}' \
  https://rvav.corp.example/api/v1/requests/REQ-10042/signal/cab-approved
```

or `rvavctl requests signal REQ-10042 cab-approved --data '{"by": "the change board"}'`.
The answer is a 202 naming the step. The requester can't send it, so a wait
the item puts in their way stays there. A signal sent when no step of the
request waits for it is refused with a 409: it isn't kept for later. Its
body, a JSON object of at most 64 KiB, is the step's outputs, which later
steps read as `steps.cab.by`, and it is in the request's history: send no
secret. Each signal is audited (`request.signal`).

An event counts only if it happens once the step waits, and only one of
the request's own project. Its data, as a subscription gets it, is the
step's outputs: `steps.backup.request.number`, say.

At the deadline, `continue` goes on with `steps.<key>.timed_out` true; read
it with `has(steps.backup.timed_out)`, since a step that got its signal
has only what came. A wait for a signal has no `with` or `undo`, can't
loop and isn't on a catch path. Cancelling the request stops it at once.
**Retry** waits again, with its whole timeout. A requester's form shows
what it waits for and how long.

#### A loop
Runs one step once for each item of a list, such as each host of an input
([ADR-0057](adr/0057-run-data-variables-schedules-and-loops.md)):

```yaml
  - key: agents
    title: Install the agent
    awx: {job_template: monitoring-agent}
    for_each:
      items: inputs.hosts
      parallel: 3
    with: {target: each, position: index}
```

| Field | What it is |
|---|---|
| `for_each.items` | CEL giving a list, over `inputs`, `steps` before it, `environment` and `project`. At most 100 items |
| `for_each.parallel` | How many passes run at once, up to 10. 0 or 1 runs one after the other |

The step's `with`, `when` and a REST call's URL and body read `each`, the
item, and `index`, from 0. `when` is read for each pass, so a pass can be
skipped. An item step that isn't a VM, an AWX job, a custom workflow, a
REST call and a message can loop. A VM step can't: its VMs are planned
when the request is submitted, so give its item a count instead. Nor can a
custom resource step, one resource of its deployment under the step's key,
a wait or a branch. A loop has no `undo`.

The timeline shows each pass under the step. Later steps read
`steps.<key>.items`, each pass's outputs in the list's order, and
`steps.<key>.count`. A pass that fails fails the step: passes not begun are
skipped, those running finish. **Retry** reads the list again and keeps the
passes that were done, by their position. In a group of steps that run at
the same time, another step's failure stops the loop too: its passes not
begun are skipped, "Another step of its group failed.", and the step is
cancelled.

What the steps give later steps is bounded, as Temporal carries it: a
loop's passes give 512 KiB of outputs at most, and all the steps 1.5 MiB.
The step that goes over fails, saying so, without its outputs: keep less of
what it answers, such as with a REST call's `keep`.

#### A catch path and an error handler
A catch path runs when its step fails, and the request goes on; the error
handler runs when a failure nothing catches fails the request
([ADR-0057](adr/0057-run-data-variables-schedules-and-loops.md)):

```yaml
steps:
  - key: dns
    rest: {method: POST, url: "https://ipam.corp.example/api/records", body: "{'name': inputs.name}"}
    catch:
      - key: dns_ticket
        rest: {method: POST, url: "https://itsm.corp.example/api/incidents",
               body: "{'summary': 'DNS record failed: ' + error.message}"}
  - parallel:
      - key: backup
        awx: {job_template: backup-agent}
      - key: monitoring
        awx: {job_template: monitoring-agent}
    catch:
      - key: tell_ops
        notify: {group: ops, message: "${error.step} failed on ${inputs.name}."}
on_error:
  - key: incident
    rest: {method: POST, url: "https://itsm.corp.example/api/incidents",
           body: "{'summary': 'Request failed at ' + error.step + ': ' + error.message}"}
```

| Field | What it is |
|---|---|
| `catch` | On a step, a side's step or a parallel group: the steps that run, in order, when it fails |
| `on_error` | Beside `steps`: the steps that run, in order, when a failure fails the request |

Their steps read `error.step`, the key of the step that failed (a loop's,
when one of its passes did), and `error.message`, what it said. They are
AWX jobs, custom workflows, REST calls and messages, which run once: no
wait, branch, loop, parallel group, custom resource, VM step, `undo` or
catch path of their own.

When a step with a catch path fails, the steps of its group that were
running stop, its catch path runs, and the request goes on. The step
stays **Failed** on the timeline, its outputs `caught` (true) and `error`,
so a later step can read `has(steps.dns.caught)`. A step of a parallel
group has no catch path of its own: put it on the group. A VM step, a
custom resource step, a group with one of them, and a branch have none:
what they built is the rollback's to delete, so the request can't go on
past them. When the step succeeds, its catch path is skipped. If the
catch path fails, the request fails.

The error handler runs before the undo steps and the rollback, and the
request still fails. It runs again on each failure, a Retry's too, and is
skipped when nothing fails.

#### A run's data
A request's page shows what each step was given and what it gave
([ADR-0057](adr/0057-run-data-variables-schedules-and-loops.md)). To read a
step's data, choose **Show data** on the step, in the timeline or in the
selected node's details. The **Step data** panel lists the step's inputs and its
outputs, which later steps read as `steps.<key>.<name>`.

| Step | Its inputs |
|---|---|
| An AWX job, wherever it runs | Its extra variables, without rvav's `rvav` context |
| A custom workflow | Its `inputs` |
| A REST call, a poll's check, an undo's call | Its method, URL, secret's name and body |
| A custom resource's create or update | What its type's workflow or job is given: the inputs bound |
| A custom resource's read or delete | The resource's properties, as its type's workflow or job is given them |
| A VM step, a wait, a branch, a message | None: the request shows what the form asked |

A custom resource's step gives the resource's properties after it, or, for a
delete, what its type's workflow or job gave. Its step runs wherever the
resource is made or changed: a blueprint's resources, a composite's resource
step, a day-2 action or the deployment's deletion.

You need `catalog.manage` or `extensibility.read` for all of rvav to read a
step's data: catalog admins and auditors read it. A requester sees each
step's status and error, not its data.
Secrets stay masked: a secret input reads `••••`, a stored secret its
`${secret.<name>}`, and a secret input's value repeated in a text, such as a
REST answer, reads `[removed]`. A person's answer is shown only to those it
asks and those who read every approval. A **Retry** clears the data of the
steps it runs again.

The API serves the same: `GET /requests/{requestId}/steps/{stepId}/data`, for
the steps whose `has_data` is true.

#### Shared variables
A shared variable is a named value that isn't secret, which several items
read from one place, such as a site's DNS servers
([ADR-0057](adr/0057-run-data-variables-schedules-and-loops.md)). A
composite's steps and conditions read it as `vars.<group>.<name>`, wherever
they read `inputs`: `with`, `when`, a REST call's URL and body, a wait's or
a poll's `until`, a branch's cases and a message's text.

```yaml
steps:
  - key: register
    rest:
      method: POST
      url: https://cmdb.corp.example/api/ci/${inputs.name}?site=${vars.cmdb.site}
      body: "{'name': inputs.name, 'dns': vars.network.dns_servers}"
  - key: monitoring
    awx: {job_template: monitoring-agent}
    when: vars.monitoring.enabled
    with: {target: inputs.name}
```

To add one, go to **Administration > Variables** and choose **New
variable**. You need `catalog.manage` for all of rvav; with
`extensibility.read`, as an auditor, you read the variables and change
none. A value is text, a number, true or false, or a list or an object
written as JSON, 16 KiB at most.

- A step reads the value as it is when the step runs. A change reaches the
  steps that haven't run yet, in requests already running too. Unlike a
  property group's constants, you don't publish the items again.
- Nothing masks a variable: the run data and the audit log show its value.
  Store a password, a token or a key as a secret instead. A group or a name
  that says so, such as `admin_password`, `api_token` or `ssh_key`, is
  refused.
- Read a variable written as `vars.<group>.<name>`. Saving refuses another
  way, such as `vars['cmdb']['site']` or the group `vars.cmdb` whole: rvav
  finds the variables an item reads by name, to check they exist and keep
  them.
- To publish a version, every variable its steps read must exist; a draft
  may read one you haven't added yet.
- You can't remove a variable that a draft or a published version reads,
  or that a request of an older version reads while it's still to run or
  running. The page lists the items and requests that read each one.

The API is `/admin/variables`, with each variable at
`/admin/variables/<group>.<name>`. Each change is audited with its value.

### VM blueprints
A `vm_blueprint` item's blueprint ([ADR-0024](adr/0024-multi-vm-blueprints.md)).
Strings in `bindings` and `count`, a disk's `size_gb` and `datastore` and
an adapter's `network` are CEL over `inputs`, `sizes` and `project` (a
literal is quoted: `'"vsan-01"'`); numbers are themselves. `project` has
`id`, `code`, `name` and `properties`, the project's custom properties.
The other fields are plain values.

A binding written as a number or a boolean, as YAML gives `memory_gb: 4`,
is that constant, as if it were the string `"4"`. Saving a draft refuses,
at its field and in words:

- a value of the wrong kind, such as a list where a binding's CEL is
  (`/blueprint/bindings/memory_gb`) or a number where a name is;
- CEL that reads an input the form doesn't have, its property groups'
  inputs among them: `network: inputs.zone` with no `zone` in the form is
  refused at `/blueprint/bindings/network`, as a request would fail on it.
  This holds for bindings, counts, disks, adapters and custom resources'
  `with`.

Publishing refuses the same. A draft saved before these checks still
opens, and its next save or publish names what to fix.

CEL in rvav, here as in form rules, policies, composites and
subscriptions, has CEL's string functions: `lowerAscii()`, `upperAscii()`,
`trim()`, `replace(old, new)`, `split(sep)`, a list's `join(sep)`,
`substring(start, end)`, `indexOf(s)`, `lastIndexOf(s)`, `charAt(i)`,
`reverse()`, `'%s-%d'.format([a, b])` and `strings.quote(s)`. For example,
`inputs.app.trim().lowerAscii().replace('_', '-')`. An expression fails if
one of these functions would build a string of more than 1 MiB, or `split`
a list of more than 100,000 items. What they build also counts against the
expression's cost limit.

| Field | What it is |
|---|---|
| `naming` | Naming template of the VMs (a naming policy wins over it): `{project}`, `{environment}`, `{component}`, `{inputs.<name>}`, `{bindings.<key>}` (a binding of the VMs it names, such as `inputs.app.lowerAscii()`), ending with `{seq}` or `{seq:03}` |
| `template`, `library` | The vCenter template; with `library`, the item of that content library. The `template` binding gives the key of a template of **Templates and networks** instead (the `rvav.templates` picker): see **Templates per vCenter** below |
| `guest` | `os_family` (`linux`, `windows`), `domain`, `time_zone`; `customization_spec` (a vCenter specification as the base); Windows: `admin_password` (a secret), `domain_join: {domain, ou, credential}` (a basic secret: the domain must be the Active Directory integration's when the VM gets a computer object, whose OU then replaces `ou`); `active_directory: {ou}` or `{skip: true}`, the OU of the VM's computer object or none, which count only when the project's OU lets blueprints choose (ADR-0064); Linux, or Windows on a template with cloudbase-init: `cloud_init: {user_data}` (a Go template over `.vm` (`name`, `fqdn`, `ip`, `ip6`, `ips`, `index`), `.inputs`, `.project`, `.environment`, `.request`, `.components`; inputs printed through `json`: `{{ .inputs.motd \| json }}`, and an input in the middle of a text through the whole text: `{{ printf "/etc/%v.conf" .inputs.app \| json }}`; no `define`, `template` or `block`; a stored secret as `{{ secret "name" \| json }}`, its name in quotes, opened only when the VM is built; saving refuses a secret that doesn't exist, with its line; on Linux, user data holding a stored secret or a secret input has cloud-init clear it from the VM's guestinfo once read); `remote_access` (the user people sign in as: see **Remote access** below) |
| `sizes` | Size presets for bindings (`sizes[inputs.size].vcpu`); the offered ones when absent |
| `bindings` | `vcpu`, `memory_gb` (both needed to publish: every VM gets a number of vCPUs and GB of memory, a plain number of 1 or more, or CEL over the inputs), `disk_gb` (the first disk), `cluster`, `resource_pool` (a pool's path below the cluster's root pool, such as `rvav/dev`), `host`, `datastore`, `datastore_cluster`, `network` (the first adapter), `template`, `post_config` (keys of `awx_templates`), `tags` (a list; it can read `self` and `components`, below), `groups` (a list of NSX groups, added to `groups`: from an `nsx.groups` field, whose values are checked against its list) |
| `disks` | Disks added after the template's: `key` (optional: up to 16 lower-case letters and digits, starting with a letter, unique among the VM's disks), `size_gb`, `datastore` (the VM's when absent), `storage_policy`, `constraints` (the disk's own storage constraints, below), `provisioning` (over the blueprint's). An update matches the disks of two versions by `key` when every extra disk of both has one, else by place: to remove some disks and change others in one version, give every disk a key ([ADR-0052](adr/0052-disk-keys.md)) |
| `nics` | Adapters added after the template's: `key` (in DNS names: `<name>-<key>`), `network` (or `segments.<key>`, a segment made for the deployment, never with DHCP), `ipam` (`efficientip`, the default, or `dhcp`) |
| `ipam` | How the VMs' first adapter gets its address ([ADR-0039](adr/0039-guest-access-and-networking.md)): `efficientip` (the default: an address reserved in the network's subnet, and DNS records) or `dhcp` (no reservation and no DNS records, so the network needs no subnet; not on a segment made for the deployment). On DHCP, the VM's FQDN is its name in `guest.domain`, `{{ .vm.ip }}` is empty in its own user data, and other components read the address it reports once powered on |
| `ipv6` | `true` gives the VMs' first adapter an IPv6 address beside its IPv4 one, from the IPv6 subnet linked to its network, with an AAAA record and no PTR ([ADR-0039](adr/0039-guest-access-and-networking.md)). Not with `ipam: dhcp`, nor on a segment made for the deployment; a network without an IPv6 subnet refuses the request. The guest gets it from vCenter's customization or from cloud-init's network data; user data reads it as `{{ .vm.ip6 }}`. The VM's address, everywhere rvav names one, stays its IPv4 one |
| `segments` | NSX segments made for each deployment ([ADR-0028](adr/0028-nsx-networking.md)), up to four: `key`, `title`. The `network` binding, or an added adapter's `network`, names one as `segments.<key>`: the VMs are on it, their subnet carved when the request runs. In a composite, a VM step's segments are made under its step, before its VMs |
| `security_groups` | NSX groups made for each deployment ([ADR-0028](adr/0028-nsx-networking.md)), up to eight: `key`, `title`, and the firewall `rules` into each: `from` (`any`, another group's key, or `nsx:<group ID>`), `services` (NSX service IDs, or `any`), `action` (`allow`, the default, `drop`, `reject`). In a composite, a VM step's groups and rules are made under its step, before its VMs |
| `groups` | The NSX groups the VMs are in: keys of `security_groups`, or existing groups that select VMs by tag, as `nsx:<group ID>`; a component may name its own |
| `tags` | vSphere tags, `category/tag` |
| `constraints` | The vSphere tags the clusters (`compute`), datastores (`storage`) and network (`network`) of the VMs must carry, or must not; below |
| `storage_policy` | The VMs' storage policy (their home and disks without their own), over the placement rule's |
| `provisioning` | How the VMs' disks are provisioned, the template's included ([ADR-0036](adr/0036-placement-targets.md)): `thin`, `thick_lazy_zeroed` or `thick_eager_zeroed`. Without it, the template's disks stay as they are and added disks are provisioned as the template's first disk |
| `linked_clone` | `true` to clone the VMs from the template's current snapshot as linked clones, `false` for full clones ([ADR-0037](adr/0037-images-per-vcenter.md)). Without it, as the template of **Templates and networks** the `template` binding chose says, else full clones. Not with `library` |
| `properties` | Custom properties of the VMs, over the project's of the same key ([ADR-0038](adr/0038-names-groups-secrets-properties.md)): text, or text with `${CEL}` over `inputs`, `project` and `propgroup` (`${inputs.app}`, `${project.properties.costCenter}`, `${propgroup.network.dns_domain}`). Each VM gets them as vSphere custom attributes and in its notes. A property that reads a secret input, the form's or a property group's, shows as `••••` everywhere |
| `folder` | The VMs' folder instead of the placement rule's: a path from the root (`/GVA/vm/shop`), or below the VM folder of the rule folder's datacenter (`shop/${inputs.app}` for a rule folder `/GVA/vm/rvav/web/dev` gives `/GVA/vm/shop/...`). Missing folders are created. A folder from the root must be in the datacenter of the cluster the VMs go on. No `..`, and no secret input: saving refuses a folder that reads one, the form's or a property group's |
| `prevent_delete` | `true` keeps the VMs from being deleted: the deployment's **Delete** is refused and names them, and the end of its lease doesn't delete it: rvav tells the owner once, and cancels nothing waiting on it. An administrator (anyone holding `project.update` in the project) runs the deployment's **Allow deletion** first; project admins and owners can't. VMs adopted from vRA keep vRA's `preventDelete` the same way, as do VMs adopted with **Keep from deletion** ([vRA import](operations/vra-import.md#delete-protection)) |
| `property_groups` | Property groups the item uses, each at a version: `[{key: network, version: 2}]` ([ADR-0038](adr/0038-names-groups-secrets-properties.md)). Their inputs join the request form, each group's in a section of its own, and go where the item's own go: AWX jobs, the events' `inputs`, and the AWX inventory when the input is exposed to it. Bindings, properties, folders, form rules and composite steps read their constants as `propgroup.<key>.<name>`. The item keeps the version it names when the group is saved again. An input both in the form and in a group, or in two groups, is refused |
| `awx_templates` | Post-configuration AWX templates, by key |
| `rollback_on_failure` | Delete what was built when a step fails |
| `components` | Groups of VMs, built as one deployment: `key`, `title`, `count` (0 to 20, a number or CEL), `anti_affinity`, `depends_on` (keys of the components it is built after), and any of `naming`, `template`, `library`, `guest`, `bindings`, `disks`, `nics`, `ipam`, `ipv6`, `tags`, `storage_policy`, `provisioning`, `linked_clone`, `groups`, `constraints`, `folder`, `prevent_delete`, which win over the blueprint's (guest field by field, bindings key by key, constraints kind by kind), and `properties`, added to the blueprint's key by key |
| `resources` | With `components` only: the deployment's custom resources, made once its VMs are built ([ADR-0056](adr/0056-custom-resource-types.md)): `key` (as a component's, unique among components and resources), `type` (a resource type's key), `title`, `with` (its create step's inputs). See **Custom resources** below |
| `deployment_name` | With components, the deployment's naming template, without `{seq}`; else the item's name and the request number. A name the project's live deployments already hold gets the request number after it. A name the requester gives wins ([ADR-0046](adr/0046-deployment-names-and-versions.md)) |

Publishing refuses a VM item whose VMs' size can't be worked out: each
VM, the blueprint's one or each component with the bindings it takes from
the blueprint, needs `vcpu` and `memory_gb`, and one that is a plain
number needs it to be whole and 1 or more. The refusal is at the binding,
such as `/blueprint/components/1/bindings/memory_gb`. CEL that reads the
inputs is worked out when the item is requested, and a request whose
inputs give no size is refused. A VM item made with **New item** starts
with `vcpu: "2"` and `memory_gb: "4"`, and with
`naming: "{project}-{environment}-{component}{seq:03}"`: `web-dev-001`,
or `web-dev-db001` for a `db` component's VMs. A VM item without a
naming is published, as a naming policy may name its VMs; a request for
it is refused (422, `no_naming`) where no policy applies, and asks the
requester to have a catalog admin check the item. Its version warns of
it (`warnings`, `no_naming`), at `/definition/blueprint/naming`, or at a
component without a naming when others have one, unless a naming policy
applies everywhere.

Without `components` the blueprint builds one VM, and a request for
several makes one deployment per VM; with them, one deployment holding
every component's VMs (20 at most). When a component's `cluster` isn't
bound, rvav chooses among the allowed clusters that meet its constraints
by the placement rule's strategy: the most free memory by default, the
least free memory with room for the VMs (`binpack`), or the fewest of
rvav's VMs (`spread`). It chooses the datastore with the most free space
among those that meet its constraints.

**Templates per vCenter**
([ADR-0037](adr/0037-images-per-vcenter.md)): when the `template` binding
gives the key of a template of **Templates and networks**, rvav chooses
the cluster among the vCenters that template has a source in, then clones
that vCenter's source, a VM template or a content library item. The
clusters of the vCenters without a source aren't used, and the preview
names those vCenters. A bound cluster in a vCenter without a source is an
error. A blueprint's own `template`, without the binding, is cloned in the
vCenters that have a template of that name. Such a template may have
cloud-init user data and constraint tags of its own: its constraints are
added to the component's, and its user data is merged with
`guest.cloud_init.user_data` when the VM is customized (two
`#cloud-config` documents: maps merged, the template's list entries
first, the blueprint's other values; else a multipart document, the
template's part first). Its VMs are configured by cloud-init even when
the blueprint has no `cloud_init`, so the blueprint can't name a
`customization_spec` then. A template that makes linked clones applies
`linked_clone` unless the blueprint sets it: a linked clone keeps the
template's disk size, so a `disk_gb` larger than the template's first
disk fails the clone.

**Remote access** ([ADR-0039](adr/0039-guest-access-and-networking.md)):
`guest.remote_access` gives the VMs a user people sign in as, on the
blueprint for every component or on a component for its own VMs:

| Field | What it is |
|---|---|
| `kind` | `ssh_key` (a public key you give), `generated_key` (rvav makes a key pair for each deployment) or `password` |
| `username` | The user, created on the VM with `sudo` without a password. On Linux: lowercase letters, digits, `_` and `-`, not `root` |
| `public_key` | With `ssh_key`: the key itself (`ssh-ed25519 AAAA… ops@laptop`), or the input that holds it, as `${inputs.ssh_key}` |
| `password` | With `password`: a secret input, as `${inputs.password}`. A plain input is refused: it is kept with the request |
| `secret` | With `ssh_key` or `password`: a stored secret that holds the public key or the password, instead of `public_key` or `password` |

```yaml
guest:
  os_family: linux
  remote_access: {kind: ssh_key, username: ops, public_key: "${inputs.ssh_key}"}
```

On Linux, rvav adds the user to the VM's cloud-init user data, after the
template's and the blueprint's `cloud_init`: as a list entry when every
part is `#cloud-config`, else as a part of its own. The distribution's
default user (`ubuntu`, `cloud-user`) stays unless the user data lists
`users` itself. A password turns on SSH password sign-in and has
cloud-init clear the user data from the VM's guestinfo once it read it.
The VMs are configured by cloud-init, so the blueprint can't name a
`customization_spec` with `remote_access`, and a request is refused when
the `template` binding chooses a template with a customization
specification.

On Windows, how the user is given depends on the template the `template`
binding chooses:
- **A template with cloudbase-init** (see the administrator's guide): any
  kind. cloudbase-init makes the user, a member of Administrators, with
  its password or its keys. The VM's user data (`cloud_init.user_data`, a
  PowerShell script starting `#ps1_sysnative`, `rem cmd` or
  `#cloud-config`) runs after the template's. `domain_join` and
  `customization_spec` are refused.
- **Any other Windows template:** `password` only, for the user
  `Administrator`, set by vCenter's customization. A key, another user, or
  `cloud_init` is refused when the request is made.

A Windows guest can't have both `remote_access` and `admin_password`. The
user name has up to 20 letters, digits, dots, dashes and underscores. The
deployment's request shows the user and the key's fingerprint on its
customize step, never the key or the password.

With `generated_key`, rvav makes one key pair for the deployment and puts
its public key on every VM whose component has `generated_key`. The
deployment's page shows the user and the key's fingerprint under **Remote
access**, where the deployment's owner downloads the private key once:
rvav forgets it as it hands it over, and when the deployment's VMs are
deleted.

**Placement targets** ([ADR-0036](adr/0036-placement-targets.md)): the
placement rule may put the VMs in a resource pool, on some hosts, or in a
datastore cluster. Bindings narrow them further, within what the rule
allows:

- `resource_pool` is the rule's pool or one below it. A bound cluster
  without the pool is an error.
- `host` is one of the cluster's hosts that the rule's patterns allow,
  connected and not in maintenance. All the VMs start on it, so
  `anti_affinity` with more than one VM is an error.
- `datastore_cluster` takes the VMs to the datastore of that datastore
  cluster that rvav chooses. When Storage DRS is on for it, the clone goes
  to the datastore Storage DRS recommends, so the VMs can end up on
  another datastore than the preview's, in the same datastore cluster.

**Constraint tags** ([ADR-0035](adr/0035-constraint-tags.md)) say what
the VMs need from vCenter, with the vSphere tags an administrator puts on
clusters, datastores and networks:

```yaml
blueprint:
  kind: vm
  constraints:
    compute: [rvav-env/prod, "!rvav-zone/pci"]   # a production cluster outside the PCI zone
    storage: ["rvav-backup/${inputs.backup}", rvav-tier/gold:soft]
    network: ["!rvav-zone/dmz"]
  disks:
    - size_gb: 200
      constraints: [rvav-tier/platinum]           # this disk on its own datastore
```

- An entry is `category/tag`, as the inventory writes tags. `!` in front
  rules out what carries the tag; quote the entry in YAML.
- `:soft` makes it a preference: rvav prefers what meets it and, when
  nothing allowed does, places the VMs anyway and says so on the
  request's timeline. Without `:soft` (or with `:hard`), the request fails
  before anything is made, saying which constraint nothing meets.
- `compute` chooses the cluster, `storage` the datastore and `network` the
  first adapter's network. A host's tags count for `compute`: the VMs
  start only on the hosts that carry the tag, or whose cluster does. A
  datastore cluster's tags count for `storage` on each of its datastores. rvav chooses the network only when the
  `network` binding is absent: the first allowed network, by name, with an
  EfficientIP subnet that meets them. Network constraints don't apply to a
  segment made for the deployment.
- A component's `constraints` replace the blueprint's kind by kind:
  `storage: []` removes the blueprint's storage constraints.
- A disk's `constraints` place that disk on a datastore of its own when its
  `datastore` is absent.
- `${...}` is CEL over `inputs`, `sizes`, `segments`, `environment` (its
  key) and `project`, worked out at submit. An entry that is only `${...}`
  may give a list of entries; an empty string gives none.
- Constraints choose among what the placement rules allow, never beyond.
  What the blueprint or the form binds is checked against them: a hard
  constraint it doesn't meet is an error on that field.
- When you save the item, rvav refuses an entry that isn't
  `[!]category/tag[:soft]` (vRA's `env:prod` included) and CEL that
  doesn't compile. It warns about a tag that no cluster, datastore or
  network in vCenter carries, and still saves.

The request form's summary says which constraint chose or ruled out which
cluster, datastore or network.

**References between components**: a component's `tags` binding and
cloud-init user data can read what each VM is and what other components
built. rvav works them out as each VM is built, so they see the
addresses:

| In CEL (`tags`) | In cloud-init | What it is |
|---|---|---|
| `self.index` | `{{ .vm.index }}` | The VM's index within its component, from 0 |
| `self.name`, `self.fqdn`, `self.address`, `self.component` | `{{ .vm.name }}`, `{{ .vm.fqdn }}`, `{{ .vm.ip }}` | The VM itself |
| `components.db.count` | `{{ .components.db.count }}` | How many VMs `db` has |
| `components.db.names`, `.fqdns`, `.addresses` | `{{ .components.db.addresses \| json }}` | Lists, in index order; an address is the first adapter's |
| `components.db.vms[0].address` | `{{ (index .components.db.vms 0).address }}` | One VM: `name`, `fqdn`, `address`, `index` |

A component that reads another is built after it, and `depends_on`
orders components without a reference. Components that depend on nothing
are built first, at the same time; then those that depend only on them,
and so on.

```yaml
components:
  - key: db
    title: Database
  - key: web
    title: Web servers
    count: 2
    bindings:
      tags: "['app/node' + string(self.index), 'app/db-' + components.db.addresses[0].replace('.', '-')]"
    guest:
      cloud_init:
        user_data: |
          #cloud-config
          write_files:
            - path: /etc/shop.conf
              content: |
                db={{ (index .components.db.vms 0).address }}
                node={{ .vm.index }} of {{ .components.web.count }}
```

- A component's tags can't read its own component: its VMs get their
  addresses at the same time. Its cloud-init can.
- A `tags` binding that reads `self` or `components` sees `inputs` too,
  but not `sizes`. Other bindings are worked out when the request is
  submitted, before any VM has an address, so they can't read `self` or
  `components`.
- Names don't read the index: `{seq}` numbers them. To pass a name
  through a string function, use a binding, `{bindings.<key>}`.
- When you save the item, rvav refuses a reference to a component that
  doesn't exist, a `depends_on` naming one, and components that would
  each be built after the other, with the line in the YAML.

**Custom resources** ([ADR-0056](adr/0056-custom-resource-types.md)): a
blueprint with components can list resources of the resource types
administrators define, such as a load balancer in front of its web
servers. Each is made by its type's create step once every VM is built,
one at a time in list order, and kept in the deployment beside its VMs:

```yaml
components:
  - key: web
    title: Web servers
    count: 2
resources:
  - key: lb
    title: Load balancer
    type: load-balancer
    with:
      name: deployment.name + "-lb"
      members: components.web.addresses
      port: 443
  - key: dns
    type: dns-alias
    with: {target: resources.lb.vip}
```

`with` strings are CEL over `inputs`, `environment`, `project`,
`propgroup`, `deployment` (`id`, `name`), `components` (as in
**References between components**) and `resources`, the properties of
the resources listed before it; other values are taken as they are.
A resource has no `when` and no `count`. Its type's create returns its
properties, and its name: the result's `name`, else the `name` input,
else `<deployment>-<key>`.

- When you save the item, rvav refuses resources in a blueprint without
  components, a key a component or another resource has, a missing
  `type`, and a reference to a component that doesn't exist or to a
  resource not listed before. A draft may name a type that doesn't exist
  yet: publishing needs it to exist and be on.
- Once the type exists, saving also refuses an input `with` binds that
  the type's create workflow doesn't take, at its key, naming those it
  takes: `Load balancer takes members, name and port: pool isn't one of
  its inputs.` The inputs are those the workflow's input schema lists,
  and `name`, which names the resource. A workflow whose schema lists
  none, or sets `additionalProperties: true`, and an AWX template take
  any.
- A request is refused while the type is turned off.
- When the request fails and rolls back, its custom resources are deleted
  first, the last made first. Deleting the deployment deletes them before
  its VMs.
- An update makes the resources a new version adds, updates those whose
  inputs may change with the type's update step, and deletes those it
  drops. A type without an update step refuses an update that changes a
  resource's inputs.

**NSX** ([ADR-0028](adr/0028-nsx-networking.md)): an item can put its VMs
on a segment made for each deployment, in groups made for it with the
firewall rules into them, and in existing groups:

```yaml
blueprint:
  kind: vm
  segments: [{key: app, title: Application}]
  bindings: {network: segments.app, groups: "inputs.extra_groups"}   # nsx.groups in the form
  groups: [web, "nsx:grp-backup-clients"]
  security_groups:
    - key: web
      title: Web servers
      rules:
        - {from: [any], services: [HTTPS]}
        - {from: ["nsx:grp-admin-jump"], services: [SSH]}
```

The placement rule of the project and environment says where the segment
goes (its Tier-1 and EfficientIP block). Without an NSX Manager, such an
item can't be saved.

The seeded **Three-tier application** (`three-tier`) is such an item:
one to three web servers kept on separate hosts, an application server
and a database server on a segment of their own, each tier in a group
made for it with the rules between them (the database's ends with a
drop), and the database server in the existing `grp-backup-clients` too.
On the compose stack, a placement rule's segments go on the fake NSX's
Tier-1 `t1-gva-dev`, their subnets from EfficientIP's `nsx-gva-dev`
block.

The seeded **PostgreSQL 16 cluster** is the reference: a composite whose
first step is the `pg-node` item (one component of three nodes kept on
separate hosts, each with a data disk, made a Patroni cluster by the AWX
roles of its post-configuration), then the database's owner role on it.
`pg-node` has no entitlement: only the composite requests it.

### AWX import
- A content source connects to AWX and filters templates by organisation, label or
  name pattern.
- The **survey spec is converted into a form automatically**:
  - `text`, `textarea`, `password` → string fields (`password` → secret widget)
  - `integer`, `float` → number fields with min and max
  - `multiplechoice`, `multiselect` → dropdown and multi-select
  - `required`, `default`, `min`, `max` and the help text carry over
- A catalog admin can then **enhance** the generated form (dynamic values,
  conditional fields, layout). The enhancements are kept on top of the survey
  mapping.
- Periodic resync detects survey or template changes and flags the item as
  "source changed", with a diff and a one-click update to a new draft version.
- The template's launch prompts (`ask_*_on_launch`) are read so the app knows
  which overrides are allowed (limit, inventory, credentials, variables).

## 2. Catalog experience

- Home page with featured items, favourites, recently requested and categories.
- Full-text search with filters (category, tag, type) plus ⌘K.
- Item cards: icon, name, short description, typical duration, "needs approval"
  badge.
- Item detail page: Markdown description, what will be created, approval and lease
  summary, a request button, and version history.
- **Sharing**: items shared with projects, and in each with all its members or
  chosen users, OIDC groups and project roles. Users only see what they can
  request.
- **Versioning**: draft, published and deprecated states; preview the draft as a
  user; compare versions; roll back; existing deployments keep their version.
- Import and export of catalog items as YAML (GitOps-friendly), and the
  catalog kept in a Git repository (C1, ADR-0029).

**Built (S8, [ADR-0019](adr/0019-catalog-administration.md)):** catalog
admins and administrators create items, open them to every project (those
made later too) or to chosen projects, and manage categories and the icon
(from rvav's own set, no upload). Versions are draft, published or
deprecated: one draft per item at most; publishing a deprecated version
again rolls back; withdrawing the published version takes the item out of
the catalog; deployments and requests keep their version. Since C2.1
([ADR-0046](adr/0046-deployment-names-and-versions.md)) an admin may keep
several versions published: requesters pick one, the newest by default,
updates move to any of them, and a retired one is refused as stale. Two versions compare change by change. Items export to YAML
(one version or all) and import as a new item or a new draft, never
published by the import, with problems on their YAML line. The catalog can
be kept in a Git repository (ADR-0029): rvav commits what it publishes,
and what changes in the repository becomes a draft.

**Built (G1.2, [ADR-0019](adr/0019-catalog-administration.md#sharing)):**
an item's sharing names, in each project it is shared with (or in every
project), who may request it there: all the project's members, or chosen
users, OIDC groups and project roles (built in or custom, held directly or
through a group). Shares add up, and one that names nobody is all the
members, so items shared before keep their meaning. Sharing doesn't replace
`request.submit`: someone requests an item in a project when they may
request there and a share covers them. The catalog lists an item, and finds
it, its form and its fields' values, only for those it is shared with in
the project they request in; submitting it is refused (403) for the others.
Administrators and catalog admins are no exception: a role held for all of
rvav doesn't count. Narrowing applies to what comes next: a saved draft
stays, its submit is refused; a request already submitted, waiting for
approval or scheduled, runs. Categories aren't shared: an item is. The
YAML export has the item's sharing, with projects by code and users by
email; an import that makes a new item shares it so, while the catalog's
repository never has it. vRA's content sharing policies become shares when
the catalog is imported ([vRA import](operations/vra-import.md)). Catalog
admins share an item in **Administration › Catalog items** › the item ›
**Sharing**, whose sheet lists who can request it before saving
([admin guide](guides/admin.md)). Because shares add up, that list
(`POST /admin/catalog/items/{id}/entitlements/requesters`) counts the
item's every-project share too, and returns it as `everywhere`. Sharing
an item with every project keeps each project's own share.

## 3. Custom forms

A form is a **JSON Schema** (data contract) plus a **UI schema** (presentation and
behaviour). The designer edits both visually; a code view shows the YAML.

### Field types
text, textarea, number, slider, toggle, dropdown, multi-select, dual list,
search (type-ahead), tree, radio cards, date/time, tags, key-value, datagrid (rows of sub-fields), group
of fields (one object), secret, file upload,
Markdown or info block, image, link, and resource pickers:
- vSphere: cluster, datastore, network, folder, template, existing VM
- EfficientIP: subnet, free IP, DNS zone
- AWX: inventory, credential, host
- App: project, user, group, deployment

Each field is an input in the form schema (`form_schema.properties`) and a
field in the UI schema (`ui_schema.fields`) with its `widget`. The schema
holds the type and the checks, which the server applies on preview and
submit; the widget is how the form shows it. The value reaches rules, the
blueprint and AWX's extra variables as typed: a number as a number, a list
as a list.

| Widget | Schema | Value |
| --- | --- | --- |
| `secret`, or `text` with `secret: true` | `string` | Masked as you type, with a button that shows it; never shown again after submit (S14.6) |
| `date` | `string`, `format: date` | `2026-12-24` |
| `datetime` | `string`, `format: date-time` | `2026-12-24T14:30:00+01:00`: ISO 8601 with the requester's time zone |
| `number`, `slider` | `integer`, or `number` for decimals | `4`, `1.75` |
| `tags` | `array` of `string`, `uniqueItems: true` | `["backup/daily", "tier/gold"]` |
| `key_value` | `object`, `additionalProperties: {type: string}` | `{"app": "billing", "owner": "ops"}` |
| `datagrid` | `array` of `object`, with `ui.columns` | `[{"size_gb": 50, "tier": "gold"}]` |
| `dual_list` | `array` of `string`, `uniqueItems: true` | `["tnguyen", "lmartin"]`, in the order chosen |
| `object` | `object` with `properties`, with `ui.columns` | `{"policy": "daily", "keep_days": 14}` |
| `search` | `string` | `app-003`: an option its value source finds for the text typed |
| `tree` | `string` | `/GVA/vm/rvav/web`: a node of a hierarchy its options or value source offer |
| `file` | `object` | `{"id": "fil_8k2…", "name": "rules.csv", "size": 2048, "content_type": "text/csv", "sha256": "9f86…"}`: the record of a file the requester attached, not its content |
| `project` | `string` | A project's ID, `prj_web`: one the requester may request in |
| `user` | `string` | A person's ID, `usr_lmartin`, found by typing their name or email |
| `deployment` | `string` | A deployment's ID, `dep_7f3k2`: one of the project's, not deleted |
| `vsphere_vm` | `string` | A VM's name, `web-dev-app001`: one of the project's deployments' VMs |

A date or a time takes its bounds in `formatMinimum` and `formatMaximum`,
written in the input's format; a time without a time zone is refused. A
decimal number takes `minimum`, `maximum` and `multipleOf`, its step:

```yaml
form_schema:
  properties:
    window:
      type: string
      format: date-time
      formatMinimum: "2026-12-01T00:00:00+01:00"
      formatMaximum: "2026-12-31T23:59:59+01:00"
    cpu_ratio: { type: number, minimum: 0.5, maximum: 4, multipleOf: 0.25 }
ui_schema:
  fields:
    window: { widget: datetime, label: Maintenance window }
    cpu_ratio: { widget: number, label: CPU ratio }
```

Tags are typed and removed as chips. The field's `options`, or its value
source, suggest tags; requesters can type others unless `items.enum` lists
the only ones allowed. `maxItems` caps the count. For vSphere tags, the
blueprint's `tags:` takes `category/tag` strings, so give the items that
pattern and bind the input, for example `tags: ${inputs.tags}`:

```yaml
form_schema:
  properties:
    tags:
      type: array
      uniqueItems: true
      maxItems: 5
      items: { type: string, pattern: "^[^/]+/[^/]+$" }
ui_schema:
  fields:
    tags:
      widget: tags
      label: vSphere tags
      options: [{ value: backup/daily, label: Daily backup }]
```

Key and value pairs make an object of strings. A key can be given once: the
form flags a repeated key and leaves that pair out until it changes, and
`propertyNames: {minLength: 1}` makes the server refuse an empty key.

A table (`datagrid`) is a list of rows. Each column in `ui.columns` edits one
property of the rows' schema (`items.properties`), with a `text`, `number`,
`toggle` or `select` widget that fits the property's type. A `select`
column's property lists its choices in an `enum`, which the server checks.
`minItems` and `maxItems` bound the rows, and `items.required` names the
cells every row needs. The server checks every row and says where a problem
is: "Row 2, Size (GB): Enter 10 or more."

```yaml
form_schema:
  properties:
    disks:
      type: array
      minItems: 1
      maxItems: 4
      items:
        type: object
        required: [size_gb]
        additionalProperties: false
        properties:
          size_gb: { type: integer, minimum: 10 }
          tier: { type: string, enum: [gold, silver] }
          thin: { type: boolean }
ui_schema:
  fields:
    disks:
      widget: datagrid
      label: Extra disks
      columns:
        - { key: size_gb, label: Size (GB), widget: number }
        - key: tier
          label: Tier
          widget: select
          options: [{ value: gold, label: Gold }, { value: silver, label: Silver }]
        - { key: thin, label: Thin, widget: toggle }
```

A column with no property, a property with no column, or a widget that
doesn't fit its type is refused when the version is saved.

A multi-select (`multiselect` or `checklist`) takes its options from the
field's `options` or its `value_source`, like a dropdown. With
`depends_on`, a change to a field it depends on fetches the options again
and drops the picks they no longer offer. The value is a list of the
options' values.

A dual list (`dual_list`) shows two lists: the options available and those
chosen, in order. Requesters move an option across with **Add** and
**Remove**, a double click, or Enter, and order the chosen ones with
**Move up** and **Move down**, or Alt+Up and Alt+Down. Its options come
from `options` or a `value_source`, as a multi-select's do, and a change to
a field it depends on drops the picks the source no longer offers. The
value is the chosen values, in the order the requester gave them, so a
playbook can read the first as the primary. To have the server refuse a
value that isn't an option, list the values in `items.enum`; `minItems` and
`maxItems` bound how many are picked:

```yaml
form_schema:
  properties:
    owners:
      type: array
      uniqueItems: true
      maxItems: 3
      items: { type: string, enum: [lmartin, tnguyen, jroux] }
ui_schema:
  fields:
    owners:
      widget: dual_list
      label: Owners
      options:
        - { value: lmartin, label: Léa Martin }
        - { value: tnguyen, label: Thanh Nguyen }
        - { value: jroux, label: Jean Roux }
```

A group of fields (`object`) edits one object: each sub-field edits one of
its `properties`. Its sub-fields are `ui.columns`, as a table's columns
are, with the same `text`, `number`, `toggle` and `select` widgets and the
same checks, against the object's own properties rather than its rows'.
`required` names the sub-fields the object needs. A problem says which
sub-field it is in: "Keep (days): Enter 1 or more." Use a group of fields
when a playbook takes one object, and a table when it takes a list of
them:

```yaml
form_schema:
  properties:
    backup:
      type: object
      required: [policy]
      additionalProperties: false
      properties:
        policy: { type: string, enum: [daily, weekly] }
        keep_days: { type: integer, minimum: 1 }
        offsite: { type: boolean }
ui_schema:
  fields:
    backup:
      widget: object
      label: Backup
      columns:
        - key: policy
          label: Policy
          widget: select
          options: [{ value: daily, label: Daily }, { value: weekly, label: Weekly }]
        - { key: keep_days, label: Keep (days), widget: number }
        - { key: offsite, label: Copy off site, widget: toggle }
```

Information text (`info`), an image (`image`) and a link (`link`) show
something and take no input: they have a field in the UI schema and no
property in the form's schema, and an image or a link given a property is
refused. An image shows the picture at its `url` with its text alternative,
`alt`, which screen readers read in its place; its `help` is the caption. A
link's `label` is its text, and it opens its `url` in a new tab; its `help`
shows under it. An address starts with `https://` or `http://`, or is a
path on rvav's own site (`/...`): any other, such as `javascript:` or
`data:`, is refused when the version is saved. A draft may leave the
address and the text alternative out; to publish, give them:

```yaml
ui_schema:
  fields:
    zones:
      widget: image
      label: Network zones
      url: https://wiki.corp.example/img/zones.png
      alt: The web, app and data zones, each behind its own firewall
      help: Each tier runs in its own zone.
    sizes:
      widget: link
      label: VM sizes explained
      url: https://wiki.corp.example/vm-sizes
```

The browser loads an image from another site only if the web server's
Content-Security-Policy allows that site: list its origin in
`RVAV_WEB_IMAGE_SOURCES` ([configuration](operations/configuration.md)).
Otherwise the form says the image didn't load, with its text alternative.

An image can also show a picture rvav keeps: upload it with
`POST /api/v1/admin/catalog/images?name=zones.png`, the picture as an
`application/octet-stream` body, or with **Upload a picture** in the
designer. rvav takes PNG, JPEG, GIF and WebP pictures of 2 MB at most whose
content is what their extension says, never SVG, and answers with the
picture's `url`, `upload:<id>`, which an image's `url` takes. Uploading
needs `catalog.manage`; anyone signed in sees the picture. Saving a version
whose image names a picture rvav doesn't keep is refused, with the code
`unknown` ([ADR-0040](adr/0040-file-fields.md)):

```yaml
ui_schema:
  fields:
    zones:
      widget: image
      label: Network zones
      url: upload:img_5b1e0c7f2a9d4e8c61f3
      alt: The web, app and data zones, each behind its own firewall
```

The picture stays in the rvav you upload it to. An item exported to YAML or
kept in Git names it by its `upload:<id>` only, so another rvav refuses the
version until you upload the picture there and give the image its new
`url`. To show one picture in several installations, give its address on a
site they can all reach instead.

The `vsphere_folder` widget offers the VM folders of the datacenters whose
clusters the project's placement allows in the environment, by path from
the root (`vsphere.folders`). The server refuses a folder it doesn't offer.
To put the VMs in the folder picked, bind the blueprint's `folder` to the
input; a path from the root wins over the placement rule's folder:

```yaml
form_schema:
  properties:
    folder: { type: string }
ui_schema:
  fields:
    folder:
      widget: vsphere_folder
      label: Folder
      value_source: { kind: builtin, name: vsphere.folders }
blueprint:
  kind: vm
  folder: ${inputs.folder}
```

The `project`, `user`, `deployment` and `vsphere_vm` widgets always take
their choices from rvav's own records: `rvav.projects`, `rvav.users`,
`rvav.deployments` and `rvav.vms`. A field may leave its `value_source`
out; another source is refused when the version is saved. Each offers only
what the requester may see, and the server refuses, on preview and submit,
a value they couldn't have picked, such as another project's deployment.
To narrow the choices, give the source parameters:
- `rvav.deployments`: `item`, a catalog item's slug, keeps its deployments.
- `rvav.vms`: `deployment`, a deployment's ID, keeps its VMs. With
  `${inputs.deployment}` and `depends_on`, the VMs follow the deployment
  picked.

```yaml
ui_schema:
  fields:
    server:
      widget: deployment
      label: Web server
      value_source: { kind: builtin, name: rvav.deployments, params: { item: rhel9-server } }
    vm:
      widget: vsphere_vm
      label: VM
      depends_on: [server]
      value_source: { kind: builtin, name: rvav.vms, params: { deployment: "${inputs.server}" } }
```

A search field (`search`) finds its choices as the requester types: from
two letters, it asks its value source with the text, a quarter of a second
after the last key, and lists what it finds, 20 at most. A source that
searches by the text itself takes `search: true`: a `rest` source reads the
text as `${search}` in its URL or body, escaped as inputs are, and a `cel`
source's `options` expression reads it as `search`. A `library` source
searches when its form library entry has `search: true`, the same way. For any other source,
built-in ones among them, rvav asks the source as for a dropdown and keeps
the options whose value, label or detail contains the text. To publish, a
search field needs a source that gives options.

On preview and submit, the server asks the source for the field's value as
the searched text, and refuses the value unless the source finds it. A
source that searches must therefore find an option by its value, as well as
by the text people type:

```yaml
ui_schema:
  fields:
    app:
      widget: search
      label: Application
      placeholder: Name or code
      value_source:
        kind: rest
        search: true
        params:
          url: https://cmdb.corp.example/apps?q=${search}
          items: $.apps
          value: $.id
          label: $.name
          meta: $.team
```

A tree field (`tree`) shows its options as a hierarchy and takes one node.
Each option names the option it sits under by that option's value, in
`parent`; an option whose parent isn't offered sits at the top. The value
is the node's, never a path of labels, so a label may hold any character
and a source keeps its own IDs: a directory's distinguished names, a CMDB's
keys. A `rest` source maps `parent` with a JSONPath in each item, as it maps
`label`; a `cel` source's options give `parent` with `value` and `label`;
static options give it as a third part, `value | label | parent`, in the
designer.

The built-in `vsphere.folders` gives each VM folder under its parent
folder. In a tree, a folder shows its own name, and a folder at the top its
path; in a dropdown, every folder shows its path. The tree opens at the
chosen node. On preview and submit, the server refuses a node the field's
options or source don't offer:

```yaml
ui_schema:
  fields:
    folder:
      widget: tree
      label: VM folder
      value_source: { kind: builtin, name: vsphere.folders }
    unit:
      widget: tree
      label: Directory unit
      value_source:
        kind: rest
        params:
          url: https://directory.corp.example/units
          items: $.units
          value: $.dn
          label: $.name
          parent: $.parent_dn
    zone:
      widget: tree
      label: Zone
      options:
        - { value: dmz, label: DMZ }
        - { value: dmz-web, label: Web, parent: dmz }
        - { value: lan, label: LAN }
```

A file field (`file`) takes one file the requester attaches
([ADR-0040](adr/0040-file-fields.md)). `accept` lists the types it takes:
extensions (`.csv`), MIME types (`application/pdf`) or a family
(`image/*`). An extension stands for its type, so `.jpg` takes `.jpeg`
files too. `max_size_mb` is its largest file, from 1 to 25 MB, 10 if left
out. rvav takes PDF, PNG, JPEG, GIF, WebP, text and logs, CSV, Markdown,
JSON, YAML, XML, shell and PowerShell scripts, PEM, CRT and CER
certificates, ZIP, gzip, and Word, Excel and PowerPoint (`.docx`, `.xlsx`,
`.pptx`) files; never HTML or SVG. A type outside the list is refused on
save, and a file field without `accept` can't be published. A file field
can't be secret, editable by approvers, or have an initial value.

The file goes to rvav when the requester chooses it, before the request
(`POST /forms/files`). rvav checks its size, its extension, and that its
content is what the extension says, whatever type the browser claims: a
`.csv` that holds a PDF is refused. It keeps the file in its database with
the request. The form waits until each file is sent before it submits,
saves a draft or goes to its next page. An update may name the
deployment's files again: they stay its first request's, and a new file
goes to the update request. A file attached but never requested goes
after a day. AWX's
extra variables, workflows and events get the file's record. A playbook
that needs the content downloads it with `GET /api/v1/files/{id}` and a
token of an account allowed to: the requester, the request's approvers,
and its project's administrators. rvav always sends a file as a download,
never shown in the browser:

```yaml
form_schema:
  required: [rules]
  properties:
    rules: { type: object }
ui_schema:
  fields:
    rules:
      widget: file
      label: Firewall rules
      help: The export of the rules to open, one per line.
      accept: [.csv, .xlsx]
      max_size_mb: 2
```

### Behaviour
- **Dynamic values**: options, defaults or validation results from a *value source*:
  - built-in sources (vSphere inventory cache, EfficientIP, AWX, app data),
  - REST source (URL template, auth, JSONPath mapping, caching),
  - CEL expression over other fields.
- **Cascading dependencies**: a field declares `dependsOn` and re-resolves when
  those fields change (cluster → datastores → networks → subnets).
- **Conditional rules** (CEL): visible, required, read-only, default, value.
- **Validation**: JSON Schema constraints, regex, CEL cross-field rules, async
  server-side validation (for example "hostname free in DNS").
- **Layout**: sections and their columns, shown on one page, as a wizard
  with a review step, or as tabs.
- **Computed fields**: for example the VM name preview from the naming policy.
- Every form is validated again on the server. The client is only for UX.

**Built (S8):**
- The form designer: fields by section, a live preview as a chosen project
  member, and the selected field's properties; every change saves the draft
  and the server's problems show where they are
  ([frontend.md](frontend.md#catalog-administration-and-the-form-designer)).
  The YAML view edits anything the designer doesn't.
- Value sources: the built-in ones, `rest` (a URL template with escaped
  inputs, GET or POST, a stored secret as bearer, basic or header, JSONPath
  mapping, cached; only the hosts listed for REST value sources, in
  Administration › Integrations › Allowed hosts or `RVAV_REST_SOURCE_HOSTS`, the list a composite's REST calls share; no redirects,
  5 s and 1 MB at most), `cel` (options or a value over the inputs,
  project, user and environment) and `library` (an entry of the form
  library, below). On a field that takes one value, a
  `rest`, `cel` or `library` source gives its default, or a computed field's value
  (see Defaults below). Dependencies re-resolve a field when the
  inputs it depends on change. A search field passes its text to the
  source (`${search}`, `search`) or keeps the options that contain it. A
  tree field's options name their `parent`.
- A `rest` template's references (`${inputs.x}`, `${params.x}`,
  `${search}`) are filled so that the call stays what the admin wrote. In
  the URL, each is escaped text: a list's items joined with commas, as a
  multi-select's, and a map as JSON. In a JSON body, a reference inside a
  string is escaped text the same way (empty when the input is absent),
  and one outside a string is a JSON value: `{"cores": ${inputs.cores}}`
  sends the number, a list or a map as JSON, a text quoted, and `null`
  when the input is absent.
- A rule or a `cel` source whose result holds an infinite number or NaN,
  as a division by zero gives, fails on its field: no input can hold one.
  A default rule then gives no default, and a computed value refuses the
  submit, saying why.
- File fields: the file is checked (types, size, content sniffed) and kept
  with its request; its requester, approvers and project administrators
  download it from the request's page.
- NSX's ([ADR-0028](adr/0028-nsx-networking.md)): `nsx.segments` (existing
  segments, by the port group name a `network` binding takes; `tier1` to
  keep one Tier-1's), `nsx.tier1s`, `nsx.groups` (groups VMs can join by
  tag, as `nsx:<ID>` for a `groups` binding) and `nsx.services`. What rvav
  made for deployments isn't offered, and no item may name a group rvav
  made, nor one selecting VMs by rvav's own tags. Saving an item that asks NSX for
  networks or groups needs an NSX Manager, and checks the groups and
  services it names.
- Rules are evaluated on the server, in the preview and again on submit:
  a computed value becomes the input, and a field a rule makes required is
  checked.
- Defaults (F1.4): a field's `default` rule, CEL over the other inputs,
  the environment, the project and the user, fills the field while the
  requester hasn't given it a value. Unlike a `value` rule's, the field
  stays editable.
  - The form shows the default as the field's value, and the default
    follows the inputs it reads. Once the requester changes the field,
    even to empty it, the field keeps their value: a later change to
    those inputs leaves it alone. A read-only field shows what the
    request gets: its default, else its initial value. A computed field
    and one with a value rule show their value instead.
  - On a dropdown whose source suggests an option (`vsphere.clusters`,
    `vsphere.datastores`, `vsphere.networks`), the default rule wins:
    the source gives no suggestion while the field has one.
  - The server works the defaults out on preview and submit, with the
    other rules. An input a new request leaves out takes the value the
    form starts with: its initial value, else its schema property's
    `default` ([ADR-0055](adr/0055-inputs-left-out.md)); without one, its
    field's default. `null` for a field with a start value or a default
    means the requester emptied it, and it gets none. A read-only field
    takes its default too. The other rules see the defaults.
  - The rules read the inputs as the request keeps them: not the input
    of a hidden or read-only field, which the request drops or replaces,
    nor a computed field's, of which they see the computed value. The
    server works the rules out again over those inputs until they stop
    changing, so a requester can't set a read-only field through a
    hidden one.
  - An approver's edit, and an update that keeps the deployment's
    version, work the inputs out again: a field the requester emptied
    stays empty, though the stored inputs leave it out.
  - A default rule doesn't see its own field's input. Defaults can read
    each other: the server works them out again until they stop
    changing. A rule that fails, or reads an input that has no value
    yet, gives no default, and the designer's preview says why.
  - A wizard's review step lists the defaults among the values that
    will be sent.
  - On a field that takes one value (`text`, `textarea`, `number`,
    `slider`, `date`, `datetime`, `toggle`), a `rest` or `cel` value
    source gives the field its default instead of options. A `rest`
    source's default is what the `value` JSONPath finds in the first
    item the `items` JSONPath finds, or in the answer itself without
    `items`; a `cel` source's is its `value` expression. On a `computed`
    field, the source gives the value, which the server works out again
    on preview and submit, whatever the request says. The value takes
    the input's type: a text input takes a number as its digits. A
    source that fails gives no default and holds nothing up. The field's
    `depends_on` names the inputs its source reads: the server works out
    the sources in that order, after the default rules, then the default
    rules again, so a default rule can read a source's default.
  - A field has one default at most. On save, rvav refuses a `default`
    rule beside a `value` rule, an initial value or a source that gives
    a default, and an initial value beside such a source, at
    `/rules/default` or `/value_source`. A computed, file, information,
    image or link field takes no `default` rule.
  - Day-2 action forms (an AWX template's survey, a workflow's inputs)
    have no rules: their defaults are the constants the survey or the
    schema gives.

  ```yaml
  ui_schema:
    fields:
      host:
        widget: text
        label: Host name
        rules: { default: 'inputs.app + "-" + environment' }
      owner:
        widget: text
        label: Owner
        depends_on: [app]
        value_source:
          kind: rest
          params:
            url: https://cmdb.corp.example/apps/${inputs.app}
            secret: cmdb
            value: $.owner.login
      cores:
        widget: number
        label: vCPUs
        value_source: { kind: cel, params: { value: 'environment == "prd" ? 4 : 2' } }
  ```
- Layouts (`ui_schema.layout.kind`, F1.3), for how the sections show, in
  their order:
  - `page`: every section on one page. A definition without a kind has
    this one.
  - `wizard`: one section per page, under a list of the steps. **Next**
    checks the page's inputs with the server's preview and stays on the
    page, its first problem focused, while one needs fixing. **Back** and
    **Next** move the focus to the new page's heading. The last step,
    **Review**, lists what was entered page by page, with option labels
    rather than values and secret fields masked, before **Submit**.
  - `tabs`: one section per tab, in the tablist pattern. A tab holding an
    input with a problem shows a mark and says how many.

  A page or tab whose fields a `visible` rule all hides is left out, and
  the server doesn't check its inputs. The layout is presentation only:
  the server checks a request the same way whatever it is. Saving a
  definition with another kind is refused at `/ui_schema/layout/kind`.
  The vRA importer makes a form with several pages tabs, as vRA shows
  them ([vRA import](operations/vra-import.md)).

  ```yaml
  ui_schema:
    layout:
      kind: wizard
      sections:
        - { key: basics, title: Basics, columns: 2, fields: [role, with_db] }
        - { key: db, title: Database, columns: 1, fields: [database] }
  ```
- The form library (F1.5, [ADR-0043](adr/0043-form-library.md)): value
  sources and validations written once, under Administration › Form
  library, and named by key and version from any item's form.
  - An entry is a `source` (a field's choices, or its value on a field
    that takes one) or a `validation` (a check of the whole form), of kind
    `rest` or `cel`, with the same `params` as an inline source or
    validation of its kind. It takes up to 20 parameters, each with a
    name, a label, a description, and whether it's required.
  - An entry reads its parameters, not the form's inputs: `${params.x}`
    in a REST entry's URL and body, `params.x` in its expressions, which
    also read `environment`, `project`, `user` and, for a source with
    `search`, `search`. The form gives each parameter as a CEL expression
    over what its rules read.
  - Saving an entry makes its next version; the versions before it don't
    change. Each item whose draft or published version names an earlier
    version gets a draft that names the new one, to review and publish:
    its draft is changed, or a new draft is made from its published
    version. Until that draft is published, the item keeps the version it
    names.
  - Saving an item refuses an entry or version that doesn't exist, an
    entry used for the wrong purpose, a parameter the entry doesn't have,
    an expression that isn't valid CEL, and one that reads an input the
    form (with its property groups) doesn't have. Publishing also refuses
    a required parameter the form doesn't give. While a required
    parameter's expression gives nothing, or fails because an input it
    reads has no value yet, the entry doesn't run: a source gives no
    options, and a validation isn't run. An expression that fails
    otherwise, such as on a type, fails the source and refuses the
    request.
  - An entry can't be deleted while a published, deprecated or retired
    version of an item names it. A draft that names a deleted entry is
    refused the next time it's saved.
  - Inline `rest` and `cel` sources and validations keep working.

  ```yaml
  ui_schema:
    fields:
      service:
        widget: select
        label: Service
        depends_on: [app]
        value_source:
          kind: library
          name: cmdbServices
          version: 2
          params: { environment: environment, app: inputs.app }
  ```
- Validations (F1.5): `ui_schema.validations` lists up to 20 checks of the
  whole form, run in order on submit, once the inputs pass the form's own
  checks, and before approval.
  - Each has a `key`, which names it in messages and the audit log, a
    `title`, a `kind` (`cel`, `rest` or `library`), its `params`, a
    `message` for a refusal that doesn't say why, and `fields`, the inputs
    a message for no input shows on (on the form when none).
  - `cel`: `params.check` is an expression over what form rules read.
    `true`, `null`, `""` or `{}` passes; `false` refuses with `message`; a
    text refuses with that text; a map refuses with a message for each
    input it names.
  - `rest`: `params.url`, `method`, `body` and `secret`, as a `rest`
    value source's. rvav reads a 2xx answer with JSONPath: `valid`
    (`$.valid` unless given), `message` (`$.message`) and `errors`
    (`$.errors`: a map of input to message, or a list of `{field,
    message}`). Without `valid`, an answer with no message and no errors
    passes. A `valid` path the validation names must be in the answer,
    and an empty answer (`null`) can't be read: either refuses the
    request.
  - `library`: a validation entry of the form library, at `version`, its
    parameters in `params`. A message for a parameter shows on the input
    that the parameter's expression reads alone, such as `inputs.cc`.
  - A validation that can't run (another status, a timeout, a host rvav
    may not call, an answer it can't read, an expression that fails)
    refuses the request and says so. Nothing is submitted while a
    validation's service is down.
  - Validations also run on an approver's edits and on an update request.
    A wizard runs them when the requester moves to the review step: the
    wizard stays on the first page with a message, and focuses its input.
    A message for no input shows above the form. Day-2 actions' forms
    have no validations.
  - Each run is audited (`form_validation.run`) with its outcome: `ok`,
    `refused`, `failed`, or `not_run` when a library entry's required
    parameter had no value.

  ```yaml
  ui_schema:
    validations:
      - key: cpu
        title: vCPUs for the environment
        kind: cel
        params: { check: 'environment != "prd" || inputs.cpu >= 2 ? {} : {"cpu": "Production needs 2 vCPUs or more."}' }
      - key: costCentre
        kind: library
        name: costCentreOpen
        version: 1
        params: { cost_center: inputs.cost_center }
  ```
- For AWX items, the form can choose the launch's inventory and credentials
  (inputs picked from `awx.inventories` and `awx.credentials`), which the
  template must prompt for. A composite's step running such an item
  launches with what its bindings give those inputs, not as extra
  variables: bind them to the composite's own `awx.inventories` and
  `awx.credentials` pickers, so a requester's choice is checked as the
  item's form checks it.

Not built yet: a server-side check of one field as it's typed (validations
run at the review step and on submit), the group and AWX host pickers, and
a way to put a changed field back to its default.

## 4. Requesting

- Live validation, quota usage preview ("this uses 8 of your 40 remaining vCPU"),
  and approval preview ("needs approval from the Infra leads").
- Request for yourself or **on behalf** of another user.
- **Count**: request N identical deployments.
- **Scheduled** requests ("deploy on Monday 08:00").
- Save as draft; **re-request** from history with prefilled inputs.
- Cancel while pending or, where possible, while running.

## 5. Policies

All policies have a scope (global, project or item), a CEL condition, and a priority.
Project admins write their project's approval, lease and day-2 policies,
lease rules and deployment limit policies, and each member's quota within
the project's own. Administrators write the global ones, which a project's
can't loosen, and the naming policies and quotas
([ADR-0045](adr/0045-deployment-sharing-and-project-policies.md)):
- an approval policy adds its levels to those of every other policy that
  matches; levels with the same approvers merge only within one scope, so
  a project's level never changes a global level's timeout or escalation;
- the first day-2 policy that matches decides, unless a hard global one
  that matches after it is stricter; a project's approval and a hard
  global approval both apply, the global levels first;
- the first lease rule that matches decides the lease; without one, a
  project's lease override replaces the environment's lease. A project
  admin's override or rule can't give more than an administrator's;
- each deployment limit is decided by the first policy, by priority, that
  sets it, and every hard global policy that sets it holds too, so a
  project's policy only tightens it.

Lease and day-2 policies, lease rules and deployment limit policies are
hard or soft. Hard refuses or asks for approval; soft warns, and the
requester may go on. The request keeps the
warnings it went on despite, and the audit log has them.

### Approval
- Multi-level (sequential), with any-of or all-of approvers per level.
- Approvers: users, OIDC groups, project roles (such as Supervisor) and the
  requester's manager (from an OIDC claim).
- Conditions, for example `inputs.memoryGB > 32 || inputs.environment == "prod"`.
- Auto-approve rules, delegation (out of office), timeout → auto-approve,
  auto-reject or escalate.
- Approve or reject from the web inbox, email links, or Slack/Teams buttons.
- Approvers can edit allowed inputs before approving (for example reduce RAM),
  and this is audited.

### Lease
- Default and maximum lease; "never expires" only where the policy allows it.
- Lease rules: a CEL condition, such as `item.slug == "gpu-server"`, gives
  the deployments it matches their own lease, tried by priority before the
  lease policies ([ADR-0048](adr/0048-lease-rules-and-lifetime.md)).
- Lifetime: how long a deployment may live in all, counted from its
  creation. An extension past it needs approval.
- Reminders before expiry (for example 7 days and 1 day).
- Self-service extension within limits, or extension through approval.
- Soft: a longer lease, or an extension beyond the limits or the
  lifetime, warns instead.
- Expiry action: power off → grace period → delete (configurable), set
  when the deployment is requested.

### Day-2 entitlement
- Which roles can run which actions, on which resource types, under which
  conditions (for example "members can snapshot but not delete prod VMs").
- Soft: a deny or an approval warns instead, with its reason.

### Quota
- vCPU, RAM, storage and VM count, at five levels
  ([ADR-0047](adr/0047-user-and-global-quotas.md)): the project, the
  project in one environment, each member of the project, all of rvav,
  and each user across rvav. A request must fit every level.
- A request made on someone's behalf counts for them.
- Hard (block) or soft (warn, or require approval).

### Deployment limits
- Deployments per user and per project, and vCPU and memory per
  deployment, for every project or one, and every environment or one
  ([ADR-0047](adr/0047-user-and-global-quotas.md)).
- Checked on a request, on an update or a resize that grows the
  deployment, on a move to another project, on a handover, and when an
  approver changes a request's inputs.
- Hard (block) or soft (warn).

### Placement
- Project → allowed compute, storage and network targets: clusters, with a
  resource pool and host patterns; datastores or a datastore cluster;
  networks ([ADR-0036](adr/0036-placement-targets.md)).
- Strategy, per placement rule, for choosing among the clusters left: most
  free memory (the default), binpack (the fullest cluster that still has
  room), or spread (the fewest of rvav's VMs). Constraint tags narrow the
  targets first (for example `rvav-env/prod`, `rvav-tier/gold:soft`; see VM
  blueprints).
- The form can let the user choose among allowed targets, or hide them entirely.

### Naming
- Template such as `{project}-{environment}-{inputs.role}{seq:03}`
  (`{role}` is short for `{inputs.role}`), offered by item, project or
  environment.
- Counter: one per name prefix (`web-prd-app`, `web-prd-db`…) or one for the
  whole policy, with where it starts and how much it moves (**Start at** 100
  and **Increment** 10 give 100, 110, 120). Uniqueness checked in vCenter and DNS.
- The most specific policy wins over the blueprint's `naming`; where none
  applies, the blueprint's names the VMs. A request for VMs neither names
  is refused (422, `no_naming`), never left to fail.

## 6. Deployments

- List with filters (project, owner, status, item, lease expiring), saved views,
  bulk actions.
- Shared with all the project's members, or shown to their owners only
  (project admins, supervisors and auditors still see all of them).
- Detail page: summary, resources (VMs with live power state, IPs, DNS names;
  custom resources with their type and properties), topology diagram, inputs,
  **timeline** of all requests and day-2 runs, lease, audit.
- Edit the name and description, change owner or project (with quota,
  deployment limit and name checks).
- Onboarding: adopt existing vCenter VMs into a deployment.

## 7. Day-2 actions

### Native (vSphere)
Power on, off, shut down guest, reset, restart guest · reconfigure CPU and RAM
(hot-add aware) · add or extend disk · add or remove NIC · change network ·
snapshot create, revert, delete (with a retention policy) · web console (VMRC/HTML
console ticket) · rename (vCenter + DNS) · mount or unmount ISO · edit the
deployment's name and description · change owner or project · extend lease · delete (VM + IPAM/DNS release; refused while the
blueprint's `prevent_delete` keeps a VM) · allow deletion (administrators
only).

### AWX-backed custom actions
- Any job template can become a day-2 action bound to resource types and
  conditions, for example "Install monitoring agent" (Linux VMs only) or
  "Patch now".
- Form generated from the survey, unless an administrator designs one in
  the form designer: layout, value sources and rules, with defaults read
  from the target VM or deployment
  ([ADR-0050](adr/0050-designed-day2-forms.md)). The target VM is passed
  as `limit` plus `extra_vars.rvav` context (deployment, resource,
  project, requester).

### On custom resources
A resource type lists the day-2 actions offered on its resources
([ADR-0056](adr/0056-custom-resource-types.md)): custom workflows
(`wf.<key>`) and AWX actions (`awx.<template>`). An action offered on VMs
or deployments is offered on the type's resources too; a custom workflow
registered as a day-2 action but not offered runs only on them.
- A workflow gets the resource in its context's `resource`; an AWX job in
  `extra_vars.rvav.resource`, on its template's own inventory, with no
  limit.
- When the type has a read step, it runs after the action, and what it
  returns replaces the resource's properties.
- Deleting the deployment runs each resource's type's delete step, before
  its VMs are deleted.

### Common to all
- Confirmation dialogs with a clear preview of what will change.
- Optional approval through the approval policy.
- Bulk execution across selected resources.
- Each run has a live timeline and goes into the deployment history.

## 8. Extensibility (event subscriptions)

| Topic | When |
|---|---|
| `request.submitted` | A request is accepted |
| `approval.requested`, `approval.decided` | Each approval level of a request, asked (and escalated) and decided |
| `provisioning.pre`, `.post` | Around provisioning |
| `compute.allocation.pre`, `network.configure` | Each VM, placed and named, then its network configured, before anything is built |
| `compute.provision.pre`, `.post` | Around each VM's cloning |
| `custom.create.pre`, `.post` | Around each custom resource's creation ([ADR-0056](adr/0056-custom-resource-types.md)) |
| `day2.pre`, `.post` | Around each day-2 action |
| `delete.pre`, `.post` | Around deletion |
| `custom.delete.pre`, `.post` | Around each custom resource's deletion: by a deletion, an update or a rollback |
| `compute.removal.pre`, `.post` | Around each VM's deletion |
| `lease.expiring`, `lease.expired` | Lease lifecycle |
| `project.created`, `project.updated` | A project is created or its settings change |

[ADR-0021](adr/0021-event-subscriptions.md) and
[ADR-0049](adr/0049-event-topics.md) give each topic's event. Targets: an
AWX job template, a webhook (HMAC-signed) or a custom workflow.
**Blocking** subscriptions, on `provisioning.pre`, `compute.allocation.pre`,
`network.configure`, `day2.pre` and `delete.pre`, can stop the flow or modify
allowed properties (for example a CMDB check that returns a cost center). On
`compute.allocation.pre` and `network.configure` they can also rename, place
or address the VM, checked again before it is built, and a recovery target
is told when one fails ([ADR-0054](adr/0054-subscriptions-that-change-the-request.md)).
Non-blocking ones run asynchronously.

## 9. Notifications

Email, Slack and Microsoft Teams: request submitted, approval needed, approved or
rejected, succeeded or failed, lease expiring or expired. Users choose their
channels in preferences.

## 10. Reporting

Dashboards for requests over time, success rate, mean provisioning time, top
items, resource usage per project versus quota, upcoming lease expiries,
and approval lead time.

> Cost estimation and showback are out of scope for now (see the
> [roadmap](roadmap.md#out-of-scope-for-now)).
