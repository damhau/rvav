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
  - the inventory token;
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
  - notifications (SMTP, Teams, Slack);
  - the allowed hosts;
  - the intervals.

  Each process reads the current settings revision before using one, and
  builds what it needs again when the revision has moved.
- **Drain and restart:** the connection clients (vCenter, AWX, EfficientIP,
  NSX, Git, vRA) hold sessions, and the Temporal worker and the sync loops
  are built at start. Swapping them under a running activity is the risk
  ADR-0020 avoided. Instead, a process that holds them and sees a newer
  revision drains:
  - it takes no new task and lets its running activities finish, for at
    most a bounded time;
  - an activity still running then is retried by Temporal, and is
    idempotent;
  - it exits with status 3.

  Its supervisor starts it again with the new settings: Kubernetes,
  Compose (`restart: on-failure`) or systemd (`Restart=on-failure`). The
  API process holds none of them across requests.
- Each process records the revision it runs with (ADR-0020). The UI shows
  a change as applying until every process has it, then applied, in place
  of the restart notice.

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
  - A successful sign-in starts again from zero.
  - An unknown username takes as long to refuse as a wrong password.
- **Audited:** a sign-in (`auth.sign_in`), a failure
  (`auth.sign_in_failed`), a password change (`auth.password_changed`), a
  reset (`auth.password_reset`) and a sign-out (`auth.sign_out`).
- **Reset from the command line:** `rvav admin reset-password` sets a new
  generated password (or one read from standard input), to be changed at
  the next sign-in. It ends every session of the account.

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
- `RVAV_OIDC_*` and `RVAV_WEB_OIDC_*` still win:
  - `RVAV_OIDC_*` locks the fields, as for any integration.
  - `RVAV_WEB_OIDC_*`, on the web container, win in the browser, and lock
    their fields in the page.

### Certificates
- A connection's test shows the server's certificate: subject, issuer,
  SHA-256 fingerprint and expiry. Trusting it pins that certificate for
  that connection, as vRA does when a cloud account is added.
- A CA bundle set in Administration is trusted by every connection.
- Turning TLS checks off stays possible, discouraged in the UI.

### First run
- An install without seed data or integration settings opens, for the
  administrator, on a setup checklist. Each step links to its page and
  shows when it is done:
  - the password;
  - the identity provider;
  - a group mapped to `admin`;
  - vCenter;
  - environments and projects;
  - IPAM;
  - AWX;
  - a first catalog item.
- A System page shows the version, each process and the settings revision
  it runs, and the install settings, read only (secrets masked).

## Consequences
- rvav is set up from its UI, as vRA is; only the database, keys,
  Temporal and addresses are given at install.
- **The local administrator is a password-guarded admin.** Its password is
  shown once, slows down guessing, is audited, and can be reset only by
  someone with the database credentials and the command line. Its session
  is the one cookie rvav sets, guarded against cross-site requests.
- **A connection change restarts the worker and the sync process,** each
  draining first. A provisioning running then finishes, or its activity
  is retried by Temporal.
- **Two ways to sign in, one principal.** The local account is kept for
  first setup and break-glass. Day-to-day access goes through the
  provider and its groups.
