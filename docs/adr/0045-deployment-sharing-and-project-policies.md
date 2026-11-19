# ADR-0045: Deployment sharing, project policies, soft enforcement

- Status: accepted
- Date: 2026-11-19
- Extends the roles of [ADR-0034](0034-custom-roles.md), the approvers of
  [ADR-0041](0041-approvers-by-role.md) and the policy administration of
  [ADR-0020](0020-administration.md). G1.3 (#155).
- Amended by G1.4 (#156): how approvals combine across scopes.

## Context
A vRA project has a **Deployment sharing** setting: on, every member sees
every deployment of the project; off, members see only their own, and
project administrators and supervisors still see them all. vRA's
policies (approval, lease, day-2) can be written for one project by its
administrators, and each has an enforcement type, `HARD` or `SOFT`: a
soft one tells the requester and lets them go on.

rvav showed every deployment of a project to everyone who saw the project.
Only administrators (`policies.manage`) wrote policies, and every policy
was hard, quotas aside. The vRA importer reported both settings as
unmapped. A custom role without `project.see`, such as a VM operator,
didn't list the deployments its holder owned in the project (B4).

## Decision

### Who sees a project's deployments
- **A project has `deployment_sharing`:** `all_members`, the default and
  every existing project's after migration 00078, or `owner_only`.
- **The owner always sees a deployment,** in any project and whether or
  not they hold `project.see` there: the list, its details, VMs, history,
  day-2 actions and updates. That fixes B4.
- **With `all_members`,** whoever holds `project.see` sees every
  deployment of the project, as before.
- **With `owner_only`,** a member sees the deployments they own
  (requested, requested for them, or handed over to them) and the requests
  they made or own. A new project permission, `deployments.read_all`,
  shows every one: the built-in Project admin, Supervisor, Auditor and
  Administrator hold it, and a custom role may.
- **Approvers read the request they are asked to approve,** whatever the
  sharing, as they already did outside the project.
- **The rule is one function** (`SeesDeployment`), applied to every list,
  facet, search, detail, day-2 action, update, console, report, the
  `rvav.deployments` and `rvav.vms` pickers, the requests list and the
  event streams (`viewer:<id>` topics and followed deployments).
  Notifications go to a deployment's owner and the request's people, so
  they need no filter.
- **Whoever holds `project.members.manage` sets it,** with
  `PUT /projects/{id}/deployment-sharing`, audited as
  `project.sharing.set`; open event streams resync, as after a role
  change. To share one deployment with someone, hand it over: rvav has no
  per-deployment share list, as vRA has none.

### Project policies
- **A new project permission, `project.policies.manage`,** held by Project
  admin and Administrator, writes approval, day-2 and lease policies whose
  `project_id` is a project where the caller holds it. A global policy, or
  one of another project, still needs `policies.manage`; changing or
  removing one is refused, with 404 for a policy the caller can't see.
- **Holders read the global policies and their projects' own,** and
  **What applies here** for their projects; the policy lists leave out
  other projects' policies. Naming policies and quotas stay
  administrators'.
- **Moving a policy up or down stays administrators':** it renumbers the
  policies of every project. A project admin sets a priority instead.
- **Every change is audited** with the project: `policy.approval.*`,
  `policy.day2.*`, `policy.lease.*`.

### How project and global policies combine
- **Approval:** every policy that matches adds its levels, global and
  project's alike, in priority order. A project's policy can add
  approval, never remove a global one's.
- **Levels merge only within a scope.** Levels with the same approvers
  become one level, keeping the shorter timeout and the stricter action
  (ADR-0011), only when their policies are all global or all the same
  project's. A project's level with a global level's approvers stays a
  level of its own: otherwise its timeout, `on_timeout` and escalation
  would take over the global level, and a project admin could escalate a
  production approval to herself.
- **Day-2:** policies are tried by priority, global and project's
  together, and the first one that matches decides, as before. When it is
  a project's, the first global one that matches after it still decides
  if it is hard and stricter: deny over approval, approval over a soft
  policy's warning, a warning over allow. A soft global policy doesn't
  override a project's. So a project admin can tighten a global rule
  (deny where it allows) and loosen only a soft one.
- **A project's day-2 approval adds to a hard global approval.** When
  both match, the action needs both: the global policy's levels first,
  then the project's. Choosing who approves a hard global approval would
  loosen it, so a project's approval never replaces it.
- **Lease:** a project's override replaces the environment's lease for
  the project, as before. A project admin's can't be looser: a longer
  default or maximum, no lease where the environment gives one, more
  extensions or grace days, a gentler end (notify only over power off then
  delete, over delete), or soft over a hard environment lease. Each is
  refused with the code `looser`. Administrators may write any override.
- **What applies here** shows both, saying which policy decides and why,
  and when a policy only warns.

### Soft and hard enforcement
- **Lease and day-2 policies get `enforcement`,** `hard` by default.
  Approval policies have none: they always apply. Quotas keep their own.
- **Hard** refuses, or asks for approval, as before.
- **A soft lease policy** warns instead: a request longer than its
  `max_days` (the form then offers longer leases), or an extension beyond
  its days or extensions, which then needs no approval.
- **A soft day-2 policy,** with the effect deny or approval, leaves the
  action enabled and warns, with the policy's reason or a default
  sentence. A soft allow is refused: it has nothing to warn about.
- **The warnings are in every preview:** `RequestPreview.warnings`,
  `UpdatePreview.warnings` and `Day2Action.warnings`. Submitting,
  running the action or updating without `accept_warnings: true` is
  refused with 422, one error per warning at `/accept_warnings` with the
  code `warning`, so an API client can't go on without knowing.
- **Going on is recorded:** the request keeps the warnings it went on
  despite (`Request.warnings`), and the audit log has
  `request.warnings.accept`. The request page lists them under **Policy
  warnings**. Bulk day-2 actions skip a deployment whose action warns.

### vRA import
- A vRA project's Deployment sharing (`sharedResources`, on when absent)
  becomes `all_members` or `owner_only` on the project's plan, beside the
  rvav project's. Like the placement strategy, it is reported, not
  applied.
- A policy's `enforcementType` becomes the rvav policy's enforcement where
  it has one: lease, and day-2 with the effect approval. A soft approval
  policy, quota or allow is reported as having none. A project's soft
  lease override over a hard environment lease is reported as an
  administrator's to make. A policy for one project is reported as its
  project admins' to write.

## Consequences
- An installation that turns `owner_only` on hides from members what they
  saw before; links they kept answer "not found". Project admins,
  supervisors and auditors are unaffected.
- A custom role that should see everything in an `owner_only` project
  needs `deployments.read_all`. vRA's project administrators and
  supervisors become Project admins and Supervisors, who hold it.
- Project admins can tighten policies for their project without asking an
  administrator, and can't loosen a hard global rule or lease.
- A soft policy's warning can be ignored, by design: the audit log, not
  the policy, holds the requester to it.
- The event streams resync when a project's sharing changes, a cost paid
  once per change.
