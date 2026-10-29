# Integrations

Each integration sits behind a Go interface in `internal/integrations/...` so it
can be faked in tests and swapped later.

## vSphere (native, govmomi)

### Connections
- The vCenter integration's endpoint (Integrations, or `RVAV_VCENTER_*`),
  and others administrators add under it (**vCenters**: name, SDK address,
  account, password sealed like a secret, certificate check), with a test
  that signs in and reads the inventory ([ADR-0024](adr/0024-multi-vm-blueprints.md)).
  A password goes only where it was given for: a new address, or the
  certificate not checked, needs it again.
- rvav connects to each when it first needs it, and again when its
  settings change. The inventory sync reads every vCenter in turn; one
  that fails doesn't stop the others, and the inventory of a vCenter no
  longer managed goes. A vCenter that deployments have VMs in, or that
  placement rules name, can't be removed. A VM whose vCenter name rvav
  doesn't know is the integration's: its name may have changed since.
- Every VM resource knows its vCenter: provisioning, day-2 actions, the
  console (the console account is the first vCenter's) and deletion use
  it. Placement rules may name the vCenter their clusters are in; a
  cluster name allowed in two vCenters needs it. When a blueprint doesn't
  bind a cluster, rvav chooses, among those allowed in every vCenter that
  has the template, the one with the most free memory that has the
  network and a datastore with room. Templates and storage policies are
  checked in the vCenter the VMs go to.
- Testing uses **vcsim** (govmomi's simulator) in unit and integration tests and in CI.

### Driver interface (sketch)

```go
type VSphereDriver interface {
    CloneVM(ctx context.Context, spec CloneSpec) (VMRef, error)
    Reconfigure(ctx context.Context, vm VMRef, spec ReconfigSpec) error
    Power(ctx context.Context, vm VMRef, op PowerOp) error
    WaitForGuestIP(ctx context.Context, vm VMRef, timeout time.Duration) ([]net.IP, error)
    CreateSnapshot(ctx context.Context, vm VMRef, spec SnapshotSpec) (SnapshotRef, error)
    RevertSnapshot(ctx context.Context, vm VMRef, snap SnapshotRef) error
    DeleteSnapshot(ctx context.Context, vm VMRef, snap SnapshotRef) error
    AcquireConsoleTicket(ctx context.Context, vm VMRef) (ConsoleTicket, error)
    Rename(ctx context.Context, vm VMRef, name string) error
    Destroy(ctx context.Context, vm VMRef) error
}
```

### Provisioning
How a blueprint is built is in
[ADR-0024](adr/0024-multi-vm-blueprints.md); what vCenter sees:

- **Clone:** from the VM template, into the cluster's resource pool, on a
  connected host that mounts the datastore (VMs kept apart start on
  separate hosts), in the placement rule's folder. CPU, memory and the
  first disk's size are set; `guestinfo.rvav.deployment` and
  `guestinfo.rvav.request` are written to the VM.
- **Content library:** a template that is an item of a content library (a
  VM template or an OVF) is deployed through vCenter's REST API to the
  same place, then reconfigured like a clone; its first adapter is
  connected to the network after. The inventory sync lists the libraries'
  items as templates of their library.
- **Storage policies** (SPBM): the VM's home and disks get the policy the
  blueprint or the placement rule names, resolved by name through the
  storage policy service; a disk may have its own. The inventory sync
  lists the policies, so placement rules and previews check them.
- **Disks and adapters** added by the blueprint, after the clone: a disk
  on another datastore gets the path `[datastore]` alone, and vCenter
  names its file in the VM's folder there. A retry adds only what is
  missing.
- **Tags:** `category/tag`, attached through the tagging service (vCenter's
  REST API, same account). A tag missing from its category is created; the
  category must exist.
- **DRS rules:** a VM anti-affinity rule `rvav-<deployment>-<component>`
  per component kept apart, created before the VMs power on, removed with
  the deployment.
- **Guest customization:** vCenter's Linux preparation or Sysprep, with
  the host name, every adapter's address (EfficientIP's, or DHCP; only the
  first has the gateway) and the DNS servers. A vCenter customization
  specification named by the template (Templates and networks) or the
  blueprint is the base. Windows can join a domain, in an OU (vCenter
  8.0.2 and later), with the account of a `basic` secret, and get its
  local administrator's password from a secret; the worker opens them
  when it customizes.
- **cloud-init** instead, for Linux: `guestinfo.metadata` (host name, and
  the network as version 2, adapters matched by MAC address) and
  `guestinfo.userdata` (the blueprint's template, rendered), both base64,
  for cloud-init's VMware data source (cloud-init 21.3 and later).

### Inventory sync (`rvav-sync`)
- Follows each vCenter's changes with the property collector
  (`WaitForUpdatesEx` on a container view): VMs (name, folder, power, host,
  guest addresses and Tools, size, devices, snapshots, template or not),
  hosts, clusters, datastores and networks (name and membership). A VM that
  changes is read again at once, so a VM powered off in vCenter is off in
  rvav within a second; when anything else changes, its vCenter is read
  again in full a few seconds later, once for a burst of changes.
- A full read every `RVAV_SYNC_INTERVAL` (2 minutes by default) brings in
  what changes all the time and isn't followed (quick stats, free space)
  and what has no change notifications (tags, content library items,
  storage policies), and makes up for a change notification lost.
- When its session breaks (vCenter restarted, the session timed out), the
  sync signs in again, follows again and reads that vCenter in full, after
  a pause that grows to a minute while it keeps failing.
- Writes into cache tables used by form pickers, placement and the deployment view.
- Flags VMs changed outside rvav (power, size, disks, adapters' networks)
  with what differs, and VMs deleted outside rvav as missing ([ADR-0025](adr/0025-drift.md)).
- Changes are pushed to the UI over SSE (power state, IPs, reconfigurations made
  outside the app).

### vCenter permissions (service account)
A custom role on the target folders, clusters and datastores, with: Virtual
machine (inventory, configuration, interaction, provisioning, snapshot
management), Datastore allocate space, Network assign, Resource assign to resource
pool, Content library read, vSphere tagging (assign, and create tags for
tags made on the fly), Host configuration of the cluster (for DRS rules:
Host → Inventory → Modify cluster), VM guest customization (and Read
customization specifications), and Sessions validate (for console
tickets).

## AWX

AWX runs all Ansible automation. The app never runs Ansible itself.

### API usage (`/api/v2`)
| Purpose | Endpoint |
|---|---|
| Discover templates | `GET /job_templates/`, `GET /workflow_job_templates/` |
| Read survey | `GET /job_templates/{id}/survey_spec/` |
| Read launch prompts | `GET /job_templates/{id}/launch/` |
| Launch | `POST /job_templates/{id}/launch/`, `POST /workflow_job_templates/{id}/launch/` |
| Follow a job | `GET /jobs/{id}/`, `GET /jobs/{id}/job_events/?id__gt=<last>` |
| Follow a workflow | `GET /workflow_jobs/{id}/workflow_nodes/` |
| Cancel | `POST /jobs/{id}/cancel/` |
| Refresh inventory | `POST /inventory_sources/{id}/update/`, then poll `/inventory_updates/{id}/` |

Targets the **latest AWX release** (API `/api/v2`). Red Hat AAP is not a
target for now: its controller API is close to AWX's but sits behind the AAP
gateway with different paths and auth, so it would need its own small adapter.

Authentication uses an AWX OAuth2 token for a dedicated service user, with
**Execute** permission on the templates exposed in the catalog. With
`RVAV_AWX_ORGANIZATION` set, templates are looked up in that organisation
only, so a template of the same name elsewhere is never launched.

A workflow job is followed node by node: each node is a task of the step,
and a finished node's job output goes to the step's log. The client is
checked against a real AWX by contract tests ([deploy/awx](../deploy/awx/README.md)).

### Launch contract
- Job templates used by rvav must enable **Prompt on launch** for *Limit* and
  *Variables* (and for *Credentials* if the form overrides them). The import
  checks this and warns.
- An AWX item's form may choose the launch's inventory and credentials:
  its blueprint names the inputs (picked from `awx.inventories` and
  `awx.credentials`) under `awx.inventory` and `awx.credentials`, they are
  launch settings rather than extra variables, and the template must prompt
  for them (checked before launching). AWX items are launched by their
  template's ID when rvav knows it.
- Templates that run on rvav's VMs (post-configuration, AWX day-2 actions)
  must also prompt for *Inventory*: rvav launches them with its own
  inventory, the one it has just refreshed, and refuses to launch one that
  doesn't prompt ([ADR-0017](adr/0017-awx-inventory.md)).
- `extra_vars` sent to AWX:
  ```yaml
  # user inputs mapped from the form
  <survey variables...>
  # context added by rvav (namespaced)
  rvav:
    request_id: "..."
    deployment_id: "..."
    project: "web-team"
    requester: "jdoe@example.com"
    resources:
      - name: "web-prod-web001"
        ip: "10.1.2.34"
        fqdn: "web-prod-web001.example.com"
        moref: "vm-1234"
  ```
- Job progress: events polled incrementally (every 1–2 seconds while running) and
  mapped into the request timeline. Polling is simple and reliable; the AWX
  websocket can be added later as an optimisation.

### Day-2 actions from AWX

Templates with the `day2` label (`RVAV_AWX_ACTION_LABELS`) become day-2
actions in the Automation group, with their form from the survey; an
administrator chooses whether they run on VMs or whole deployments, a CEL
condition for where they are offered, and can turn them off. A run refreshes
the inventory and launches the template on rvav's inventory with the VM as
`limit` ([ADR-0018](adr/0018-awx-day2-actions.md)).

### Dynamic inventory: AWX pulls from rvav

AWX gets its host list from rvav through a custom **inventory plugin** that rvav
ships (an Ansible collection, for example `rvav.platform`). Hosts and variables
come straight from the rvav database, so they're always correct and include
deployment context.

- rvav exposes `GET /api/v1/ansible/inventory`, authenticated with an API
  token of a user with the auditor role, made with the inventory scope so
  it reads nothing else (`rvav api-token -user usr_svc-awx -scope inventory`).
- The output contains hosts (managed VMs with `ansible_host` = IP), groups built
  from project, environment, catalog item, the item's tags and OS family, and
  hostvars (`rvav_deployment_id`, `rvav_project`, `rvav_owner`, and in
  `rvav_inputs` the form inputs marked "expose to inventory").
- The collection is `ansible/ansible_collections/rvav/platform`; AWX installs
  it from this repository (`collections/requirements.yml`) when it syncs the
  project. `make test-ansible` tests it with ansible-core.
- In AWX: an inventory "rvav" with an inventory source pointing to an SCM project
  that contains the plugin config (`deploy/awx/inventory/rvav.yml`). Its
  credential is a custom credential type "rvav API" that injects `RVAV_URL`
  and `RVAV_TOKEN`. `rvav awx-bootstrap` sets this up in a test AWX.
- **Freshness**: before launching a job that targets a newly provisioned VM, the
  workflow calls `POST /inventory_sources/{id}/update/` and waits for it to
  finish. Then it launches on that source's inventory with
  `limit=<hostname>`. Setting *Update on launch* with cache timeout 0 is an
  alternative, but an explicit update gives clearer errors and timing.
- VMs of deployments being deleted are left out; those of failed deployments
  stay, so a retried post-configuration step reaches them.

An alternative is the `community.vmware.vmware_vm_inventory` plugin filtered on
rvav tags. It's simpler but slower, and it lacks deployment context, so it isn't
the default.

## EfficientIP SOLIDserver (IPAM and DNS)

### Use cases
- At provision: find the next free IP in the subnet linked to the chosen network,
  reserve it (with hostname, MAC address once known, and rvav metadata as class
  parameters), then create DNS A and PTR records.
- At rename: rename the address and update the DNS records.
- At delete: delete the DNS records and release the IP.
- Form pickers: subnets, free IPs, DNS zones; async validation that a hostname or
  IP is free.

### Client
`internal/integrations/efficientip`: the calls, authentication and how
retries stay safe are in [ADR-0015](adr/0015-efficientip-client.md). The
client follows EfficientIP's SOLIDserverRest library and the API reference
it ships (SOLIDserver 9.0.1a): services under `/rest/`, the free-address
search under `/rpc/`. `FakeServer` speaks the same API for tests and the
development stack.

```go
type Client interface {
    NextFree(ctx context.Context, s Subnet) (string, error)
    Reserve(ctx context.Context, s Subnet, hostname, owner string) (Address, error)
    Release(ctx context.Context, a Address) error
    RenameAddress(ctx context.Context, a Address, hostname string) error
    CreateRecord(ctx context.Context, r Record) (Record, error)
    UpdateRecord(ctx context.Context, r Record, value string) (Record, error)
    DeleteRecord(ctx context.Context, r Record) error
    NameExists(ctx context.Context, fqdn string) (bool, error)
    Subnets(ctx context.Context) ([]SubnetInfo, error)
    Zones(ctx context.Context) ([]string, error)
    Managed(ctx context.Context) ([]Address, []Record, error)
    Existing(ctx context.Context, addressIDs, recordIDs []string) (addresses, records map[string]bool, err error)
}
```

### Notes
- Race safety: the reservation looks for the hostname first, takes back
  only an address rvav reserved for the same deployment, and skips an
  address taken between the search and the add, so activity retries are
  idempotent.
- The mapping between vSphere networks and EfficientIP subnets comes from the
  subnet class parameters `vsphere_network`, `gateway`, `domain` and
  `dns_servers`, read by the sync service; mappings set by an administrator
  win (ADR-0015).
- VM names are checked free in DNS and IPAM as well as in vCenter.

## Identity (OIDC)

- Any OIDC provider (Keycloak, Entra ID, Okta). Authorization code flow with
  PKCE for the SPA, bearer tokens for the API.
- Group claims map to global and project roles. Optional manager claim for
  approval routing.

## Events and webhooks (ADR-0021)

Subscriptions send rvav's events (`rvav.event.v1`) to webhooks, AWX
templates or custom workflows ([ADR-0021](adr/0021-event-subscriptions.md)).
Writing a custom workflow is covered in
[custom-workflows.md](custom-workflows.md). For a webhook receiver:

- **Hosts.** rvav posts only to hosts in `RVAV_WEBHOOK_HOSTS` and doesn't
  follow redirects.
- **Body and headers.** The body is the event: `id`, `topic`, `contract`,
  `occurred_at`, `data`. The headers carry `X-Rvav-Event-Id`,
  `X-Rvav-Topic` and `X-Rvav-Signature: t=<unix time>,v1=<hex>`.
- **Signature.** `v1` is the HMAC-SHA256 of `<t>.<body>` under the stored
  secret the subscription names. Check it with a constant-time compare,
  and refuse a `t` far from now.
- **Retries.** A 2xx answer delivers it. Anything else is retried after
  10 s, 30 s, 1 min, 5 min, then every 15 min for a day. The event ID stays
  the same across retries, so ignore an ID you already have.
- **Blocking subscriptions** (`.pre` topics) wait for the answer within
  their timeout:
  - empty or `{"outcome": "continue"}` lets the request go on;
  - `{"outcome": "stop", "reason": "…"}` fails it with the reason, before
    anything is built or changed;
  - on `provisioning.pre`, `"properties": {"cmdb_id": "CI0042"}` sets
    deployment properties, which AWX jobs get in `rvav.properties`.
  - A failure or timeout stops or continues the request as the
    subscription's `on_failure` says, after one more try within the
    timeout.

```python
import hashlib, hmac, time

def verify(secret: bytes, header: str, body: bytes, tolerance: int = 300) -> bool:
    parts = dict(p.split("=", 1) for p in header.split(","))
    t, sig = parts.get("t", ""), parts.get("v1", "")
    if not t.isdigit() or abs(time.time() - int(t)) > tolerance:
        return False
    want = hmac.new(secret, t.encode() + b"." + body, hashlib.sha256).hexdigest()
    return hmac.compare_digest(want, sig)
```

## Notifications (ADR-0023)

Notifications are kept in the app's notification centre and sent on the
channels each user chooses per kind ([ADR-0023](adr/0023-notifications.md)).
Templates exist per kind and per language (English, French).

- **Email (SMTP):** plain text and HTML, with links into rvav.
- **Microsoft Teams:** an Azure bot. It sends Adaptive Cards, and
  approvers press Approve or Reject on the card. Teams' calls come with a
  Bot Framework token.
- **Slack:** a Slack app. It sends Block Kit messages, and approvers press
  Approve or Reject; rejecting asks for a comment. Slack's calls are signed
  with the app's signing secret.

The channels are set like the other integrations (Administration →
Integrations, or `RVAV_SMTP_*`, `RVAV_TEAMS_*`, `RVAV_SLACK_*`).

### Microsoft Teams setup
1. **App registration and bot.** Register an app in Entra ID
   (single-tenant) with a client secret. Create an Azure Bot resource on
   that app with the Microsoft Teams channel. Its messaging endpoint is
   `https://<rvav>/api/v1/integrations/teams/messages`.
2. **Graph permission.** Give the app the application permission
   `User.Read.All` (admin consent). rvav uses it to find each user's object
   ID by their email.
3. **Teams app.** Package a Teams app with the bot (personal scope) and
   install it for the people who approve, for example with a setup policy.
   Teams lets a bot write to someone only once its app is installed for
   them.
4. **rvav settings:** the app ID, tenant ID and client secret, and the Bot
   Connector region (`RVAV_TEAMS_SERVICE_URL`, for example
   `https://smba.trafficmanager.net/emea`).

Teams' calls carry a Bot Framework token. rvav checks it against the keys
published at `RVAV_TEAMS_OPENID_URL` (the Bot Framework's by default), and
checks its audience (the app ID) and the service URL it was issued for.

### Slack setup
1. **Slack app.** Create a Slack app in the workspace with these bot
   scopes:
   - `users:read.email` (to find people by their email);
   - `im:write` (to open direct messages);
   - `chat:write`.
2. **Interactivity.** Turn Interactivity on, with the request URL
   `https://<rvav>/api/v1/integrations/slack/interactions`.
3. **Install it** in the workspace.
4. **rvav settings:** the bot token (`xoxb-…`) and the app's signing secret.
   Slack's calls are refused when they aren't signed with the signing
   secret, or are more than five minutes old.
