# ADR-0001: Go for the backend

- Status: accepted
- Date: 2026-10-26

## Context
The app provisions VMs and runs day-2 operations natively against vCenter, runs
durable workflows, and keeps a live inventory cache.

## Decision
Write the backend in Go, using govmomi for vSphere and the Temporal Go SDK for
workflows. Ship a single binary with `api`, `worker` and `sync` subcommands.

## Consequences
- The best-maintained vSphere SDK, and **vcsim** for realistic tests without a vCenter.
- Strong concurrency for inventory sync and SSE fan-out; simple deployment.
- Ansible stays outside the app (in AWX), so there is no Python dependency.
- The team needs Go skills; the frontend stays TypeScript/React.
