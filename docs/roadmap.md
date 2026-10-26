# Roadmap

Milestones are ordered so that each one ends in something usable end to end.

## M0 – Foundations
- Monorepo layout: `cmd/rvav`, `internal/...`, `api/openapi.yaml`, `web/`, `deploy/`.
- docker-compose for development: PostgreSQL, Temporal, Keycloak, vcsim, a
  mock AWX and a mock EfficientIP for local work.
- OIDC login, users, global roles, projects and members.
- OpenAPI spec-first pipeline (Go server stubs + TypeScript client).
- CI: lint, unit tests, integration tests against vcsim.
- UI shell: layout, navigation, ⌘K, theming.

## M1 – First VM end to end (MVP core)
- vCenter connection, inventory sync (`rvav-sync`), inventory cache.
- Placement rules per project (cluster, folder, datastore, network).
- EfficientIP: subnet mapping, reserve and release IP, create and delete DNS records.
- A `vm_blueprint` catalog item with a static form: template, size, network.
- Provision workflow in Temporal: IP → DNS → clone → customize → power on → wait for IP.
- Live request timeline over SSE.
- Deployments list and detail; native day-2: power, delete.
- Audit log.

## M2 – AWX integration
- AWX connection and content source; template import with survey → form.
- `awx_job_template` and `awx_workflow` catalog items.
- rvav inventory endpoint and the `rvav.platform` inventory plugin collection.
- AWX post-config steps in blueprints, with inventory refresh and `limit`.
- AWX job events in the timeline.
- AWX-backed custom day-2 actions.

## M3 – Full forms and catalog
- Form designer (visual and YAML views).
- Dynamic value sources (vSphere, EfficientIP, AWX, REST, CEL), dependencies,
  conditional rules, async validation, wizard layouts.
- Catalog UX: categories, tags, icons, search, favourites, featured.
- Entitlements; item versioning (draft, published, deprecated).
- Naming policy.

## M4 – Governance
- Approval policies (multi-level, conditional, delegation, timeouts) and the
  approvals inbox.
- Lease policies, reminders, expiry actions, extensions.
- Quotas, with preview in the request form.
- Day-2 entitlement policies.
- Full native day-2 set: reconfigure, disks, NICs, snapshots, console, rename,
  change owner, ISO.

## M5 – Extensibility and notifications
- Event subscriptions (AWX or webhook, blocking or non-blocking).
- Email, Slack and Teams notifications, including interactive approvals.
- Scheduled requests, on-behalf-of requests, count, re-request.
- Composite catalog items.

## M6 – Operations and reporting
- Reports and dashboards (usage, success rate, provisioning time, leases, approvals).
- Drift detection and onboarding of existing VMs.
- Catalog import and export (YAML) and Git sync.
- Helm chart, HA guidance, backup and restore, upgrade guide.

## Phase 2 – vRA importer
Planned after the first production release (M0–M6). Scope to be defined; likely:
- Read vRA (8.x / Aria Automation) through its API: projects, cloud templates,
  custom forms, catalog items and sharing, policies, deployments.
- Map cloud templates to `vm_blueprint` versions and custom forms to
  JSON Schema + UI schema; report anything that can't be mapped (for example
  vRO-specific actions) instead of guessing.
- Adopt existing vRA deployments: link their VMs (by instance UUID) to new
  deployments with owner, project and lease.
- Dry-run mode with a diff report, then an idempotent import.
- vRO workflows aren't converted automatically; the report lists them so they
  can be rewritten as AWX job templates.

## Decided

- Cost estimation and showback: **out of scope for now**.
- vRA importer: **Phase 2**, after the first release.
- EfficientIP: target the **latest SOLIDserver** release and its current REST API.
- AWX: target the **latest AWX** release. Red Hat AAP is not a target for now.

## Out of scope for now

- Cost estimation, showback and chargeback.
- Multi-tenancy (see [ADR-0002](adr/0002-single-organisation.md)).
- Hypervisors other than vSphere.
- Red Hat AAP as the automation backend.
