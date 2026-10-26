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
- Reports and dashboards, showback costs.
- Drift detection and onboarding of existing VMs.
- Catalog import and export (YAML) and Git sync.
- Helm chart, HA guidance, backup and restore, upgrade guide.
- Migration helpers from vRA: export catalog items and deployments.

## Open points
- Cost model: flat rates per vCPU, GB of RAM and GB of storage, or an external source?
- vRA migration scope: which existing blueprints, forms and deployments must be
  imported?
- The SOLIDserver version and API flavour (legacy `/rest/` or `/api/v2.0/`).
- AWX version, and whether AAP (the Red Hat product) also needs support.
