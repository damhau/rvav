# ADR-0041: Supervisors, and approvers by role or manager

- Status: accepted
- Date: 2026-11-19
- Extends the approvals of [ADR-0011](0011-approvals.md) and the roles of
  [ADR-0034](0034-custom-roles.md), whose "Approvers stay people" it
  revises. G1.1 (#153).

## Context
vRA approval policies name their approvers as users, groups or roles: the
project's supervisors, its administrators or members, or the requester's
manager. Its projects have a supervisor role for that: it approves and
sees the project, and requests nothing. rvav's levels named users and an
OIDC group only, so an installation with many projects wrote one policy per
project, and changed it whenever someone joined or left. The importer
mapped vRA's supervisor to project-admin, who also requests and manages
members.

## Decision

### The supervisor
- **A built-in project role, `supervisor` (Supervisor),** holds
  `project.see` and `approval.project`: its holders see the project, its
  deployments included, and are among its approvers. It holds no other
  permission, so a supervisor doesn't request or run day-2 actions; a
  day-2 policy may name it, as it names the other built-in roles.
- Like the other built-in roles, it is listed in `roles` for the
  assignments that reference it (migration 00071). A custom role made with
  the key before keeps its holders, its group mappings and the day-2
  policies naming it, as `supervisor-custom`.
- It has no rank among viewer, member and project-admin: the deprecated
  `role` and `my_role` fields, the strongest of those three, stay empty for
  someone who is only a supervisor, as for a custom role.
- `approval.project` now reads "one of the project's approvers": the lease
  extension that asked the project's admins asks every holder.

### Approvers by role
- **A level may name roles, `roles`, by key:** built-in or custom, any role
  given in projects. Saving refuses a key that is no role, or a role given
  for all of rvav only. A level names an OIDC group, users, roles, or any
  of them.
- **A level's roles are resolved when it opens,** in the request's
  project: the users given one of them there, and the OIDC groups mapped to
  one there. The level keeps both (`role_ids`, `role_groups`), so those it
  asks don't change while it waits, as a named user doesn't. A role held
  for all of rvav doesn't count, as `approval.project` doesn't.
- Those it resolves to are approvers as named users and the level's group
  are: their inbox lists the level, they are told when it opens, by every
  channel they have, and they decide. A member of a mapped group decides
  with the groups in their token, as for the level's group.
- **In mode `all`,** every user named or given a role must approve, the
  requester aside; a group's members are never all required, so a level
  of groups only needs one.
- Levels merge when their group, users and roles are the same.
- The request form's preview lists the role's holders, and the seen
  members of its groups, as the level would ask them now.
- Day-2 policies' levels take roles the same way. The lease extension
  beyond the policy is a level named "Project approvers" asking the roles
  that hold `approval.project`, resolved the same way when it opens.
- A role a level names can't be deleted: the role's `policies` lists the
  approval and day-2 policies whose levels name it.

### The requester's manager
- **The manager is a claim of the identity provider,** named by the
  provider setting `manager_claim` (`RVAV_OIDC_MANAGER_CLAIM`, `manager`
  by default). Its value is the manager's email or subject, as Keycloak's
  user attribute or a directory gives it; the first one of a list counts.
  rvav stores it on the user, `manager`, at each sign-in: a token without
  it clears it, so a manager removed at the provider is no longer asked.
  The identity provider's trial shows the manager it reads.
- **A level may ask the requester's manager, `manager: true`,** besides
  its group, users and roles. When it opens, rvav looks for the user whose
  email (in any case), subject or ID is the claim's value, the requester
  and rvav's own system users aside, and keeps it on the level
  (`manager_id`). The manager then approves as a named user does: inbox,
  notifications, and mode `all`.
- **A requester without a manager has none on that level.** The level asks
  its other approvers, and is rejected at once when it has none, saying
  "as the requester has no manager", "as the requester's manager, x, has
  never signed in to rvav", or "as the requester is their own manager". A
  manager must have signed in once: rvav knows only users who have.
- Seeded users have managers (Thomas: Léa; Léa, Sophie and Alain: Marc;
  Marc: Claire), and the development Keycloak realm gives Léa, Marc and
  Thomas the same, in a `manager` attribute and mapper. A seeded user's
  manager follows the seed until they sign in, then the provider.

### A level nobody may decide
- **When a level opens with nobody to decide, it is rejected at once,**
  rather than wait for no one, or approve itself on time-out: nobody holds
  its roles in the project, or only the requester does, it names no other
  user, and it has no group and no delegate. A group counts even when none
  of its members has signed in yet: they may.
- Its outcome says why, for example "Rejected automatically: nobody can
  approve it, as nobody holds Supervisor in Web Platform". The request is
  rejected with it, the requester is told it, and the audit log has
  `approval.unanswerable`. The fix is to give the role in the project and
  request again.
- This applies to every policy level, not only those naming roles or the
  manager: a level whose only user is the requester is rejected the same
  way.
- The lease extension still refuses the request at once (409) when nobody
  but the requester holds such a role in the project, given it or in a
  group whose members rvav has seen, as it refused it when the project had
  no other admin.

### The importer
- vRA's `ROLE:project_supervisor` becomes `supervisor` in a day-2
  policy's authorities, instead of `project-admin`.

## Consequences
- ADR-0034's "Approvers stay people" now reads: the users a level names or
  who hold one of its roles in the project, its groups' members and its
  delegates. A permission still makes no one an approver of a level that
  doesn't ask them, except `approval.project` for the lease extension.
- A policy can name the supervisors of every project at once, and follows
  who holds the role without being changed.
- Those a role resolves to are fixed when the level opens. Someone given
  the role later isn't asked; someone it is taken from still may decide,
  as a user a level names does. A group mapped to it is kept too, and its
  members decide with the groups in their token.
- A request can be rejected for a reason the requester can't fix: the
  outcome names the role and the project, or the manager, so they know
  whom to ask.
- A manager is found by email or subject, not by a directory's
  distinguished name: a provider giving `CN=…` needs a mapper that gives
  the manager's email instead.
