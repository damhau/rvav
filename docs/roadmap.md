# Roadmap

Milestones are ordered so that each one ends in something usable end to end.

## Status

Slice 1 is built: catalog to running VM, including the AWX post-configuration steps.
See [ADR-0006](adr/0006-contract-first-vertical-slices.md) for how slices work.

Verified so far: Go unit tests, vCenter driver tests against vcsim, workflow
tests, and an end-to-end API test against PostgreSQL 16 and vcsim. Frontend:
typecheck, lint, 43 unit tests, build, and Playwright smoke tests on mock
data. The web app was also run against the real Go API for catalog, form,
dynamic values, preview, requests and deployments.

Not verified yet: a real Temporal server, the Docker Compose stack, Keycloak
sign-in, real EfficientIP and AWX, and a full submit from the browser to the
real API (it needs Temporal).

### Open items found in slice 1
- AWX dynamic inventory endpoint and the `rvav.platform` inventory plugin (S6).
- EfficientIP client: confirm the endpoints against the current SOLIDserver API (S7).
- Inventory sync polls; switch to property collector updates (S13).
- Value sources `awx.*` (S6), REST and CEL (S8) are not implemented yet.
- Approvals API, replacing `rvav approve` on the command line (S3).
- Retry and rollback of a failed request, deployment history (S4).
- Per-field rule results in the preview, descriptions on checklist options (S8).
- CI pipeline (S2), accessibility audit (S14), Storybook.

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

## Out of scope for now

- Cost estimation, showback and chargeback.
- Multi-tenancy (see [ADR-0002](adr/0002-single-organisation.md)).
- Hypervisors other than vSphere.
- Red Hat AAP as the automation backend.
