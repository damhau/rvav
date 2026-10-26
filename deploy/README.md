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

To check that the stack works, `deploy/smoke.sh` (needs curl and jq)
submits a RHEL 9 server in dev and waits until the request succeeds:

```sh
RVAV_API_PORT=18080 deploy/smoke.sh
```

The stack uses the fake EfficientIP and the fake AWX. Set
`RVAV_FAKE_AWX_FAIL_TASK="idm : join realm"` to see how a failed AWX task
looks in the timeline.

To use Keycloak sign-in instead of dev authentication, start with
`RVAV_AUTH_MODE=oidc`. Tokens are issued for `http://localhost:8081/realms/rvav`
and the API discovers Keycloak at `http://keycloak:8080` inside the network.
The web app reads the same variable, but at build time, so rebuild it when you
switch: `RVAV_AUTH_MODE=oidc docker compose -f deploy/docker-compose.yml up --build`.
This combination has not been tested yet.

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

## Approvals

Slice 1 has no approvals API. Production requests wait for approval; decide
them from the command line:

```sh
go run ./cmd/rvav approve REQ-10002 -by mkeller -comment "OK for Black Friday"
go run ./cmd/rvav approve REQ-10003 -reject -comment "Use tst first"
```

## Trying it

```sh
curl -s localhost:8080/api/v1/me
curl -s 'localhost:8080/api/v1/catalog/items?project_id=web'
curl -s -X POST localhost:8080/api/v1/forms/values -H 'Content-Type: application/json' \
  -d '{"version_id":"ver_rhel9-server_1","project_id":"web","environment":"dev","field":"cluster","inputs":{}}'
curl -s -N 'localhost:8080/api/v1/events?topics=requests,deployments,notifications&project_id=web'
```
