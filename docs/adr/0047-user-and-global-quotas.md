# ADR-0047: Quotas per user and for all of rvav, and deployment limits

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

vRA's deployment limit policies cap the vCPU and memory of each
deployment, and its resource quotas can cap how many deployments a user
or a project has. rvav had only indirect limits: 20 VMs per request, the
form's maximums and size presets per VM, and a VM-count quota per
project.

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

### Deployment limits are a policy
A deployment limit policy (`/admin/deployment-limit-policies`) sets any of
four limits: `deployments_per_user`, `deployments_per_project`,
`vcpu_per_deployment` and `memory_gb_per_deployment`. Like the other
policies of ADR-0045, it is global or a project's, with a priority, an
environment or none, enabled or not, hard or soft, `set_by_admin`, and
audited (`policy.limit.create`, `.update`, `.delete`). A global policy
applies to each project on its own: its deployment count is per project.

- **Each limit is decided on its own.** The enabled policies that apply
  (global or the project's, for every environment or the request's) are
  tried by priority, global first on a tie, and the first that sets the
  limit decides. When it is a project's, the first global one that sets
  the limit after it still decides if it is hard and stricter: a lower
  limit, or the same limit when the project's is soft. A soft global
  policy doesn't win over a project's. So a project admin tightens a
  limit, and loosens only a soft one, as with day-2 policies. A project
  admin's policy is not checked against the global ones when written:
  the combining keeps a hard global limit.
- **What counts:** a project's deployments not deleted, in the policy's
  environment when it has one. Failed deployments count, and so do those
  of requests not done yet, waiting for approval included: their rows
  exist from submit, as quotas reserve from submit. A rejected or
  cancelled request's deployments are deleted and stop counting. A
  user's count is the deployments they own; a request made for someone
  counts for them.
- **A deployment's size** is all its VMs together: a multi-VM item makes
  one deployment, a single-VM item one per VM, and a composite one per VM
  step's deployment. A request is checked with its largest deployment.
- **When it is checked:** a new request, on preview and on submit; an
  update (ADR-0042) and a resize, against the size only, and only when
  they grow the deployment's vCPU or memory, so a deployment over a limit
  lowered since can still be updated; moving a deployment to another
  project, against that project's limits; handing it over, against the
  new owner's count; an approver's edits, against the size limits as
  they are now.
- **Hard refuses, soft warns.** A hard limit passed is an error with the
  code `limit_exceeded`, saying the limit, the policy and what the change
  would make. A soft one is a warning (`PolicyWarning`) the requester
  accepts through `accepted_warnings`, as for the soft policies of
  ADR-0045; it doesn't ask for approval. Approver edits don't warn: the
  requester accepted at submit.
- **Previews list the limits that apply** (`RequestPreview.limits`,
  `UpdatePreview.limits`), each with the policy that decides, and **What
  applies here** lists them with the count the explained user, or the
  project, has now (`PolicyExplanation.limits`).

### Storage
Migration 00081 adds `user_quotas` (a project, or null for rvav) and
`global_quotas`. The project's own limits stay in `quotas`, unchanged.
Migration 00083 adds `deployment_limit_policies`.

## Consequences
- A project admin can share the project's quota out between members, and
  can't give anyone more than the project has.
- A handover to someone at their limit is refused, or needs approval with
  a soft limit.
- Each level adds a query to a preview only when it has a limit.
- Deployment limits count deployments, not resources: a quota on VMs
  and a limit on deployments differ for multi-VM items.
- A soft deployment limit warns and lets the requester go on; one that
  should hold for approval is a soft quota, or an approval policy.
- The vRA importer's plan maps each quota level to the rvav level that
  counts the same, and a deployment limit policy's vCPU, memory and count
  to an rvav one, with vRA's enforcement. A user level of a policy for
  every project becomes each user across rvav, which counts all their
  projects together: the plan says so. Storage per deployment, limits per
  resource and criteria are reported.
