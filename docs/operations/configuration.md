# Configuration and secrets reference

Every rvav process (`api`, `worker`, `sync`, `migrate`) reads the same
environment variables. On Kubernetes the chart's `config` values become a
ConfigMap and its `secret.env` values a Secret; with Compose they are in
`deploy/docker-compose.yml` and `deploy/.env`. A test
(`internal/config/docs_test.go`) checks that this page lists every setting
the code reads, and only those.

**Secret** marks a value to keep in the Secret (or a vault that fills it),
never in the ConfigMap or a file in Git. Durations are Go durations (`90s`,
`5m`, `2h`); lists are comma-separated; booleans are `1`, `true`, `yes` or
`on`.

## Process and database

| Setting | Default | Secret | What |
|---|---|---|---|
| `RVAV_DATABASE_URL` | `postgres://rvav:rvav@localhost:5432/rvav?sslmode=disable` | yes | rvav's PostgreSQL database. Use `sslmode=verify-full` in production. |
| `RVAV_HTTP_ADDR` | `:8080` | | Where the API (and the fake SOLIDserver) listens. |
| `RVAV_METRICS_ADDR` | `:9090` | | Where every process serves its Prometheus metrics (`/metrics`), apart from the API. `off` serves none. |
| `RVAV_LOG_LEVEL` | `info` | | `debug`, `info`, `warn` or `error`. Logs are JSON on standard output. |
| `RVAV_PUBLIC_URL` | `http://localhost:5173` | | The web app's address, for links in notifications. |
| `RVAV_CORS_ORIGINS` | none | | Origins allowed to call the API from another site (the Vite dev server). Not needed when the web app proxies the API. |
| `RVAV_INSTANCE_ID` | `rvav` | | Tells this rvav's objects in EfficientIP from another's on a shared SOLIDserver. |

Traces use OpenTelemetry's standard variables, read by every process:
`OTEL_EXPORTER_OTLP_ENDPOINT` (an OTLP/HTTP collector, for example
`http://otel-collector:4318`; without it nothing is traced),
`OTEL_EXPORTER_OTLP_HEADERS`, `OTEL_TRACES_SAMPLER` and
`OTEL_TRACES_SAMPLER_ARG` ([observability.md](observability.md)).

## Sign-in

| Setting | Default | Secret | What |
|---|---|---|---|
| `RVAV_AUTH_MODE` | `oidc` | | `oidc` in production. `dev` trusts a header and starts only with `RVAV_ALLOW_DEV_AUTH`; the Helm chart refuses it. |
| `RVAV_ALLOW_DEV_AUTH` | off | | Allows `dev` sign-in, for developer machines and tests only. |
| `RVAV_DEV_USER` | `lmartin` | | Who `dev` sign-in is when no header names a user. |
| `RVAV_OIDC_ISSUER` | none (required with `oidc`) | | The identity provider's issuer, as in its tokens. |
| `RVAV_OIDC_DISCOVERY_URL` | the issuer | | Where rvav reads the provider's discovery document, when it reaches it at another address than the browser (inside a cluster). |
| `RVAV_OIDC_CLIENT_ID` | `rvav` | | The client tokens are issued to. |
| `RVAV_OIDC_GROUPS_CLAIM` | `groups` | | The token claim with the user's groups (roles and approver groups map to them). |

## Temporal

| Setting | Default | Secret | What |
|---|---|---|---|
| `RVAV_TEMPORAL_ADDRESS` | `localhost:7233` | | The frontend service, or Temporal Cloud's namespace endpoint. |
| `RVAV_TEMPORAL_NAMESPACE` | `default` | | rvav's namespace. |
| `RVAV_TEMPORAL_TASK_QUEUE` | `rvav-provision` | | The task queue the worker polls. |
| `RVAV_TEMPORAL_TLS` | off (on with an API key or a client certificate) | | TLS to the frontend. |
| `RVAV_TEMPORAL_TLS_CA_FILE` | the system's CAs | | A private CA's certificate, a mounted file. |
| `RVAV_TEMPORAL_TLS_CERT_FILE` | none | | A client certificate for mutual TLS, a mounted file. |
| `RVAV_TEMPORAL_TLS_KEY_FILE` | none | yes (the file) | Its private key, a mounted file. |
| `RVAV_TEMPORAL_TLS_SERVER_NAME` | the address's host | | The name the frontend's certificate has, when it differs. |
| `RVAV_TEMPORAL_API_KEY` | none | yes | Temporal Cloud's API key. |

## vCenter

| Setting | Default | Secret | What |
|---|---|---|---|
| `RVAV_VCENTER_URL` | none | | The first vCenter's SDK address (`https://vc01.corp.example/sdk`). Others are added in the app (Integrations → vCenters). |
| `RVAV_VCENTER_USERNAME` | none | | rvav's service account (permissions: docs/integrations.md). |
| `RVAV_VCENTER_PASSWORD` | none | yes | Its password. |
| `RVAV_VCENTER_INSECURE` | off | | Doesn't check the certificate. For labs only. |
| `RVAV_VCENTER_NAME` | `vc01.corp.example` | | How rvav names this vCenter in the app and on VMs. |
| `RVAV_CONSOLE_VCENTER_USERNAME` | none | | An account that may only open consoles: VMRC links carry a clone ticket of it (ADR-0012). |
| `RVAV_CONSOLE_VCENTER_PASSWORD` | none | yes | Its password. |
| `RVAV_WMKS_SDK_URLS` | none | | The WebMKS SDK's scripts, which turn on the console in the browser. |
| `RVAV_SYNC_INTERVAL` | `2m` | | The time between full reads of the inventory; changes come in between from vCenter's notifications. |

## EfficientIP SOLIDserver

| Setting | Default | Secret | What |
|---|---|---|---|
| `RVAV_EFFICIENTIP_URL` | none | | The SOLIDserver's address. |
| `RVAV_EFFICIENTIP_USERNAME` | none | | rvav's account, or a token's key ID. |
| `RVAV_EFFICIENTIP_PASSWORD` | none | yes | Its password, or the token's secret. |
| `RVAV_EFFICIENTIP_AUTH` | `native` | | `native` (user and password) or `token`. |
| `RVAV_EFFICIENTIP_INSECURE` | off | | Doesn't check the certificate. For labs only. |
| `RVAV_EFFICIENTIP_SPACE` | all spaces | | The IPAM space rvav's subnets are in. |
| `RVAV_EFFICIENTIP_DNS_SERVER` | none | | The DNS server (or smart architecture) records are created on. |
| `RVAV_EFFICIENTIP_DNS_VIEW` | none | | The DNS view, when the server has views. |
| `RVAV_IPAM_SYNC_INTERVAL` | `5m` | | How often the subnets are read for network mappings. |
| `RVAV_FAKE_EFFICIENTIP` | off | | Uses a fake SOLIDserver in the process. For development only. |
| `RVAV_FAKE_EFFICIENTIP_LATENCY` | none | | How long `rvav fake-efficientip` takes to answer each call, as a real SOLIDserver would. For load tests. |

## AWX

| Setting | Default | Secret | What |
|---|---|---|---|
| `RVAV_AWX_URL` | none | | AWX's address. |
| `RVAV_AWX_TOKEN` | none | yes | An OAuth token of rvav's AWX user. |
| `RVAV_AWX_ORGANIZATION` | none (required with a real AWX) | | The organisation rvav's templates and inventories are in. |
| `RVAV_AWX_INVENTORY_SOURCE_ID` | none | | The inventory source that reads rvav's inventory, refreshed before jobs. |
| `RVAV_AWX_CATALOG_LABELS` | `catalog` | | Labels of templates offered as catalog items. |
| `RVAV_AWX_ACTION_LABELS` | `day2` | | Labels of templates offered as day-2 actions. |
| `RVAV_AWX_SYNC_INTERVAL` | `5m` | | How often templates are read. |
| `RVAV_AWX_INSECURE` | off | | Doesn't check the certificate. For labs only. |
| `RVAV_FAKE_AWX` | off | | Simulates AWX jobs. For development only. |
| `RVAV_FAKE_AWX_TASK_DELAY` | `1.5s` | | How long each simulated task takes. |
| `RVAV_FAKE_AWX_FAIL_TASK` | none | | A simulated task that fails, to try failures. |
| `RVAV_INVENTORY_TOKEN` | none | yes | `rvav awx-bootstrap`: the rvav API token AWX's inventory source uses. |

## Secrets, value sources and webhooks

| Setting | Default | Secret | What |
|---|---|---|---|
| `RVAV_SECRETS_KEY` | none | yes | The 32-byte key (base64) that seals stored secrets, vCenter passwords and integration settings (ADR-0019). Required to use them. |
| `RVAV_SECRETS_OLD_KEYS` | none | yes | Earlier keys, to open what they sealed while rotating. |
| `RVAV_REST_SOURCE_HOSTS` | none | | Hosts REST value sources may call (`https://cmdb.corp.example`, or `http://host` for plain HTTP). |
| `RVAV_WEBHOOK_HOSTS` | none | | Hosts webhook subscriptions may post to. |

## Notifications

| Setting | Default | Secret | What |
|---|---|---|---|
| `RVAV_SMTP_HOST` | none (no email) | | The mail server. |
| `RVAV_SMTP_PORT` | `587` | | Its port. |
| `RVAV_SMTP_TLS` | `starttls` | | `starttls`, `tls` or `none`. |
| `RVAV_SMTP_FROM` | `rvav <rvav@localhost>` | | The sender. |
| `RVAV_SMTP_USERNAME` | none | | Its account, when it asks for one. |
| `RVAV_SMTP_PASSWORD` | none | yes | Its password. |
| `RVAV_TEAMS_APP_ID` | none (no Teams) | | The bot's Microsoft app ID. |
| `RVAV_TEAMS_TENANT_ID` | none | | The tenant. |
| `RVAV_TEAMS_APP_PASSWORD` | none | yes | The app's secret. |
| `RVAV_TEAMS_SERVICE_URL` | `https://smba.trafficmanager.net/teams` | | Bot Framework's service. |
| `RVAV_TEAMS_LOGIN_URL` | `https://login.microsoftonline.com` | | Where the bot signs in. |
| `RVAV_TEAMS_GRAPH_URL` | `https://graph.microsoft.com` | | Microsoft Graph. |
| `RVAV_TEAMS_OPENID_URL` | Bot Framework's | | Where the keys of Teams' calls to rvav are published. |
| `RVAV_SLACK_BOT_TOKEN` | none (no Slack) | yes | The Slack app's bot token. |
| `RVAV_SLACK_SIGNING_SECRET` | none | yes | Checks Slack's calls to rvav. |
| `RVAV_SLACK_API_URL` | `https://slack.com/api/` | | Slack's API. |

## The web app

The web image reads these at start-up and gives them to the browser
(`/config.js`); they default to the values the image was built with.

| Setting | Default | Secret | What |
|---|---|---|---|
| `RVAV_API_UPSTREAM` | `http://api:8080` | | The API, as nginx reaches it (the service's full name on Kubernetes). |
| `RVAV_WEB_AUTH_MODE` | the build's (`oidc`) | | `oidc`, or `dev` with a dev-auth API. |
| `RVAV_WEB_OIDC_AUTHORITY` | the build's | | The identity provider's issuer, as the browser reaches it. |
| `RVAV_WEB_OIDC_CLIENT_ID` | `rvav` | | The browser's client. |
| `RVAV_WEB_OIDC_SCOPE` | `openid profile email` | | The scopes it asks for (add `offline_access` for Entra ID). |
| `RVAV_WEB_SCRIPT_SOURCES` | none | | Where the browser may load scripts from besides the web app: the origins of `RVAV_WMKS_SDK_URLS` (`https://vc01.corp.example`), for the Content-Security-Policy. Add `'unsafe-eval'` only if the console's SDK needs it. |
