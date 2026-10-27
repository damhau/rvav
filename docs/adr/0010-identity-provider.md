# ADR-0010: Any OIDC provider; Keycloak for development and tests

- Status: accepted
- Date: 2026-10-27

## Context
rvav signs users in with OIDC and maps their groups to global and project
roles ([architecture](../architecture.md#security)). The corporate identity
provider and its group names aren't known yet, but development, tests and the
lab need sign-in now.

## Decision
- rvav works with **any OIDC provider**: authorization code with PKCE in the
  web app, bearer access tokens checked by the API (signature, issuer,
  audience `rvav`, expiry), groups read from a configurable claim
  (`RVAV_OIDC_GROUPS_CLAIM`).
- **Keycloak** is the provider for development, tests and the lab. The realm
  in `deploy/keycloak/realm-rvav.json` has the `rvav` public client with PKCE,
  an audience mapper, the `groups` and `title` claims, and users for each role.
- The corporate provider (Keycloak, Entra ID or Okta) is configured when it is
  known: issuer, client ID, group names in the group mappings, and
  `VITE_OIDC_SCOPE` with `offline_access` for providers that need it to issue
  refresh tokens (Entra ID).
- **Dev authentication** (`RVAV_AUTH_MODE=dev`) only starts with
  `RVAV_ALLOW_DEV_AUTH=1`; it is for developer machines and tests.
- The API discovers the provider on first use and retries after a failure
  (at most every 5 seconds), so it starts, and recovers, while the provider is
  down. Until then requests answer `503`, and `/readyz` fails, so a wrong
  issuer stops a rollout instead of replacing working instances. Keys that
  can't be fetched also answer `503`, not `401`.

## Consequences
- The same code path is tested against Keycloak (`pnpm e2e:stack` with
  `RVAV_STACK_AUTH=oidc`) and against a fake provider in Go
  (`TestOIDCTokens`).
- Choosing the corporate provider stays open without blocking any slice.
