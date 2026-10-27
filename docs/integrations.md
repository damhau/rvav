# Integrations

Each integration sits behind a Go interface in `internal/integrations/...` so it
can be faked in tests and swapped later.

## vSphere (native, govmomi)

### Connections
- One or more vCenter endpoints. Credentials are stored as secrets, and sessions
  are kept alive and pooled per endpoint.
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
- Source: VM template or content library item (OVF or VM template).
- Guest customization: vSphere customization specs (Linux and Windows) or
  cloud-init through `guestinfo` metadata and userdata.
- Network: NIC → port group or distributed port group. Static IP from EfficientIP
  is injected through the customization spec or cloud-init network config.
- Tags and custom attributes are written to the VM: `rvav.deployment`,
  `rvav.project`, `rvav.owner`. They are used by sync and drift detection.

### Inventory sync (`rvav-sync`)
- PropertyCollector `WaitForUpdatesEx` on VMs, hosts, clusters, datastores,
  networks, folders, resource pools and tags.
- Writes into cache tables used by form pickers, placement and the deployment view.
- Changes are pushed to the UI over SSE (power state, IPs, reconfigurations made
  outside the app).

### vCenter permissions (service account)
A custom role on the target folders, clusters and datastores, with: Virtual
machine (inventory, configuration, interaction, provisioning, snapshot
management), Datastore allocate space, Network assign, Resource assign to resource
pool, Content library read, vSphere tagging, and Sessions validate (for console
tickets). The exact privilege list will be documented once the driver is built.

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
**Execute** permission on the templates exposed in the catalog.

### Launch contract
- Job templates used by rvav must enable **Prompt on launch** for *Limit* and
  *Variables* (and for *Inventory* or *Credentials* if the form overrides them).
  The import checks this and warns.
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

### Dynamic inventory: AWX pulls from rvav

AWX gets its host list from rvav through a custom **inventory plugin** that rvav
ships (an Ansible collection, for example `rvav.platform`). Hosts and variables
come straight from the rvav database, so they're always correct and include
deployment context.

- rvav exposes `GET /api/v1/ansible/inventory`, authenticated with a read-only API token.
- The output contains hosts (managed VMs with `ansible_host` = IP), groups built
  from project, environment, catalog item, OS family and tags, and hostvars
  (`rvav_deployment_id`, `rvav_project`, `rvav_owner`, form inputs marked
  "expose to inventory").
- In AWX: an inventory "rvav" with an inventory source pointing to an SCM project
  that contains the plugin config (`rvav.yml`). Its credential is a custom
  credential type that injects `RVAV_URL` and `RVAV_TOKEN`.
- **Freshness**: before launching a job that targets a newly provisioned VM, the
  workflow calls `POST /inventory_sources/{id}/update/` and waits for it to
  finish. Then it launches with `limit=<hostname>`. Setting *Update on launch*
  with cache timeout 0 is an alternative, but an explicit update gives clearer
  errors and timing.
- Only resources in state `ready_for_config` or `ready` appear in the inventory.

An alternative is the `community.vmware.vmware_vm_inventory` plugin filtered on
rvav tags. It's simpler but slower, and it lacks deployment context, so it isn't
the default.

## EfficientIP SOLIDserver (IPAM and DNS)

### Use cases
- At provision: find the next free IP in the subnet linked to the chosen network,
  reserve it (with hostname, MAC address once known, and rvav metadata as class
  parameters), then create DNS A and PTR records.
- At rename: update the DNS records.
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
    Reserve(ctx context.Context, s Subnet, hostname string) (Address, error)
    Release(ctx context.Context, a Address) error
    CreateRecord(ctx context.Context, r Record) (Record, error)
    DeleteRecord(ctx context.Context, r Record) error
    NameExists(ctx context.Context, fqdn string) (bool, error)
}
```

### Notes
- Race safety: the reservation looks for the hostname first and skips an
  address taken between the search and the add, so activity retries are
  idempotent.
- The mapping between vSphere networks and EfficientIP subnets comes from the
  subnet class parameters `vsphere_network`, `gateway`, `domain` and
  `dns_servers`, read by the sync service; mappings set by an administrator
  win (ADR-0015).
- VM names are checked free in DNS as well as in vCenter.

## Identity (OIDC)

- Any OIDC provider (Keycloak, Entra ID, Okta). Authorization code flow with
  PKCE for the SPA, bearer tokens for the API.
- Group claims map to global and project roles. Optional manager claim for
  approval routing.

## Notifications

- Email (SMTP), Slack (bot with interactive approval buttons), Microsoft Teams
  (Adaptive Cards through a webhook or bot).
- Templates per event type, user preferences per channel.
