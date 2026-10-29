# Runbooks

What to do when an alert of [observability.md](observability.md) fires.
Each section is the anchor its alert links to.

## Before anything

**Where things run:**

| Process | What it does |
|---|---|
| API | The web app's calls. |
| Worker | Requests' workflows; sends events, webhooks and notifications. |
| Sync | Follows vCenter; reads EfficientIP's subnets and AWX's templates. One replica is active, the others stand by. |

**Where to look:**
- **Logs:** JSON on standard output, with the API's `request_id`
  (`kubectl logs deploy/rvav-api`, or `journalctl -u rvav` on VMs).
- **The Grafana dashboard:** what changed, and when.
- **A request:** its page shows each step's log and error.
- **Temporal's UI:** the workflow `request-<id>`, for what it waits on.

**Health:**
- `GET /readyz` answers 204 when the API reaches PostgreSQL and the
  identity provider. `rvav healthcheck` asks it from inside the container.
- The worker and the sync have no HTTP port. Their health is in their
  logs, their metrics (`:9090`), and Temporal's task queue.

## api-down

**RvavAPIDown:** no API replica answers Prometheus.

1. **Pods or units:** running? `kubectl get pods -l app.kubernetes.io/component=api`,
   or `systemctl status rvav`. A crash loop's reason is in the previous
   logs (`kubectl logs --previous`).
2. **Not ready (503 from `/readyz`):**
   - PostgreSQL unreachable: see the database's own health and
     `RVAV_DATABASE_URL`;
   - or the identity provider down: the log says
     `identity provider unavailable`. Users can't sign in until it is back,
     and requests in flight go on.
3. **Failing at start:** after an upgrade, a migration that failed holds
   back every pod (the init container's log). Fix forward or restore
   ([upgrades.md](upgrades.md)).
4. **Ready, but Prometheus can't scrape it:** the metrics port 9090, the
   PodMonitor and network policies. Check that users can still use the
   app.

## replica-down

**RvavReplicaDown:** one replica of a process doesn't answer for 5 minutes.
The others carry on ([high-availability.md](high-availability.md)).

Find the replica (the `instance` label) and why. Usually it is a node
drained or lost: Kubernetes reschedules it. When it doesn't come back,
the cause is the same as for [api-down](#api-down). A sync replica that
stands by answers too: it is down, not idle.

## api-errors

**RvavAPIErrors:** more than 5% of API calls answer 5xx.

1. **Which routes:** the dashboard's "API calls" by code, or
   `sum by (route, code) (rate(rvav_http_requests_total{code=~"5.."}[5m]))`.
2. **The errors in the logs:** find `"status":5` and the `request_id`.
   - `503` with `Retry-After`: the identity provider is unreachable.
   - `502` from nginx: no API replica reachable, see
     [api-down](#api-down).
   - `500`: a bug or the database. The log has the error; a database
     error names its constraint or the connection.
3. **An integration's errors:** the API calls EfficientIP, AWX and vCenter
   for forms and checks. Their failures answer 502 with the system named;
   see [integration-errors](#integration-errors).

## api-slow

**RvavAPISlow:** the API's 95th percentile is over 300 ms for 15 minutes.
The load test answered in under 15 ms ([load-test.md](load-test.md)).

1. **Which routes:** the dashboard's latency by route.
   - **Every route:** look at PostgreSQL first. Its CPU, slow queries
     (`pg_stat_statements`), locks (`pg_locks` waiting), connections.
   - **One route:** that route's queries, or the integration it calls
     (forms call EfficientIP and AWX for their options).
2. **The API's CPU and memory:** at their limits? Add replicas, or raise the
   limits.
3. **Many submissions for one name prefix at once** wait for each other
   (load-test.md, bottleneck 1). That is expected for bursts, not for
   15 minutes.

## requests-failing

**RvavProvisioningFailing:** more than 2% of requests failed in the last
hour (the 1.0 gate wants 98% to succeed).

1. **Requests → Failed:** the failed step and its error tell which
   system. The dashboard's "Workflow steps" shows failures by system.
2. **By system:**
   - **vsphere:** template, datastore space, cluster capacity, or rvav's
     vCenter permissions ([integrations.md](../integrations.md)).
   - **ipam or dns:** subnet full, or a name or address taken outside rvav.
   - **awx:** the job's output on the step. A playbook failing on every
     host is a playbook or credential problem, not rvav's.
3. **Fixed?** Retry the requests (Retry on the request's page, or
   `POST /api/v1/requests/{id}/retry`). Retrying starts again from the
   failed step.

## integration-errors

**RvavIntegrationErrors:** more than 10% of calls to vCenter, EfficientIP
or AWX fail.

1. **Which system:** the `system` label.
2. **Its log lines:** messages name it.
   - Refused credentials: a password expired or a token revoked. Update it
     under Integrations, then test it there.
   - Certificate errors: a renewed certificate from another CA. Add the CA,
     rather than setting `*_INSECURE`.
   - Timeouts or 5xx: the system itself. Ask its team.
3. **Meanwhile:** workflows retry their steps with backoff, so requests
   wait rather than fail, until a step's attempts run out.

## requests-stuck

**RvavRequestsNotPickedUp:** requests are queued and none runs, for 15
minutes.

1. **Workers:** running? `kubectl get pods -l app.kubernetes.io/component=worker`.
   Their logs say whether they reach Temporal.
2. **Temporal:** its frontend reachable (`RVAV_TEMPORAL_ADDRESS`, TLS)?
   The task queue `rvav-provision` in Temporal's UI should have pollers;
   none means no worker is connected.
3. **A request queued but without a workflow** (none named
   `request-<id>` in Temporal): its workflow never started. The API
   stopped between saving the request and starting it. Cancel the
   request (that works without its workflow), and the requester submits
   it again.

## inventory-stale

**RvavInventoryNotFollowed / RvavInventoryStale:** the sync doesn't follow
a vCenter, or hasn't read it for 15 minutes.

1. **The active sync:** its logs say which vCenter fails and why
   (connection, credentials, a session expired).
2. **No active sync:** the replicas take turns through a PostgreSQL lock.
   If all are down, see [replica-down](#replica-down).
3. **The effect:** VM states shown in rvav age, and drift isn't seen.
   Provisioning still works; it talks to vCenter directly.
4. **Afterwards:** a full read every `RVAV_SYNC_INTERVAL` (2 minutes)
   catches up; nothing is lost.

## queue-backlog

**RvavQueueBacklog:** events, subscription runs or notifications have
waited more than 10 minutes.

1. **Which queue:** the `queue` label. The worker sends all three.
2. **events:** the worker's dispatcher is behind or stopped. Check the
   worker's logs.
3. **subscriptions:** a webhook or AWX target failing; runs retry with
   backoff. Its subscription page lists the runs and their errors.
   - A blocking subscription holds its requests until it answers or times
     out.
   - Fix the target, or turn the subscription off.
4. **notifications:** the mail server, Teams or Slack refusing. The
   delivery's error names it (`RVAV_SMTP_*`, the bot's credentials).

## approvals-overdue

**RvavApprovalsOverdue:** approvals are past their due time for an hour.

Not a fault. Approvers haven't decided, and levels with a timeout act on
it (escalate, approve or reject) by their policy. The request's approvals
show who should decide; they were notified when it reached them. Raise it with the
approvers, or change the policy's timeout under Administration →
Policies.

## vms-missing

**RvavVMsMissing:** more VMs are missing from vCenter than an hour ago.

1. **Which:** Deployments → sync state "missing". A VM deleted in vCenter
   outside rvav is missing, and its address and DNS records are still
   held.
   - **Deleted on purpose:** delete the deployment in rvav to release them.
   - **Not on purpose:** restore it in vCenter. rvav matches VMs by their
     instance UUID, so a restore that keeps it is found again. Otherwise
     adopt the restored VM (Administration → Onboarding).
2. **Many at once:** usually a vCenter, a datacenter or a folder rvav can no
   longer see. That is a permission or connection change, not deletions;
   see [inventory-stale](#inventory-stale). Check before deleting
   anything.
