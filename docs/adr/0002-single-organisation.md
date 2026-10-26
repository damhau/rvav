# ADR-0002: Single organisation, projects as the isolation boundary

- Status: accepted
- Date: 2026-10-26

## Context
vRA supports multiple organisations (tenants). Multi-tenancy adds a tenant ID to
every table and query, tenant-scoped configuration (identity providers,
integrations, branding), and cross-tenant admin roles. It is also a common
source of data-leak bugs.

The target is one company running its own vSphere estate.

## Decision
Build for a **single organisation**. **Projects** provide the separation teams
need: membership, entitlements, quotas, placement, cost center, and visibility of
deployments.

## Consequences
- A simpler data model, queries, RBAC and admin UX; faster delivery.
- Integrations (vCenter, AWX, EfficientIP, OIDC) are configured once, globally.
- If multi-tenancy is ever needed (for example for a managed service provider),
  it would mean adding an `org_id` to the top-level tables plus a migration. That
  cost is accepted as unlikely.
