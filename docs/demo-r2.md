# R2 demo script

About an hour, on the Docker Compose lab stack with Keycloak sign-in, or
on the pilot environment once its systems are connected. The lab stack
runs vcsim, the fake SOLIDserver and the fake AWX.

It covers what R2 adds to R1 ([demo-r1.md](demo-r1.md)):
- extensibility (S10);
- notifications and request options (S11);
- advanced provisioning (S12);
- inventory and reporting (S13);
- production readiness (S14).

Each step says what to show and what to point out. The report for the
sign-off is [release-r2.md](release-r2.md).

## Before the demo

```sh
RVAV_AUTH_MODE=oidc docker compose -f deploy/docker-compose.yml \
  -f deploy/observability/docker-compose.observability.yml up -d --build --wait
# The example custom workflow's worker, on the stack's Temporal:
docker build -f examples/decommission/Dockerfile -t decommission-worker .
docker run -d --rm --name decommission-worker --network rvav_default \
  -e TEMPORAL_ADDRESS=temporal:7233 decommission-worker
```

For step 5, `govc` against vcsim:

```sh
export GOVC_URL=https://user:pass@127.0.0.1:8989/sdk GOVC_INSECURE=1
```

| What | Where |
|---|---|
| Portal | http://localhost:3000 |
| Keycloak (users `lea`, `thomas`, `marc`; password `rvav`) | http://localhost:8081 |
| Mail (Mailpit) | http://localhost:8025 |
| Temporal UI | http://localhost:8233 |
| Grafana, Prometheus, Jaeger | http://localhost:3001, :9091, :16686 |

**Who is who:**
- **Thomas** is a developer, member of Web Platform.
- **Léa** is Web Platform's project admin and a catalog admin.
- **Marc** is an Infrastructure lead and an administrator.

Use one private window per person.

## 1. A composite of several VMs: the PostgreSQL cluster (Marc, S10, S12)

The PostgreSQL 16 cluster is offered to the Data, Finance and Core
projects. Marc requests it in Data, as an administrator is project admin
everywhere.

1. **Catalog → PostgreSQL 16 cluster:** one form for a whole service. The
   preview shows the three nodes, their size and placement, and the quota
   they use.
2. **Submit request.** The timeline shows:
   - three RHEL nodes built side by side, each with a data disk, its
     address and DNS record;
   - a DRS rule keeping them on separate hosts;
   - then AWX installing PostgreSQL and forming the Patroni cluster;
   - then the database's owner role, a later step bound to the earlier
     ones' outputs.
3. **The deployment:** the nodes as one deployment.
4. **On failure:** the undo steps run, and **Retry from the failed step**
   resumes.

## 2. Request options and notifications (Thomas, Léa, Marc; S11)

1. **Thomas, in production:**
   - two VMs at once, starting tomorrow at 7:00 (Options);
   - **Save as draft**, then resume it from the catalog.
2. **Léa, the project admin:** requests for Thomas (For someone else).
   Thomas owns it and is told.
3. **The bell:** what concerns each person. **Mailpit:** the emails,
   per person, in their language.
4. **Marc approves** from the inbox. Thomas's notification says so. On the
   pilot, the same approval works from a Teams card or a Slack message
   (#82).

## 3. Subscriptions (Marc, S10)

1. **Administration → Subscriptions → New:** on `provisioning.post`, run
   an AWX template with the event (on the lab stack, one of the fake
   AWX's, such as `ddi-dns-record`). Test it with a sample event: the run
   is listed with its result.
2. **Thomas requests a VM:** once it is ready, the subscription's run
   appears, linked to the request.
3. **Explain blocking subscriptions:** a subscription on
   `provisioning.pre` marked blocking is run inside the request's
   workflow. A receiver such as a CMDB can stop the request before
   anything is built, or set properties on the deployment. The timeline
   shows each check ([ADR-0021](adr/0021-event-subscriptions.md)).

## 4. A custom workflow (Marc, Thomas; S10)

1. **Administration → Custom workflows:** register `decommission`, as its
   README says ([examples/decommission](../examples/decommission/README.md)),
   as a day-2 action on deployments.
2. **Thomas runs Decommission** on a VM: the workflow's own steps appear in
   the timeline. Its approval arrives in Marc's inbox, like any other.
3. **The Temporal UI:** the request's workflow and its child. The helpers
   (`pkg/rvavflow`, `sdk/python`) keep the contract with rvav.

## 5. The inventory, drift and onboarding (Marc, S13)

1. **Live inventory:** `govc vm.power -off <a VM's name>`. The deployment
   shows it off within a second.
2. **Drift:** `govc vm.change -vm <name> -m 8192`. The deployment shows
   what differs. **Accept the changes** takes the VM as it is.
3. **Administration → Onboarding:** vcsim's own VMs, which rvav doesn't
   manage. Adopt several at once with an owner, project, item and lease.
   Their addresses are looked up in EfficientIP, and nothing changes
   there or in vCenter.
4. **Reports:**
   - requests over time and their success rate;
   - provisioning time and approval lead time;
   - quota use and leases ending;
   - export as CSV.

## 6. Production readiness (Marc, S14)

1. **Grafana:** API calls and latency by route, requests finished,
   workflow steps and integration calls by system, queues, the inventory.
   **Prometheus → Alerts:** the twelve rules, each naming its runbook
   ([runbooks.md](operations/runbooks.md)).
2. **Jaeger → service rvav-api:** the trace of Thomas's request, from
   `POST /api/v1/requests` through Temporal into each activity and its
   calls to EfficientIP and AWX.
3. **Security:**
   - a secret form field shows as `••••` on the request page, and isn't in
     its plan or the workflow history (Temporal UI);
   - the response headers (Content-Security-Policy);
   - an API token made and revoked;
   - `/api/v1/openapi.json` with it.
4. **What was tested beyond the demo**
   ([release-r2.md](release-r2.md#the-10-gate-implementation-plan-section-8)):
   - replicas killed under load;
   - a restore to a moment;
   - an upgrade with requests in flight;
   - an hour at 200 people;
   - accessibility checks on every page;
   - AWX 24.6.1 in CI.

## What the demo does not show

- **Real vCenters, SOLIDserver, AWX, identity provider, mail, Teams and
  Slack:** the [Lab acceptance](https://github.com/damhau/rvav/milestone/9).
- **The penetration test, a person's accessibility check, and the pilot's
  feedback** ([release-r2.md](release-r2.md)).
