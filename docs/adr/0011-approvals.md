# ADR-0011: Approvals

- Status: accepted
- Date: 2026-10-27

## Context
Slice 1 stops a request that needs approval and resumes it on a signal sent
by `rvav approve`, which anyone with shell access can run for anyone. S3
needs the real thing ([service catalog](../service-catalog.md#approval),
[implementation plan](../implementation-plan.md#s3-approvals)): several
levels, any or all approvers per level, auto-approval, timeouts, delegation,
approvers editing allowed inputs, one inbox, and a full history.

## Decision

### Policies
An approval policy (`approval_policies`) has a CEL `condition`, a `priority`,
an optional `auto_approve` CEL expression, and ordered `levels`. A level has
a `name`, approvers (`group`, an OIDC group, and/or `approvers`, user IDs),
a `mode` (`any`: one approval is enough; `all`: every named user approves),
and optionally a `timeout` with `on_timeout`: `approve`, `reject` or
`escalate` (to `escalate_to`, a group and/or users).

### The chain of a request
At submit, every matching policy contributes its levels, policies in
priority order (lower first). Levels with the same approvers (group and
users) are merged into one, keeping all their reasons, the stricter mode and
the shorter timeout: a 48 GB production VM matches "Large production VMs" and
"Production VMs", and the Infrastructure leads approve it once. A policy
whose `auto_approve` is true is satisfied without anyone, and says so.

Each level is a row in `approvals`. Levels run one after the other; only the
current one is `pending`, later ones `waiting`. Decisions are rows in
`approval_votes`; with `all`, the level is approved once every named
approver has approved, and any rejection rejects it.

### Who may decide
The approvers of the level: the users named in it, members of its group, and
users it was delegated to. Group membership comes from the token at the time
of the decision. Groups seen in tokens are also stored per user, so the inbox
and notifications know who is in a group before they sign in again. Admins
see every approval but decide only where they are an approver. The requester
never approves their own request.

### Editing inputs
UI schema fields marked `approver_editable` can be changed by an approver
when approving, within the field's JSON Schema. The request is evaluated
again with the new inputs (naming, placement, quota) and its plan replaced
before it continues; the change is shown on the request and kept in the vote
and the audit log. Later levels are not recomputed.

### Workflow
The Provision workflow runs the levels in order: an activity opens the level
(sets it pending, its due time, notifies the approvers), then the workflow
waits for the `approval` signal for that level or a Temporal timer at the due
time. A timeout applies `on_timeout`; escalation adds the escalation
approvers and gives one more timeout, after which the level is rejected.
When every level is approved, the workflow loads the plan again (approvers
may have changed it) and provisions. The API records votes in the database
and signals the workflow only when a level is decided, so the workflow's
history stays small and the database is the source of truth.

### API, events and audit
`/approvals` lists what waits for the caller (or everything, for admins and
auditors), `/approvals/{id}` shows one level with the facts an approver
needs, and `POST /approvals/{id}/decision` and `/delegate` act on it.
`/requests/{id}/approvals` is the request's approval history. Approval events
go to each approver as `approval.updated` on the `approvals` topic, with IDs
only. Every vote, delegation, escalation, timeout and input change is an
`audit_events` row; reading the audit log comes in S9.

## Consequences
- One inbox for every approval, whoever started the workflow; custom Temporal
  workflows will use the same tables (ADR-0007).
- `rvav approve` is removed.
- Out-of-office delegation and approving from email, Teams or Slack build on
  this in S11.
