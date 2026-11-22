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
| Stored secrets and secret form inputs | `secrets`, `integration_settings` and `request_secrets`, sealed | Domain-join accounts, local administrator passwords, tokens that cloud-init and AWX steps read, passwords given in forms. |
| Pictures for image fields | `form_images` | Pictures catalog administrators upload for forms ([ADR-0040](../adr/0040-file-fields.md)); anyone signed in sees them. |
| Files attached to requests | `request_files`, not sealed | Firewall rules, certificates and documents requesters attach to file fields ([ADR-0040](../adr/0040-file-fields.md)); readable by whoever reads the database or its backups. |
| Generated private keys | `deployment_keys`, sealed until the deployment's owner downloads the key once, then forgotten | Whoever holds one signs in to the deployment's VMs as their remote access user, with `sudo` ([ADR-0039](../adr/0039-guest-access-and-networking.md)). |
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
                                                   │                        ├── syslog servers and SIEM webhooks (audit forwarding)
 AWX inventory source ── API (inventory token)     │                        ├── webhooks, REST sources and calls (allowlisted hosts)
 Teams / Slack ── API (signed calls)               │                        └── the site's own workflows (their task queues)
 ServiceNow ── API (ServiceNow token)              │
 automation ── API (API token)                     └── sync ── vCenter, ServiceNow
```

**Actors:**
- **People:** requesters, approvers, project admins, administrators and
  auditors. Each is signed in by the identity provider. Roles come from
  rvav's database and from the provider's groups.
- **The local administrator** (`admin`, ADR-0030): rvav's only local
  account, for first setup and for when the provider is down.
- **Automation:** acts with API tokens.
- **AWX:** reads rvav's inventory with a token that may do nothing else.
- **The site's workers:** run custom workflows on their own task queues.
- **Teams and Slack:** call rvav back when someone presses a button.
- **ServiceNow:** requests on people's behalf, with a token that may do
  nothing else.

**What rvav trusts:**
- the identity provider's signed tokens;
- the database;
- Temporal;
- the integrations' answers, over verified TLS.

**What it doesn't trust:**
- the browser;
- callers of the API;
- every value a form, a REST source, a REST call's answer or a webhook brings.

## Threats and what answers them

| Threat | Answer | What remains |
|---|---|---|
| **Spoofing:** a forged or replayed sign-in token | Tokens are checked against the provider's keys (issuer, audience = rvav's client, expiry). Only public-key algorithms are accepted, never a shared secret or `none` (S14.6). | A stolen access token works until it expires: keep its lifetime short at the provider (5 minutes by default in Keycloak). |
| **Spoofing:** the local administrator's password guessed | Kept as an argon2id hash (PBKDF2-HMAC-SHA-512 in FIPS 140-3 mode, [fips.md](fips.md)), at most four computed at once. Given at installation or generated, and changed at the first sign-in (12 characters at least). From the third failure in a row, attempts wait 1 s, then twice as long each time, up to 15 minutes, without checking the password; an attempt is counted before it is checked, so attempts made at once can't pass the wait together. An unknown username is refused at once, taking no hash slot; an attempt waits for one only as long as its request. Every sign-in and failure is audited. `rvav admin reset-password` needs the server; it and a password change revoke the account's API tokens. | Someone guessing can keep the account waiting up to 15 minutes at a time: the ingress should rate-limit `/api/v1/auth/sign-in` per client. |
| **Spoofing:** a provider's token taken as one of rvav's own users | A provider's user is found by the token's subject. Subjects of rvav's own users are refused from any provider, whatever the token's groups: `local:` (the local administrator), `seed:` (seeded users not yet signed in) and `system:` (the inventory user and ServiceNow's). The trial sign-in says so. | |
| **Spoofing:** a server impersonating an integration | Every connection checks its server's certificate against the system's CAs and the CA bundle, set by administrators in Administration (audited) or `RVAV_CA_BUNDLE`; a pinned certificate is trusted for its connection only, and a new pin needs the password or token again, as a new address does. A connection's test checks the certificate before any password is sent. | A CA added to the bundle is trusted by every connection, for any host it signs for: add issuing CAs of your own, not public ones. Unlike a pin, it is saved without the connections' passwords and tokens given again: an administrator who adds a CA of their own and stands between rvav and a server could be sent its secret. Accepted, since that also needs a place on the network, and the bundle saved is in the audit log in full, with who saved it (`settings.integration.trust`). `*_INSECURE` turns the checks off. |
| **Information disclosure:** the Active Directory service account's password | rvav binds over LDAPS or StartTLS, the domain controller's certificate checked as any integration's before the password is sent; plain LDAP needs an administrator to tick it, warned, and the password given again ([ADR-0064](../adr/0064-active-directory-computer-objects.md)). The password is sealed as other integrations' are. | With plain LDAP ticked, the password and the objects cross the network in the clear: for labs only. |
| **Elevation of privilege:** rvav's service account in Active Directory | It makes and deletes computer objects in the OUs the placements name, and deletes only those whose DN and description say rvav made them: an object of the same name that rvav didn't make fails the request, and one whose description changed since is left, not deleted or renamed. A blueprint's own OU counts only where the project's OU row lets it, under the base DN ([ADR-0064](../adr/0064-active-directory-computer-objects.md)). | Its rights are the directory's to limit: delegate creating and deleting computer objects on those OUs only ([integrations.md](../integrations.md#active-directory-permissions-service-account)). |
| **Spoofing:** someone signing in who shouldn't, or trying to | Every sign-in, failed sign-in and sign-out, and each API token's first use a day, is audited with the address it came from, the user agent, how (web app or rvavctl; OIDC, device code, API token or local) and why it failed ([ADR-0058](../adr/0058-audit-forwarding-and-sign-ins.md)). The address is the peer's, or the one the trusted proxies (`RVAV_TRUSTED_PROXIES`) put in `X-Forwarded-For`, read from the right, so a client can't choose it. | A failure before there is a token (a provider refusing a disabled user, a state mismatch, a code that can't be exchanged) is reported by the web app or rvavctl to an endpoint anyone can call: rvav records it as `reported_by: client`, keeps at most 10 a minute from one address (an IPv6 /64) and 300 in all per API process, and checks its fields, but can't tell a real report from a made-up one. Past 300, real failures go unrecorded too: the ingress should rate-limit `/api/v1/auth/sign-ins`. The local administrator's unknown usernames share these limits and are recorded as `unknown`. |
| **Spoofing:** sign-in skipped | `dev` sign-in starts only with `RVAV_ALLOW_DEV_AUTH`. The Helm chart sets OIDC on every container, which neither its values nor its Secret can change. | |
| **Spoofing:** an API token guessed or leaked | 32 random bytes with an `rvav_pat_` prefix (secret scanners find it). Only its SHA-256 is stored. It can expire and be revoked, and its last use is recorded. A token gets no roles from groups, so it can't approve. An inventory token reads only the inventory. The one Administration makes for AWX is for rvav's inventory user, an auditor no one signs in as, and making another revokes it. | A token acts as its user until revoked: give automation its own user. |
| **Spoofing:** ServiceNow's token stolen | Only `/api/v1/servicenow/` takes it, and nothing else is taken there. It reads the catalog and users' entitlements, and requests as a user what that user may request themselves, with their approvals. Calls from outside `allowed_sources` are refused, read after the trusted proxies; submissions are limited to `requests_per_minute` across the API processes, reads to ten times that in each, answered 429 beyond. Each request made, refused or limited is audited with the token and the address. It expires in a year, and Revoke ends it at once ([ADR-0065](../adr/0065-servicenow.md)). | With no `allowed_sources`, the token works from anywhere until revoked: set them. |
| **Spoofing:** a ServiceNow order in someone else's name | rvav believes the user ServiceNow names: the scripts take it from the requested item's **Requested for**, which ServiceNow's ACLs guard, never from a variable. The user is matched by email first, and an ambiguous user name is refused. The request gets that user's entitlements and approvers, who see it as made via ServiceNow. | Anyone who can set **Requested for** in ServiceNow can request as that person what they may request: ServiceNow's ACLs decide who can. |
| **Elevation of privilege:** rvav's account in ServiceNow | It writes CIs of one class through the IRE, retires (never deletes) the ones rvav made, and writes the state and work notes of the requested items rvav took. | Its rights are ServiceNow's to limit: give it the roles in [integrations.md](../integrations.md#servicenow-roles-rvavs-account) only. |
| **Spoofing:** Teams or Slack calls | Slack calls are checked against the signing secret, Teams calls against Bot Framework's published keys. | |
| **Tampering:** a request changed after approval | Approver edits are checked against the form and evaluated again. Every decision and change is in the request's history and the audit log. | |
| **Tampering:** a page of another site acting in the user's name | The provider's token lives in the page's memory, never in a cookie, so there's nothing to forge cross-site. The local administrator's session is a cookie: `HttpOnly`, `SameSite=Strict`, `Secure` over HTTPS, and a change made with it needs `X-Requested-With`, which another origin can't send (ADR-0030). CORS allows only `RVAV_CORS_ORIGINS`. The console's WebSocket checks its Origin. | |
| **Tampering:** cross-site scripting | React escapes what it renders. The Content-Security-Policy allows scripts from the web app only (and the console's SDK from `RVAV_WEB_SCRIPT_SOURCES`), images from it and `RVAV_WEB_IMAGE_SOURCES`, and no objects or other sites' frames (S14.6). A form's image and link take only `https://`, `http://` or this site's addresses, checked when the version is saved. | Styles allow inline (the UI libraries insert them). |
| **Repudiation:** denying an action | Changes to the catalog, policies, projects and members, integrations, settings and tokens, approvers' edits, and sign-ins, are in the audit log with their user, which can be searched and exported. Administrators forward it to syslog servers and SIEM webhooks (Administration › Audit forwarding), each event queued in the transaction that writes it and sent until delivered. Requests keep who asked, who decided and what each step did. | The audit log is in rvav's database: an administrator of the database can change what isn't forwarded yet, or remove a destination. Forward it, and watch for a destination's backlog (`RvavQueueBacklog`). |
| **Tampering:** the audit log on its way to a SIEM | Syslog goes over TLS (RFC 5425), the server's certificate checked against the system's CAs, the CA bundle and the destination's own CAs, and rvav presents a client certificate when the server asks, its key sealed as a stored secret. A webhook's body is signed (HMAC-SHA256, `X-Rvav-Signature`) and goes only to an allowed host, without following redirects. | Syslog over TCP or UDP goes in clear and unauthenticated: for labs only. |
| **Information disclosure:** secrets in the database, backups or Temporal's history | Stored secrets, integration passwords and secret form inputs are sealed (AES-256-GCM, the name as associated data; a project's own secret under `project:<id>:<name>`, so it opens only in its scope, and only that project's requests open it). The API never returns them. Secret inputs are held in requests, plans and workflow histories as a marker that is opened only to launch the AWX job or render cloud-init user data, and are deleted once the request can't run again (S14.6). Stored secrets named in templates, `{{ secret "name" }}` in user data and `${secret.name}` as an AWX step's binding, travel by name only: the worker opens them as it writes the user data or launches the job. The name is a literal of the blueprint, so a requester's input never chooses which secret is opened; anywhere else, where it would be worked out with the request, saving refuses it ([ADR-0038](../adr/0038-names-groups-secrets-properties.md)). A remote access's password or key is worked out by the worker as it writes the user data; a generated key pair is made there, its private key sealed in `deployment_keys` and never in an activity's input or result ([ADR-0039](../adr/0039-guest-access-and-networking.md)). Cloud-init clears user data carrying a password from guestinfo once it read it. | AWX shows a job's extra variables to those who can see the job: restrict the templates' visibility, or use AWX credentials for secrets. Cloud-init user data is readable in vCenter's guestinfo by vCenter users. |
| **Information disclosure:** other projects' data | Every read is filtered by the projects where the caller's roles give `project.see`, and each check is a permission, held in a project or for all of rvav, never a role name ([ADR-0034](../adr/0034-custom-roles.md)). Administrators and auditors hold `project.see` for all of rvav. A live stream checks what its caller may see when it opens, with their roles read once it is subscribed, and every change of roles, assignments or group mappings ends every stream with `resync`, so each reconnects with the permissions held then; a replay from before such a change gets `resync` too. | Ending only the streams of those concerned is left for later: changes of access are rare. |
| **Information disclosure:** server-side request forgery | REST value sources, form library entries and validations ([ADR-0043](../adr/0043-form-library.md)), a composite's REST calls and webhooks call only the hosts allowed for each (Administration › Integrations › Allowed hosts, an administrator's setting, audited; or `RVAV_REST_SOURCE_HOSTS` and `RVAV_WEBHOOK_HOSTS`, which lock them). Inputs and step outputs fill a URL's path and query, never its host, and redirects aren't followed. | |
| **Information disclosure:** a secret left behind by a REST call | Resolved in the activity, so Temporal holds names only; the answer is scrubbed of the secrets used and the request's secret inputs, in their usual encodings, and its headers aren't kept; errors name the method, host and status only; a requester's topology masks the URL and body ([ADR-0033](../adr/0033-composite-step-kinds.md)). A poll's checks are such calls. Any step's CEL that fails (a `when`, an input, a URL, a body, a case, a message) is named with its literals masked and why in rvav's words, never cel-go's, which quote a key a map lacks or a pattern (`TestStepExpressionErrors`). | An API that transforms a secret (hashes it, say) before echoing it isn't caught: `keep` picks only what a step needs. |
| **Information disclosure:** a person's answer in a step | A step's form can't ask for a secret: refused on save, as an answer travels through Temporal's history to every later step. The audit, events and deliveries don't have it. Only those it names, and those who see everything, read its question and answer; a requester's topology shows that it waits, not who else it names or what it asks ([ADR-0033](../adr/0033-composite-step-kinds.md)). | A comment is free text, stored and read by the requester, and nothing stops someone from typing a secret into a text answer. |
| **Information disclosure:** the audit log | It records no token, authorization code, device code, state or password: a sign-in records who and how, a refused token the verifier's reason, a failure the provider's error code and description, an API token its ID and name (`TestSignInsAudited`, `TestCLIOIDC`). Its readers (`audit.read`) and the SIEMs it is forwarded to see who signed in from where. | A provider's error description is the provider's text: rvav keeps it to 500 characters on one line. |
| **Information disclosure:** logs | Secrets are never logged, but for the local administrator's generated first password, once, by the process that made the account (it must be changed at the first sign-in). Sign-in and API tokens, and the local session's cookie, travel in headers, which neither the API nor nginx logs. | A console ticket is in the WebSocket's URL, so access logs have it: it is single use and valid one minute. |
| **Denial of service:** large or slow requests | Bodies are limited (1 MB, 2 MB for catalog YAML; 10 MB at nginx), and so is the time to send headers (10 s). A file field's upload reads no more than the field's largest file, 25 MB at most, a picture for an image field 2 MB, and no one has more than 20 files waiting for their request. Workflows retry with backoff, so an integration that is down delays requests but doesn't lose them. | rvav doesn't rate-limit callers: the ingress should (per client, on `/api/`). |
| **Tampering:** a file that runs in a reader's browser | A file field takes only types of a fixed list, never HTML or SVG. Its content is sniffed and must be what its extension says, whatever type the client claims. A file is sent only as a download: `application/octet-stream`, `Content-Disposition: attachment`, `nosniff`, and a sandboxing Content-Security-Policy ([ADR-0040](../adr/0040-file-fields.md), `TestFileFields`). Only the requester, the request's approvers and its project's administrators download it. A picture for an image field is PNG, JPEG, GIF or WebP, never SVG, sniffed the same way, and sent with its checked type and `nosniff`; only catalog administrators upload one (`TestFormImages`). | A valid PDF or Office document can carry what its reader runs: rvav doesn't scan files. |
| **Tampering:** a logo or a notification template that runs in other people's browsers or mail | A logo is a PNG, a JPEG or an SVG that holds only drawing elements; an SVG with scripts, event handlers, links, animations, `foreignObject`, a DOCTYPE or entities, or references outside itself is refused, not cleaned. It is served with its sniffed type, `nosniff` and a sandboxing Content-Security-Policy. A template's HTML goes through `html/template`, and values are escaped for each channel, so a request's title can't add markup; the preview shows an email in a frame with scripts off ([ADR-0059](../adr/0059-notification-templates-and-branding.md), `TestBranding`, `TestNotificationTemplates`). Only administrators change them, audited. | An administrator can put misleading text around rvav's buttons in an email or a card: the audit log says who did. |
| **Tampering:** a requester's value in a message to other people | A message's values are data: mail goes through `html/template` with its subject on one line, a Teams card's markdown is escaped, and Slack gets it as plain text, with no mention or link (`TestStepMessageEscapes`). A template reaches only inputs and outputs, a secret input is refused on save and fails the step if reached another way, and the filled text is scrubbed of the request's secret inputs. It stays out of the timeline, the outputs, a requester's topology and Temporal's history |
| **Denial of service:** a poll calling an API over and over | Refused on save unless it checks at most once a minute, has a deadline and repeats no POST or PATCH; it calls only allowed hosts, and a long wait keeps Temporal's history bounded by continuing as new ([ADR-0033](../adr/0033-composite-step-kinds.md)). | Each request polls for itself: many requests at a short interval are that many calls. Catalog admins set the interval. |
| **Elevation of privilege:** a role taken | Roles are rvav's, given to users or mapped from the provider's groups by those holding `access.manage` (administrators), and `project-admin` in a project also by whoever creates it (`project.create`), who names its first project admins, themselves included; a role can't be given where it grants nothing, and rvav refuses the change that would leave no one holding `access.manage`, or a project no one holding `project.members.manage`. Each change is audited. Catalog, policy and integration changes need their own permissions, which only administrators and catalog admins hold among the built-in roles; a custom role that holds one is made, and given, by someone holding `access.manage`. Approvers are named groups or users, and a requester can't approve their own request. A composite's wait for a person is answered only by those it names, once, checked on the server; an administrator isn't named by being one, and it can't be delegated. Only `requester: true` or `owner: true`, written in a published item, lets someone answer their own request. | |
| **Elevation of privilege:** code run by rvav | Policies are CEL (no I/O, with a cost limit, S14.6). String functions refuse to build a string over 1 MiB or a list over 100,000 items, and are charged for what they build (T1.7). Cloud-init templates are Go templates over given values. Only administrators write either. On Kubernetes the chart runs every container without root (rvav as 65532, nginx as 101), on a read-only file system, without a service account token. The VM installation's web container starts nginx as root, as nginx's image does. | Custom workflows run the site's own code on its own workers, with what rvav gives them. |
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

- **`make vuln`** fails on any of these:
  - **Go code:** `govulncheck` reports a vulnerability the code reaches;
  - **web app:** `pnpm audit` reports one in its dependencies, including
    build and test tools;
  - **images:** Trivy finds a HIGH or CRITICAL vulnerability with a fix in
    either image.
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
    off): a private CA in the CA bundle (Integrations › Trusted
    certificates), or a self-signed server's certificate pinned for its
    connection.
- **FIPS 140-3:** where a requirement asks for a validated module, run
  every rvav process with `RVAV_FIPS=on` (the chart's `fips.mode`): rvav's
  image carries the Go Cryptographic Module v1.0.0. What it changes, what
  `only` refuses and what stays outside it are in [fips.md](fips.md).
- **The secrets key:** keep it in the vault, apart from database backups
  ([backups.md](backups.md)). Rotate it with `RVAV_SECRETS_OLD_KEYS` and
  `rvav secrets rotate`, which seals again everything sealed with an old
  key: stored secrets, integrations' passwords and tokens, the passwords
  of the vCenters and NSX Managers added, requests' secret inputs,
  generated private keys not downloaded yet, and audit webhooks' header
  values.
- **Service accounts:** with only the permissions in
  [integrations.md](../integrations.md); the console account may only
  open consoles; the Active Directory account may only create and delete
  computer objects in the OUs rvav uses, over LDAPS or StartTLS; the
  ServiceNow account holds no admin role.
- **ServiceNow's token:** set its allowed sources to the instance's
  outbound addresses or its MID server's.
- **The identity provider:** short access tokens; rvav's client public
  with PKCE, its redirect URIs limited to rvav's address and rvavctl's
  loopback address (`http://127.0.0.1/callback`); groups kept to those
  rvav maps.
- **rvavctl:** a sign-in is kept in the user's configuration directory,
  readable by them only; `rvavctl logout` forgets it.
- **The network:**
  - only the ingress reaches the web app and the API;
  - metrics (`:9090`) stay inside;
  - rvav reaches only its integrations and the hosts in its allowlists;
  - rate limiting at the ingress.
- **AWX:** restrict who can see the jobs rvav launches; their extra
  variables include the form's inputs.
- **The audit log:** forward it over TLS to the SIEM (Administration ›
  Audit forwarding), and set `RVAV_TRUSTED_PROXIES` to the addresses of
  the web containers and the ingress controller, so that it records
  where requests came from rather than the proxy's address
  ([configuration.md](configuration.md)).
- **Watching:** the alerts in [observability.md](observability.md), the
  audit log, and the daily security run.
