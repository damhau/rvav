# Upgrades

rvav is upgraded while it runs, with requests in flight. Some requests wait
days for an approval, others are in the middle of an AWX job. An upgrade
must neither lose nor break them (S14.4).

## Procedure

- **Kubernetes:** `helm upgrade` with the new version. Each Deployment's
  rolling update starts a new pod and waits for it to be ready before
  stopping an old one, so each process keeps serving. The disruption
  budgets do the same for node drains. Each new pod applies the new
  migrations as it starts, one pod at a time, before the new code runs.
- **VMs:** change `RVAV_VERSION`, then `systemctl restart rvav` on one VM,
  then on the other.

Read the release notes first: they say when a release needs more than this
(see "Breaking changes" below). Take a base backup before upgrading
([backups.md](backups.md)). It is the way back if an upgrade goes wrong.

## Database migrations

While pods roll, the old and the new version run side by side against the
new schema. So every migration is **expand, then contract**:

- **Expand (any release):** add tables, columns that are nullable or have
  a default, indexes (`create index concurrently` on big tables),
  constraints the old code already respects.
- **Contract (a later release):** drop or rename a column or table only
  once no running version reads or writes it. That is one release after
  the code stopped using it.
- **Never:** change what a column means in place, or make a column
  required that the old code doesn't write.
- **Data changes** run in the migration only when they are fast; a large
  one is done by the code, in batches, after the release that can read
  both forms.
- **No down migrations in production:** a release that went wrong is fixed
  forward, or the database is restored from the backup taken before it.

Migrations are SQL files in `migrations/`, applied in order by every rvav
process as it starts (one at a time, under a PostgreSQL advisory lock).

## Workflow code

A Temporal workflow that waits (for an approval, an AWX job, a schedule)
is replayed from its history when it resumes, on whichever worker version
picks it up. So the new code must take the same path as the old one did,
up to where the workflow is. Two rules:

1. **A change to what a workflow does goes behind
   `workflow.GetVersion`.** Old histories keep the old path, new workflows
   take the new one. `internal/provision/workflow.go` already does it for
   blocking subscriptions (`blocking-subscriptions`). The old branch can be
   removed once no workflow in flight can predate the change: after the
   longest approval timeout plus the history retention.
2. **Activities can change freely, but stay idempotent.** An activity runs
   again after a worker is replaced, so it must find what it already did
   (a VM already cloned, an address already reserved, an AWX job already
   launched or, if AWX lost it, launched again).

`TestReplayHistories` (`internal/provision/replay_test.go`) replays
recorded histories against the code in every CI run. A change that breaks
workflows in flight fails there, not in production. The histories in
`internal/provision/testdata/histories` were recorded at 1.0:

- provisioning: one VM, approved, cancelled while waiting, failed;
- the PostgreSQL cluster composite;
- NSX (N1, N2): a VM on its own segment in security groups, and a
  composite whose VM step is (recorded on the compose stack's fake NSX);
- day-2 actions;
- housekeeping.

Add a history whenever a workflow gains a path:

```
temporal workflow show -w request-<id> -o json > internal/provision/testdata/histories/<name>.json
```

Look through it for anything secret first. A secret form field's answer
that is passed on to AWX can be in the plan, and so in the history.

## Tested

`deploy/upgrade-test.sh [old-ref]` runs an earlier rvav (by default S13.7,
before 1.0's S14) as a compose project of its own. It leaves a production
request waiting for approval, and catches the PostgreSQL composite while
one of its AWX jobs runs. It then upgrades everything to the checked-out
version: images rebuilt, migrations applied, every process replaced.

After the upgrade, it checks that:

- the composite finishes;
- the waiting request, approved after the upgrade, succeeds;
- a new request succeeds.

## Breaking changes

A release that can't be rolled (a contract migration together with the
code that needs it, or a change to the workflow contract custom workflows
use, ADR-0022) says so in its notes. It is upgraded with rvav's API and
workers stopped for the migration, or in two steps across two releases.
None has been needed so far.

## Upgrading to 1.0

1.0 seals secret form inputs ([security.md](security.md)). While the pods
roll, a request whose secret inputs a new API sealed could be launched by
a worker still on the old version: it would send AWX the marker in their
place. Only items with secret fields are concerned. Retry such a request
once the upgrade is done, or roll the workers before the APIs (`kubectl
rollout` on the worker Deployment first).
