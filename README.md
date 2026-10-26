# rvav

A modern self-service cloud portal for VMware vSphere, built as a replacement for
vRealize / Aria Automation (vRA).

- **Native vSphere**: VM provisioning, inventory and day-2 operations run directly
  against vCenter (Go + govmomi). No vRO, no extra appliance.
- **AWX for automation**: AWX job templates and workflow job templates are
  first-class catalog items and day-2 actions, and run as steps after provisioning.
- **Full service catalog**: custom forms, dynamic values, approvals, leases,
  quotas, placement, naming, versioning and entitlements.
- **EfficientIP SOLIDserver** for IPAM and DNS.
- **Modern UX**: command palette, live request timelines, inline day-2 actions,
  dark mode, keyboard-first.

> Status: slice 1 built. The catalog, the request form, provisioning a VM and
> following it live, and deployments work end to end against vcsim (the vCenter
> simulator) with fake AWX and EfficientIP. See the [roadmap](docs/roadmap.md).

## Run it locally

| Part | How | Details |
|---|---|---|
| Whole stack | `docker compose -f deploy/docker-compose.yml up --build`, then http://localhost:3000 | [deploy/README.md](deploy/README.md) |
| Backend only | `make vcsim`, `rvav migrate -seed`, `rvav dev` | [deploy/README.md](deploy/README.md), [internal/README.md](internal/README.md) |
| Frontend on mock data | `cd web && pnpm install && pnpm dev` | [web/README.md](web/README.md) |
| Frontend on the local API | `cd web && pnpm dev:real` (API on `:8080` with `RVAV_AUTH_MODE=dev`) | [web/README.md](web/README.md) |

## Documentation

| Doc | Content |
|---|---|
| [Architecture](docs/architecture.md) | Components, tech stack, runtime flows |
| [Domain model](docs/domain-model.md) | Core entities and relationships |
| [Service catalog](docs/service-catalog.md) | Full catalog feature specification |
| [Integrations](docs/integrations.md) | vSphere, AWX, EfficientIP, identity, notifications |
| [UX](docs/ux.md) | UX principles and key screens |
| [Frontend](docs/frontend.md) | React/TS/Vite, TanStack Router + Query, Tailwind + shadcn/ui architecture |
| [API](api/README.md) | OpenAPI contract (`api/openapi.yaml`) and API conventions |
| [Roadmap](docs/roadmap.md) | MVP and later milestones |
| [ADRs](docs/adr/) | Architecture decision records |
