# Configuration and secrets reference

Every rvav process (`api`, `worker`, `sync`, `migrate`) reads the same
environment variables. On Kubernetes the chart's `config` values become a
ConfigMap and its `secret.env` values a Secret; with Compose they are in
`deploy/docker-compose.yml` and `deploy/.env`. A test
(`internal/config/docs_test.go`) checks that this page lists every setting
the code reads, and only those.

**Set in** says where each setting is set ([ADR-0030](../adr/0030-settings-and-local-administrator.md)):

- **install**: in the environment only. What a process needs before it
  can read its database (the database, the secrets keys, Temporal, where
  it listens, the public URL), the web container's own settings, and
  development's fakes.
- **Integrations › …**: in that tab of **Administration › Integrations**
  too, where the environment variable, when set, still wins and locks its
  field. An install can leave them all out and set them from the UI.

Saved in Administration, a setting applies without a restart by hand:
the identity provider, the console's account and SDK, vRA, email, Teams,
Slack, the allowed hosts and the sync intervals on next use; a connection
(vCenter, AWX, EfficientIP, NSX, Git) once each process has drained (no
new work, running work finished, within `RVAV_DRAIN_TIMEOUT`), exited with
status 3 and been started again by its supervisor: Kubernetes, Compose's
`restart` policy, or systemd. Run rvav's processes under one of those.

**Secret** marks a value to keep in the Secret (or a vault that fills it),
never in the ConfigMap or a file in Git. Durations are Go durations (`90s`,
`5m`, `2h`); lists are comma-separated; booleans are `1`, `true`, `yes` or
`on`.

## Process and database

| Setting | Default | Secret | Set in | What |
|---|---|---|---|---|
| `RVAV_DATABASE_URL` | `postgres://rvav:rvav@localhost:5432/rvav?sslmode=disable` | yes | install | rvav's PostgreSQL database. Use `sslmode=verify-full` in production. |
| `RVAV_HTTP_ADDR` | `:8080` | | install | Where the API (and the fake SOLIDserver) listens. |
| `RVAV_METRICS_ADDR` | `:9090` | | install | Where every process serves its Prometheus metrics (`/metrics`), apart from the API. `off` serves none. |
| `RVAV_LOG_LEVEL` | `info` | | install | `debug`, `info`, `warn` or `error`. Logs are JSON on standard output. |
| `RVAV_PUBLIC_URL` | `http://localhost:5173` | | install | The web app's address, for links in notifications. |
| `RVAV_CORS_ORIGINS` | none | | install | Origins allowed to call the API from another site (the Vite dev server). Not needed when the web app proxies the API. |
| `RVAV_INSTANCE_ID` | `rvav` | | Integrations › EfficientIP | Tells this rvav's objects in EfficientIP from another's on a shared SOLIDserver. |
| `RVAV_DRAIN_TIMEOUT` | `5m` | | install | How long a process applying newer connection settings lets its running work finish before it exits; a worker's activity still running then is retried by the next worker. Also how long a worker stopped by Kubernetes or Compose waits for its activities, within their grace period. |

Traces use OpenTelemetry's standard variables, read by every process:
`OTEL_EXPORTER_OTLP_ENDPOINT` (an OTLP/HTTP collector, for example
`http://otel-collector:4318`; without it nothing is traced),
`OTEL_EXPORTER_OTLP_HEADERS`, `OTEL_TRACES_SAMPLER` and
`OTEL_TRACES_SAMPLER_ARG` ([observability.md](observability.md)).

## Sign-in

The local administrator, `admin`, is the way in at first start and when
the identity provider is down (ADR-0030). The API makes it at its first
start: with `RVAV_ADMIN_PASSWORD`, or with a password it generates and logs
once (`local administrator created`, with the password). Either must be
changed at the first sign-in. `rvav admin reset-password` sets another,
generated and printed, or read from stdin with `-password-stdin`; it must
be changed at the next sign-in too, and the account's sessions end.

The identity provider is set in **Administration › Integrations ›
Identity provider**, tested there with a trial sign-in, and applies
without a restart ([admin guide](../guides/admin.md#the-identity-provider)).
The `RVAV_OIDC_*` variables below win over it and lock their fields. Until
a provider is set, only the local administrator and API tokens sign in.
`/readyz` waits for a provider only when `RVAV_OIDC_ISSUER` sets it: one
set in Administration doesn't hold it, so the local administrator can
still sign in and correct a wrong one.

| Setting | Default | Secret | Set in | What |
|---|---|---|---|---|
| `RVAV_ADMIN_PASSWORD` | generated, logged once | yes | install | The local administrator's first password, read when the account is made and ignored afterwards. |
| `RVAV_AUTH_MODE` | `oidc` | | install | `oidc` in production. `dev` trusts a header and starts only with `RVAV_ALLOW_DEV_AUTH`; the Helm chart refuses it. |
| `RVAV_ALLOW_DEV_AUTH` | off | | install | Allows `dev` sign-in, for developer machines and tests only. |
| `RVAV_DEV_USER` | `lmartin` | | install | Who `dev` sign-in is when no header names a user. |
| `RVAV_OIDC_ISSUER` | Administration's | | Integrations › Identity provider | The identity provider's issuer, as in its tokens and as the browser reaches it. None: only the local administrator and API tokens sign in. |
| `RVAV_OIDC_DISCOVERY_URL` | the issuer | | Integrations › Identity provider | Where rvav reads the provider's discovery document, when it reaches it at another address than the browser (inside a cluster). |
| `RVAV_OIDC_CLIENT_ID` | `rvav` | | Integrations › Identity provider | The web app's client at the provider; tokens must have it as their audience. |
| `RVAV_OIDC_SCOPES` | `openid profile email` | | Integrations › Identity provider | The scopes the web app asks for, space-separated: with Entra ID, a scope of rvav's API (`api://<client>/access`) and `offline_access`. |
| `RVAV_OIDC_GROUPS_CLAIM` | `groups` | | Integrations › Identity provider | The token claim with the user's groups (roles and approver groups map to them); `roles` for Entra ID's app roles. |

## Temporal

| Setting | Default | Secret | Set in | What |
|---|---|---|---|---|
| `RVAV_TEMPORAL_ADDRESS` | `localhost:7233` | | install | The frontend service, or Temporal Cloud's namespace endpoint. |
| `RVAV_TEMPORAL_NAMESPACE` | `default` | | install | rvav's namespace. |
| `RVAV_TEMPORAL_TASK_QUEUE` | `rvav-provision` | | install | The task queue the worker polls. |
| `RVAV_TEMPORAL_TLS` | off (on with an API key or a client certificate) | | install | TLS to the frontend. |
| `RVAV_TEMPORAL_TLS_CA_FILE` | the system's CAs | | install | A private CA's certificate, a mounted file. |
| `RVAV_TEMPORAL_TLS_CERT_FILE` | none | | install | A client certificate for mutual TLS, a mounted file. |
| `RVAV_TEMPORAL_TLS_KEY_FILE` | none | yes (the file) | install | Its private key, a mounted file. |
| `RVAV_TEMPORAL_TLS_SERVER_NAME` | the address's host | | install | The name the frontend's certificate has, when it differs. |
| `RVAV_TEMPORAL_API_KEY` | none | yes | install | Temporal Cloud's API key. |

## vCenter

| Setting | Default | Secret | Set in | What |
|---|---|---|---|---|
| `RVAV_VCENTER_URL` | none | | Integrations › vCenter | The first vCenter's SDK address (`https://vc01.corp.example/sdk`). Others are added in the app (Integrations → vCenters). |
| `RVAV_VCENTER_USERNAME` | none | | Integrations › vCenter | rvav's service account (permissions: docs/integrations.md). |
| `RVAV_VCENTER_PASSWORD` | none | yes | Integrations › vCenter | Its password. |
| `RVAV_VCENTER_INSECURE` | off | | Integrations › vCenter | Doesn't check the certificate. For labs only. |
| `RVAV_VCENTER_NAME` | `vc01.corp.example` | | Integrations › vCenter | How rvav names this vCenter in the app and on VMs. |
| `RVAV_CONSOLE_VCENTER_USERNAME` | none | | Integrations › vCenter › Console | An account that may only open consoles: VMRC links carry a clone ticket of it (ADR-0012). |
| `RVAV_CONSOLE_VCENTER_PASSWORD` | none | yes | Integrations › vCenter › Console | Its password. It goes only to the vCenter connection it was given for: saved in Administration, it is given again when vCenter's address changes or its TLS checks are turned off. |
| `RVAV_WMKS_SDK_URLS` | none | | Integrations › vCenter › Console | The WebMKS SDK's scripts, which turn on the console in the browser: paths on the web app (`/wmks/wmks.min.js`), or URLs, whose origins the API gives nginx for the page's Content-Security-Policy. |
| `RVAV_SYNC_INTERVAL` | `2m` | | Integrations › vCenter | The time between full reads of the inventory; changes come in between from vCenter's notifications. VMs' tags in NSX are compared as often. |

## EfficientIP SOLIDserver

| Setting | Default | Secret | Set in | What |
|---|---|---|---|---|
| `RVAV_EFFICIENTIP_URL` | none | | Integrations › EfficientIP | The SOLIDserver's address. |
| `RVAV_EFFICIENTIP_USERNAME` | none | | Integrations › EfficientIP | rvav's account, or a token's key ID. |
| `RVAV_EFFICIENTIP_PASSWORD` | none | yes | Integrations › EfficientIP | Its password, or the token's secret. |
| `RVAV_EFFICIENTIP_AUTH` | `native` | | Integrations › EfficientIP | `native` (user and password) or `token`. |
| `RVAV_EFFICIENTIP_INSECURE` | off | | Integrations › EfficientIP | Doesn't check the certificate. For labs only. |
| `RVAV_EFFICIENTIP_SPACE` | all spaces | | Integrations › EfficientIP | The IPAM space rvav's subnets are in. |
| `RVAV_EFFICIENTIP_DNS_SERVER` | none | | Integrations › EfficientIP | The DNS server (or smart architecture) records are created on. |
| `RVAV_EFFICIENTIP_DNS_VIEW` | none | | Integrations › EfficientIP | The DNS view, when the server has views. |
| `RVAV_IPAM_SYNC_INTERVAL` | `5m` | | Integrations › EfficientIP | How often the subnets are read for network mappings. |
| `RVAV_FAKE_EFFICIENTIP` | off | | install | Uses a fake SOLIDserver in the process. For development only. |
| `RVAV_FAKE_EFFICIENTIP_LATENCY` | none | | install | How long `rvav fake-efficientip` takes to answer each call, as a real SOLIDserver would. For load tests. |

## AWX

| Setting | Default | Secret | Set in | What |
|---|---|---|---|---|
| `RVAV_AWX_URL` | none | | Integrations › AWX | AWX's address. |
| `RVAV_AWX_TOKEN` | none | yes | Integrations › AWX | An OAuth token of rvav's AWX user. |
| `RVAV_AWX_ORGANIZATION` | none (required with a real AWX) | | Integrations › AWX | The organisation rvav's templates and inventories are in. |
| `RVAV_AWX_INVENTORY_SOURCE_ID` | none | | Integrations › AWX | The inventory source that reads rvav's inventory, refreshed before jobs. |
| `RVAV_AWX_CATALOG_LABELS` | `catalog` | | Integrations › AWX | Labels of templates offered as catalog items. |
| `RVAV_AWX_ACTION_LABELS` | `day2` | | Integrations › AWX | Labels of templates offered as day-2 actions. |
| `RVAV_AWX_SYNC_INTERVAL` | `5m` | | Integrations › AWX | How often templates are read. |
| `RVAV_AWX_INSECURE` | off | | Integrations › AWX | Doesn't check the certificate. For labs only. |
| `RVAV_FAKE_AWX` | off | | install | Simulates AWX jobs. For development only. |
| `RVAV_FAKE_AWX_TASK_DELAY` | `1.5s` | | install | How long each simulated task takes. |
| `RVAV_FAKE_AWX_FAIL_TASK` | none | | install | A simulated task that fails, to try failures. |
| `RVAV_INVENTORY_TOKEN` | none | yes | Integrations › AWX | The rvav API token AWX's inventory source reads rvav with, which `rvav awx-bootstrap` puts in AWX. Unset, AWX's tab makes one (Inventory token) and puts it in AWX; set, it makes none. |

## NSX

The NSX Manager rvav makes networks and security groups in
([ADR-0028](../adr/0028-nsx-networking.md)), through its Policy API
(NSX 4.x, or NSX-T 3.x). Also set from Administration › Integrations.

| Setting | Default | Secret | Set in | What |
|---|---|---|---|---|
| `RVAV_NSX_URL` | none | | Integrations › NSX | The NSX Manager's address (`https://nsx.corp.example`), or its cluster's virtual IP. |
| `RVAV_NSX_USERNAME` | none | | Integrations › NSX | rvav's NSX user, to sign in with a password. |
| `RVAV_NSX_PASSWORD` | none | yes | Integrations › NSX | Its password. |
| `RVAV_NSX_CERT_FILE` | none | | Integrations › NSX | Or a principal identity: its certificate, a PEM file on rvav's hosts. NSX recommends this for automation. |
| `RVAV_NSX_KEY_FILE` | none | | Integrations › NSX | The principal identity's private key, a PEM file. With the Helm chart, name a `kubernetes.io/tls` Secret in `nsx.certSecret`: it is mounted at `/etc/rvav/nsx` (`tls.crt`, `tls.key`). |
| `RVAV_NSX_INSECURE` | off | | Integrations › NSX | Doesn't check the certificate. For labs only. |

rvav's NSX account needs a role that can:
- **read:** Tier-1 gateways, segments, groups, services, transport zones
  and the VM inventory;
- **write:** segments, groups and security policies in the `default`
  domain;
- **tag:** VMs.

It needs only to read Tier-1 gateways (and attach segments to them), and
nothing on Tier-0s, NAT, VPN or the gateway firewall. NSX's built-in
*Network Engineer* plus *Security Engineer* cover this; a custom role can
narrow it.

**Choose one way to sign in, and keep it:** what a principal identity
makes in NSX, only that identity can change or delete. Moving between a
password and a certificate, or making the identity again under another
name, leaves rvav unable to delete what it made, and deleting those
deployments fails.

`rvav fake-nsx` serves a fake NSX Manager for development: the compose
stack runs one as `nsx`.

## Git (the catalog in a repository)

The repository the catalog is kept in, one YAML file per item
([ADR-0029](../adr/0029-catalog-in-git.md)): rvav commits what it
publishes, and makes drafts of what changes in the repository. Also set
from Administration › Integrations. rvav needs to read the branch and push
to it, never to force a push.

| Setting | Default | Secret | Set in | What |
|---|---|---|---|---|
| `RVAV_GIT_URL` | none | | Integrations › Git | The repository: `https://git.corp.example/infra/catalog.git`, or `ssh://git@git.corp.example/infra/catalog.git` (`git@git.corp.example:infra/catalog.git` too). |
| `RVAV_GIT_BRANCH` | `main` | | Integrations › Git | The branch rvav reads and commits to. A protected branch must let rvav's account push. |
| `RVAV_GIT_DIR` | `catalog` | | Integrations › Git | The directory of item files in the repository: `<slug>.yaml`. |
| `RVAV_GIT_USERNAME` | `rvav` (HTTPS), `git` (SSH) | | Integrations › Git | The user name to sign in with: some servers want a fixed one with a token (GitLab: anything; Bitbucket: `x-token-auth`). |
| `RVAV_GIT_CREDENTIAL` | none | yes | Integrations › Git | HTTPS: a token that may read and write the repository (a project access token, a fine-grained token). SSH: a deploy key with write access, its private key in PEM (no passphrase). |
| `RVAV_GIT_KNOWN_HOSTS` | none | | Integrations › Git | SSH: the server's host keys, `known_hosts` lines (`ssh-keyscan git.corp.example`). Required over SSH unless `RVAV_GIT_INSECURE`. |
| `RVAV_GIT_INSECURE` | off | | Integrations › Git | Doesn't check the certificate (HTTPS) or the host key (SSH). For labs only. |
| `RVAV_GIT_COMMITTER` | `rvav@localhost` | | Integrations › Git | The address of rvav's commits. Their author is the administrator who published. |
| `RVAV_GIT_SYNC_INTERVAL` | `5m` | | Integrations › Git | How often the repository is read. |
| `RVAV_GIT_WEBHOOK_SECRET` | none | yes | install | The secret of the Git server's webhook (`/api/v1/integrations/git/webhook`, push events): GitHub's and Gitea's signature, or GitLab's token. The repository is then read as soon as it changes. Without it, the webhook answers 404. |
| `RVAV_GIT_CACHE` | the system's temporary directory | | install | Where `rvav sync` (and the API, to read on demand) keep their clone. It is made again when missing; the Helm chart mounts a writable `/tmp`. |

The sync process (`rvav sync`) reads the repository and commits rvav's
changes, one process at a time. The repository's account needs to read
the branch and push to it; a protected branch must allow its pushes
(rvav reads and commits to the same branch).

Git servers often refuse webhooks to private addresses: allow rvav's
(Gitea: `webhook.ALLOWED_HOST_LIST`, which `private` doesn't cover for
100.64.0.0/10; GitLab: *Allow requests to the local network from webhooks
and integrations*). Without the webhook, rvav still reads the repository
every `RVAV_GIT_SYNC_INTERVAL`.

## vRA (the importer)

What the importer reads (P2, [ADR-0027](../adr/0027-vra-importer.md)). It
only reads. In Administration › Integrations › vRA, one field takes the
credential: the user's password when a user is given, else a refresh
token. Saved there, it is used from the next read.

| Setting | Default | Secret | Set in | What |
|---|---|---|---|---|
| `RVAV_VRA_URL` | none | | Integrations › vRA | vRA 8 / Aria Automation's address (`https://vra.corp.example`). |
| `RVAV_VRA_TOKEN` | none | yes | Integrations › vRA | A refresh token (API token) of a vRA user who may read the organisation. |
| `RVAV_VRA_USERNAME` | none | | Integrations › vRA | Or a user's name, to sign in with a password. |
| `RVAV_VRA_PASSWORD` | none | yes | Integrations › vRA | Its password. |
| `RVAV_VRA_DOMAIN` | none | | Integrations › vRA | Its identity source's domain (`corp.example`). |
| `RVAV_VRA_INSECURE` | off | | Integrations › vRA | Doesn't check the certificate. For labs only. |

## Secrets, value sources and webhooks

| Setting | Default | Secret | Set in | What |
|---|---|---|---|---|
| `RVAV_SECRETS_KEY` | none | yes | install | The 32-byte key (base64) that seals stored secrets, vCenter passwords and integration settings (ADR-0019). Required to use them. |
| `RVAV_SECRETS_OLD_KEYS` | none | yes | install | Earlier keys, to open what they sealed while rotating. |
| `RVAV_REST_SOURCE_HOSTS` | none | | Integrations › Allowed hosts | Hosts REST value sources may call over HTTPS (`cmdb.corp.example`, `cmdb.corp.example:8443`, `*.corp.example` for its subdomains), or `http://host` for plain HTTP. |
| `RVAV_WEBHOOK_HOSTS` | none | | Integrations › Allowed hosts | Hosts webhook subscriptions may post to, listed in the same way. |

## Notifications

| Setting | Default | Secret | Set in | What |
|---|---|---|---|---|
| `RVAV_SMTP_HOST` | none (no email) | | Integrations › Email | The mail server. |
| `RVAV_SMTP_PORT` | `587` | | Integrations › Email | Its port. |
| `RVAV_SMTP_TLS` | `starttls` | | Integrations › Email | `starttls`, `tls` or `none`. |
| `RVAV_SMTP_FROM` | `rvav <rvav@localhost>` | | Integrations › Email | The sender. |
| `RVAV_SMTP_USERNAME` | none | | Integrations › Email | Its account, when it asks for one. |
| `RVAV_SMTP_PASSWORD` | none | yes | Integrations › Email | Its password. |
| `RVAV_TEAMS_APP_ID` | none (no Teams) | | Integrations › Microsoft Teams | The bot's Microsoft app ID. |
| `RVAV_TEAMS_TENANT_ID` | none | | Integrations › Microsoft Teams | The tenant. |
| `RVAV_TEAMS_APP_PASSWORD` | none | yes | Integrations › Microsoft Teams | The app's secret. |
| `RVAV_TEAMS_SERVICE_URL` | `https://smba.trafficmanager.net/teams` | | Integrations › Microsoft Teams | Bot Framework's service. |
| `RVAV_TEAMS_LOGIN_URL` | `https://login.microsoftonline.com` | | Integrations › Microsoft Teams | Where the bot signs in. |
| `RVAV_TEAMS_GRAPH_URL` | `https://graph.microsoft.com` | | Integrations › Microsoft Teams | Microsoft Graph. |
| `RVAV_TEAMS_OPENID_URL` | Bot Framework's | | Integrations › Microsoft Teams | Where the keys of Teams' calls to rvav are published. |
| `RVAV_SLACK_BOT_TOKEN` | none (no Slack) | yes | Integrations › Slack | The Slack app's bot token. |
| `RVAV_SLACK_SIGNING_SECRET` | none | yes | Integrations › Slack | Checks Slack's calls to rvav. |
| `RVAV_SLACK_API_URL` | `https://slack.com/api/` | | Integrations › Slack | Slack's API. |

## The web app

The web app reads its sign-in settings from the API (`GET
/api/v1/auth/options`), and nginx asks the API for the provider's origin
when it serves the page, for its Content-Security-Policy. The
`RVAV_WEB_OIDC_*` variables are for a browser that must sign in otherwise
than the API says: they win in the browser (`/config.js`), and lock their
fields in Administration.

| Setting | Default | Secret | Set in | What |
|---|---|---|---|---|
| `RVAV_API_UPSTREAM` | `http://api:8080` | | install | The API, as nginx reaches it (the service's full name on Kubernetes). |
| `RVAV_WEB_AUTH_MODE` | the build's (`oidc`) | | install | `oidc`, or `dev` with a dev-auth API. |
| `RVAV_WEB_OIDC_AUTHORITY` | the API's | | install | The identity provider's issuer, as the browser reaches it. |
| `RVAV_WEB_OIDC_CLIENT_ID` | the API's | | install | The browser's client. |
| `RVAV_WEB_OIDC_SCOPE` | the API's | | install | The scopes it asks for. |
| `RVAV_WEB_SCRIPT_SOURCES` | none | | install | Where the browser may load scripts from besides the web app and the origins of the console's SDK the API says (`RVAV_WMKS_SDK_URLS`, or set in Administration): for the Content-Security-Policy, when nginx can't ask the API (`https://vc01.corp.example`). Add `'unsafe-eval'` only if the console's SDK needs it. |
