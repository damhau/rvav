# ADR-0008: Self-hosted Temporal on PostgreSQL

- Status: accepted
- Date: 2026-10-27

## Context
[ADR-0004](0004-temporal-workflows.md) chose Temporal but left open where it
runs: self-hosted, or Temporal Cloud. Requests can wait days for an approval,
so Temporal's state matters as much as rvav's own database. The organisation
already runs PostgreSQL and wants its data on its own infrastructure.

## Decision
- Run Temporal ourselves, with its persistence and visibility stores in the
  same PostgreSQL cluster as rvav, in separate databases (`temporal`,
  `temporal_visibility`).
- Development uses the `temporalio/auto-setup` image in Docker Compose, and
  tests use the Temporal CLI dev server that the Go SDK downloads
  (`TestThroughTemporal`), so no test needs Docker.
- Production runs the Temporal services (frontend, history, matching,
  worker) and the Web UI next to rvav, with the same backups as rvav's
  database. The Helm chart and the HA setup come in S14.
- rvav reaches Temporal through `RVAV_TEMPORAL_ADDRESS` and
  `RVAV_TEMPORAL_NAMESPACE` only, so moving to Temporal Cloud later is a
  configuration change plus mTLS certificates.

## Consequences
- No new vendor or data leaving the network; one database technology to run.
- The platform team operates Temporal: upgrades, schema migrations, and
  backups taken together with rvav's so they stay consistent. A runbook is
  part of S14.
- Temporal Cloud stays an option if running it proves costly.
