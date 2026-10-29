# ADR-0024: Multi-VM blueprints and advanced provisioning

- Status: accepted
- Date: 2026-10-29

## Context
Until S12 a `vm` blueprint builds one VM from a template, with its first
disk resized and its first network adapter on one network; a request for
several (the form's "how many") makes one deployment per VM. The vRA
templates rvav replaces describe applications: several machines with
their own sizes, extra disks and adapters, vSphere tags, machines kept on
separate hosts, Windows machines joined to the domain, Linux machines
configured by cloud-init, templates from content libraries, storage
policies, and more than one vCenter. S12 adds these to the blueprint, the
evaluation (placement, names, addresses, quotas) and the workflow.

## Decision

### Components
A `vm` blueprint lists **components**: groups of identical VMs, each with
its own count, template, size, placement, disks, adapters, guest and
post-configuration.

```yaml
kind: vm
naming: "{project}-{environment}-{inputs.app}{component}{seq:02}"
deployment_name: "{project}-{environment}-{inputs.app}"
rollback_on_failure: true
guest: {os_family: linux, domain: corp.example, time_zone: Europe/Zurich}
sizes:
  S: {label: Small, vcpu: 2, memory_gb: 4, disk_gb: 60}
  L: {label: Large, vcpu: 8, memory_gb: 32, disk_gb: 120}
bindings: {datastore: inputs.datastore, network: inputs.network}
awx_templates:
  linux-baseline: {title: Linux baseline, job_template: linux-baseline}
components:
  - key: web
    title: Web servers
    count: inputs.web_nodes
    template: rhel9-template
    anti_affinity: true
    bindings: {vcpu: sizes[inputs.size].vcpu, memory_gb: sizes[inputs.size].memory_gb,
               disk_gb: sizes[inputs.size].disk_gb, cluster: inputs.web_cluster,
               post_config: '["linux-baseline"]'}
    disks:
      - {size_gb: inputs.data_gb}
    tags: [backup/daily]
  - key: db
    title: SQL Server
    template: win2022-template
    guest:
      os_family: windows
      domain_join: {domain: corp.example, ou: "OU=Servers,DC=corp,DC=example", credential: ad-join}
      admin_password: win-local-admin
    bindings: {vcpu: "8", memory_gb: "32", disk_gb: "120", cluster: inputs.db_cluster,
               tags: '["sql/" + inputs.app]'}
    disks:
      - {size_gb: 200, storage_policy: Gold}
      - {size_gb: 50}
    nics:
      - {key: backup, network: '"dvpg-backup-vl900"', ipam: efficientip}
```

- A component's **key** (`^[a-z][a-z0-9]{0,15}$`) names it in VM names
  (`{component}`), step keys, the AWX inventory and the API. **count** is
  a number or CEL over the inputs, from 0 (an optional component) to 20;
  the default is 1. A request builds at most 20 VMs in all.
- What a component doesn't set comes from the blueprint: `naming`,
  `template` (and `library`), `guest` (field by field), `bindings` (key by
  key), `disks`, `nics` and `tags`. `sizes` and `awx_templates` are the
  blueprint's, shared.
- A blueprint **without components** is one component, `vm`, made of its
  own fields: every item made before S12 keeps working, and a request's
  "how many" still makes one deployment per VM. A blueprint **with
  components** makes **one deployment** holding all its VMs; its counts are
  in the form, so the request's own count must be 1.

### Disks and network adapters
- The template's first disk is grown to `disk_gb`, as before. **disks**
  adds disks in order: `size_gb` (a number or CEL), `datastore` (CEL;
  default the VM's) and `storage_policy` (below).
- The template's first adapter is connected to `network`, as before.
  **nics** adds adapters: `key` (default `nic2`, `nic3`…), `network` (CEL)
  and `ipam`: `efficientip` (the default) or `dhcp`.
- An `efficientip` adapter gets an address in the subnet linked to its
  network, and A and PTR records in that subnet's zone: `<name>` for the
  first adapter, `<name>-<key>` for the others. Customisation gives each
  adapter its address; only the first has the gateway. A `dhcp` adapter
  gets neither address nor records.
- Strings in `disks` and `nics` are CEL, as bindings are (a literal is
  quoted: `'"dvpg-backup-vl900"'`); numbers are themselves.

### vSphere tags
`tags` lists fixed tags as `category/tag`; `bindings.tags` is CEL giving
more from the inputs. They are attached after the clone. A tag missing
from its category is created there, as vRA does; a missing category fails
the step (categories carry cardinality and types, an administrator's
choice).

### Anti-affinity
`anti_affinity: true` keeps a component's VMs on separate hosts:

- the clone puts the component's *n*th VM on the *n*th eligible host of
  the cluster (connected, not in maintenance, mounting the datastore), so
  they start apart even before DRS acts;
- a DRS **VM anti-affinity rule** `rvav-<deployment>-<component>` with
  those VMs is created before they power on, and recorded as a resource of
  type `vsphere.drs_rule`: deleting the deployment (or rolling it back)
  removes it;
- a component needs at least two VMs for a rule, and no more than the
  cluster has hosts (from the inventory), which the preview checks.

### Placement
Each component is placed on its own: an application can span clusters,
and vCenters.

- A component's `cluster`, `datastore` and `network` are checked against
  the project's placement rules for the environment, as a single VM's are.
- When `cluster` isn't bound, rvav chooses: among the clusters placement
  allows that carry the component's network and an allowed datastore with
  room for its disks, the one with the most free memory. When `datastore`
  isn't bound, the allowed datastore of that cluster with the most free
  space. The network is always chosen (it decides the subnet).
- **Storage policies** (SPBM): a placement rule may name one; a disk's or
  component's `storage_policy` wins over it. It applies to the VM's home
  and disks.

### Templates and customisation specifications
- `template` names a VM template, as before. With `library`, it names an
  item of that **content library**, OVF or VM template, deployed into the
  same cluster, host, datastore and folder, then reconfigured like a
  clone. The templates catalogue (ADR-0020) gets a content library too.
- A **customisation specification** of vCenter, named by the templates
  catalogue's `customization` or the guest's `customization_spec`, is the
  base of the guest customisation: rvav sets the host name and every
  adapter's settings on it, and the domain join when the blueprint asks.

### Guests
- **Linux**: as before, vCenter's Linux preparation sets the host name,
  domain, time zone and addresses. With `cloud_init.user_data`, the VM
  gets cloud-init through `guestinfo` (cloud-init's VMware data source)
  instead: the user data is a Go template over `.vm` (name, fqdn, ip,
  nics), `.inputs`, `.project`, `.environment` and `.request`; the
  metadata holds the host name and the network (version 2), so the
  addresses are set the same way.
- **Windows**: Sysprep sets the computer name, the time zone (a Windows
  index; default 110, W. Europe), the local administrator's password from
  the secret `admin_password` names, and the addresses. `domain_join`
  joins `domain`, in `ou` (vCenter 8.0.2 and later), with the `basic`
  secret `credential` names. The worker reads secrets when it customises;
  they are never in the plan, the timeline or the logs.

### Names
- Every VM is named by the naming policy that applies (ADR-0020), or else
  by its component's naming template (or the blueprint's), where
  `{component}` is its key (empty for a single-VM item). Names with the
  same prefix continue one counter, so components without `{component}`
  in their name share it.
- The deployment is named by `deployment_name` (a naming template without
  `{seq}`), or else after the item and the request number
  (`Shop application REQ-10077`).

### The deployment
- **Resources**: every VM (with its `component`), every address and
  record, and every DRS rule.
- **Quotas** count every VM: vCPU, memory, storage (every disk) and VMs.
- **Approval policies** see `vm` as before (the first VM, `count` all of
  them) and `vms`, one entry per VM: `component`, `vcpu`, `memory_gb`,
  `storage_gb`.
- **Lease**: one, the deployment's; its expiry acts on every VM.
- **Day-2**: VM actions act on one VM, deployment actions on all of them;
  deleting removes every VM, address, record and rule. A component's
  post-configuration runs on its own VMs (the AWX limit); rvav's AWX
  inventory puts each VM in a group `rvav_component_<key>` too.

### The workflow
One timeline, with a step per component and its VMs' steps under it.

1. Every VM, at the same time: its addresses, its DNS records, the clone
   (with its disks, adapters and tags).
2. The DRS rules.
3. Every VM, at the same time: customisation and power on.
4. Each component's post-configuration, in the blueprint's order.

When a VM fails, the VMs still building are cancelled; with
`rollback_on_failure` everything is deleted, else it stays for someone to
look at, and **Retry** resumes after what was done (ADR-0013). Workflows
started before this change finish the way they began
(`workflow.GetVersion`).

### Several vCenters
- Administrators add vCenters (Administration → Integrations → vCenters):
  name, address, account and password (sealed), certificate check. The
  one `RVAV_VCENTER_*` configures stays, as the first.
- The inventory sync reads each; the inventory, placement rules (which may
  name a vCenter) and every VM resource know which vCenter they belong to.
  The worker connects to each when it first needs it; day-2 actions and
  the console use the VM's own.
- A cluster name found in two vCenters is ambiguous: a placement rule
  allowing it must name the vCenter.

## Consequences
- vRA templates with several machines, disks, adapters and tags translate
  to one blueprint; the importer (P2) maps onto components.
- Items made before S12 are components of one VM and behave as they did.
- Deployments are no longer one VM: the deployment page lists VMs by
  component, and placement is shown per VM.
- What vcsim can't show is part of Lab acceptance: a real domain join (and
  its OU), cloud-init in a real guest, content library OVF deployments,
  storage policy compliance, DRS keeping VMs apart while they run, and a
  second real vCenter.
