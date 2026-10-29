# Architecture

## Goals

1. Replace vRA for a single organisation running VMware vSphere.
2. Run VM provisioning and day-2 operations **natively** against vCenter.
3. Use **AWX** to run Ansible job templates, both as catalog items and as steps
   inside provisioning and day-2 workflows.
4. Provide a full-featured **service catalog** with a modern, fast UX.
5. Be API-first: everything the UI does goes through a documented OpenAPI API.

## Non-goals (for now)

- Multi-tenancy (separate organisations). See [ADR-0002](adr/0002-single-organisation.md).
- Hypervisors other than vSphere. The driver interface allows them later.
- Running Ansible inside the app. All Ansible runs go through AWX.

## Component overview

```
                    ┌──────────────────────────────────────┐
                    │ Web UI                               │
                    │ React · TypeScript · Vite            │
                    │ shadcn/ui · Tailwind · TanStack      │
                    └──────────────────┬───────────────────┘
                                       │ REST (OpenAPI) + SSE
┌──────────────────────────────────────▼──────────────────────────────────────┐
│ rvav-api (Go)                                                               │
│  auth (OIDC) · RBAC · projects · catalog · forms · requests · policies      │
│  deployments · day-2 actions · audit · dynamic-value resolver · inventory   │
│  API for AWX dynamic inventory                                              │
└───────┬──────────────────────────┬──────────────────────────┬───────────────┘
        │                          │ start / signal           │
┌───────▼───────┐        ┌─────────▼──────────┐     ┌─────────▼──────────┐
│ PostgreSQL    │        │ Temporal           │     │ rvav-sync (Go)     │
│ state, audit, │        │ durable workflows  │     │ vCenter inventory  │
│ event outbox  │        └─────────┬──────────┘     │ property collector │
└───────────────┘                  │                └─────────┬──────────┘
                         ┌─────────▼──────────┐               │
                         │ rvav-worker (Go)   │               │
                         │ Temporal activities│               │
                         └──┬───────┬──────┬──┘               │
                            │       │      │                  │
                 ┌──────────▼┐ ┌────▼────┐ ┌▼──────────────┐  │
                 │ vSphere   │ │ AWX     │ │ EfficientIP   │  │
                 │ driver    │ │connector│ │ SOLIDserver   │  │
                 │ (govmomi) │ │ (REST)  │ │ (REST)        │  │
                 └─────┬─────┘ └────┬────┘ └──────┬────────┘  │
                       ▼            ▼             ▼           ▼
                    vCenter        AWX       IPAM / DNS     vCenter
```

### Processes

| Process | Role |
|---|---|
| `rvav-api` | HTTP API, auth, business rules, SSE streams to the UI. Stateless; scales horizontally. |
| `rvav-worker` | Temporal worker running workflow and activity code: vSphere, AWX, IPAM, notifications. |
| `rvav-sync` | Keeps a cached copy of vCenter inventory in PostgreSQL in near real time. Runs as a single active instance (leader election via a Postgres advisory lock). |
| `rvav-web` | Static SPA served by the API or a CDN/ingress. |

All three Go processes come from the same binary (`rvav api|worker|sync`), which
keeps building and versioning simple.

## Tech stack

| Layer | Choice | Why |
|---|---|---|
| Backend language | Go | govmomi, vcsim for tests, Temporal Go SDK, single binary |
| vSphere SDK | govmomi | Most complete and best-maintained vSphere SDK |
| Workflow engine | Temporal | Durable, retryable, human-in-the-loop (approvals), full history. Replaces vRO. |
| Database | PostgreSQL 16+ | Relational core plus JSONB for form schemas and inputs |
| DB access | sqlc + pgx, migrations with goose | Type-safe SQL, no heavy ORM |
| API | OpenAPI 3.1, spec-first (oapi-codegen) | Generated server stubs and a generated TypeScript client |
| Policy rules | CEL (cel-go) | Safe, fast expressions for approval and policy conditions and form rules |
| Auth | OIDC (Keycloak, Entra ID, Okta) | Groups from token claims map to app roles |
| Secrets | HashiCorp Vault, or AES-GCM encryption in the DB | vCenter, AWX and EfficientIP credentials |
| Frontend | React 19, TypeScript, Vite, TanStack Router and Query, shadcn/ui, Tailwind | Modern, fast, accessible. See [frontend](frontend.md) |
| Forms | JSON Schema plus a UI schema, rendered by our own renderer on top of react-hook-form | Full control over dynamic values and conditional rules |
| Realtime | Server-Sent Events | Request timelines, inventory changes, notifications |
| Observability | OpenTelemetry (traces, metrics), structured JSON logs | |
| Packaging | Docker images, docker-compose for development, Helm chart | |

## Key runtime flows

### 1. Provision a VM blueprint with AWX post-configuration

```
User submits request
  └─ API validates inputs (schema + CEL rules), checks quota and entitlement
  └─ API creates Request (status: pending) → starts Temporal workflow ProvisionDeployment
       ├─ Policy evaluation → approval needed? → wait for signal (approve / reject / timeout)
       ├─ Resolve name (naming policy) and placement (placement policy)
       ├─ EfficientIP: reserve IP in the selected subnet + create DNS A/PTR records
       ├─ vSphere: clone template → apply customization spec / cloud-init → reconfigure
       │           → attach NICs → power on → wait for VMware Tools + IP
       ├─ Mark resource "ready for config" (visible in the AWX dynamic inventory)
       ├─ AWX: update inventory source → launch job template (limit=<vm>, extra_vars)
       │       → stream job events into the request timeline
       ├─ On failure: run compensation (delete VM, release IP/DNS) according to the item's
       │             rollback policy, or keep resources for troubleshooting
       └─ Deployment status: ready → notify the requester
```

### 2. Day-2 action

```
User clicks an action on a resource
  └─ API checks the day-2 entitlement policy → shows the action form (if any)
  └─ Temporal workflow RunDay2Action
       ├─ Optional approval
       ├─ Native action (vSphere driver) OR AWX job template (limit=<vm>)
       └─ Update resource state, audit, notify
```

### 3. AWX job template as a standalone catalog item

```
Request → (approval) → AWX launch with extra_vars from the form → stream events → done
```

The result is a Deployment with no VM resources and an "AWX job" record attached,
so it shows up in history and can be re-run.

## Realtime timeline

Each workflow step emits `RequestEvent` rows (step, status, message, timestamps,
log reference). The API streams them to the UI over SSE. AWX job events are
mapped to timeline sub-steps (play → task → host result). Raw stdout is
available on demand.

## Reliability

- Every external call is a Temporal activity with a retry policy and timeouts.
- Activities are idempotent: use a deterministic VM name, check whether it exists
  before cloning, and IPAM reservations are keyed by request ID.
- Events going out (webhooks, notifications) use the outbox pattern.
- `rvav-sync` reconciles: VMs deleted or changed outside the app are flagged
  (drift, [ADR-0025](adr/0025-drift.md)) rather than silently diverging.

## Security

- OIDC login; API tokens for automation, scoped to a project and role.
- Role-based access control at two levels: global roles and project roles.
- Secrets are never sent to the browser. Form secret fields are stored encrypted
  and passed to AWX as extra_vars at launch, then cleared after the run.
- Full audit log of every change and action, including approvals.
- The vCenter service account follows least privilege; see [integrations](integrations.md).
