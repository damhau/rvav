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

> Status: design phase. See [`docs/`](docs/).

## Documentation

| Doc | Content |
|---|---|
| [Architecture](docs/architecture.md) | Components, tech stack, runtime flows |
| [Domain model](docs/domain-model.md) | Core entities and relationships |
| [Service catalog](docs/service-catalog.md) | Full catalog feature specification |
| [Integrations](docs/integrations.md) | vSphere, AWX, EfficientIP, identity, notifications |
| [UX](docs/ux.md) | UX principles and key screens |
| [Frontend](docs/frontend.md) | React/TS/Vite, TanStack Router + Query, Tailwind + shadcn/ui architecture |
| [Roadmap](docs/roadmap.md) | MVP and later milestones |
| [ADRs](docs/adr/) | Architecture decision records |
