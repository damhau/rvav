# Roadmap

Milestones are ordered so that each one ends in something usable end to end.

## Status

R0 (slices 1 to 5) is built, on the lab stack; what remains for its
acceptance needs the lab vCenter (below). The demo script is
[demo-r0.md](demo-r0.md). Built: catalog to running VM, including the AWX
post-configuration steps, running for real on the Docker Compose stack;
approvals (levels, timeouts and escalation, delegation, approvers editing
inputs, the inbox); and day-2 actions (power, resize, disks, network
adapters, snapshots with retention, rename, ISO images, owner and project,
delete, the VM console), each a request with its own timeline, decided by
day-2 policies, with retry and rollback of failed requests
([ADR-0013](adr/0013-day2-actions.md), [ADR-0012](adr/0012-vm-console.md));
and leases and quotas (lease policies with project overrides, extension
with approval beyond the policy, reminders, power off at expiry and
deletion after a grace period; hard and soft quotas per project and
environment, [ADR-0014](adr/0014-leases-and-quotas.md)).
See [ADR-0006](adr/0006-contract-first-vertical-slices.md) for how slices
work, and [progress.md](progress.md) for the R0 work item by item.

R1 (slices 6 to 9) is built, item by item in
[progress-r1.md](progress-r1.md) and, from S8 on, in GitHub issues. S9
projects, placement and governance is built and accepted on the compose
stack ([ADR-0020](adr/0020-administration.md)): administrators set a new
project up from the UI (members and OIDC group mappings, placement by name
patterns or vCenter tags with a preview, quotas, naming, templates and
sizes) and it requests a VM without SQL or YAML; quotas, lease, approval
and day-2 policies are edited with their CEL checked when saved and
explained per project, environment, item and action; the vCenter, AWX and
EfficientIP settings are set from the UI (an environment variable wins, a
saved password goes only where it was given for, secrets re-sealed with
`rvav secrets rotate`); API tokens and the audit log, searched and
exported. S8
catalog administration is built and accepted on the compose stack
([ADR-0019](adr/0019-catalog-administration.md)): catalog admins create
items, publish versions and roll back (deployments keep theirs), open items
to projects, export and import them as YAML, and build forms in a designer
with a live preview as a chosen user; value sources call REST services
(allowed hosts only, secrets encrypted in PostgreSQL) or compute with CEL,
and form rules are evaluated on the server. S7 EfficientIP is built: the client
follows EfficientIP's own library and runs everywhere against a fake
SOLIDserver speaking the same API ([ADR-0015](adr/0015-efficientip-client.md)),
network mappings come from subnet class parameters, renames reach
EfficientIP, and a reconcile report lists orphans; its acceptance needs a
real SOLIDserver. S6 AWX is built and accepted against a test AWX
(24.6.1): templates with the catalog label become catalog items with forms
from their surveys, changed surveys flagged
([ADR-0016](adr/0016-awx-catalog.md)); AWX reads rvav's VMs through the
`rvav.platform` inventory plugin, and jobs on rvav's VMs run on that
inventory ([ADR-0017](adr/0017-awx-inventory.md)); templates with the day2
label become day-2 actions bound to resource types and a condition
([ADR-0018](adr/0018-awx-day2-actions.md)). The work
left, slice by slice, is in [GitHub milestones and issues](https://github.com/damhau/rvav/milestones).

Verified so far:
- `make check` (also in CI on every push): spec lint, generated code, Go vet
  and tests, end-to-end tests on their own PostgreSQL with vcsim, through
  Temporal's test environment and through a real Temporal dev server, OIDC
  token checks against a fake provider, day-2 actions against vcsim and the
  console proxy against a fake ESXi WebSocket, the lease and quota
  acceptance scenario through a real Temporal server, the catalog
  administration acceptance scenario with a fake REST service, the S9
  acceptance (a project set up through the administration API requests a
  VM); web
  typecheck, lint, unit tests, build and Playwright on mock data (day-2
  menus, dialogs and bulk actions, the catalog admin pages and the form
  designer included).
- The Docker Compose stack, run locally: it starts in order with health
  checks, `deploy/smoke.sh` gets a request to succeeded through the real
  Temporal server, and `pnpm e2e:stack` requests a VM in the browser,
  follows it to Ready and powers it off from its menu, with dev
  authentication and with Keycloak sign-in (groups mapped to project roles);
  the housekeeping schedule runs every 15 minutes there. With a stand-in
  CMDB on the stack's network (`deploy/cmdb`), `pnpm e2e:stack` also builds
  an item from YAML with a REST dropdown, publishes, requests, changes and
  rolls it back in the browser, and as an administrator sets a new project
  up and requests a VM in it.

Not verified yet: a real vCenter (S2 and S4 acceptance on the lab vCenter:
guest operations with VMware Tools, hot add, the web console through the
ESXi hosts' firewalls), a real EfficientIP SOLIDserver, the corporate
identity provider. AWX is verified against a test AWX 24.6.1, not yet the
organisation's.

### Open items found in slice 1
- EfficientIP client: endpoints confirmed against EfficientIP's own library (S7); a real SOLIDserver still has to confirm them.
- Inventory sync polls; switch to property collector updates (S13).
- Accessibility audit (S14), Storybook.

## Plan

The detailed plan is in [implementation-plan.md](implementation-plan.md): what
"80% of vRA" means feature by feature, the slices with scope and acceptance
criteria, effort, calendar, risks and decisions.

| Release | Contents | Target (reference team) |
|---|---|---|
| R0 Lab | Running for real, approvals, day-2 actions, leases and quotas (S2–S5) | Month 3 |
| R1 Pilot | Real AWX and EfficientIP, catalog and admin UI (S6–S9) | Month 6 |
| R2 1.0 | Extensibility, notifications, advanced provisioning, inventory, production readiness (S10–S14); 83% of vRA | Month 10 |
| R3 vRA off | vRA importer, vRO triage, parallel run, cutover (P2) | Month 13 |

## Decided

- Cost estimation and showback: **out of scope for now**.
- vRA importer: **Phase 2**, after the first release.
- EfficientIP: target the **latest SOLIDserver** release and its current REST API.
- AWX: target the **latest AWX** release. Red Hat AAP is not a target for now.
- Temporal: **self-hosted** on PostgreSQL ([ADR-0008](adr/0008-self-hosted-temporal.md)).
- Hosting: **Kubernetes with Helm** in production, Docker Compose for development and the lab ([ADR-0009](adr/0009-hosting.md)).
- Identity: **any OIDC provider**; Keycloak for development and tests ([ADR-0010](adr/0010-identity-provider.md)).

## Out of scope for now

- Cost estimation, showback and chargeback.
- Multi-tenancy (see [ADR-0002](adr/0002-single-organisation.md)).
- Hypervisors other than vSphere.
- Red Hat AAP as the automation backend.
