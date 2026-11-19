# Moving from vRA

How vRA 8 / Aria Automation's content and deployments move to rvav
([ADR-0027](../adr/0027-vra-importer.md)). The importer only reads vRA.

## The importer

Administration › **vRA import** is where it happens, for administrators.
Its **Steps** say where the move stands (read vRA, map the projects,
import, move each project) and mark the one to do now. The plan opens on
that step's tab, and each tab is in the page's address, for example
`/admin/vra-import?tab=cutover`.

1. **Read vRA** (rvav reads the vRA it is configured with), or **Upload
   file…** (what `rvav vra-export` wrote, where vRA is reachable). What was
   read is kept, and everything below works from the latest.
2. **The dry run.** The page shows what an import would do, and writes
   nothing:
   - **Catalog items:** for each cloud template, how much of it maps,
     what the import would do (create, new draft, unchanged, conflict,
     not imported), who it would be shared with, and the notes, which
     open in a side panel;
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
  again, and the plan says so. A project the item is already shared
  with keeps its sharing, even when vRA's changed.
- **Content sharing policies** (vRA 8.x) share an item with chosen
  people in a project: the import shares it with them there, not with
  all the project's members. A content sharing policy without a project
  applies in each project. A project an entitlement or a `PROJECT`
  principal shares the item with gets it for all its members. When none
  of the people a policy names maps, the item isn't shared there, and
  the plan says so:

  | vRA principal | rvav's |
  |---|---|
  | `USER` | The user with that email. Someone who hasn't signed in to rvav yet is reported: share with them later in **Catalog items › Sharing**. |
  | `GROUP` | The OIDC group, by the name vRA gives. Check that the identity provider's token names it the same. |
  | `ROLE` | `administrator`, `member`, `viewer` and `supervisor` (or their `project_` forms) are `project-admin`, `member`, `viewer` and `supervisor`. Another role is reported. |
- **Twice the same name:** two templates of the same name get the
  project's name in their slug.
- **A new draft** is mapped from vRA's template again: what was changed
  in rvav since is in the published version, not in it. The plan warns
  to compare them.
- **Deployments:** a deployment rvav manages already isn't adopted again,
  whether the import adopted it or someone did from Onboarding.
- **One import at a time:** a second one started meanwhile is refused.
  An import finishes even if the page is closed.

Policies aren't imported, except content sharing: they decide who may
do what, so an administrator sets each from what the plan says. The
plan's content sharing policies are imported with the catalog items
they name.

An approval policy's approvers read as a level's
([ADR-0041](../adr/0041-approvers-by-role.md)):

| vRA approver | The level's |
|---|---|
| `GROUP:<email>` | OIDC group: give the group's name in the identity provider, not vRA's email. |
| `USER:<name>` | Users. |
| `ROLE:project_supervisor`, `ROLE:project_administrator`, `ROLE:project_member`, `ROLE:project_viewer` | Project roles: `supervisor`, `project-admin`, `member`, `viewer`. Another role is reported. |
| `MANAGER`, or the approval type `MANAGER` | The requester's manager: also set the identity provider's **Manager claim**. |

A day-2 policy's `ROLE:project_supervisor` authority becomes the
`supervisor` role.

vRA's **Update** of a deployment is rvav's **Update**
([ADR-0042](../adr/0042-deployment-updates.md)):

| vRA | rvav |
|---|---|
| The day-2 action `Deployment.Update`, in a day-2 policy or an approval policy | The action `update`, in a day-2 policy. An approval policy that covers only `Deployment.Update` becomes a day-2 policy with the effect approval on `update`. |
| An approval policy for `Deployment.Create` | An approval policy, which also applies to updates in rvav. When it doesn't cover `Deployment.Update`, the plan says to add `request.kind == "provision"` to its condition. |
| An update with new inputs, or to the cloud template's latest version | An update to the item's published version, with the new inputs. rvav shows the plan before you confirm, as vRA does. |
| A change vRA makes by deleting and creating a machine again, such as another image or network | Refused, with its reason: delete the deployment and request it again. |
| A cluster's `count` input changed | Scale out or in: new VMs are named and addressed as in a new request, and the VMs with the highest numbers are removed. |
| An update of a failed deployment | The same: it builds the VMs that are missing and runs the AWX jobs again. |

Placement rules aren't imported either: they name vCenter objects that
vRA's projects don't. The **Projects** tab gives each project's
**Placement strategy**, the one that places as vRA's `placementPolicy`
does ([ADR-0036](../adr/0036-placement-targets.md)). Set it on the rvav
project's placement rules:

| vRA `placementPolicy` | Placement rule strategy |
|---|---|
| `DEFAULT` | **Most free memory**, rvav's default. vRA takes zones by priority: rvav takes rules by priority. |
| `SPREAD` | **Spread**. vRA counts machines per zone, rvav counts its VMs per cluster. |
| `SPREAD_MEMORY` | **Most free memory**, the nearest: vRA takes the zone with the lowest memory use. |
| `BINPACK` (a cloud zone's) | **Binpack**. |

A vRA project's custom properties go to its rvav project when you import
the catalog ([ADR-0038](../adr/0038-names-groups-secrets-properties.md)):
each VM of the project gets them as vSphere custom attributes. The
**Custom properties** column of the **Projects** tab lists those the
rvav project lacks, which the import adds, and those it leaves out (a
key rvav refuses, or more than 64). A property the rvav project already
has keeps its rvav value.

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
| `inputs` (string, integer, number, boolean; `enum`, `oneOf`, `default`, `pattern`, `min`/`max`, `title`, `description`, `format: date`, `format: date-time`, `encrypted`) | The form: fields, choices, defaults, checks; `encrypted` becomes a secret field, without its default, which the requester types. An input without a default is required. |
| List inputs (`type: array`) | Text items are tags; text items with `enum` or `oneOf` a multi-select of those choices; object items a table, with a column per property: text, a number, a boolean, or text with choices (a select column). `minItems`, `maxItems`, `uniqueItems` and the rows' `required` are kept. Lists of numbers or booleans, and rows with a list or an object, are reported, and left out. |
| Object inputs (`type: object`) | A group of fields, with a sub-field per property: text, a number, a boolean, or text with choices (a select). The object's `required` is kept. An object without properties, or with a list or an object in it, is reported, and left out. |
| An input named `environment` or `env` | rvav's own request environment, not a field. |
| `Cloud.vSphere.Machine` (or `Cloud.Machine`) | The VM. Several machines, or a `count`, make a multi-VM item with a component each (ADR-0024), its key the machine's name in lower-case letters and digits (`web_server` is `webserver`). |
| `count` | The component's count: a number, or CEL over the inputs (`${input.ha ? 2 : 1}` is `inputs.ha ? 2 : 1`). A count that reads a machine is reported, and left out. |
| `dependsOn` | The component's `depends_on`, for the machines it names: it is built after them. rvav makes networks, disks and security groups before any VM; a dependency on a resource left out is reported. |
| `tags` | vSphere tags, the key as the category: fixed ones as the component's `tags`, those with expressions as its `tags` binding. The categories must exist in vCenter. A tag reading its own machines' addresses is reported. |
| `image` | When vRA maps the image to one vCenter template in every region, with no cloud-init or constraints of its own: that template, by name. Otherwise, the catalogue template the image becomes ([Image profiles](#image-profiles)), as the `template` binding. An image from an input isn't mapped. |
| `flavor` (fixed or from an input), `cpuCount`, `totalMemoryMB` | vCPU and memory: the flavor profile's sizes, as the item's sizes. |
| `customizationSpec` | The guest's vCenter customization specification. |
| `remoteAccess` | The guest's `remote_access`, the user people sign in as ([ADR-0039](../adr/0039-guest-access-and-networking.md)): `publicPrivateKey` is `ssh_key`, with the `sshKey` written in or `${input.x}` as `${inputs.x}`; `generatedPublicPrivateKey` is `generated_key`, a key pair rvav makes for each deployment; `usernamePassword` is `password`, read from an `encrypted` input. A password written in the template or read from a plain input, a user name from an input, `root`, another `authentication` (such as `keyPairName`), and remote access with a customization specification (other than Windows' `Administrator` with a password) are reported, and left out: store the password as a secret and name it in `remote_access.secret`. On Windows, a key or another user than `Administrator` needs a template with cloudbase-init (a warning). |
| `cloudConfig` | cloud-init user data. `${self.resourceName}`, `${self.address}`, `${count.index}`, `${input.x}` and other machines' names, addresses and count are translated; functions, operators and other references are reported. rvav prints an input only through `json`, which quotes it, so in a `#cloud-config` document a YAML value that holds an input becomes one action that prints the whole value: `path: /etc/${input.app}.conf` is `path: {{ printf "/etc/%v.conf" .inputs.app \| json }}`, and a block scalar (`\|` or `>`) becomes one quoted text. A value rvav can't rewrite (a plain value on several lines, a block with an indentation or `+` indicator, a tag) keeps the input printed through `json` in place, quotes included, and is reported: write that value as one action yourself. In a script (`#!`), inputs are printed through `json` in place. |
| `${secret.x}` in `cloudConfig` | `{{ secret "x" }}`: the stored secret, printed as it is. Its name becomes an rvav name (lower case, a dash for any other character: `Root_Password` is `root-password`). vRA never gives a secret's value, so the item waits (a conflict) until you store each secret it opens in rvav under that name (**Catalog items › Secrets**), then import again. A secret anywhere else, such as a property or a tag, is reported. |
| An input of a property group (`$ref: /ref/property-groups/<name>`) | The item names the rvav property group that vRA's becomes ([Property groups](#property-groups)), and the form gets the group's inputs in place of the input. `${input.<input>.<property>}` is `${inputs.<property>}`. A group that wasn't read is reported, and so are a group input of the same name as a form input (or `cluster`, `datastore`, `network`) or another group's, which rvav refuses to save, and two inputs of the same group (`primary` and `secondary`), which read the same answers in rvav. |
| `${propgroup.<group>.<property>}` | `${propgroup.<key>.<property>}`, and the item names the group. In `cloudConfig`, the constant's value is written in, with an info note: a later change to the group doesn't reach the item's cloud-init. |
| `preventDelete: true` on a machine | The component's `prevent_delete`: its VMs can't be deleted until an administrator runs the deployment's **Allow deletion**. |
| Other resource flags (`ignoreChanges`, `createBeforeDelete`, `forceRecreate`, `allocatePerInstance`, `dependsOnPrevious`...) | Reported, left out. Timeouts (`createTimeout`...) are left out with an info note: rvav's own apply. |
| `folderName` | The component's `folder`, below the VM folder of the placement rule folder's datacenter. `${input.x}` is `${inputs.x}`; a folder that reads a machine is reported. |
| A machine's `name` or `hostName` | Reported, left out: the item's naming names the VMs, its `{seq}` numbering them across deployments, where `${count.index}` would repeat ([ADR-0024](../adr/0024-multi-vm-blueprints.md)). |
| The vSphere machine schema's settings rvav has no equivalent of (`description`, `imageRef`, `flavorRef`, `coreCount`, `customizeGuestOs`, `cloneFromSnapshot`, `snapshotLimit`, `ovfProperties`, CPU and memory limits, reservations and shares...) | Reported, left out. |
| A machine's other properties (not in vRA's vSphere machine schema) | Custom properties: the component's `properties`, which each VM gets as vSphere custom attributes. Text and numbers carry over, with `${input.x}` as `${inputs.x}`. Lists, maps, a key rvav refuses, and a value that reads a machine are reported. Check the list in the notes: a property that was a vSphere setting rvav doesn't have would become an attribute. |
| Expressions in `count` and `tags` | CEL. `${input.x}` is `inputs.x`, `${count.index}` is `self.index`, `${self.address}` is `self.address`. Another machine's `address` (or `networks[0].address`), `resourceName` (or `name`) and `count`: `${resource.db.address}` is `components.db.addresses[0]`, or the list `components.db.addresses` for a machine with a `count`; `${resource.db[0].address}` is `components.db.vms[0].address`; `${resource.db[*].address}` is the list. `to_lower`, `to_upper`, `replace`, `join`, `split` and `substring` are CEL's `lowerAscii`, `upperAscii`, `replace`, `join`, `split` and `substring` (`${to_lower(input.app)}` is `inputs.app.lowerAscii()`). Other functions and references (`env.*`, a disk's or a network's properties) are reported. |
| `Cloud.vSphere.Disk` attached by `attachedDisks` | An added disk of its `capacityGb`, with its constraint tags as its own `constraints`. |
| A machine's `linkedClone`, `provisioningType` and `storagePolicy` | The component's `linked_clone` (true or false; linked clones need the template's current snapshot, [ADR-0037](../adr/0037-images-per-vcenter.md)), `provisioning` (as a disk's, below) and `storage_policy` (by name, which vCenter must have). An expression, or another value, is reported. |
| A disk's `provisioningType` | The disk's `provisioning` ([ADR-0036](../adr/0036-placement-targets.md)): `thin` is `thin`, `thick` is `thick_lazy_zeroed`, `eagerZeroedThick` is `thick_eager_zeroed`. Another value, or an expression, is reported, and the disk is provisioned as the template's first disk. |
| `Cloud.vSphere.Network` (existing) | Without constraint tags, the item asks for cluster, datastore and network, within rvav's placement rules. A machine's other adapters on existing networks are reported (a warning): add them to the blueprint's `nics`. |
| A machine's `networks`: `assignment`, `assignIPv6Address` | On the first adapter, `assignment: dynamic` is `ipam: dhcp`: the address comes from the network's DHCP server, with no EfficientIP address or DNS records; `assignIPv6Address: true` is `ipv6: true`, an IPv6 address beside the IPv4 one, from the network's IPv6 subnet ([ADR-0039](../adr/0039-guest-access-and-networking.md)). `static` is rvav's default. Either on a segment made for the deployment, `assignIPv6Address` on another adapter or with DHCP, a fixed `address`, and the adapters' other settings (`macAddress`...) are reported, and left out. `dynamic` on another adapter is an info note: give it `ipam: dhcp` when you add it to `nics`. |
| `constraints` on a machine, its `storage`, a disk and an existing network | rvav's constraints ([ADR-0035](../adr/0035-constraint-tags.md)): the machine's are the component's `compute` constraints, its `storage.constraints` its `storage` ones, a disk's the disk's own, and those of its first adapter's network its `network` ones. `key:value` is `category/tag` (`env:prod` is `env/prod`); `:soft`, `:hard` and `!` are kept; `${input.x}` is `${inputs.x}`, and `${input.environment}` is rvav's `${environment}`. rvav then places the VMs within its placement rules, and the form asks for no cluster, datastore or network. When the first adapter's network is an existing one without constraint tags, rvav can't choose it: the form asks for all three, and rvav checks the choice against the constraints (a warning). A tag without a value, a tag that is one expression, and a tag that reads a machine are reported. The tags must exist on the vCenter objects ([Tags on vCenter objects](../guides/admin.md#tags-on-vcenter-objects)). |
| The project's custom naming (**Infrastructure › Custom names**, its pattern for machines), or else its machine naming template | The item's naming, when every part of it translates. The project's own custom naming comes first, then the organization's default. A counter that doesn't start at 1 or step by 1 is reported: to keep vRA's numbers, make a naming policy for the project (**Policies › Naming**) with the item's naming as its template and the same **Start at** and **Increment**: a naming policy names the VMs in place of the item's naming. A multi-VM item doesn't use vRA's naming: it names each component's VMs after the component's key, for example `{project}-{environment}-db{seq:02}`, and the plan says so. To keep vRA's names for a multi-VM item, make a naming policy for the project with the translated pattern as its template. |
| `Cloud.NSX.Network`, `Cloud.Network` or `Cloud.vSphere.Network`, `networkType` routed, outbound or private | An NSX segment made for each deployment (`segments`; the VMs' adapters on it, the first by the `network` binding, the others as `nics`). A placement rule says where: its Tier-1 and EfficientIP block ([ADR-0028](../adr/0028-nsx-networking.md)). A private network gets the rule's Tier-1 too. When every VM is on one, the form asks for no network. Its constraint tags chose vRA's network profile: they aren't mapped. |
| `Cloud.SecurityGroup`, new | A security group made for each deployment (`security_groups`), joined by the machines whose networks name it. Its inbound rules from any or another of its groups, on a service or a usual port (22, 80, 443, 1433, 3306, 3389, 5432, ICMP), carry over; outbound rules, and those from addresses or other ports, are reported. |
| `Cloud.SecurityGroup`, existing | Reported: rvav can't tell its NSX group from vRA's constraint tags. Add it to `groups` as `nsx:<group ID>`. |
| Load balancers, other types | Reported, left out. An AWX job in a composite item can do what they did. |

### Image profiles

Each image name of vRA's image profiles becomes a catalogue template
(Administration › Templates and networks,
[ADR-0037](../adr/0037-images-per-vcenter.md)), its key the name in
lower-case letters, digits and dashes:

| vRA | rvav |
|---|---|
| An image profile's region | The vCenter of the region's vSphere cloud account, matched to a vCenter rvav manages by its name or its URL's host. A region in a vCenter rvav doesn't manage is reported, and left out: add the vCenter in Administration › vCenters, then import again. |
| The image's mapping in a region | The template's source in that vCenter. Two regions in one vCenter give one source, the first region's. |
| The mapping's `cloudConfig` | The template's cloud-init user data, translated as a cloud template's `cloudConfig` is. On a Windows image (below), it is reported and left out: whether the vCenter template runs cloudbase-init is for you to say. When it does, mark rvav's template **Uses cloudbase-init** and give it the user data ([Windows templates with cloudbase-init](../guides/admin.md#windows-templates-with-cloudbase-init)). |
| The mapping's `constraints` | The template's compute constraints, as a machine's are. A constraint that isn't mandatory is soft. One with an expression is reported, and left out. |
| An image name with a word starting with `win` (`win2022`, `Windows-2019`; not `rhel-twin`) | The template's operating system, Windows. Others are Linux. |
| `${secret.x}` in the mapping's `cloudConfig` | As in a cloud template's. A template whose cloud-init opens a secret rvav doesn't have isn't made, and its items say so: store the secret, then import again. |

The template has one cloud-init and one set of constraints: where
regions differ, the first region's (by name) is kept, and the plan warns.
New templates are offered to every project and environment; restrict
them in Administration › Templates and networks.

The import makes the templates rvav doesn't have, before the items that
use them. The plan's **Catalog items** tab lists them under the cloud
templates: what the import does with each, its vCenter templates, and
its notes. It never changes one rvav has: the plan marks it **Differs in
rvav** when it isn't what vRA's mappings give. An item whose image has no
template, because none of its regions is in a vCenter rvav manages, is
imported with a warning, and its requests fail until an administrator
makes the template.

### Property groups

The import makes each vRA property group an rvav property group
(**Catalog items › Property groups**,
[ADR-0038](../adr/0038-names-groups-secrets-properties.md)) before the
items that name it. The **Catalog items** tab lists them under the
templates.

| vRA | rvav |
|---|---|
| Name | The key: letters, digits and underscores, starting with a letter (`dns-settings` is `dns_settings`). When groups share a name, the organization's keeps it and projects' groups get `_2`, `_3`. A template uses its project's group first, then the organization's. |
| Display name, description | The group's name and description. |
| An input group's properties (string, integer, number, boolean; `title`, `description`, `default`, `enum`, `oneOf`, `encrypted`, `required`) | The group's inputs. `enum` and `oneOf` give a string input its options; `oneOf` titles aren't kept. `encrypted` makes a string input secret, without a default. Lists, objects, options on a number, and checks such as `minimum` or `pattern` are reported, and left out. A group keeps at most 50 inputs. |
| A constant group's properties | The group's constants: text, numbers and booleans. Lists, maps and encrypted constants are reported: vRA doesn't give an encrypted value, so store it as a secret. |
| A project's property group | A group every item can name, with an info note. |

The import never changes an rvav property group. When rvav's group of
that key isn't what vRA's gives, the plan marks it **Differs in rvav**,
and the items name rvav's latest version, each with a warning: check
that the group has the inputs and constants the item reads.

Changing an imported group in rvav therefore reaches its items at the
next import: each item that names the group gets a new draft that names
the group's new version (or a conflict, if the item has a draft already),
even when nothing changed in vRA. The plan says so on the group, and
lists the items it makes a new draft of. Publish the drafts, or discard
them to keep the version the items name.

### vRO actions

The import makes a form library entry
(**Administration › Form library**,
[ADR-0043](../adr/0043-form-library.md)) of each vRO action that a custom
form takes a field's choices or default from, when the action's script
makes one REST call rvav can make in its place. It makes the entries
before the items that name them. The **Catalog items** tab lists them
under the property groups, as **Form library**.

| vRO action | rvav entry |
|---|---|
| Name | The key, as a property group's (`get-versions` is `get_versions`). When actions share a name, the second gets `_2`. The name, in words, is the entry's name, and the description names the action and its version. |
| A script that makes one `GET`, to a literal URL, or to a literal host URL followed by `createRequest`'s path | A value source of kind `rest`, on that URL. Each action input the URL concatenates is a required parameter, read as `${params.<input>}`. An input the URL doesn't read is left out, with an info note. |
| Output type `Array/string` or `Array/number` | The answer is the list of choices (`items` and `value` `$`). |
| Output type `Array/Properties` | Choices with `$.value` and `$.label`. |
| Output type `string`, `number` or `boolean` | The answer is one value, a field's default. |
| An `Authorization` header | A warning: store the credential as a secret in rvav, and name it in the entry. |

Every entry made of a script carries a warning: check its JSONPaths
against what the service answers, with **Change or test** on the **Form
library** page. To call the service, rvav needs its host among the hosts
REST value sources may call (**Administration › Integrations › Allowed
hosts**). The plan skips an action whose host isn't there and says so:
add the host, then plan the import again.

The import makes no entry, and the fields keep their unmapped notes,
naming the action and why, when an action:

- works its values out in JavaScript, which rvav doesn't run: write a CEL
  entry, or have a REST service give them;
- makes several REST calls, or a call other than `GET`;
- calls a vRO REST host by name, whose URL the import doesn't know: add
  an entry with that host's URL and the path the note gives;
- builds its URL in steps the import can't follow, names several URLs,
  or takes its host from an input;
- returns another type, such as `Properties` or a vCenter object;
- isn't in what the import read from vRO.

The import never changes an rvav entry. When rvav's entry of that key
isn't what the action gives, the plan marks it **Differs in rvav**, and
the items name rvav's latest version, each with a warning. Changing an
imported entry in rvav gives the items that name it a new draft at once,
as any change of an entry does.

### Custom forms

| vRA form | rvav |
|---|---|
| Labels, placeholders, signposts (help), constant defaults | The fields'. A constant default is the field's initial value. |
| A default bound to a field (`bind`), or to an operation on fields and constants (`concatenate`, `add`, `subtract`, `multiply`, `divide`) | The field's `default` rule, in CEL: `{"bind": "hostName"}` is `inputs.hostName`, and ``{"bind": {"values": ["hostName", "`.corp.example`"], "operator": "concatenate"}}`` (a constant between backquotes) is `inputs.hostName + ".corp.example"`. The requester can change the field; until they do, it follows the fields it reads. An operation's numbers are worked out as decimals. On a field read-only in vRA's form, it's the field's `value` rule instead, which the requester can't change. A bind to a field that isn't an input of the template, or another operator, is reported (unmapped): write the rule. |
| A default from a vRO action (`scriptAction`) | The field's value source names the form library entry made of the action ([vRO actions](#vro-actions)), which gives a text, number, date or toggle field its default. The requester can change it. When the import makes no entry of the action, or the entry gives a list, it's reported (unmapped) with why: give the field a value source. |
| A conditional default (values, each with its condition) | Reported (unmapped): write a `default` rule (CEL). |
| Static value lists | Choices. |
| `textField`, `textArea`, `dropDown`, `radio`, `checkbox`, `integerField`, `decimalField`, `passwordField`, `datetime`, `multiSelect`, `multiValuePicker`, `dualList`, `datagrid`, `objectField` | text, textarea, select, radio cards, toggle, number, number (a decimal on a `number` input), secret, datetime, multiselect, multiselect, dual list, table, group of fields. |
| `datetime` on a date input | date: a `date` input keeps `format: date`, a `date-time` one `format: date-time`, which the server checks. |
| `tagField` on an array input | tags, a list of strings. Its value list gives suggestions, not a limit. |
| A `datagrid`'s columns, an `objectField`'s fields | The table's columns, or the group's sub-fields, take their labels and order; the properties the form doesn't name come after. |
| `text`, `image` and `link` elements | Information text, an image and a link, showing the element's constant value. An image's text alternative is its label, as vRA has none (a warning: say what the picture shows). An image from another site loads only if that site is in `RVAV_WEB_IMAGE_SOURCES`. An element whose value isn't a constant, and an image or a link whose address isn't `https://` or `http://`, such as a `data:` URI, are reported, and left out: upload such a picture in the designer. |
| `valuePicker` | Reported (unmapped): rvav's equivalent is a search field, which needs a value source that searches (REST or CEL). The field stays text until you give it one. |
| `passwordField` | A secret. A default, in the form or the template, isn't kept, since anyone who reads the item would see it: the requester types the secret (a warning). |
| A widget that can't edit the input as rvav typed it | A warning: the field keeps the widget of its type. |
| A widget rvav has none of, such as `treeView` | Reported (unmapped): the field keeps the widget of its type. |
| Pages and sections | One page keeps its sections. Several pages become tabs, as vRA shows them: one tab per page, with the fields of its sections in their order. To show them one after the other instead, choose the wizard layout in the form designer. |
| Required, pattern, lengths, minimum and maximum | The form's checks. |
| Values from a vRO action | The field's value source names the form library entry made of the action ([vRO actions](#vro-actions)), each of its parameters bound to what vRA's form passes the action: a field (`inputs.x`, and the field depends on it), the environment, or a constant. A text field becomes a dropdown. When the import makes no entry of the action, or vRA's form passes the action nothing for one of its inputs, it's reported (unmapped) with why. |
| Values from vRO actions the import doesn't map, or external sources | Reported: give the field an rvav value source (REST, CEL or the form library). A `multiSelect`, `multiValuePicker` or `dualList` without choices of its own becomes tags, which take any value typed: once it has a source, make it a multi-select or a dual list again. |
| Conditions on visibility or read-only | Reported: write an rvav rule (CEL). A field read-only in vRA's form is editable in rvav's (a warning), unless its default is bound: then its `value` rule computes it. |

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
| Its owner (or who created it) | The rvav user of that email, who must hold a role in the project that requests there (`request.submit`), such as Member or Project admin. |
| The catalog item it was requested from | The item imported from that cloud template. Its published version is the deployment's. |
| Its name | The same, made valid for rvav: lower case, other characters as dashes ("Web Server for Thomas" is web-server-for-thomas). |
| The item | Must be published in rvav and offered to the project: the plan says when it isn't. |
| `leaseExpireAt` | What remains of the lease, at least a day, at most what rvav's lease policy allows. A lease already ended gives a day to decide. No lease in vRA: the policy's default, with a warning, since it ends in a deletion. |
| Its on-demand NSX network, its new security groups | Adopted with it when rvav has an NSX Manager: found in NSX by the name vRA gave them there (`resourceName`), when exactly one object has it and no rvav has it already; tagged as rvav's and the deployment's (their tags only), and deleted with it ([ADR-0028](../adr/0028-nsx-networking.md)). A group's security policies come too when their rules name only the deployment's groups and the account that made the groups (vRA's) made them. A group another policy uses is left as it is, and reported. The segment's subnet stays in EfficientIP; the VMs stay in their groups as vRA put them. |
| Existing security groups, load balancers, other resources | Stay as they are; reported. |

Each plan is **adopt**, **adopted** (rvav manages its VMs already),
**skipped** (with why: VMs not found, a project, environment, owner or item
missing) or **failed** (onboarding refused it, with why).

Running it again adopts only what isn't yet, so it can run after each fix:
- a project mapped;
- an owner who has signed in to rvav;
- an item imported and published.

An adopted deployment has no inputs in rvav: vRA's aren't carried over.
Its first **Update** opens the item's form with the fields' defaults, and
applies only what you change from them. Before you confirm it, check the
plan against what its VMs have.

### vRA's onboarding plans

The importer doesn't read vRA's onboarding plans. The deployments a vRA
plan onboarded are vRA deployments, so the import adopts them as it
adopts any other. To keep adopting VMs as they appear, save an
onboarding plan in rvav: a filter by name, folder, tag, cluster, vCenter
or power, and where the VMs go
([admin guide](../guides/admin.md#existing-vms-and-efficientip),
[ADR-0044](../adr/0044-onboarding-plans.md)).

## vRO

vRO's workflows aren't imported, and neither are its actions, except those
that custom forms take values from ([vRO actions](#vro-actions)). They are
sorted, for people to decide, into the groups of
[ADR-0007](../adr/0007-extensibility-model.md#migrating-vro-workflows):

```sh
rvav vro-triage -f estate.json -md > vro-triage.md   # a table per group
rvav vro-triage -f estate.json                        # the same, as JSON
rvav vro-triage -standard -md                         # the workflows an enterprise vRA typically has
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
| **A. Native** | A workflow that only drives vCenter: rvav's built-in steps, or a composite item. One that also calls REST APIs, or only them, unless a subscription or day-2 action runs it: a composite item with REST call steps ([ADR-0033](../adr/0033-composite-step-kinds.md)). One that sends mail, a step that sends a message, under the same condition. A decision is a branch's, one nesting too deep a custom workflow's. One with a user interaction or a timer and no wait for an event, under the same condition: a composite item with steps that wait for a person or a time, or poll (a timer around a REST call). An action that fills a form field: a value source on the field (REST when it calls REST, CEL otherwise). |
| **B. AWX** | A workflow that calls other systems (Active Directory, SQL, PowerShell, SSH, NSX...), mail when a subscription or day-2 action runs it or alongside those, or REST APIs when a subscription or day-2 action runs it, which can't run a composite: a playbook, with the Ansible collections the report names. ABX actions. |
| **C. Custom Temporal workflow** | A workflow that waits for events, until a step can. One with a user interaction or a timer that also does, or that a subscription or day-2 action runs: it waits durably, and asks with rvav's approvals. |
| **D. Retire** | Nothing in vRA runs it (or only disabled subscriptions), and it hasn't run for six months or is a copy of another that never ran. An action nothing calls. |

The report ends with each workflow to keep **as a composite item**: its
steps (rvav's, an AWX job), and the step kinds rvav may add for vRO's
patterns (wait for an event), with how many workflows need each. That sizes
which step kinds rvav builds ([implementation plan, D2](../implementation-plan.md#d2-composite-step-kinds-after-n1)).
`-standard` runs it on twelve typical enterprise workflows instead of a
vRA's.

An action that only workflows call goes with them, in their group.
A workflow keeps its group however old its last run, as long as something
runs it; the report says when it last ran.

The suggestion reads what the content shows: a script that reaches a
system through a vRO object the report doesn't know is a guess away from
the right group. Check each B and C, and every A whose reason says it
reaches no system.
