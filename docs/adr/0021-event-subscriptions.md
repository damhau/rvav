# ADR-0021: Events and subscriptions

- Status: accepted
- Date: 2026-10-28

## Context
vRA's event broker lets administrators run their own code when something
happens: register a VM in the CMDB before it is built, refuse a request
the CMDB doesn't know, tell the backup system a VM is gone. S10
([milestone](https://github.com/damhau/rvav/milestone/3)) brings the same
to rvav, as level 1 and 2 of [ADR-0007](0007-extensibility-model.md): an
administrator subscribes a webhook, an AWX template or a custom workflow
([ADR-0022](0022-custom-workflows-and-composites.md)) to a topic.

rvav already has an `events` table: the live feed of the UI (request
updated, step changed), written after the change and allowed to lose an
event. Subscriptions need more: no event lost, each delivered at least
once, and a record of what was sent and answered.

## Decision

### Topics

| Topic | When | May block |
|---|---|---|
| `request.submitted` | A request (catalog or day-2) is accepted, before any approval | No |
| `provisioning.pre` | A catalog request is approved and about to build | Yes |
| `provisioning.post` | A catalog request ended (succeeded, failed or cancelled; `outcome` says) | No |
| `day2.pre` | A day-2 request is about to run (not a deletion) | Yes |
| `day2.post` | A day-2 request ended | No |
| `delete.pre` | A deployment is about to be deleted (by someone, or at the end of its lease) | Yes |
| `delete.post` | The deletion ended | No |
| `lease.expiring` | The reminder before a lease ends | No |
| `lease.expired` | A lease ended and its VMs are being powered off | No |

### The event
Every event has the same envelope, version `rvav.event.v1`:

```json
{
  "id": "evt_01…", "topic": "provisioning.pre", "contract": "rvav.event.v1",
  "occurred_at": "2026-10-28T10:00:00Z",
  "data": {
    "request": {"id": "req_…", "number": "REQ-10042", "kind": "provision", "action": null,
                "status": "running", "outcome": null},
    "item": {"id": "itm_…", "slug": "rhel9-server", "type": "vm_blueprint", "version": "3.2"},
    "project": {"id": "prj_web", "code": "web", "name": "Web Platform", "cost_center": "CC-4120"},
    "environment": "dev", "production": false,
    "requester": {"id": "usr_…", "name": "Thomas Nguyen", "email": "…"},
    "owner": {"id": "usr_…", "name": "…"},
    "deployment": {"id": "dep_…", "name": "web-dev-app001", "status": "provisioning",
                   "properties": {}, "lease_expires_at": "…"},
    "deployments": ["… every deployment of the request"],
    "resources": [{"id": "res_…", "type": "vsphere.vm", "name": "web-dev-app001",
                   "fqdn": "…", "ip": "10.31.10.57", "moref": "vm-1042", "deployment_id": "dep_…"}],
    "inputs": {"role": "app", "size": "M"},
    "vm": {"vcpu": 4, "memory_gb": 16, "disk_gb": 80, "count": 1}
  }
}
```

Fields that don't apply are null: `request` and `requester` on lease
topics, `vm` on day-2 ones; `resources` is empty before provisioning. A
post event's `request.outcome` is `succeeded`, `failed` or `cancelled`. Inputs of secret fields are
left out, as in the API. Within `v1`, fields are only added, never removed
or changed: receivers ignore what they don't know.

### The outbox
An event is written to `integration_events` in the same transaction as
the change it describes (the request inserted, the request marked running,
the request finished, the reminder sent). A dispatcher in rvav's worker
process reads new events, finds the enabled subscriptions to their topic
whose condition is true, and makes one **run** per subscription and event
(unique on both, so a crash between the two never doubles it). A
subscription gets the events that happen once it exists, never earlier
ones. Each run is
delivered at least once: on failure it is retried with back-off (10 s,
30 s, 1 min, 5 min, then every 15 min) for 24 hours, then marked failed.
Runs keep the payload, every attempt's status, answer (first 4 KB) and
duration, for 30 days, and an administrator can retry a failed one.

Several worker replicas share the dispatching (`for update skip locked`).
Events are not ordered across subscriptions; receivers use the event ID
(`X-Rvav-Event-Id`) to ignore one they already have.

### Subscriptions
A subscription has a name, a topic, a CEL **condition** over the event's
`data` (`environment == "prd" && item.slug == "rhel9-server"`, as in the
approval and day-2 policies; checked on a sample of the topic when saved), a
**priority** (lower first), a **timeout**, a target, whether it is
**blocking**, and whether it is enabled. Administrators manage them;
auditors read them; every change is audited (`subscription.*`).

### Targets
- **Webhook.** A `POST` of the event as JSON to an `https` URL on a host
  listed in `RVAV_WEBHOOK_HOSTS` (no allowlist, no webhooks). It is signed
  with HMAC-SHA256 under a secret from the secret store:
  `X-Rvav-Signature: t=<unix seconds>,v1=<hex of HMAC("<t>.<body>")>`, so
  a receiver can check it and refuse old ones. A 2xx answer is delivered;
  anything else, or no answer within the timeout, is retried.
- **AWX template.** Launched with the event as the extra variable
  `rvav_event`; delivered when the job succeeds.
- **Custom workflow** ([ADR-0022](0022-custom-workflows-and-composites.md)).
  Started with the event in its context; delivered when it completes.

### Blocking
Only `*.pre` topics may have blocking subscriptions. They don't go through
the dispatcher: the request's workflow runs them, in priority order, as
steps of its timeline ("CMDB check"), right before it builds, runs the
action or deletes. Each answers one of:

```json
{"outcome": "continue", "properties": {"cmdb_id": "CI00042", "cost_center": "CC-4120"}}
{"outcome": "stop", "reason": "The CMDB has no application web-checkout."}
```

- **Stop** ends the request as failed at that step, with the reason, before
  anything was built or changed. Later subscriptions don't run.
- **Continue** may set **properties** on the deployment: string values
  under keys of lower-case letters, digits and underscores, at most 50.
  They are shown on the deployment, passed to AWX jobs in `rvav.properties`
  and to later subscriptions. Nothing else can be changed: not inputs,
  sizes, placement or names, which policies and quotas already approved.
  Properties are for `provisioning.pre`; on `day2.pre` and `delete.pre` a
  subscription can only continue or stop.
- A webhook answers with that JSON (an empty 2xx is continue). An AWX job
  continues when it succeeds and stops when it fails, with its error as the
  reason. A custom workflow returns that object.
- **On timeout or error** (no answer, 5xx, malformed answer), the
  subscription's `on_failure` decides: `stop` (the default: a check that
  can't be made doesn't pass) or `continue`. A blocking webhook is tried
  once more within its timeout, not retried for hours.
- A blocking run is recorded like the others, linked to the request step.
  A request stopped by a check changed nothing: a day-2 deployment keeps
  its status, and the request can be retried once the check would pass.
- Workflows that started before blocking subscriptions existed don't run
  them (`workflow.GetVersion`), so upgrading rvav doesn't break requests in
  flight.

## Consequences
- What happens before and after provisioning is configured, not coded,
  for most integrations, and every delivery can be traced.
- At-least-once delivery means receivers must be idempotent; the event ID
  is there for that.
- Blocking subscriptions make provisioning depend on the systems they
  call: their timeout and `on_failure` are the administrator's choice, and
  the timeline shows which one stopped a request.
- `v1` of the event is a contract: changing it means `v2`, sent alongside
  `v1` for a deprecation period, chosen per subscription.
