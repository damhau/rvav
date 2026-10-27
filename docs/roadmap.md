# Roadmap

Milestones are ordered so that each one ends in something usable end to end.

## Status

Slices 1 to 3 are built: catalog to running VM, including the AWX
post-configuration steps, running for real on the Docker Compose stack, and
approvals (levels, timeouts and escalation, delegation, approvers editing
inputs, the inbox).
See [ADR-0006](adr/0006-contract-first-vertical-slices.md) for how slices
work, and [progress.md](progress.md) for the R0 work item by item.

Verified so far:
- `make check` (also in CI on every push): spec lint, generated code, Go vet
  and tests, end-to-end tests on their own PostgreSQL with vcsim, through
  Temporal's test environment and through a real Temporal dev server, OIDC
  token checks against a fake provider; web typecheck, lint, 43 unit tests,
  build and Playwright on mock data.
- The Docker Compose stack, run locally: it starts in order with health
  checks, `deploy/smoke.sh` gets a request to succeeded through the real
  Temporal server, and `pnpm e2e:stack` requests a VM in the browser and
  follows it to Ready, with dev authentication and with Keycloak sign-in
  (groups mapped to project roles).

Not verified yet: a real vCenter (S2 acceptance on the lab vCenter), real
EfficientIP and AWX, the corporate identity provider.

### Open items found in slice 1
- AWX dynamic inventory endpoint and the `rvav.platform` inventory plugin (S6).
- EfficientIP client: confirm the endpoints against the current SOLIDserver API (S7).
- Inventory sync polls; switch to property collector updates (S13).
- Value sources `awx.*` (S6), REST and CEL (S8) are not implemented yet.
- Retry and rollback of a failed request, deployment history (S4).
- Per-field rule results in the preview, descriptions on checklist options (S8).
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
