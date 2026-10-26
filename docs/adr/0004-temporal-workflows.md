# ADR-0004: Temporal for workflow orchestration

- Status: accepted
- Date: 2026-10-26

## Context
Provisioning chains slow, failure-prone steps across several systems (IPAM, DNS,
vCenter, AWX). It has to wait for humans (approvals) and for time (leases,
scheduled requests), and it has to survive restarts. vRA used vRO for this.

## Decision
Use Temporal. Workflows and activities are written in Go and run in `rvav-worker`.

## Consequences
- Durable state, retries, timeouts, signals (approvals) and timers (leases)
  without writing our own state machine.
- Workflow history helps debugging; compensation (rollback) is written as code.
- One more component to run: Temporal plus its database, which can be the same
  PostgreSQL cluster in a separate database. Temporal Cloud is an option.
- Workflow code must stay deterministic; the team needs to learn the patterns.
