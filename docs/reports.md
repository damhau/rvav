# Reports

How the platform is used (S13.4). `GET /reports` answers for a range of days
(the last 30 by default, two years at most), optionally one project and one
environment. `GET /reports/export?section=…` gives one section as CSV.

**Who sees what:** administrators and auditors see every project; others
see the projects they are members of. Asking for a project you're not a
member of is refused.

## What each figure means

All times are UTC. Days run from `from` to `to`, both included.

- **Requests over time:** the requests submitted in each day, week (starting
  on Monday) or month, provisioning and day-2 alike, counted by how they
  ended: succeeded, failed, rejected or cancelled. Every period of the
  range is listed, empty ones too.
- **Success rate:** of the requests that ran to an end, the share that
  succeeded: succeeded ÷ (succeeded + failed). Rejected and cancelled
  requests didn't run, so they don't count. It is absent when none ran.
- **Provisioning time:** for provisioning requests that succeeded, from the
  start of the run to its end; waiting for approval or a schedule isn't
  included. It is given as the median (p50), the 90th percentile and the
  average, overall and for the 10 items requested most.
- **Approval lead time:** for approval levels decided in the range, from
  the level opening to its decision. Also: how many levels wait now, and
  since when the oldest has waited.
- **Usage against quota:** what each project uses now, project-wide for
  every resource, and per environment where the environment has a limit.
  It doesn't depend on the range.
- **Leases ending:** live deployments whose lease ends in the next 30 days,
  soonest first. It doesn't depend on the range.

## CSV sections

| Section | One row per | Columns |
|---|---|---|
| `requests` | request submitted in the range | number, kind, title, item, project, environment, requester, status, submitted_at, started_at, finished_at, run_seconds |
| `approvals` | approval level decided in the range, or pending | request, level, name, status, opened_at, decided_at, lead_seconds, outcome |
| `quota` | quota line | project, environment, resource, limit, used |
| `leases` | lease ending in the next 30 days | deployment, project, environment, owner, expires_at |
