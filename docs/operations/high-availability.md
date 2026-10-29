# High availability

rvav has no single point of failure when each of its parts runs at least
twice, on separate nodes (S14.1). Losing one API, worker, sync or Temporal
node interrupts nothing: requests carry on, the web app keeps working, and
the inventory keeps following vCenter.

## The parts

| Part | Replicas | How they share the work |
|---|---|---|
| **API** (`rvav api`) | 2 or more, behind a load balancer | Stateless. Sessions are OIDC tokens; console tokens and idempotency keys are in PostgreSQL. Live updates come from PostgreSQL (`LISTEN`), so every replica streams every event. |
| **Web app** (nginx) | 2 or more | Static files. It proxies `/api/` to the API service; with several addresses it skips one that is gone and retries idempotent requests on the next. |
| **Worker** (`rvav worker`) | 2 or more | Temporal hands each workflow task and activity to one worker. An activity on a worker that dies runs again elsewhere after its heartbeat or start-to-close timeout: activities are idempotent. Subscription deliveries and notifications are claimed row by row (`for update skip locked`). |
| **Sync** (`rvav sync`) | 2 | One active, holding a PostgreSQL advisory lock; the other checks every 5 seconds and takes over as soon as the active one's connection is gone. The EfficientIP subnet sync and the AWX catalog sync run one at a time under their own locks. |
| **Temporal** | a cluster of 3 or more nodes, or Temporal Cloud | See below. |
| **PostgreSQL** | a primary and a synchronous standby, with automatic failover | rvav's database and Temporal's. See backups (S14.3). |
| **Identity provider** | the organisation's | Tokens are checked against its published keys, which rvav caches. |

Long waits aren't sleeping workflows: approvals wait for signals, and leases
and retention run on Temporal schedules. So replicas can come and go, and
upgrades roll, while requests wait (S14.4).

## Temporal

A production Temporal is either:

- **Self-hosted:** the official Helm chart with each service (frontend,
  history, matching, worker) at two replicas or more, on PostgreSQL (or
  Cassandra) with its own high availability. rvav connects to the frontend
  service: `RVAV_TEMPORAL_ADDRESS`, and with TLS `RVAV_TEMPORAL_TLS=1`,
  `RVAV_TEMPORAL_TLS_CA_FILE`, and for mutual TLS
  `RVAV_TEMPORAL_TLS_CERT_FILE` and `RVAV_TEMPORAL_TLS_KEY_FILE`
  (`RVAV_TEMPORAL_TLS_SERVER_NAME` when the certificate names another
  host).
- **Temporal Cloud:** `RVAV_TEMPORAL_ADDRESS` is the namespace's endpoint,
  `RVAV_TEMPORAL_NAMESPACE` its name, and `RVAV_TEMPORAL_API_KEY` an API
  key (or a client certificate as above).

rvav's client resolves the address with DNS and reconnects to another
frontend when one goes.

## Trying it on one machine

`deploy/docker-compose.ha.yml` runs two of each rvav process and a Temporal
cluster of two nodes on the compose stack's PostgreSQL; `deploy/ha-test.sh`
then kills an API replica, a worker, the active sync and a Temporal node in
turn while requests are submitted and read, and checks that:

- every request submitted succeeds (a submission a dying replica refused is
  sent again with its Idempotency-Key);
- reads keep answering (at most a few fail while a dead replica's address
  is still tried);
- a live event stream carries on after reconnecting from its last event;
- the standby sync takes over and reads the inventory.

```
docker compose -f deploy/docker-compose.yml -f deploy/docker-compose.ha.yml up -d --build --wait
deploy/ha-test.sh
```

One machine shows how failover behaves, not the capacity or the network of
a real cluster: the Helm chart (S14.2) is for that.

## What isn't covered here

PostgreSQL's own failover (Patroni, a managed database, or the platform's
operator) and the load balancer in front of the web app are the platform's:
rvav reconnects to the database when it fails over, and the API's
`/readyz` tells a load balancer when a replica can't reach the database.
