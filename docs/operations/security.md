# Security

This page covers:
- rvav's threat model;
- the review of sign-in sessions, API tokens and secrets made for 1.0
  (S14.6);
- how dependencies and images are scanned, and the SBOM published with
  each release;
- what an installation must do on its side.

The external penetration test is the organisation's (Lab acceptance).

## What rvav protects

| Asset | Where | Why it matters |
|---|---|---|
| Access to vCenter, EfficientIP and AWX | rvav's service accounts: passwords and tokens sealed in the database, or in the installation's Secret | Whoever holds them can create, change and delete VMs, addresses, DNS records and jobs across the estate. |
| The secrets key (`RVAV_SECRETS_KEY`) | The installation's Secret or vault, never the database | It opens every sealed value. |
| Stored secrets and secret form inputs | `secrets`, `integration_settings` and `request_secrets`, sealed | Domain-join accounts, local administrator passwords, passwords given in forms. |
| Who may do what | Users, project roles and group mappings in the database; groups from the identity provider | Approving a request, or acting in another team's project. |
| API tokens | Their SHA-256 hashes, in `api_tokens` | Automation acting as a user. |
| The audit log and request history | The database | The record of who did what; also evidence. |
| Workflow state | Temporal's database | Requests in flight, and their inputs and plans. |

## Actors and trust boundaries

```
 browser ──HTTPS── ingress ── web (nginx) ── API ──┬── PostgreSQL (rvav)
    │                                              ├── Temporal ── worker ──┬── vCenter
    └── identity provider (OIDC)                   │                        ├── EfficientIP
                                                   │                        ├── AWX
 AWX inventory source ── API (inventory token)     │                        ├── webhooks, REST sources (allowlisted hosts)
 Teams / Slack ── API (signed calls)               │                        └── the site's own workflows (their task queues)
 automation ── API (API token)                     └── sync ── vCenter
```

**Actors:**
- **People:** requesters, approvers, project admins, administrators and
  auditors. Each is signed in by the identity provider. Roles come from
  rvav's database and from the provider's groups.
- **Automation:** acts with API tokens.
- **AWX:** reads rvav's inventory with a token that may do nothing else.
- **The site's workers:** run custom workflows on their own task queues.
- **Teams and Slack:** call rvav back when someone presses a button.

**What rvav trusts:**
- the identity provider's signed tokens;
- the database;
- Temporal;
- the integrations' answers, over verified TLS.

**What it doesn't trust:**
- the browser;
- callers of the API;
- every value a form, a REST source or a webhook brings.

## Threats and what answers them

| Threat | Answer | What remains |
|---|---|---|
| **Spoofing:** a forged or replayed sign-in token | Tokens are checked against the provider's keys (issuer, audience = rvav's client, expiry). Only public-key algorithms are accepted, never a shared secret or `none` (S14.6). | A stolen access token works until it expires: keep its lifetime short at the provider (5 minutes by default in Keycloak). |
| **Spoofing:** sign-in skipped | `dev` sign-in starts only with `RVAV_ALLOW_DEV_AUTH`, and the Helm chart refuses it. | |
| **Spoofing:** an API token guessed or leaked | 32 random bytes with an `rvav_pat_` prefix (secret scanners find it). Only its SHA-256 is stored. It can expire and be revoked, and its last use is recorded. A token gets no roles from groups, so it can't approve. An inventory token reads only the inventory. | A token acts as its user until revoked: give automation its own user. |
| **Spoofing:** Teams or Slack calls | Slack calls are checked against the signing secret, Teams calls against Bot Framework's published keys. | |
| **Tampering:** a request changed after approval | Approver edits are checked against the form and evaluated again. Every decision and change is in the request's history and the audit log. | |
| **Tampering:** a page of another site acting in the user's name | The token lives in the page's memory, never in a cookie, so there's nothing to forge cross-site. CORS allows only `RVAV_CORS_ORIGINS`. The console's WebSocket checks its Origin. | |
| **Tampering:** cross-site scripting | React escapes what it renders. The Content-Security-Policy allows scripts from the web app only (and the console's SDK from `RVAV_WEB_SCRIPT_SOURCES`), and no objects or other sites' frames (S14.6). | Styles allow inline (the UI libraries insert them). |
| **Repudiation:** denying an action | Changes to the catalog, policies, projects and members, integrations, settings and tokens, and approvers' edits, are in the audit log with their user, which can be searched and exported. Requests keep who asked, who decided and what each step did. | The audit log is in rvav's database: an administrator of the database can change it. Export it to the archive. |
| **Information disclosure:** secrets in the database, backups or Temporal's history | Stored secrets, integration passwords and secret form inputs are sealed (AES-256-GCM, the name as associated data). The API never returns them. Secret inputs are held in requests, plans and workflow histories as a marker that is opened only to launch the AWX job or render cloud-init user data, and are deleted once the request can't run again (S14.6). | AWX shows a job's extra variables to those who can see the job: restrict the templates' visibility, or use AWX credentials for secrets. Cloud-init user data is readable in vCenter's guestinfo by vCenter users. |
| **Information disclosure:** other projects' data | Every read is filtered by the caller's projects (viewers read, members request, project admins manage). Administrators and auditors see all. | |
| **Information disclosure:** server-side request forgery | REST value sources and webhooks call only `RVAV_REST_SOURCE_HOSTS` and `RVAV_WEBHOOK_HOSTS`. | |
| **Information disclosure:** logs | Secrets are never logged. Sign-in and API tokens travel in a header, which neither the API nor nginx logs. | A console ticket is in the WebSocket's URL, so access logs have it: it is single use and valid one minute. |
| **Denial of service:** large or slow requests | Bodies are limited (1 MB, 2 MB for catalog YAML; 10 MB at nginx), and so is the time to send headers (10 s). Workflows retry with backoff, so an integration that is down delays requests but doesn't lose them. | rvav doesn't rate-limit callers: the ingress should (per client, on `/api/`). |
| **Elevation of privilege:** a role taken | Roles are rvav's, mapped from the provider's groups by administrators. Catalog, policy and integration changes are for administrators only. Approvers are named groups or users, and a requester can't approve their own request. | |
| **Elevation of privilege:** code run by rvav | Policies are CEL (no I/O, with a cost limit, S14.6). Cloud-init templates are Go templates over given values. Only administrators write either. Images run without root, on a read-only file system, without a service account token. | Custom workflows run the site's own code on its own workers, with what rvav gives them. |
| **Elevation of privilege:** a vulnerable dependency or image | Scanned on every push and daily (below). | |

## Review of sessions, API tokens and secrets (S14.6)

**Fixed:**

1. **Secret form inputs were in plain text in requests, plans and Temporal's
   history.** The API hid them, but the database, its backups and the
   workflow history held them. They are now sealed in `request_secrets`
   and stand as `{"$secret": "<field>"}` everywhere else:
   - the AWX job step opens them only to launch;
   - cloud-init user data opens them only to render;
   - an approver's edit is evaluated with them, then seals them again;
   - `rvav secrets rotate` seals them again with the new key;
   - they are deleted once the request succeeded, was rejected or
     cancelled (a failed one keeps them for a retry).

   Approvers' changes to secret inputs show as `••••` in the request
   and the audit log.
2. **A field with the `secret` widget but without `secret: true` was not
   treated as secret**, except by drafts. The API returned its value, and
   events carried it. The widget alone now makes it secret.
3. **Tokens signed with a shared secret** were accepted whenever the
   provider listed such an algorithm. Only public-key algorithms are
   accepted now.
4. **Policy expressions had no cost limit.** One written to loop over large
   lists could hold a CPU. Evaluation now stops past a cost far above any
   real policy.
5. **The web app loaded fonts from Google** (a request to a third party
   from every browser), and sent no Content-Security-Policy. Fonts are
   now served with the app. The web app now sends these headers:
   - `Content-Security-Policy`;
   - `X-Frame-Options`;
   - `Permissions-Policy`;
   - `Strict-Transport-Security`.

**Checked, no change needed:**

- **Sessions:**
  - authorization code with PKCE;
  - tokens in memory only (never `localStorage` or cookies);
  - silent renewal;
  - signing out ends the provider's session.
- **API tokens:** hashed, can expire, scoped, and revocable by their owner
  and by administrators.
- **Console tickets:** single use, stored hashed, valid one minute,
  shared by API replicas.
- **The secrets box:**
  - AES-256-GCM with a random nonce;
  - the value's name as associated data (a sealed value can't be moved
    to another name);
  - key IDs, so values are rotated without downtime.
- **Errors:** messages and logs carry no secrets.

## Scanning and SBOMs

- **`.github/workflows/security.yml`** runs on every push and daily. It fails on any of these:
  - **Go code:** `govulncheck` reports a vulnerability the code reaches;
  - **web app:** `pnpm audit` reports one in its dependencies, including
    build and test tools;
  - **images:** Trivy finds a HIGH or CRITICAL vulnerability with a fix in
    either image.
- **Locally:** `make vuln` runs the same three checks.
- **`.github/workflows/release.yml`**, on a tag `v1.2.3`:
  - scans the images, then pushes them to `ghcr.io/damhau/rvav` and
    `ghcr.io/damhau/rvav-web` as `1.2.3`;
  - creates the GitHub release with the Helm chart and three CycloneDX
    SBOMs: each image (its OS packages and Go modules), and the web app's
    npm dependencies (its image holds only the build).

  Run by hand, it does the same without publishing. `make sbom` writes
  the SBOMs locally.
- **Base images:** `distroless/static` (nonroot) for rvav, nginx on
  Alpine (packages upgraded at build) for the web app.

## The installation's part

- **TLS everywhere:**
  - HTTPS at the ingress (HSTS takes effect there);
  - `sslmode=verify-full` to PostgreSQL;
  - `RVAV_TEMPORAL_TLS` with mutual TLS or an API key;
  - the integrations' certificates checked (every `*_INSECURE` setting
    off).
- **The secrets key:** keep it in the vault, apart from database backups
  ([backups.md](backups.md)). Rotate it with `RVAV_SECRETS_OLD_KEYS` and
  `rvav secrets rotate`.
- **Service accounts:** with only the permissions in
  [integrations.md](../integrations.md); the console account may only
  open consoles.
- **The identity provider:** short access tokens; rvav's client public
  with PKCE, its redirect URIs limited to rvav's address; groups kept to
  those rvav maps.
- **The network:**
  - only the ingress reaches the web app and the API;
  - metrics (`:9090`) stay inside;
  - rvav reaches only its integrations and the hosts in its allowlists;
  - rate limiting at the ingress.
- **AWX:** restrict who can see the jobs rvav launches; their extra
  variables include the form's inputs.
- **Watching:** the alerts in [observability.md](observability.md), the
  audit log, and the daily security run.
