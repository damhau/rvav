# Installing rvav

rvav runs on Kubernetes with its Helm chart, or on VMs with Docker Compose
and systemd (ADR-0009). Both use the same two images:

- `ghcr.io/damhau/rvav`: the `api`, `worker`, `sync` and `migrate`
  processes;
- `ghcr.io/damhau/rvav-web`: the web app (nginx), which also proxies the
  API on the same origin.

Every setting is an environment variable
([configuration.md](configuration.md)). Each release publishes the images'
SBOMs; what the installation must do for security (TLS, the key, the
network) is in [security.md](security.md).

## What rvav needs

- **PostgreSQL** 14 or later for rvav's database, highly available
  (a primary and a synchronous standby), with point-in-time backups
  ([backups.md](backups.md)). Temporal can use another database on the same
  server.
- **Temporal**: a cluster (the official Helm chart) or Temporal Cloud, with
  a namespace for rvav ([high-availability.md](high-availability.md)).
- **An OIDC identity provider**: a client for rvav (public, with PKCE; the
  web app's address as a redirect URI), with a groups claim.
- **vCenter, EfficientIP SOLIDserver and AWX**, with rvav's service accounts
  ([integrations.md](../integrations.md)).
- A 32-byte key for sealing secrets: `openssl rand -base64 32`, kept in a
  vault, in `RVAV_SECRETS_KEY`.

## Kubernetes, with Helm

The chart (`deploy/helm/rvav`) runs two replicas each of the API, the
worker, the sync (one active) and the web app, spread over nodes, with
disruption budgets, a read-only file system and no root. Every rvav pod
applies the database migrations as it starts, one at a time.

1. Write a values file with the site's settings. Plain settings go under
   `config`, passwords and keys under `secret.env`. Alternatively, leave
   `secret.create` false and name a Secret made elsewhere (sealed-secrets,
   External Secrets) in `secret.existingSecret`.

   ```yaml
   config:
     RVAV_PUBLIC_URL: https://rvav.corp.example
     RVAV_OIDC_ISSUER: https://sso.corp.example/realms/rvav
     RVAV_TEMPORAL_ADDRESS: temporal-frontend.temporal.svc.cluster.local:7233
     RVAV_TEMPORAL_NAMESPACE: rvav
     RVAV_VCENTER_URL: https://vc01.corp.example/sdk
     # ...
   secret:
     existingSecret: rvav-env
   web:
     oidcAuthority: https://sso.corp.example/realms/rvav
   ingress:
     host: rvav.corp.example
     className: nginx
     tls: { secretName: rvav-tls }
   ```

2. Install it:

   ```sh
   helm upgrade --install rvav deploy/helm/rvav -n rvav --create-namespace -f site-values.yaml --wait
   ```

3. Check it: the API pods are ready once they reach the database and the
   identity provider (`/readyz`). Sign in as an administrator (a member of
   the group mapped to `admin`) and check Integrations.

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

The web app listens on `127.0.0.1:8080` (`RVAV_WEB_BIND` to change it).
Put the site's reverse proxy or load balancer, with TLS, in front of it.

**High availability:** two VMs run the same file behind a load balancer.
On both, the sync is active on one and waits on the other.

**Upgrades:** change `RVAV_VERSION`, then `systemctl restart rvav`. The
migrations run first. With two VMs, upgrade one, then the other.

**Logs:** Docker's JSON files, rotated at 50 MB × 5 per container. Ship
them to the site's log platform ([observability.md](observability.md)).
