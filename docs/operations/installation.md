# Installing rvav

rvav runs on Kubernetes with its Helm chart, or on VMs with Docker Compose
and systemd (ADR-0009). Both use the same two images:

- `ghcr.io/damhau/rvav`: the `api`, `worker`, `sync` and `migrate`
  processes;
- `ghcr.io/damhau/rvav-web`: the web app (nginx), which also proxies the
  API on the same origin.

An install is given only what rvav needs before it can read its database:
the database, the secrets key, Temporal, its addresses and the local
administrator's first password ([configuration.md](configuration.md),
**Set in: install**). The rest is set from rvav's UI (ADR-0030): its first
sign-in opens on a setup checklist, from the identity provider to a first
catalog item ([admin guide](../guides/admin.md#first-steps)). Each
integration's environment variables still work, and lock their fields in
Administration. Each release publishes the images' SBOMs; what the
installation must do for security (TLS, the key, the network) is in
[security.md](security.md).

## What rvav needs

- **PostgreSQL** 14 or later for rvav's database, highly available
  (a primary and a synchronous standby), with point-in-time backups
  ([backups.md](backups.md)). Temporal can use another database on the same
  server.
- **Temporal**: a cluster (the official Helm chart) or Temporal Cloud, with
  a namespace for rvav ([high-availability.md](high-availability.md)).
- **An OIDC identity provider**: a client for rvav (public, with PKCE; the
  web app's address as a redirect URI), with a groups claim. Set in
  Administration once rvav runs; until then the local administrator signs
  in.
- **vCenter, EfficientIP SOLIDserver and AWX**, with rvav's service accounts
  ([integrations.md](../integrations.md)), also set in Administration.
- A 32-byte key for sealing secrets: `openssl rand -base64 32`, kept in a
  vault, in `RVAV_SECRETS_KEY`.

## Kubernetes, with Helm

The chart (`deploy/helm/rvav`) runs two replicas each of the API, the
worker, the sync (one active) and the web app, spread over nodes, with
disruption budgets, a read-only file system and no root. Every rvav pod
applies the database migrations as it starts, one at a time.

1. Write a values file with the install settings. Plain settings go under
   `config`, passwords and keys under `secret.env` (`RVAV_DATABASE_URL`,
   `RVAV_SECRETS_KEY`, `RVAV_ADMIN_PASSWORD`). Alternatively, leave
   `secret.create` false and name a Secret made elsewhere (sealed-secrets,
   External Secrets) in `secret.existingSecret`.

   ```yaml
   config:
     RVAV_PUBLIC_URL: https://rvav.corp.example
     RVAV_TEMPORAL_ADDRESS: temporal-frontend.temporal.svc.cluster.local:7233
     RVAV_TEMPORAL_NAMESPACE: rvav
   secret:
     existingSecret: rvav-env
   ingress:
     host: rvav.corp.example
     className: nginx
     tls: { secretName: rvav-tls }
   ```

2. Install it:

   ```sh
   helm upgrade --install rvav deploy/helm/rvav -n rvav --create-namespace -f site-values.yaml --wait
   ```

3. Check it: the API pods are ready once they reach the database, and
   the identity provider when one is set (`/readyz`).
4. Sign in as `admin`, the local administrator (ADR-0030), and choose its
   password. Its first one is `secret.env.RVAV_ADMIN_PASSWORD`, or, without
   it, the one the API generated and logged once; the chart's notes say
   how to find it. `rvav admin reset-password`, run in an API pod, sets
   another.
5. Follow the setup checklist (**Administration › Setup**), which opens
   at that sign-in: the identity provider and a group mapped to `admin`,
   vCenter, environments and projects, EfficientIP, AWX and a first
   catalog item ([admin guide](../guides/admin.md#first-steps)).
   **Administration › System** then shows each process applying the
   connections, and the install settings as the API reads them.

**Applying a connection:** each pod's container exits with status 3 and
Kubernetes starts it again, the API pods one at a time: the next once
the one before has run for 10 s, so that one is always ready. Each counts as a
container restart: several within ten minutes of each other wait
Kubernetes' back-off (10 s, 20 s, 40 s…), so save connections together
where you can (saved within 3 s, they apply with one restart).
**Administration › System** shows a process restarting until it runs
again.

**Ingress:** the ingress sends everything to the web app, which proxies
`/api/` to the API. It must not buffer the live updates (SSE), close the
console's WebSocket or a long vRA import, or refuse a vra-export file of
up to 64 MB. For ingress-nginx the chart sets this by default
(`ingress.annotations`):
- `proxy-read-timeout` and `proxy-send-timeout`: `"3600"`;
- `proxy-buffering`: `"off"`;
- `proxy-body-size`: `64m`.

Another controller needs the same settings in its own annotations.

**Upgrades:** `helm upgrade` with the new image tag. The pods roll one at
a time, and the new ones migrate before they start
([upgrades.md](upgrades.md)).

**Tried on kind:** `deploy/helm/kind/kind-test.sh` installs the chart on a
kind cluster of its own, with what rvav needs (`deps.yaml`). It then:

- signs in through Keycloak and runs the smoke test through the web app;
- replaces an API pod while reading;
- upgrades with a changed setting.

## On VMs, with Docker Compose and systemd

For sites without Kubernetes. On each VM (Docker Engine with the Compose
plugin):

1. Copy `deploy/vm/docker-compose.yml` to `/opt/rvav/` and
   `deploy/vm/rvav.service` to `/etc/systemd/system/`.
2. Write `/etc/rvav/rvav.env` from `deploy/vm/rvav.env.example`, and
   `/etc/rvav/web.env` from `web.env.example`. Make both readable by root
   only (`chmod 600`).
3. Set the version in `/etc/rvav/version.env` (`RVAV_VERSION=1.0.0`).
4. Start it: `systemctl enable --now rvav`.
5. Sign in as `admin`, the local administrator (ADR-0030), and choose its
   password. Its first one is `RVAV_ADMIN_PASSWORD` in `rvav.env`, or the
   one the API generated and logged once:
   `docker compose -f /opt/rvav/docker-compose.yml logs api | grep "local administrator created"`.
6. Follow the setup checklist, as on Kubernetes (step 5 above).

`rvav.env` holds the install settings only (`rvav.env.example`). The
processes restart themselves to apply a connection saved in
Administration (they exit with status 3); the Compose file's `restart`
policy and the systemd unit start them again.

The web app listens on `127.0.0.1:8080` (`RVAV_WEB_BIND` to change it).
Put the site's reverse proxy or load balancer, with TLS, in front of it.

**High availability:** two VMs run the same file behind a load balancer.
On both, the sync is active on one and waits on the other.

**Upgrades:** change `RVAV_VERSION`, then `systemctl restart rvav`. The
migrations run first. With two VMs, upgrade one, then the other.

**Logs:** Docker's JSON files, rotated at 50 MB × 5 per container. Ship
them to the site's log platform ([observability.md](observability.md)).

## Trying a new install

The development stack (`deploy/docker-compose.yml`) starts seeded, with
every integration set in its environment. With
`deploy/docker-compose.fresh.yml` over it, it starts as a new install:
the schema without seed data, no integration variables for rvav's
processes, and OIDC sign-in with the local administrator. The fakes
(vcsim, EfficientIP, NSX, Keycloak, Mailpit) still run, and are set up
from the UI:

```sh
RVAV_ADMIN_PASSWORD='a first password' docker compose -p rvav-fresh \
  -f deploy/docker-compose.yml -f deploy/docker-compose.fresh.yml up --build -d
```

Open `http://localhost:3000`, sign in as `admin` and follow the checklist.
The override's header lists the fakes' addresses and accounts to enter
(vCenter `https://vcsim:8989/sdk`, EfficientIP `http://efficientip:8080`,
Keycloak's realm as the identity provider). AWX stays the in-process
fake. On a database without network mappings the fake EfficientIP serves
vcsim's networks, so the sync reads them from it.

A project name of its own (`-p`) keeps its database apart from the
development stack's; the `RVAV_*_PORT` variables move its ports when both
run. `docker compose -p rvav-fresh -f deploy/docker-compose.yml -f
deploy/docker-compose.fresh.yml down -v` removes it, its volume included.
