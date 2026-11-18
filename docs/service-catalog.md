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

### VM blueprints
A `vm_blueprint` item's blueprint ([ADR-0024](adr/0024-multi-vm-blueprints.md)).
Strings in `bindings` and `count`, a disk's `size_gb` and `datastore` and
an adapter's `network` are CEL over `inputs`, `sizes` and `project` (a
literal is quoted: `'"vsan-01"'`); numbers are themselves. `project` has
`id`, `code`, `name` and `properties`, the project's custom properties.
The other fields are plain values.

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
| `guest` | `os_family` (`linux`, `windows`), `domain`, `time_zone`; `customization_spec` (a vCenter specification as the base); Windows: `admin_password` (a secret), `domain_join: {domain, ou, credential}` (a basic secret); Linux, or Windows on a template with cloudbase-init: `cloud_init: {user_data}` (a Go template over `.vm` (`name`, `fqdn`, `ip`, `ip6`, `ips`, `index`), `.inputs`, `.project`, `.environment`, `.request`, `.components`; inputs printed through `json`: `{{ .inputs.motd \| json }}`; a stored secret as `{{ secret "name" \| json }}`, its name in quotes, opened only when the VM is built; saving refuses a secret that doesn't exist, with its line; on Linux, user data holding a stored secret or a secret input has cloud-init clear it from the VM's guestinfo once read); `remote_access` (the user people sign in as: see **Remote access** below) |
| `sizes` | Size presets for bindings (`sizes[inputs.size].vcpu`); the offered ones when absent |
| `bindings` | `vcpu`, `memory_gb`, `disk_gb` (the first disk), `cluster`, `resource_pool` (a pool's path below the cluster's root pool, such as `rvav/dev`), `host`, `datastore`, `datastore_cluster`, `network` (the first adapter), `template`, `post_config` (keys of `awx_templates`), `tags` (a list; it can read `self` and `components`, below), `groups` (a list of NSX groups, added to `groups`: from an `nsx.groups` field, whose values are checked against its list) |
| `disks` | Disks added after the template's: `size_gb`, `datastore` (the VM's when absent), `storage_policy`, `constraints` (the disk's own storage constraints, below), `provisioning` (over the blueprint's) |
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
| `folder` | The VMs' folder instead of the placement rule's: a path from the root (`/GVA/vm/shop`), or below the VM folder of the rule folder's datacenter (`shop/${inputs.app}` for a rule folder `/GVA/vm/rvav/web/dev` gives `/GVA/vm/shop/...`). Missing folders are created. No `..`, and no secret input: saving refuses a folder that reads one, the form's or a property group's |
| `prevent_delete` | `true` keeps the VMs from being deleted: the deployment's **Delete** is refused and names them, and the end of its lease doesn't delete it: rvav tells the owner once, and cancels nothing waiting on it. An administrator (anyone holding `project.update` in the project) runs the deployment's **Allow deletion** first; project admins and owners can't |
| `property_groups` | Property groups the item uses, each at a version: `[{key: network, version: 2}]` ([ADR-0038](adr/0038-names-groups-secrets-properties.md)). Their inputs join the request form, each group's in a section of its own, and go where the item's own go: AWX jobs, the events' `inputs`, and the AWX inventory when the input is exposed to it. Bindings, properties, folders, form rules and composite steps read their constants as `propgroup.<key>.<name>`. The item keeps the version it names when the group is saved again. An input both in the form and in a group, or in two groups, is refused |
| `awx_templates` | Post-configuration AWX templates, by key |
| `rollback_on_failure` | Delete what was built when a step fails |
| `components` | Groups of VMs, built as one deployment: `key`, `title`, `count` (0 to 20, a number or CEL), `anti_affinity`, `depends_on` (keys of the components it is built after), and any of `naming`, `template`, `library`, `guest`, `bindings`, `disks`, `nics`, `ipam`, `ipv6`, `tags`, `storage_policy`, `provisioning`, `linked_clone`, `groups`, `constraints`, `folder`, `prevent_delete`, which win over the blueprint's (guest field by field, bindings key by key, constraints kind by kind), and `properties`, added to the blueprint's key by key |
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
text, textarea, number, slider, toggle, dropdown, multi-select, dual list,
search (type-ahead), radio cards, date/time, tags, key-value, datagrid (rows of sub-fields), group
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
source's `options` expression reads it as `search`. For any other source,
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
  inputs it depends on change. A search field passes its text to the
  source (`${search}`, `search`) or keeps the options that contain it.
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
project · extend lease · delete (VM + IPAM/DNS release; refused while the
blueprint's `prevent_delete` keeps a VM) · allow deletion (administrators
only).

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
