# Service catalog specification

The goal is parity with vRA Service Broker, plus the UX it never delivered.

## 1. Content sources and item types

| Type | Source | What it does |
|---|---|---|
| `vm_blueprint` | Authored in the app | Provisions one or more VMs natively, with optional AWX post-config steps |
| `awx_job_template` | Imported from AWX | Launches a job template with inputs from the form |
| `awx_workflow` | Imported from AWX | Launches a workflow job template |
| `composite` | Authored in the app | Ordered or parallel steps combining other items (VM, AWX, custom workflow), AWX templates, custom workflows, REST calls and waits for a person, a time or a condition, inputs bound to earlier steps' outputs ([ADR-0022](adr/0022-custom-workflows-and-composites.md), [ADR-0033](adr/0033-composite-step-kinds.md)) |
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
`status`, `json` and `met`, a branch's `case`, a message's `told`.

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

### VM blueprints
A `vm_blueprint` item's blueprint ([ADR-0024](adr/0024-multi-vm-blueprints.md)).
Strings in `bindings` and `count`, a disk's `size_gb` and `datastore` and
an adapter's `network` are CEL over `inputs` and `sizes` (a literal is
quoted: `'"vsan-01"'`); numbers are themselves. The other fields are plain
values.

CEL in rvav, here as in form rules, policies, composites and
subscriptions, has CEL's string functions: `lowerAscii()`, `upperAscii()`,
`trim()`, `replace(old, new)`, `split(sep)`, a list's `join(sep)`,
`substring(start, end)`, `indexOf(s)`, `lastIndexOf(s)`, `charAt(i)`,
`reverse()`, `'%s-%d'.format([a, b])` and `strings.quote(s)`. For example,
`inputs.app.trim().lowerAscii().replace('_', '-')`.

| Field | What it is |
|---|---|
| `naming` | Naming template of the VMs (a naming policy wins over it): `{project}`, `{environment}`, `{component}`, `{inputs.<name>}`, `{bindings.<key>}` (a binding of the VMs it names, such as `inputs.app.lowerAscii()`), ending with `{seq}` or `{seq:03}` |
| `template`, `library` | The vCenter template; with `library`, the item of that content library. The `template` binding gives the key of a template of **Templates and networks** instead (the `rvav.templates` picker): see **Templates per vCenter** below |
| `guest` | `os_family` (`linux`, `windows`), `domain`, `time_zone`; `customization_spec` (a vCenter specification as the base); Windows: `admin_password` (a secret), `domain_join: {domain, ou, credential}` (a basic secret); Linux: `cloud_init: {user_data}` (a Go template over `.vm` (`name`, `fqdn`, `ip`, `ips`, `index`), `.inputs`, `.project`, `.environment`, `.request`, `.components`; inputs printed through `json`: `{{ .inputs.motd \| json }}`) |
| `sizes` | Size presets for bindings (`sizes[inputs.size].vcpu`); the offered ones when absent |
| `bindings` | `vcpu`, `memory_gb`, `disk_gb` (the first disk), `cluster`, `resource_pool` (a pool's path below the cluster's root pool, such as `rvav/dev`), `host`, `datastore`, `datastore_cluster`, `network` (the first adapter), `template`, `post_config` (keys of `awx_templates`), `tags` (a list; it can read `self` and `components`, below), `groups` (a list of NSX groups, added to `groups`: from an `nsx.groups` field, whose values are checked against its list) |
| `disks` | Disks added after the template's: `size_gb`, `datastore` (the VM's when absent), `storage_policy`, `constraints` (the disk's own storage constraints, below), `provisioning` (over the blueprint's) |
| `nics` | Adapters added after the template's: `key` (in DNS names: `<name>-<key>`), `network` (or `segments.<key>`, a segment made for the deployment, never with DHCP), `ipam` (`efficientip`, the default, or `dhcp`) |
| `segments` | NSX segments made for each deployment ([ADR-0028](adr/0028-nsx-networking.md)), up to four: `key`, `title`. The `network` binding, or an added adapter's `network`, names one as `segments.<key>`: the VMs are on it, their subnet carved when the request runs. In a composite, a VM step's segments are made under its step, before its VMs |
| `security_groups` | NSX groups made for each deployment ([ADR-0028](adr/0028-nsx-networking.md)), up to eight: `key`, `title`, and the firewall `rules` into each: `from` (`any`, another group's key, or `nsx:<group ID>`), `services` (NSX service IDs, or `any`), `action` (`allow`, the default, `drop`, `reject`). In a composite, a VM step's groups and rules are made under its step, before its VMs |
| `groups` | The NSX groups the VMs are in: keys of `security_groups`, or existing groups that select VMs by tag, as `nsx:<group ID>`; a component may name its own |
| `tags` | vSphere tags, `category/tag` |
| `constraints` | The vSphere tags the clusters (`compute`), datastores (`storage`) and network (`network`) of the VMs must carry, or must not; below |
| `storage_policy` | The VMs' storage policy (their home and disks without their own), over the placement rule's |
| `provisioning` | How the VMs' disks are provisioned, the template's included ([ADR-0036](adr/0036-placement-targets.md)): `thin`, `thick_lazy_zeroed` or `thick_eager_zeroed`. Without it, the template's disks stay as they are and added disks are provisioned as the template's first disk |
| `linked_clone` | `true` to clone the VMs from the template's current snapshot as linked clones, `false` for full clones ([ADR-0037](adr/0037-images-per-vcenter.md)). Without it, as the template of **Templates and networks** the `template` binding chose says, else full clones. Not with `library` |
| `awx_templates` | Post-configuration AWX templates, by key |
| `rollback_on_failure` | Delete what was built when a step fails |
| `components` | Groups of VMs, built as one deployment: `key`, `title`, `count` (0 to 20, a number or CEL), `anti_affinity`, `depends_on` (keys of the components it is built after), and any of `naming`, `template`, `library`, `guest`, `bindings`, `disks`, `nics`, `tags`, `storage_policy`, `provisioning`, `linked_clone`, `groups`, `constraints`, which win over the blueprint's (guest field by field, bindings key by key, constraints kind by kind) |
| `deployment_name` | With components, the deployment's naming template, without `{seq}`; else the item's name and the request number |

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
- **Entitlements**: items or categories shared with projects, groups or users. Users
  only see what they can request.
- **Versioning**: draft, published and deprecated states; preview the draft as a
  user; compare versions; roll back; existing deployments keep their version.
- Import and export of catalog items as YAML (GitOps-friendly), and the
  catalog kept in a Git repository (C1, ADR-0029).

**Built (S8, [ADR-0019](adr/0019-catalog-administration.md)):** catalog
admins and administrators create items, open them to every project (those
made later too) or to chosen projects, and manage categories and the icon
(from rvav's own set, no upload). Versions are draft, published or
deprecated: one draft and one published version per item at most;
publishing a deprecated version again rolls back; withdrawing the published
version takes the item out of the catalog; deployments and requests keep
their version. Two versions compare change by change. Items export to YAML
(one version or all) and import as a new item or a new draft, never
published by the import, with problems on their YAML line. The catalog can
be kept in a Git repository (ADR-0029): rvav commits what it publishes,
and what changes in the repository becomes a draft. Not built yet:
entitlements to groups or users rather than projects.

## 3. Custom forms

A form is a **JSON Schema** (data contract) plus a **UI schema** (presentation and
behaviour). The designer edits both visually; a code view shows the YAML.

### Field types
text, textarea, number, slider, toggle, dropdown, multi-select, radio cards,
date/time, tags, key-value, datagrid (rows of sub-fields), secret, file upload,
Markdown or info block, and resource pickers:
- vSphere: cluster, datastore, network, folder, template, existing VM
- EfficientIP: subnet, free IP, DNS zone
- AWX: inventory, credential, host
- App: project, user, group, deployment

### Behaviour
- **Dynamic values**: options, defaults or validation results from a *value source*:
  - built-in sources (vSphere inventory cache, EfficientIP, AWX, app data),
  - REST source (URL template, auth, JSONPath mapping, caching),
  - CEL expression over other fields.
- **Cascading dependencies**: a field declares `dependsOn` and re-resolves when
  those fields change (cluster → datastores → networks → subnets).
- **Conditional rules** (CEL): visible, required, read-only, value.
- **Validation**: JSON Schema constraints, regex, CEL cross-field rules, async
  server-side validation (for example "hostname free in DNS").
- **Layout**: sections, columns, tabs, multi-step wizard with a review step.
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
  5 s and 1 MB at most) and `cel` (options or a value over the inputs,
  project, user and environment). Dependencies re-resolve a field when the
  inputs it depends on change.
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
- For AWX items, the form can choose the launch's inventory and credentials
  (inputs picked from `awx.inventories` and `awx.credentials`), which the
  template must prompt for. A composite's step running such an item
  launches with what its bindings give those inputs, not as extra
  variables: bind them to the composite's own `awx.inventories` and
  `awx.credentials` pickers, so a requester's choice is checked as the
  item's form checks it.

Not built yet: tabs and wizard steps (forms render as sections), file upload, async
server-side validation rules, and the group and AWX host pickers.

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

### Approval
- Multi-level (sequential), with any-of or all-of approvers per level.
- Approvers: users, groups, the project admin, the requester's manager (from an
  OIDC claim).
- Conditions, for example `inputs.memoryGB > 32 || inputs.environment == "prod"`.
- Auto-approve rules, delegation (out of office), timeout → auto-approve,
  auto-reject or escalate.
- Approve or reject from the web inbox, email links, or Slack/Teams buttons.
- Approvers can edit allowed inputs before approving (for example reduce RAM),
  and this is audited.

### Lease
- Default and maximum lease; "never expires" only where the policy allows it.
- Reminders before expiry (for example 7 days and 1 day).
- Self-service extension within limits, or extension through approval.
- Expiry action: power off → grace period → delete (configurable).

### Day-2 entitlement
- Which roles can run which actions, on which resource types, under which
  conditions (for example "members can snapshot but not delete prod VMs").

### Quota
- Per project and optionally per environment: vCPU, RAM, storage, VM count.
- Hard (block) or soft (warn, or require approval).

### Placement
- Project → allowed compute, storage and network targets.
- Strategy: first fit, most free capacity, or round robin; constraint tags
  (for example `rvav-env/prod`, `rvav-tier/gold:soft`; see VM blueprints).
- The form can let the user choose among allowed targets, or hide them entirely.

### Naming
- Template such as `{project.code}-{inputs.env}-{inputs.role}{seq:03}`.
- Counter scope (global, project, template), uniqueness checked in vCenter and DNS.

## 6. Deployments

- List with filters (project, owner, status, item, lease expiring), saved views,
  bulk actions.
- Detail page: summary, resources (VMs with live power state, IPs, DNS names),
  topology diagram, inputs, **timeline** of all requests and day-2 runs, lease,
  audit.
- Change owner or project (with quota checks).
- Onboarding: adopt existing vCenter VMs into a deployment.

## 7. Day-2 actions

### Native (vSphere)
Power on, off, shut down guest, reset, restart guest · reconfigure CPU and RAM
(hot-add aware) · add or extend disk · add or remove NIC · change network ·
snapshot create, revert, delete (with a retention policy) · web console (VMRC/HTML
console ticket) · rename (vCenter + DNS) · mount or unmount ISO · change owner or
project · extend lease · delete (VM + IPAM/DNS release).

### AWX-backed custom actions
- Any job template can become a day-2 action bound to resource types and
  conditions, for example "Install monitoring agent" (Linux VMs only) or
  "Patch now".
- Form generated from the survey; the target VM is passed as `limit` plus
  `extra_vars.rvav` context (deployment, resource, project, requester).

### Common to all
- Confirmation dialogs with a clear preview of what will change.
- Optional approval through the approval policy.
- Bulk execution across selected resources.
- Each run has a live timeline and goes into the deployment history.

## 8. Extensibility (event subscriptions)

| Topic | When |
|---|---|
| `request.submitted`, `request.approved`, `request.rejected` | Request lifecycle |
| `deployment.provision.pre`, `.post` | Around provisioning |
| `resource.day2.pre`, `.post` | Around each day-2 action |
| `deployment.delete.pre`, `.post` | Around deletion |
| `lease.expiring`, `lease.expired` | Lease lifecycle |

Targets: an AWX job template or a webhook (HMAC-signed). **Blocking**
subscriptions can stop the flow or modify allowed properties (for example a
CMDB check that returns a cost center). Non-blocking ones run asynchronously.

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
