# Moving from vRA

How vRA 8 / Aria Automation's content and deployments move to rvav
([ADR-0027](../adr/0027-vra-importer.md)). The importer only reads vRA.

## The importer

Administration › **vRA import** is where it happens, for administrators:

1. **Read vRA** (rvav reads the vRA it is configured with), or **Upload
   file…** (what `rvav vra-export` wrote, where vRA is reachable). What was
   read is kept, and everything below works from the latest.
2. **The dry run.** The page shows what an import would do, and writes
   nothing:
   - **Catalog items:** for each cloud template, how much of it maps,
     what the import would do (create, new draft, unchanged, conflict,
     not imported), who it would be shared with, and the notes;
   - **Deployments:** which would be adopted, and why the others stay in
     vRA;
   - **Policies:** what each becomes in rvav, with the values to set;
   - **vRO:** the triage report, also as Markdown.
3. **Projects.** Each vRA project maps to the rvav project of the same
   name. Choose another where names differ, or **Stays in vRA** to keep
   it there even if rvav has a project of its name. A vRA project without
   an rvav project stays in vRA: its items aren't shared there, its
   deployments aren't adopted.
4. **Import…** the catalog, the deployments, or both.
5. **Cutover:** project by project, what is ready, the mark that a
   project moved, and what the next read of vRA says about it. The
   procedure is [vra-cutover.md](vra-cutover.md).

The import can run again, as often as needed. Each template is linked
to the item it became:
- **Unchanged:** what the template maps to hasn't changed. Nothing
  happens.
- **New draft:** vRA released a version, or the mapping changed. The
  item gets a new draft; its published version stays as it is.
- **Conflict:** the item already has a draft. Publish or discard it,
  then import again. An rvav item not made from this template has the
  slug: rename one of them.
- **Sharing** is only ever added, never removed. Each share the import
  made is recorded: one an administrator takes away later isn't made
  again, and the plan says so.
- **Twice the same name:** two templates of the same name get the
  project's name in their slug.
- **A new draft** is mapped from vRA's template again: what was changed
  in rvav since is in the published version, not in it. The plan warns
  to compare them.
- **Deployments:** a deployment rvav manages already isn't adopted again,
  whether the import adopted it or someone did from Onboarding.
- **One import at a time:** a second one started meanwhile is refused.
  An import finishes even if the page is closed.

Policies aren't imported: they decide who may do what, so an
administrator sets each from what the plan says.

**Import…** adopts every project's deployments; the Cutover tab adopts
one project's (`projects` in the API).

The same through the API: `GET /api/v1/admin/vra/plan`,
`POST /api/v1/admin/vra/snapshots`,
`PUT /api/v1/admin/vra/projects/{vraProjectId}`,
`POST /api/v1/admin/vra/import`, `GET /api/v1/admin/vra/triage`.

## Reading vRA

Give rvav a vRA account that can read every project, by a refresh token
(an API token) or a user and password in a domain
([configuration.md](configuration.md#vra-the-importer)).

```sh
rvav vra-export -o estate.json   # everything the importer reads, as JSON
rvav vra-plan -f estate.json     # what it would make of it, and why (JSON)
rvav vra-plan                    # the same, reading vRA directly
```

`vra-export` can run where vRA is reachable. Its file is then all the
importer needs.

The page takes files of up to 64 MB (some 100,000 deployments). The API
needs about four times the file's size in memory while it reads it. With
ingress-nginx, the chart sets the body size and timeouts the upload and
a long read of vRA need; another ingress controller needs the same
([installation.md](installation.md)).

## What becomes what

Each cloud template's plan has a status:

| Status | Meaning |
|---|---|
| **complete** | Everything in the template has an rvav equivalent. |
| **partial** | It maps, but parts are left out: the notes marked *unmapped* say which, and what to do instead. |
| **skipped** | Nothing to import (no released version, or no vSphere machine). |

Each note is *info* (how something was mapped), *warning* (mapped, but
check it) or *unmapped* (left out).

### Cloud templates

| vRA | rvav |
|---|---|
| Released versions | The catalog item's versions, imported as drafts (`1` becomes `1.0`). Unreleased versions are left out. |
| `inputs` (string, integer, number, boolean; `enum`, `oneOf`, `default`, `pattern`, `min`/`max`, `title`, `description`, `format: date`, `encrypted`) | The form: fields, choices, defaults, checks; `encrypted` becomes a secret field. An input without a default is required. |
| An input named `environment` or `env` | rvav's own request environment, not a field. |
| `Cloud.vSphere.Machine` (or `Cloud.Machine`) | The VM. Several machines, or a `count`, make a multi-VM item with a component each (ADR-0024). |
| `image` | The vCenter template of vRA's image profile. An image from an input isn't mapped. |
| `flavor` (fixed or from an input), `cpuCount`, `totalMemoryMB` | vCPU and memory: the flavor profile's sizes, as the item's sizes. |
| `customizationSpec` | The guest's vCenter customization specification. |
| `cloudConfig` | cloud-init user data. `${self.resourceName}`, `${self.address}` and `${input.x}` are translated; others are reported. |
| `Cloud.vSphere.Disk` attached by `attachedDisks` | An added disk of its `capacityGb`. |
| `Cloud.vSphere.Network` (existing) | The item asks for cluster, datastore and network, within rvav's placement rules. |
| Constraint tags | Reported: write rvav placement rules for them. |
| The project's machine naming template | The item's naming, when every part of it translates. |
| `Cloud.NSX.Network` or `Cloud.Network`, `networkType` routed, outbound or private | An NSX segment made for each deployment (`segments`; the VMs' first adapter on it). A placement rule says where: its Tier-1 and EfficientIP block ([ADR-0028](../adr/0028-nsx-networking.md)). A private network gets the rule's Tier-1 too. When every VM is on one, the form asks for no network. |
| `Cloud.SecurityGroup`, new | A security group made for each deployment (`security_groups`), joined by the machines whose networks name it. Its inbound rules from any or another of its groups, on a service or a usual port (22, 80, 443, 1433, 3306, 3389, 5432, ICMP), carry over; outbound rules, and those from addresses or other ports, are reported. |
| `Cloud.SecurityGroup`, existing | Reported: rvav can't tell its NSX group from vRA's constraint tags. Add it to `groups` as `nsx:<group ID>`. |
| Load balancers, other types | Reported, left out. An AWX job in a composite item can do what they did. |

### Custom forms

| vRA form | rvav |
|---|---|
| Labels, placeholders, signposts (help), defaults | The fields'. |
| Static value lists | Choices. |
| `textField`, `textArea`, `dropDown`, `radio`, `checkbox`, `integerField`, `passwordField`, `datetime`, `multiSelect` | text, textarea, select, radio cards, toggle, number, secret, datetime, multiselect. |
| Pages and sections | The layout (a wizard when there are several pages). |
| Required, pattern, lengths, minimum and maximum | The form's checks. |
| Values from vRO actions or external sources | Reported: give the field an rvav value source (REST or CEL). |
| Conditions on visibility or read-only | Reported: write an rvav rule (CEL). |

## Deployments

vRA's deployments are adopted, not rebuilt. Nothing changes in vCenter or
EfficientIP:

```sh
rvav vra-adopt -f estate.json -project vra-prj-data=prj_data   # the plan (JSON)
rvav vra-adopt -f estate.json -project vra-prj-data=prj_data -apply
```

Each deployment becomes one rvav deployment of the same name, through
onboarding ([ADR-0026](../adr/0026-onboarding.md)):

| vRA | rvav |
|---|---|
| Its vSphere machines (`resourceName`) | The VMs of that name in the vCenters rvav reads. A name two vCenters both have isn't adopted. |
| Its project | The rvav project it is mapped to (`-project`), or the one of the same name. |
| Its `environment` (or `env`) input | Its environment, which must be one of the project's. |
| Its owner (or who created it) | The rvav user of that email, who must be a member of the project (not a viewer). |
| The catalog item it was requested from | The item imported from that cloud template. Its published version is the deployment's. |
| Its name | The same, made valid for rvav: lower case, other characters as dashes ("Web Server for Thomas" is web-server-for-thomas). |
| The item | Must be published in rvav and offered to the project: the plan says when it isn't. |
| `leaseExpireAt` | What remains of the lease, at least a day, at most what rvav's lease policy allows. A lease already ended gives a day to decide. No lease in vRA: the policy's default, with a warning, since it ends in a deletion. |
| Its on-demand NSX network, its new security groups | Adopted with it when rvav has an NSX Manager: found in NSX by name, tagged as rvav's and the deployment's, and deleted with it ([ADR-0028](../adr/0028-nsx-networking.md)). A group's security policies come too when their rules name only the deployment's groups; a group another policy uses is left as it is, and reported. The segment's subnet stays in EfficientIP; the VMs stay in their groups as vRA put them. |
| Existing security groups, load balancers, other resources | Stay as they are; reported. |

Each plan is **adopt**, **adopted** (rvav manages its VMs already),
**skipped** (with why: VMs not found, a project, environment, owner or item
missing) or **failed** (onboarding refused it, with why).

Running it again adopts only what isn't yet, so it can run after each fix:
- a project mapped;
- an owner who has signed in to rvav;
- an item imported and published.

## vRO

vRO's workflows and actions aren't imported. They are sorted, for people
to decide, into the groups of
[ADR-0007](../adr/0007-extensibility-model.md#migrating-vro-workflows):

```sh
rvav vro-triage -f estate.json -md > vro-triage.md   # a table per group
rvav vro-triage -f estate.json                        # the same, as JSON
```

For each workflow and action, the report shows:
- its inputs;
- what in vRA runs it: catalog items, event subscriptions (with rvav's
  equivalent event), custom day-2 actions, form fields, other workflows
  and actions;
- how often and when it last ran (workflows);
- the systems it reaches, from its inputs' types and its scripts;
- a suggested group and why.

vRO's own library (`Library/...`, `com.vmware.*`) is left out. ABX
actions vRA runs are listed, but their code isn't read.

| Suggested group | When |
|---|---|
| **A. Native** | A workflow that only drives vCenter: rvav's built-in steps, or a composite item. An action that fills a form field: a value source on the field (REST when it calls REST, CEL otherwise). |
| **B. AWX** | A workflow that calls other systems (Active Directory, REST, SQL, PowerShell, SSH, mail, NSX...): a playbook, with the Ansible collections the report names. ABX actions. |
| **C. Custom Temporal workflow** | A workflow that waits for people (user interactions) or for timers and events. |
| **D. Retire** | Nothing in vRA runs it (or only disabled subscriptions), and it hasn't run for six months or is a copy of another that never ran. An action nothing calls. |

An action that only workflows call goes with them, in their group.
A workflow keeps its group however old its last run, as long as something
runs it; the report says when it last ran.

The suggestion reads what the content shows: a script that reaches a
system through a vRO object the report doesn't know is a guess away from
the right group. Check each B and C, and every A whose reason says it
reaches no system.
