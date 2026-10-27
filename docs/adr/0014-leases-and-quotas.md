# ADR-0014: Leases and quotas

- Status: accepted
- Date: 2026-10-27

## Context
S5 ([implementation plan](../implementation-plan.md#s5-leases-and-quotas))
asks that deployments don't live forever and projects stay within their
quota: lease policies with extensions and approval, reminders, an expiry
action with a grace period, and hard or soft quotas checked on submit,
resize and change of project. Slice 1 stored a lease per deployment and one
lease policy per environment; quotas were project-wide and hard only.

## Decision

### Lease policies
A lease policy is set per environment, and a project may override it for
one environment (`lease_policies.project_id`). It has the lease given at
request (`default_days`), the longest lease one may ask for or extend to
(`max_days`, from now), how many extensions the owner may make alone
(`max_extensions`), the grace period (`grace_days`) and what expiry does
(`expiry_action`): `power_off_then_delete` (power off, delete after the
grace period), `delete`, or `notify_only`. No `default_days` means no lease.

### Extending
Extending is the day-2 action `extend_lease` on the whole deployment, so
it has a timeline, an audit entry and a place in the history. Within the
policy (at most `max_days`, fewer than `max_extensions` used) it runs at
once; beyond it, it needs the approval of the project's admins. A lease
extended during the grace period stops the deletion; the VMs stay off until
someone powers them on.

### Expiry
The `Housekeeping` workflow (ADR-0013), every 15 minutes, sends in-app
reminders 7 days and 1 day before a lease ends (email comes in S11), and
starts the expiry of each deployment whose lease ended as ordinary day-2
requests by the system user `rvav`: `lease_expire` powers the VMs off and
records when; `delete` runs once the grace period is over. No workflow
waits for months, so upgrades stay safe.

### Quotas
Limits are project-wide (`environment` empty) or per environment, each
`hard` or `soft`. Submitting, resizing and moving to another project check
both: over a hard limit is refused with what is left; over a soft one needs
approval, through policies: approval policies and day-2 policies see
`quota.over_soft` (and the resources over it), and the seed asks the
Infrastructure leads.

## Consequences
- Leases, reminders and expiry are all visible as requests and audit
  entries; nothing changes a deployment out of sight.
- Who approves an extension is not configurable yet (the project's admins).
- Reminders by email and Teams arrive with notifications (S11).
