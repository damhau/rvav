# Moving from vRA

How vRA 8 / Aria Automation's content and deployments move to rvav
([ADR-0027](../adr/0027-vra-importer.md)). The importer only reads vRA.

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
| NSX, load balancers, security groups, other types | Reported, left out. An AWX job in a composite item can do what they did. |

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
| `leaseExpireAt` | What remains of the lease, at least a day. A lease already ended gives a day to decide. No lease in vRA: the project's lease policy. |
| NSX and other resources | Stay in vRA's records; reported. |

Each plan is **adopt**, **adopted** (rvav manages its VMs already),
**skipped** (with why: VMs not found, a project, environment, owner or item
missing) or **failed** (onboarding refused it, with why).

Running it again adopts only what isn't yet, so it can run after each fix:
- a project mapped;
- an owner who has signed in to rvav;
- an item imported and published.
