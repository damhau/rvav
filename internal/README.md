# Backend layout

One binary, `cmd/rvav`, runs every process (`api`, `worker`, `sync`, `dev`,
`migrate`, `seed`, `vcsim-bootstrap`, `healthcheck`). `cmd/rvav-vcsim` runs the
vCenter simulator with the GVA layout for local work.

| Package | What it does |
|---|---|
| `api` | Code generated from `api/openapi.yaml` (`make gen`); do not edit `api.gen.go` |
| `app` | Wires configuration, the database and integrations into processes |
| `auth` | OIDC access tokens, API tokens (`rvav_pat_`), dev mode; the `Principal` |
| `blueprint` | Catalog item blueprints and their CEL bindings |
| `config` | `RVAV_*` environment variables |
| `core` | Business rules: draft evaluation (schema, placement, naming, approval, quota, lease), form value sources, submit, cancel, approvals (inbox, decide, delegate) |
| `e2e` | End-to-end tests (`make test-e2e`): slice 1 in Temporal's test environment, and requests through a real Temporal dev server; they start their own PostgreSQL and are skipped with `-short` |
| `events` | Event outbox publisher and the SSE hub (Postgres LISTEN/NOTIFY) |
| `httpapi` | HTTP handlers on the generated `ServerInterface`, problems, middleware |
| `ids` | Prefixed ULIDs |
| `integrations/vsphere` | govmomi driver: clone, customize, power, wait for IP, destroy, inventory; vcsim bootstrap |
| `integrations/efficientip` | IPAM and DNS client and in-memory fake |
| `integrations/awx` | AWX `/api/v2` client and a fake that plays jobs |
| `inventory` | vCenter inventory sync into the cache tables and VM resource state |
| `naming` | Naming templates such as `{project}-{environment}-{inputs.role}{seq:03}` |
| `pgtest` | A PostgreSQL for tests: `RVAV_TEST_DATABASE_URL`, or embedded-postgres |
| `policy` | CEL evaluation |
| `present` | Store rows to API types, shared by handlers and events |
| `provision` | Temporal workflow `Provision`, its activities, the plan and step keys |
| `seed` | Example organisation and catalog |
| `store` | PostgreSQL access (pgx), keyset pagination |
| `vmprops` | VM resource properties from vCenter data |

## How a request runs

1. `POST /requests/preview` and `POST /requests` share `core.Evaluate`.
   Submit stores the request with its plan, the pending top-level steps, the
   deployments and its approval levels (ADR-0011), then starts the
   `Provision` workflow.
2. When approval is required, the workflow opens each level in turn and
   waits for its `approval` signal (sent by `POST /approvals/{id}/decision`)
   or its timeout, then loads the plan again (approvers may have changed
   inputs). It sleeps until `scheduled_at`, then runs: reserve IP and create DNS
   (EfficientIP), clone, customize and power on (vCenter), refresh the AWX
   inventory and run one AWX job per post-config template.
3. Each activity records its own step. AWX tasks become child steps and the
   job output goes to the step log. Every change is published to the outbox
   and streamed on `/events`.
4. Cancel: a waiting request ends at once; a running one rolls back (VM,
   DNS records, IP) and adds a "Rolled back" step.

## Known gaps in slice 1

- The EfficientIP HTTP client follows SOLIDserver REST service names but has
  not been checked against a real SOLIDserver; see the TODO in
  `integrations/efficientip/client.go`. The fake is used everywhere so far.
- The AWX HTTP client follows the documented `/api/v2` endpoints but has not
  run against a real AWX. The AWX dynamic inventory endpoint and plugin
  (docs/integrations.md) are not built yet; the workflow only refreshes the
  configured inventory source.
- Inventory sync polls every `RVAV_SYNC_INTERVAL` rather than using
  `WaitForUpdatesEx`.
- Value sources `awx.*` and REST/CEL sources return 400 "not available yet".
- `composite` items (PostgreSQL cluster) are listed but cannot be requested.
- Optional nullable fields are omitted rather than sent as `null` (a
  limitation of the generated types); clients should treat missing and
  `null` the same. `next_cursor` is omitted on the last page.
