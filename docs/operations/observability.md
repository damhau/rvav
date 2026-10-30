# Observability

rvav gives operators metrics, traces and logs (S14.5). The dashboard and
alerts below are the starting point of the runbooks
([runbooks.md](runbooks.md)).

## Metrics

Every process serves Prometheus metrics on `RVAV_METRICS_ADDR` (`:9090`,
`/metrics`), apart from the API's port, so they're never exposed through
the ingress. On Kubernetes the pods carry `prometheus.io/scrape`
annotations. With the Prometheus operator, the chart makes PodMonitors
(`metrics.podMonitor`) and the alerts as a PrometheusRule
(`metrics.prometheusRule`). The alerts and the dashboard expect the jobs
`rvav-api`, `rvav-worker` and `rvav-sync`: the PodMonitors name them so;
a scrape configuration of the site's own (from the annotations) must too.

| Metric | Labels | What |
|---|---|---|
| `rvav_http_requests_total` | method, route, code | API calls. `route` is the endpoint's pattern, never a path with IDs. |
| `rvav_http_request_duration_seconds` | method, route | How long they take. The live stream and the console are left out. |
| `rvav_requests_finished_total` | kind, outcome | Requests that ran to an end: succeeded, failed or cancelled. |
| `rvav_request_duration_seconds` | kind | From the start of the run to its end. |
| `rvav_steps_total` | system, outcome | Workflow steps by the system they act on (vsphere, ipam, dns, awx, nsx, rvav…), done or failed. |
| `rvav_step_duration_seconds` | system | How long they take. |
| `rvav_integration_calls_total` | system, outcome | Calls to vCenter, EfficientIP, AWX and NSX (`nsx`). An error is a failed call, or a 5xx from an HTTP API. |
| `rvav_integration_call_duration_seconds` | system | How long they take. |
| `rvav_inventory_watch_connected` | vcenter | 1 while the sync follows the vCenter's changes (sync). |
| `rvav_inventory_last_full_read_timestamp_seconds` | vcenter | The last full read (sync). |

What waits in the database is reported by the API at each scrape. Every
API replica reports the same figures, so take their `max`.

| Metric | Labels | What |
|---|---|---|
| `rvav_requests_active` | status | Requests not finished: pending_approval, scheduled, queued, running, cancelling. |
| `rvav_approvals_pending` | overdue | Approval levels waiting, past their due time or not. |
| `rvav_queue_depth`, `rvav_queue_oldest_seconds` | queue | Events not dispatched yet, subscription runs and notification deliveries due, and how long the oldest has waited. |
| `rvav_vms` | sync_state | VMs rvav manages: in sync, drifted, missing. |
| `rvav_inventory_age_seconds` | vcenter | Since a VM of the vCenter was last read. |

Go runtime and process metrics (`go_*`, `process_*`) come with them.
Temporal's server has metrics of its own: task queue backlog, workflow
latency. Scrape them too, as its Helm chart documents.

## Dashboard

`deploy/observability/grafana/dashboards/rvav.json` (Grafana 10 and later)
has these rows:

- **Stat tiles:** requests running and waiting for approval, the success
  rate over 24 hours, the API's 95th percentile, VMs drifted and missing;
- **The API:** calls by status code, and latency by route;
- **Requests:** finished, and their duration;
- **Steps:** by system, and their duration;
- **Integrations:** calls and latency;
- **Queues and approvals;**
- **The inventory:** following each vCenter, and its age.

## Alerts

`deploy/helm/rvav/files/alerts.yml`, one set of rules for Prometheus on
the compose stack and the chart's PrometheusRule. Each names its runbook.

| Alert | Severity | When |
|---|---|---|
| RvavAPIDown | critical | No API replica answers, or none is found, for 2 minutes. |
| RvavReplicaDown | warning | A replica of a process doesn't answer for 5 minutes. |
| RvavAPIErrors | warning | More than 5% of API calls fail (5xx) for 10 minutes. |
| RvavAPISlow | warning | The API's 95th percentile is over 300 ms for 15 minutes (the 1.0 gate's service level). |
| RvavProvisioningFailing | warning | More than 2% of requests failed in the last hour, of at least 10 (the gate's 98%). |
| RvavIntegrationErrors | warning | More than 10% of calls to an integration fail for 10 minutes. |
| RvavRequestsNotPickedUp | critical | Requests wait and none runs for 15 minutes: the workers or Temporal are down. |
| RvavInventoryNotFollowed | warning | The sync doesn't follow a vCenter's changes for 10 minutes. |
| RvavInventoryStale | warning | A vCenter's inventory is older than 15 minutes. |
| RvavQueueBacklog | warning | Events, subscription runs or notifications have waited more than 10 minutes to go out. |
| RvavApprovalsOverdue | info | Approvals are past their due time for an hour. |
| RvavVMsMissing | info | VMs went missing from vCenter in the last hour. |

## Traces

Set the standard `OTEL_EXPORTER_OTLP_ENDPOINT` to an OpenTelemetry
collector (OTLP over HTTP). Every process then sends traces as
`rvav-api`, `rvav-worker` and `rvav-sync`. Without it, nothing is traced.

A request's trace follows it from the API call
(`POST /api/v1/requests`) through Temporal (`StartWorkflow`,
`RunWorkflow:Provision`) into each activity (`RunActivity:CloneVM`…) and
the HTTP calls they make to EfficientIP and AWX. Sample it with
`OTEL_TRACES_SAMPLER=parentbased_traceidratio` and
`OTEL_TRACES_SAMPLER_ARG=0.1` when traffic is high.

## Logs

Every process logs JSON lines to standard output. They have `time`,
`level`, `msg` and `process`; the API adds `request_id` (also in the
`X-Request-Id` header and in error responses) and its access log line per
call. `RVAV_LOG_LEVEL` sets the level. Secrets are never logged.

**Retention policy:**

- **Application logs:** in the site's log platform, 30 days searchable,
  1 year archived.
- **The audit log:** in rvav's database, kept as long as the database (it
  is the record of who did what). Export it as CSV for the archive.
- **Request history:** steps and their logs stay with each request in
  rvav's database.
- **Temporal's workflow histories:** the namespace's retention, 30 days
  in production (`temporal operator namespace update --retention 30d`).
  rvav's own record of each request doesn't depend on it.
- **Metrics:** 30 days in Prometheus, longer downsampled when the site has
  long-term storage.

## On the compose stack

```
docker compose -f deploy/docker-compose.yml -f deploy/observability/docker-compose.observability.yml up -d --wait
```

| Tool | Address |
|---|---|
| Prometheus | http://localhost:9091 (it scrapes every replica and evaluates the alerts) |
| Grafana | http://localhost:3001 (the dashboard) |
| Jaeger | http://localhost:16686 (the traces) |
