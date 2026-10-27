# Running rvav locally

## With Docker Compose

```sh
docker compose -f deploy/docker-compose.yml up --build
```

| Service | URL | Notes |
|---|---|---|
| Web app | http://localhost:3000 | nginx serving the built app and proxying `/api` to the API |
| API | http://localhost:8080/api/v1 | Dev authentication by default |
| Temporal UI | http://localhost:8233 | Workflow history of every request |
| Keycloak | http://localhost:8081 | `admin` / `admin`; realm `rvav`, users `lea`, `marc`, `thomas` (password `rvav`) |
| vcsim | https://localhost:8989/sdk | `user` / `pass`; the GVA layout is created by `vcsim-bootstrap` |
| PostgreSQL | localhost:5432 | `rvav` / `rvav` |

Services start in order: the API and the worker wait until Temporal has
created its `default` namespace, and the web app waits until the API is
ready. `docker compose -f deploy/docker-compose.yml up --build -d --wait`
returns once everything is healthy.

When a port is already taken, change it with `RVAV_API_PORT`,
`RVAV_WEB_PORT`, `RVAV_KEYCLOAK_PORT`, `RVAV_TEMPORAL_PORT`,
`RVAV_TEMPORAL_UI_PORT`, `RVAV_VCSIM_PORT` or `RVAV_POSTGRES_PORT`, in the
shell or in `deploy/.env` (not committed). For example `RVAV_API_PORT=18080`.
Keycloak sign-in only accepts the web app on ports 3000, 5173 and 8080, the
redirect URIs in `deploy/keycloak/realm-rvav.json`.

Ports are published on 127.0.0.1 only, because with dev authentication
anyone who reaches the API is trusted. To reach the stack from other
machines, set `RVAV_BIND_ADDRESS=0.0.0.0` together with `RVAV_AUTH_MODE=oidc`
(and `RVAV_ALLOW_DEV_AUTH=` to make sure dev authentication can't start).

To check that the stack works (CI doesn't run these for now):

```sh
deploy/smoke.sh                 # API only, dev authentication; needs curl and jq
cd web && pnpm e2e:stack        # the browser: request a RHEL 9 server and follow it to Ready
```

`smoke.sh` reads `RVAV_API_PORT`; `pnpm e2e:stack` reads `RVAV_WEB_URL`
(default `http://localhost:3000`).

The stack uses the fake EfficientIP and the fake AWX. Set
`RVAV_FAKE_AWX_FAIL_TASK="idm : join realm"` to see how a failed AWX task
looks in the timeline.

To use Keycloak sign-in instead of dev authentication, start with
`RVAV_AUTH_MODE=oidc`. Tokens are issued for `http://localhost:8081/realms/rvav`
and the API discovers Keycloak at `http://keycloak:8080` inside the network.
The web app reads the same variable, but at build time, so rebuild it when you
switch: `RVAV_AUTH_MODE=oidc docker compose -f deploy/docker-compose.yml up --build`.
The API starts even while Keycloak is still booting; it answers `503`, and
reports not ready, until it can reach it. `RVAV_STACK_AUTH=oidc pnpm e2e:stack` signs in as `lea` and
`thomas` and checks that their Keycloak groups give them their project roles.

For frontend work, `cd web && pnpm dev:real` on http://localhost:5173 reloads
as you edit and uses the same API on `:8080`.

## Without Docker

You need PostgreSQL 16 and the Temporal CLI (`temporal server start-dev`).

```sh
cp .env.example .env && set -a && . ./.env && set +a
go run ./cmd/rvav migrate -seed
go run ./cmd/rvav-vcsim            # terminal 1: vCenter simulator with the GVA layout
temporal server start-dev          # terminal 2
go run ./cmd/rvav dev              # terminal 3: api + worker + sync in one process
```

`rvav api` starts without Temporal: reads work, and submitting a request
answers 502 until Temporal is reachable.

## Dev authentication

With `RVAV_AUTH_MODE=dev` every request is trusted as a seeded user:
`lmartin` (Web Platform admin) by default. Send `X-Rvav-Dev-User: tnguyen`
(member), `mkeller` (administrator) or `sbrunner` (Data Engineering) to act
as someone else. The API logs a warning at startup and answers with
`X-Rvav-Auth-Mode: dev`.

The API refuses to start in this mode unless `RVAV_ALLOW_DEV_AUTH=1` is also
set (the compose stack and `.env.example` set it; `RVAV_ALLOW_DEV_AUTH=`
turns it off in compose), so a stray `RVAV_AUTH_MODE=dev` can't open a real
installation.

## Approvals

Production requests wait for the Infrastructure leads (`mkeller`), who see
them in the approvals inbox, or through the API:

```sh
curl -s -H 'X-Rvav-Dev-User: mkeller' localhost:8080/api/v1/approvals
curl -s -X POST -H 'X-Rvav-Dev-User: mkeller' -H 'Content-Type: application/json' \
  localhost:8080/api/v1/approvals/<id>/decision -d '{"decision":"approve","comment":"OK for Black Friday"}'
```

A level times out after 48 hours and escalates to the head of infrastructure
(`cdubois`), then rejects after 48 more. An infrastructure lead's own
production VM below 32 GB approves itself. See ADR-0011.

## Trying it

```sh
curl -s localhost:8080/api/v1/me
curl -s 'localhost:8080/api/v1/catalog/items?project_id=web'
curl -s -X POST localhost:8080/api/v1/forms/values -H 'Content-Type: application/json' \
  -d '{"version_id":"ver_rhel9-server_1","project_id":"web","environment":"dev","field":"cluster","inputs":{}}'
curl -s -N 'localhost:8080/api/v1/events?topics=requests,deployments,notifications&project_id=web'
```
