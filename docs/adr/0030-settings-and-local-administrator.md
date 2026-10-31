# ADR-0030: Settings in the UI, and the local administrator

## Status
Accepted (U1, after C1). Revises how saved settings take effect in
[ADR-0020](0020-administration.md), and sign-in in
[ADR-0010](0010-identity-provider.md).

## Context
vRA is installed with what it needs to start. Everything else is set from
its UI: identity, cloud accounts, integrations, notifications. Its first
sign-in is a local account made at install.

rvav still needs environment variables for its identity provider and for
several settings, and a saved setting waits for rvav's processes to
restart (ADR-0020). An administrator who only has the UI can't do either.
There's also no way in at first start: sign-in needs a provider, and roles
come from group mappings that are made in the UI.

## Decision

### What is set at install, and what in the UI
- **At install** (environment variables, Helm values), shown read only on
  the System page:
  - the database (`RVAV_DATABASE_URL`);
  - the secrets key (`RVAV_SECRETS_KEY`, `RVAV_SECRETS_OLD_KEYS`);
  - the listen addresses (`RVAV_HTTP_ADDR`, `RVAV_METRICS_ADDR`);
  - Temporal (`RVAV_TEMPORAL_*`);
  - the public URL (`RVAV_PUBLIC_URL`).

  These are what a process needs before it can read the database, or what
  locates rvav itself.
- **In the UI:** everything else:
  - the identity provider;
  - the connections (vCenter, AWX, EfficientIP, NSX, Git, vRA);
  - notifications;
  - the hosts value sources and webhooks may call;
  - the sync intervals;
  - the console;
  - the inventory token: rvav makes it for a user of its own
    (`system:inventory`, an auditor no one signs in as) and puts it in
    AWX's credential;
  - trusted certificates.

  Each is stored as ADR-0020 stores integration settings, its secret
  sealed.
- **An environment variable still wins.** A setting given at install is
  used, and its field is shown locked (ADR-0020). Declarative deployments
  keep working unchanged.

### How a saved setting takes effect
Without a restart by hand, in one of two ways:
- **On next use:**
  - the identity provider (the token check is built again);
  - notifications (SMTP, Teams, Slack, with their endpoints);
  - the allowed hosts;
  - the intervals, kept with their connection's settings;
  - the console: its account's vCenter session is made again when the
    account changes, and the WebMKS SDK's scripts are read when a console
    opens;
  - vRA: the importer signs in for each read.

  Each process reads the current settings revision before using one, at
  most every 2 s, and builds what it needs again when the revision has
  moved. A notification channel is built again only when its own settings
  change, so Teams' tokens are kept otherwise.
- **A secret only goes where it was given for** (ADR-0020): a new address,
  a certificate pinned, or TLS checks turned off, needs it again. The console's account signs in
  to vCenter's address, so its saved password is kept with the vCenter
  connection it was given for, and isn't used with another until given
  again; while a process is about to start again with new vCenter
  settings, VMRC asks each user to sign in.
- **Drain and restart:** the connection clients (vCenter, AWX, EfficientIP,
  NSX, Git) hold sessions, and the Temporal worker and the sync loops
  are built at start. Swapping them under a running activity is the risk
  ADR-0020 avoided. The API holds them too: the pickers' AWX and
  EfficientIP. Instead, every process watches for newer
  connection settings and drains:
  - it compares the connections saved now with those it started with (the
    configuration less what is read on next use). An identical save, or a
    change to what is read on next use, drains nothing;
  - it waits until the connections have been left alone for 3 s, so that
    settings saved together apply with one restart;
  - it takes no new task and lets its running activities finish, for at
    most `RVAV_DRAIN_TIMEOUT` (5 minutes). An activity still running then
    is cancelled and retried by Temporal, and is idempotent;
  - an API answers `/readyz` with 503 for 6 s first, so the load balancer
    sends requests elsewhere, then lets requests running finish and ends
    the event streams, which clients reopen. APIs drain one at a time,
    claimed under an advisory lock: the next once the one before has
    been started again, not just exited; one that stopped recording
    itself holds the others back for 30 s at most;
  - the sync loops stop; a pass cut short is done again at the next start;
  - it exits with status 3.

  Its supervisor starts it again with the new settings: Kubernetes (the
  container restarts), Compose (`restart: on-failure` in development,
  `always` on a VM) or systemd (`Restart=always`). `rvav dev`, which has
  none, starts itself again in place. Kubernetes counts it as a container
  restart: several within ten minutes of each other wait 10 s, 20 s,
  40 s… (its back-off), which saving the setup checklist's connections
  one after the other can reach; saving them within 3 s of each other
  applies them with one.
- Each process records itself in the `processes` table every 5 s: role,
  host, version, the settings revision it runs with, running or draining.
  One that exits to apply settings is kept, restarting, until its
  successor (of the same role and host: the same pod, container or VM)
  records itself, or for 10 minutes: meanwhile nothing is applied, and
  no other API drains. `GET /admin/processes` lists those seen in the
  last 30 s, and those restarting. A setting read
  on next use moves a process's revision as soon as it reads it. An
  integration is `applying` while a process runs with an earlier
  revision than the integration's; the UI shows each process draining and
  starting again, then the change applied, in place of ADR-0020's restart
  notice. Each drain and each exit to apply settings is audited
  (`settings.process_drain`, `settings.process_restart`).

### The local administrator
- **One local account,** `admin`, with the global `admin` role: the way in
  at first start, and when the identity provider is down (break-glass).
  Everyone else signs in through the identity provider. There are no other
  local accounts.
- **Made at first start** by the API: user `usr_admin`, subject
  `local:admin`.
  - Its password is `RVAV_ADMIN_PASSWORD` (a Helm value from a Secret) if
    given.
  - Otherwise rvav generates one and writes it to the API's log once, from
    the process that made the account.
  - After that, `RVAV_ADMIN_PASSWORD` is ignored: it is the first password,
    not a setting.
- **Stored as an argon2id hash** (64 MiB, one pass, two lanes, a 16-byte
  salt), never in clear. At most four hashes are computed at once, so
  sign-in attempts in parallel can't take the API's memory.
- **Changed at first sign-in,** and after a reset. Until then, the session
  may only change the password, read who it is, and sign out. A new
  password has at least 12 characters and differs from the current one and
  from the username.
- **Failed attempts slow sign-in down:**
  - From the third failure in a row, the account refuses attempts for 1 s,
    then 2 s, 4 s, and so on, up to 15 minutes. While it waits, the answer
    is `429` with `Retry-After`, and the password isn't checked.
  - An attempt is counted before its password is checked, in one statement
    with the check that the account isn't waiting, so attempts made at once
    can't all pass: the fourth waits as the next one would.
  - A successful sign-in starts again from zero.
  - An unknown username is refused at once. The one account's name,
    `admin`, is no secret, and checking a password against nothing would
    only take one of the four hash slots the administrator's sign-in waits
    for. An attempt waits for a slot only as long as its request does.
- **Audited:** a sign-in (`auth.sign_in`), a failure
  (`auth.sign_in_failed`), a password change (`auth.password_changed`), a
  reset (`auth.password_reset`) and a sign-out (`auth.sign_out`).
- **Reset from the command line:** `rvav admin reset-password` sets a new
  generated password (or one read from standard input), to be changed at
  the next sign-in. It ends every session of the account and revokes its
  API tokens, as a password change does (but for the session changing it).
  While the password must be changed, the account's API tokens are
  refused.

### How a local sign-in is held beside the provider's tokens
- **The provider's users** keep bearer access tokens, kept in the
  browser's memory by the web app (ADR-0010). **API tokens** are
  unchanged.
- **The local administrator** gets a server-side session:
  - a random 32-byte ID, kept as its SHA-256 hash;
  - sent as the `rvav_session` cookie: `HttpOnly`, `SameSite=Strict`,
    `Secure` over HTTPS, path `/api/`;
  - it ends after 30 minutes without a request, and 12 hours after sign-in
    in any case;
  - signing out deletes it, and a password change ends every other session
    of the account.
- **Which one is used:** a request with an `Authorization` header is
  checked as a token, as before. Without one, a session cookie is checked.
  Without either, the request is unauthenticated (or the dev user in dev
  mode). Every way gives the same principal: roles and project memberships
  work alike.
- **Requests forged from another site:** a request authenticated by the
  cookie that changes something (not `GET`, `HEAD` or `OPTIONS`) must carry
  `X-Requested-With`. The web app sends it on every request. An origin
  not in `RVAV_CORS_ORIGINS` can't add it without a CORS preflight that
  rvav refuses.
  `SameSite=Strict` already keeps the cookie off cross-site requests.

### The sign-in page
- The web app opens on a sign-in page when nobody is signed in. It offers
  the local account, and "Sign in with" the identity provider once one is
  set.
- The way chosen is remembered in the browser, so the provider's users go
  straight to it next time, as before. Signing out forgets it.
- `/sign-in` itself never goes on to the provider by itself, whatever is
  remembered: it is the way in when the provider is down. A provider that
  doesn't answer (its discovery document or token endpoint) brings the
  browser back to the sign-in page, saying so, the local account below.
  An API that doesn't answer (starting again with newer settings) is said
  as such, with a retry, not as "no identity provider".
- rvav starts without an identity provider: `RVAV_OIDC_ISSUER` is no
  longer required. Until one is set, only the local administrator and API
  tokens sign in.
- `/readyz` waits for the provider only when `RVAV_OIDC_ISSUER` sets it,
  as before. One set in Administration doesn't hold it: a wrong one, or
  one that is down, must not keep the local administrator from signing
  in to correct it.

### The identity provider in the UI
- Administration sets the provider as an integration (`oidc`):
  - the issuer;
  - the discovery URL, when the API reaches the provider at another
    address;
  - the client ID, the audience tokens must have;
  - the scopes (`RVAV_OIDC_SCOPES`);
  - the claim holding groups or roles (`groups`, or `roles` for Entra ID
    app roles).
- **Its test** reads discovery and the keys. A trial sign-in, in a new
  window, then shows the token's claims and the roles the group mappings
  would give. Both use the settings in the form, before they are saved:
  - The page may reach only the saved provider's origin
    (Content-Security-Policy), and the browser fetches tokens there. So a
    test that reaches the provider lets pages reach its origin for ten
    minutes (`oidc_trial_origins`). This gives nothing an administrator
    couldn't give by saving the provider.
  - The new window runs the whole sign-in itself, its page loaded with
    that origin allowed, and passes the access token back to the
    Administration page.
  - The API checks the token as it would a request's (signature, issuer,
    audience, expiry, against the settings given). It says why it refuses
    one, with the usual fixes: a token for Microsoft Graph, a v1 token, a
    missing audience. Nothing is saved, and no user is made.
- **Applied on next use:**
  - The token check reads the settings revision, at most every two
    seconds.
  - When the provider's settings changed, the check is built again, and
    tokens checked with the earlier ones are forgotten.
  - A provider changed or cleared no longer signs anyone in.
- **The web app reads its sign-in settings from the API** (public
  `GET /auth/options`), no longer from nginx's environment.
- **The Content-Security-Policy follows the setting:**
  - nginx asks the API for the origins when it serves the page
    (`auth_request`, the `X-Rvav-Idp-Origins` header).
  - The API gives only an `http` or `https` scheme, host and port.
  - When the API can't be reached, the page is served without them.
  - The console's SDK, when it is loaded from a URL, is allowed the same
    way (`X-Rvav-Script-Origins`, in `script-src`).
- `RVAV_OIDC_*` and `RVAV_WEB_OIDC_*` still win:
  - `RVAV_OIDC_*` locks the fields, as for any integration.
  - `RVAV_WEB_OIDC_*`, on the web container, win in the browser, and lock
    their fields in the page.

### Certificates
- A connection's test (vCenter and the vCenters added, AWX, EfficientIP,
  NSX, vRA, the identity provider, Git over HTTPS) shows the certificate
  its server presents: subject, issuer, names, validity and SHA-256, and
  whether rvav trusts it. It runs first: a certificate the connection
  would refuse ends the test there, before any password is sent.
- **Pinned:** trusting it from the test puts its SHA-256 in the
  connection's `cert_sha256` (`RVAV_*_CERT_SHA256`), as vRA does when a
  cloud account is added. The connection then trusts that certificate
  besides those the CAs vouch for: a self-signed vCenter or ESXi lab,
  without turning checks off. A pin matches the server's own certificate
  only, whatever its names and dates (as govmomi's thumbprints do); a new
  one needs the password or token again, since it lets another server be
  trusted. The identity provider and Git take no pin: their CA goes in
  the bundle.
- **The CA bundle** (Integrations › Trusted certificates, or
  `RVAV_CA_BUNDLE`): certificates in PEM, trusted besides the system's CAs
  by every connection, notifications, webhooks and REST value sources
  included. A key in it is refused. From a test, "Add it to the CA bundle"
  appends the last certificate the server sent (its issuing CA, or the
  certificate itself when self-signed), after a confirmation that every
  connection will trust it.
- The bundle is set in each process as it starts, so it applies as
  connections do: a change drains and restarts each process. A test
  uses the bundle saved, before the processes apply it, so it can be
  checked at once.
- Turning TLS checks off stays possible, discouraged in the UI; its test
  still shows the certificate, not checked.

### First run
- An install without seed data or integration settings opens, for the
  administrator, on a setup checklist. Each step links to its page and
  shows when it is done:
  - the password;
  - the identity provider;
  - a group mapped to `admin`;
  - vCenter;
  - environments and projects, with a placement rule;
  - IPAM;
  - AWX;
  - a first catalog item.
- **What makes a step done** is read from what is saved, not ticked by
  hand:
  - the administrator's password no longer the install's;
  - an issuer;
  - a group with the global `admin` role;
  - vCenter, EfficientIP and AWX configured (or their development fake);
  - an environment, an unarchived project and a placement rule for it
    (without one, the project can't request anything);
  - a published item.
  Undoing a step (the last admin mapping removed) undoes its tick.
- **When it opens:** an administrator signing in lands on it once per page
  load while it is open: some step remains, no administrator hid it
  (`PUT /admin/setup`, audited), and the database holds no seed users (a
  seeded install is a development or demo one). Hidden or complete, it
  stays in **Administration › Setup**.
- Administration pages don't need a project. An administrator in none
  is offered the checklist where others are told to ask for one.
- A System page (`GET /admin/system`, administrators only) shows, read
  only:
  - the version and the schema's last migration;
  - each process and the settings revision it runs;
  - each kind saved in Administration, with whether it applies on next
    use, is applying or has applied;
  - the install settings as the API answering reads them.
  The install settings are `config.Install`, which a test keeps to
  configuration.md's install rows. A secret's value is never sent, only
  whether it is set; the database URL is sent without its password.
- **What an install is given:** the chart's values and the VM's
  `rvav.env.example` hold the install settings only. An integration's
  variables are left as examples, since each would lock its field. Tests
  keep it so. `deploy/docker-compose.fresh.yml` starts the development
  stack as a new install: no seed, no integration variables for rvav's
  processes, OIDC sign-in. Its fakes keep their own settings, and the fake
  EfficientIP serves vcsim's networks on an empty database.
- **Every process starts with the install settings alone.** An
  integration not set yet makes no process exit:
  - EfficientIP or AWX answer each call with what to set, and where. The
    same goes when saved settings make no client (AWX without its
    organisation), so the API stays up for Administration to correct them.
  - Without vCenter, the worker starts, and a request fails saying vCenter
    is not set.
  - The sync waits. It syncs vCenter, EfficientIP and AWX once each is
    saved, since saving one starts it again.

## Consequences
- rvav is set up from its UI, as vRA is; only the database, keys,
  Temporal and addresses are given at install.
- **The local administrator is a password-guarded admin.** Its password is
  shown once, slows down guessing, is audited, and can be reset only by
  someone with the database credentials and the command line. Its session
  is the one cookie rvav sets, guarded against cross-site requests.
- **A connection change restarts every process,** each draining first. A
  provisioning running then finishes, or its activity is retried by
  Temporal. With one API (Compose, a VM), the UI is unavailable for the few
  seconds it takes to start again; with two or more, the others answer.
- **A process that holds a fake (development's fake EfficientIP and AWX)
  starts it again empty.** Addresses rvav recorded are not offered again;
  a fake job in flight when the process drained is finished first.
- **Two ways to sign in, one principal.** The local account is kept for
  first setup and break-glass. Day-to-day access goes through the
  provider and its groups.
