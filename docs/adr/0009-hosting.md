# ADR-0009: Kubernetes with Helm, Docker Compose for development

- Status: accepted
- Date: 2026-10-27

## Context
rvav is one Go binary (`api`, `worker`, `sync`) and a static web app behind
nginx, plus PostgreSQL, Temporal and an identity provider. It needs somewhere
to run in development, in the lab and in production, and the plan needs the
choice by S2 and the production setup by S14.

## Decision
- **Production: Kubernetes, with a Helm chart** (S14): the API and the web
  app scale horizontally, the worker scales with load, and `sync` runs as a
  single active instance (Postgres advisory lock).
- **Development and the lab: Docker Compose** (`deploy/docker-compose.yml`),
  with health checks and start order, so `up --wait` returns a working stack.
- A **VM-based installation** (Compose plus systemd) is documented in S14 for
  sites without Kubernetes; it is not the main target.
- The images are the same everywhere: one image for the Go processes, one for
  the web app. Everything is configured through environment variables
  (`RVAV_*`, `VITE_*` at build time for the web app).

## Consequences
- The containers must not depend on Compose. The web app resolves the API at
  request time with the nameservers from `/etc/resolv.conf`; nginx ignores
  search domains, so on Kubernetes `RVAV_API_UPSTREAM` is the service's full
  name. The API and the web app have health checks (`/readyz` checks the
  database and, with OIDC, the identity provider); the worker and sync exit
  on fatal errors and are restarted.
- The Compose stack is for one machine: it publishes its ports on 127.0.0.1
  unless `RVAV_BIND_ADDRESS` says otherwise, and its Keycloak realm only knows
  the default web ports.
- The Helm chart, its secrets and the HA setup are S14 work.
