# ADR-0047: Quotas per user and for all of rvav

- Status: accepted
- Date: 2026-11-19
- Extends the quotas of [ADR-0014](0014-leases-and-quotas.md) and the
  project policies of
  [ADR-0045](0045-deployment-sharing-and-project-policies.md). C2.3 (#159).

## Context
vRA's resource quota policies limit vCPU, memory, storage and instances at
three levels: the organization, each project, and each user (in the
organization, or in a project). rvav had a project's quota, as a whole and
per environment, set by administrators. Nothing stopped one member from
taking all of a project's quota, and nothing bounded what rvav as a whole
would build. The vRA importer reported a user-level or organization-level
quota as unmapped.

## Decision

### Five levels, every one holds
A request, an update or a day-2 action that adds resources is checked at
each level that has a limit, and every one must hold:

| Level | Counts | Set by |
|---|---|---|
| `project` | The project's deployments and running requests | `project.quotas.manage` in the project (administrators) |
| `environment` | The same, in the request's environment | The same |
| `project_user` | What the owner holds in the project | The same, or the project's admins within the project's quota |
| `global` | All of rvav | `project.quotas.manage` for all of rvav (administrators) |
| `global_user` | What the owner holds across rvav | The same |

- **A user level counts the owner's:** the deployments they own and the
  requests they own that haven't finished (waiting for approval
  included), as the project's levels count reservations from submit. A
  request made for someone counts for them, and its message says so
  ("Thomas Nguyen's quota in Web Platform has 2 left").
- **A user level limits each user the same.** vRA's user level is a
  limit for every user, and so is rvav's: a limit for one named person
  is left out.
- **The quota is the same resources as before:** vCPU, memory, storage
  and VMs, each hard (refused, with what is left) or soft (an approval,
  through `quota.over_soft` in the policies).
- **Moving a deployment to another project checks the new project's
  levels** (`project`, `environment` and the owner's there); what it
  holds across rvav doesn't change. **Handing it over checks the new
  owner's levels** (`project_user`, `global_user`), so a handover can't
  get around a user's quota.
- **Previews list every limited line,** with its `level`
  (`QuotaImpact.level`). The request form shows the tightest line of
  each resource: the one with the least left.

### Who sets what
- **Each member's quota in a project:** `PUT
  /admin/projects/{id}/user-quotas`. With `project.quotas.manage` in the
  project, any limit, and the lines written or changed are marked
  `set_by_admin`. With `project.policies.manage` (a project admin), within
  the project's own quota: a limit above the project's for the same
  resource, or none where the project has one, is refused with the code
  `looser`. The lines marked `set_by_admin` stay as they are, or the
  write is refused (403), as for project policies (ADR-0045). Audited as
  `policy.quota.user.set`.
- **rvav's quota, as a whole and for each user across it:** `PUT
  /admin/quotas`, administrators' only. Audited as
  `policy.quota.global.set`.
- **What applies here** lists each level's limits, the user ones counting
  what the explained user holds.

### Storage
Migration 00081 adds `user_quotas` (a project, or null for rvav) and
`global_quotas`. The project's own limits stay in `quotas`, unchanged.

## Consequences
- A project admin can share the project's quota out between members, and
  can't give anyone more than the project has.
- A handover to someone at their limit is refused, or needs approval with
  a soft limit.
- Each level adds a query to a preview only when it has a limit.
