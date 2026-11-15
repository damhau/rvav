# ADR-0034: Custom roles

## Status
Accepted (2026-11-15). Revises who may do what in
[ADR-0020](0020-administration.md): the fixed roles become built-in roles
made of permissions, beside roles an administrator makes. It also touches
the catalog admins of [ADR-0019](0019-catalog-administration.md), the
day-2 policies' roles of [ADR-0013](0013-day2-actions.md), what approvers
and administrators see in [ADR-0011](0011-approvals.md), the group mappings
of [ADR-0010](0010-identity-provider.md) and the local administrator of
[ADR-0030](0030-settings-and-local-administrator.md). A1.1 (#106) records
the model and its catalogue (`internal/auth/permissions.go`); A1.2 (#107)
puts it in the store and the checks, A1.3 (#108) adds the editor and the
assignments, A1.4 (#109) the acceptance.

## Context
rvav has seven fixed roles: `admin`, `catalog-admin`, `auditor` and `user`
for all of rvav, `project-admin`, `member` and `viewer` in a project
(ADR-0020). Each check names roles, through a handful of predicates
(`IsAdmin`, `SeesEverything`, `CanSeeProject`, `CanRequest`,
`ProjectRole(id) == "project-admin"`, `canAdminCatalog`): about 180 places
in Go and 40 in the web app. An organisation can't give someone less than a
member (power the VMs on and off, nothing else) or a role of its own making
(an auditor who is not quite the built-in one), as vRA's custom roles do
(plan feature 44, the half 1.0 left).

A1's acceptance: an operator who may only power VMs on and off in a
project, and an auditor who sees everything and changes nothing, made and
used, with the built-in roles unchanged.

## Decision

### Permissions
- **A permission is a stable identifier**, `<area>.<verb>` in lower case,
  such as `project.see` or `policies.manage`. Roles store it, `/me` sends
  it and policies' CEL reads it, so it is never renamed. One that is
  removed grants nothing where it is still held, and saving a role with it
  is refused.
- **The catalogue is code:** `auth.Catalogue` lists each permission with
  where it can be given, its area (to group it in the editor) and a
  sentence saying what it allows, which the editor shows. The API serves it
  to the editor; nothing stores it.
- **Day-2 actions have their own:** `day2.<group>` allows every action of
  the group (`power`, `configuration`, `snapshots`, `access`, `lifecycle`,
  `automation`), those added later included; `day2.<group>:<action>` allows
  one, such as `day2.power:power_on`. AWX actions (`awx.<slug>`) and custom
  workflows (`wf.<key>`) are in `automation`. An action's group is part of
  its permission, so it never moves group. A permission naming an action
  that doesn't exist (yet, or any more) is accepted: AWX actions come and
  go with the sync.

| Permission | Given | Allows | Built-in roles |
|---|---|---|---|
| `project.see` | P | See the project: its catalog, deployments, requests, members, quotas and reports, and their live events. | admin, auditor, project-admin, member, viewer |
| `request.submit` | P | Request from the project's catalog, save drafts, own a deployment there (onboarding, vRA import) and move one into it. | admin, project-admin, member |
| `request.on_behalf` | P | Request for someone else. | admin, project-admin |
| `request.manage` | P | Cancel, retry and roll back anyone's request in the project; the requester and the owner always may their own. | admin, project-admin |
| `approval.project` | P (in the project only) | Be asked with the project's admins, as when a lease is extended beyond its policy. | project-admin (admin's isn't counted) |
| `project.members.manage` | P | Add and remove the project's members and change their roles. | admin, project-admin |
| `project.settings.read` | P | See the project's administration: placement, quotas, the policies that apply; for all of rvav, every project's. | admin, auditor, project-admin |
| `project.update` | P | Change the project's details and environments, or archive it. | admin |
| `project.quotas.manage` | P | Set the project's quotas. | admin |
| `placement.manage` | P | Change the project's placement rules. | admin |
| `audit.read` | P | Read the audit log: the project's events, or for all of rvav every event. | admin, auditor, project-admin |
| `day2.<group>`, `day2.<group>:<action>` | P | Run the day-2 action when no policy decides it (below). | none |
| `approval.read_all` | R | See every approval, and the questions and answers of composites' waits for a person. | admin, auditor |
| `project.create` | R | Create projects. | admin |
| `environments.read` | R | See the environments. | admin, auditor |
| `environments.manage` | R | Add, change and remove environments. | admin |
| `access.read` | R | See the roles and who holds them, through groups too. | admin, auditor |
| `access.manage` | R | Make roles and give them to users and groups, anywhere. | admin |
| `catalog.manage` | R | Build, publish and open catalog items, with their categories, YAML, Git repository, AWX templates and secrets. | admin, catalog-admin |
| `infrastructure.read` | R | See templates, sizes, network mappings and vCenters. | admin, auditor |
| `infrastructure.manage` | R | Change templates, sizes and network mappings, and reconcile IPAM. | admin |
| `integrations.manage` | R | See, set and test the identity provider and every connection (vCenters included), and make the inventory token. | admin |
| `policies.read` | R | See the approval, day-2, lease and naming policies. | admin, auditor |
| `policies.manage` | R | Change them. | admin |
| `extensibility.read` | R | See event subscriptions, their runs and custom workflows. | admin, auditor |
| `extensibility.manage` | R | Change subscriptions, custom workflows and AWX day-2 actions; retry and test runs. | admin |
| `import.manage` | R | Onboard existing VMs and import from vRA. | admin |
| `system.manage` | R | The setup checklist and the System page. | admin |
| `tokens.manage` | R | See and revoke everyone's API tokens. | admin |
| `inventory.read` | R | Read the Ansible inventory. | admin, auditor |

P: in a project, or for all of rvav. R: for all of rvav only.

### Scopes
- **A role is given in a project, or for all of rvav.** In a project, its
  permissions apply there. For all of rvav, its project permissions apply
  in every project, those made later included, as the administrator's and
  the auditor's do today.
- **A role holding an R permission is given for all of rvav only**
  (422 otherwise). The built-in roles keep their scope: `admin`,
  `catalog-admin`, `auditor` and `user` for all of rvav; `project-admin`,
  `member` and `viewer` in a project.
- **`approval.project` counts from a role given in the project only:**
  the project's admins asked to approve are its own, never an administrator
  (as `Store.ProjectAdmins` finds them today), nor anyone through a role
  given for all of rvav.
- **A custom role holds at least one permission, and `project.see` when it
  holds a P permission:** every check in a project starts by seeing it
  (`visibleProject`), so a role allowing power in a project it can't see
  would allow nothing. Saving one without it is refused.

### Built-in roles
They are today's roles, exactly; they can't be changed or deleted.

| Role | Given | Permissions |
|---|---|---|
| `admin` | rvav | all the catalogue but day-2 (`approval.project` is never counted for all of rvav) |
| `catalog-admin` | rvav | `catalog.manage` |
| `auditor` | rvav | `project.see`, `project.settings.read`, `audit.read`, `approval.read_all`, `environments.read`, `access.read`, `infrastructure.read`, `policies.read`, `extensibility.read`, `inventory.read` |
| `user` | rvav | none: every user signed in through the identity provider holds it (`users.global_roles`' default), and policies' CEL may name it |
| `project-admin` | project | `project.see`, `request.submit`, `request.on_behalf`, `request.manage`, `approval.project`, `project.members.manage`, `project.settings.read`, `audit.read` |
| `member` | project | `project.see`, `request.submit` |
| `viewer` | project | `project.see` |

- **None holds a day-2 permission:** for them the day-2 policies decide
  alone, as now, whatever the policies are.
- viewer ⊂ member ⊂ project-admin, so what several roles grant together is
  what the strongest grants today.
- `TestBuiltinRolesGrantTodaysChecks` holds every combination of them and
  compares each permission with the predicate it replaces; the existing
  tests prove the checks, once converted, behave as before.

### Custom roles
- A role has a **key** (`^[a-z][a-z0-9-]{1,40}$`, fixed once made, none of
  the built-in roles' nor `owner`, which day-2 policies name for a
  deployment's owner: `auth.ValidRoleKey`), a name, a description and its
  permissions.
- Those holding `access.manage` make, change and delete them. A change
  applies at the next request of everyone holding the role. Adding an R
  permission to a role given in a project is refused (422, naming where it
  is given).
- A role is deleted only when nobody holds it and no day-2 policy names it
  (409, saying which).
- **Stored in rvav's database**, in a `roles` table that also lists the
  built-in roles (with no permissions: theirs are code), so assignments
  reference every role by a foreign key.

### Assignments and groups
- **Where:** `project_members` (a user, a project, a role), which may now
  hold several roles per user and project; `group_mappings` (an OIDC group,
  a project or none, a role), as today; a new `user_roles` (a user, a role)
  for a user's roles for all of rvav given in the UI.
  `users.global_roles` keeps what rvav sets itself (`user`, the local
  administrator's `admin`, the system users'), which the API never changes.
- **Identity-provider groups map to roles, as today:** a mapping gives any
  role, built-in or custom, in a project or for all of rvav, within the
  scope rules. Groups are those of the token (of the last sign-in when
  acting for someone, `auth.ForUser`).
- **Who gives a role:** `access.manage`, any role anywhere. In a project,
  `project.members.manage` gives roles whose permissions the giver holds
  there. Project admins give `viewer`, `member` and `project-admin` as
  today, but not a role with day-2 permissions: an administrator gives it.
  No one gives more than they hold.
- **What can't be removed, generalised:** the last way anyone holds
  `access.manage` (a user or a group mapping, today the last
  administrator), and the last way a project has someone holding
  `project.members.manage` given in it (today its last project admin).
  Changing a role's permissions is checked the same way.

### How a principal's permissions are computed
- **Once per request,** in the authentication middleware, as `build` does
  now: the user's own assignments and those of their groups, the built-in
  sets from code and the custom roles' in the same query, gathered by
  `auth.Compute` into `auth.Grants`, kept on the `Principal` with the role
  keys held (for CEL and `/me`).
- **No cache across requests:** a change applies at the next request. The
  token cache keeps only the user and their groups, for five minutes, as
  today.
- Checks ask `Grants`: held in a project (`In`), for all of rvav
  (`Everywhere`), the projects where it is held (`Projects`, nil for all),
  a day-2 action (`RunsDay2`). The role predicates go once their last call
  site has moved (the appendix lists them all).

### API tokens, the local administrator and system users
- **An API token acts with its user's own assignments,** never the roles of
  their groups (ADR-0011), as today. A token limited to the inventory
  reaches `/inventory` only, whatever its user holds.
- **The local administrator** holds the built-in `admin` for all of rvav,
  in `users.global_roles`, and can't lose it: the way in stays open
  (ADR-0030).
- **System users** keep theirs: `rvav` holds `admin` and isn't subject to
  day-2 policies; `system:inventory` holds `auditor`.

### What the web app receives
- **`/me` lists permissions, not role names:** `permissions` (held for all
  of rvav), `roles` (`key` and `name`, held for all of rvav), and
  `projects`: every project where `project.see` is held (all of them, up to
  200 as today, when it is held for all of rvav), each with the `roles`
  held there and the `permissions` held there, those for all of rvav
  included.
- `global_roles`, `projects[].role` and a project's `my_role` stay until
  the web app has moved, then go. `role` is the strongest built-in role, as
  today, and null where only custom roles are held. A project gains
  `my_permissions`.
- The web app asks `can(me, permission, projectId?)` (`web/src/lib`), never
  a role name. The server decides again, and still computes what is
  per-object: a day-2 action's `enabled` and `disabled_reason`, an
  approval's `can_decide`.
- The mocks take the built-in roles' permissions from a fixture the Go
  tests write and compare (`internal/auth/testdata`), so the two can't
  drift.

### Day-2 actions: a role allows, a policy can still require approval or refuse
1. The deployment is visible: `project.see` in its project, or its owner.
2. The day-2 policies, by priority: the first that matches decides, as
   today: allow, deny, or approval with its levels.
3. When none matches, the action is allowed if a role grants it in the
   project (`day2.<group>` or `day2.<group>:<action>`), and denied
   otherwise, with today's message.

- Built-in roles grant no day-2 action, so for them step 3 always denies,
  as "nothing matching denies" does today.
- A policy holds a custom role back by naming it (or no role): requiring
  approval to power off in production for `vm-operator`, say. One naming
  only built-in roles doesn't match its holders.
- With the seeded policies, an operator holding `project.see`,
  `day2.power:power_on` and `day2.power:power_off` matches none but the
  refusal of others' handover and delete, so they power on and off and do
  nothing else. A custom auditor holds no day-2 permission and runs
  nothing.
- Owning a deployment works as today: an operator who owns one is its
  `owner` for the policies, and the seeded ones let owners run every action.
- The action list (`enabled`, `disabled_reason`), the console and a
  day-2 retry (`checkDay2Retry`) decide the same way.

### CEL
- **Day-2 conditions and policies' roles:** `user.roles` is today's (the
  owner, the strongest built-in role in the project, `project-admin` for an
  administrator, `admin`), plus the keys of the custom roles held in the
  deployment's project or for all of rvav. A policy's `roles` match the
  same list, and accept custom role keys when saved (unknown keys are
  refused).
- **Approval policies and forms:** `user.roles` is today's (the built-in
  roles held for all of rvav, `user` included), plus the keys of custom
  roles held for all of rvav.
- **`user.permissions`, new in both:** the permissions held in the
  request's or deployment's project, those for all of rvav included; in a
  form with no project yet, those for all of rvav. A condition can ask what
  someone may do (`'day2.power' in user.permissions`) rather than who they
  are. Existing conditions read the same values as before.

### Approvals
- **Approvers stay people:** the users a level names, its group's members
  and its delegates (ADR-0011). No permission makes someone an approver,
  or lets them decide a level they aren't asked.
- `approval.read_all` lets its holder see every approval (`view=all`) and
  steps' questions, as administrators and auditors do today.
- The level asking a project's admins (`projectAdminsLevel`) asks those
  holding `approval.project` from a role given in the project: today its
  project admins.

### Live events
- **A change to a role, an assignment or a group mapping writes
  `access.changed`** to the outbox. Each hub ends every stream on it with
  `resync`; clients connect again, are authenticated with what they now
  hold, and fetch again, `/me` included (the web app's bridge invalidates
  every query on `resync`). A stream's project filter is set when it
  starts, so this is what stops one going on with a permission taken away.
- `access.changed` carries no detail (the audit log says who changed what)
  and isn't offered to event subscriptions.
- Project topics are filtered by `project.see`; `request:<id>` as today
  (`CanReadRequest`, approvers included).

### Reports
All projects when `project.see` is held for all of rvav, otherwise the
projects where it is held: today, administrators and auditors see all, and
everyone else the projects they have a role in.

### Audit
- **Role changes are audited:** `role.create`, `role.update` (the
  permissions added and removed), `role.delete`; assignments as `member.*`
  (in a project, with the role) and `group_mapping.*` as today, and
  `role_assignment.*` for a user's roles for all of rvav. They are events
  of rvav, not of a project: reading them takes `audit.read` for all of
  rvav.
- `audit.read` in a project reads its events; for all of rvav, every event
  (today: administrators and auditors all, project admins their projects').

### Not in the Git catalog
Roles stay in rvav's database, not in the catalog's repository
(ADR-0029). They are given to this installation's projects and groups, as
entitlements are, and whoever could merge to the repository would make
themselves an administrator. Their changes are in the audit log.

### The auditor
The built-in `auditor` holds the read permissions above for all of rvav. A
custom role with the same permissions, given for all of rvav, sees and
changes the same (`TestCustomRoles`); the differences are its key in CEL's
`user.roles`, and that `system:inventory` keeps the built-in one.
"Everything" is what the auditor sees today: catalog administration, the
integrations' settings, the System page and everyone's API tokens stay
hidden. Read permissions for them would be new identifiers.

## Consequences
- Every check moves to a permission in #107 (the appendix), in the API
  and the web app; the equivalence test and the existing tests show the
  built-in roles unchanged.
- `access.manage` is an administrator in all but name: whoever holds it
  can give themselves any permission. The editor says so.
- A custom role allowed a day-2 action passes wherever no policy matches.
  A policy meant to restrict "everyone but admins" names the roles it
  restricts, or none.
- Project admins can't hand out day-2 actions; an administrator gives
  those roles, or gives a project admin one that holds them.
- Each change of access makes every client reconnect once. Changes are
  rare; a later version can end only the streams of those concerned.
- Permission identifiers are an API: renaming one is a migration of roles,
  policies' CEL and the web app.
- Several roles per user and project change `project_members`' key; the
  members' list shows each role, and where it comes from.

## Appendix: today's checks and what replaces them
`In(p, id)` is held in the project, `Everywhere(p)` for all of rvav,
`Projects(p)` the projects where it is held (nil for all). "Unchanged"
checks are not about roles. Line numbers drift; the function is the
reference.

### `internal/auth`
| Where | Today | Becomes |
|---|---|---|
| `auth.go` `Principal` | `GlobalRoles`, `ProjectRoles` (strongest per project) | `Grants` and the role keys held, rvav-wide and per project |
| `auth.go` `HasGlobal`, `IsAdmin`, `SeesEverything`, `ProjectRole`, `CanSeeProject`, `CanRequest`, `VisibleProjects` | role predicates | removed once unused; `Grants.In`, `Everywhere`, `Projects` |
| `auth.go` `build`, `FromGroups`, `ForUser` | `users.global_roles`, `project_members`, `group_mappings`, strongest by `roleRank` | the assignments (own, and groups' but for API tokens) and custom roles in one query, then `Compute` |

### `internal/core`
| Where | Today | Becomes |
|---|---|---|
| `service.go` `visibleProject` | `CanSeeProject` | `In(project.see, id)` |
| `day2.go` `visibleDeployment` | `CanSeeProject` or owner | `In(project.see, id)` or owner |
| `day2.go` `Actions`, `RunAction`; `console.go` `OpenConsole` | `visibleDeployment`, then `judge` | the same, then the day-2 decision above |
| `day2.go` `projectAdminsLevel` | `Store.ProjectAdmins` | holders of `approval.project` from roles given in the project |
| `day2_policy.go` `judge`, `decideQuota` | first matching policy; none denies | first matching policy; none: `RunsDay2(group, key, id)` |
| `day2_policy.go` `day2Roles` | owner, strongest project role (`project-admin` for admin), `admin` | the same, plus custom role keys held in the project or for all of rvav |
| `day2_policy.go` `day2Vars` | `user.roles` | `user.roles` as above, `user.permissions` |
| `day2_choices.go` `projectChoices` | not admin: projects where `CanRequest` | `Projects(request.submit)` |
| `day2_choices.go` `memberChoices` | `Store.ProjectMembers` | unchanged (anyone with a role given in the project) |
| `day2_awx.go` `AWXActions`, `UpdateAWXAction` | `IsAdmin` | `Everywhere(extensibility.manage)` |
| `day2_awx.go` condition check | sample roles `member` | unchanged, with `user.permissions` empty |
| `approvals.go` `isApprover`, `canDecide`, `ownRequest` | named users, groups, delegates | unchanged |
| `approvals.go` `loadLevel` | approver, `CanSeeRequest` or `SeesEverything` | approver, `CanSeeRequest` or `Everywhere(approval.read_all)` |
| `approvals.go` `ListApprovals` | `view=all` needs `SeesEverything` | `Everywhere(approval.read_all)` |
| `approvals.go` `RequestApprovals`, `DecideApproval`, `DelegateApproval` | `CanReadRequest`, approver, own request | unchanged |
| `ask.go` `question` | approver or `SeesEverything` | approver or `Everywhere(approval.read_all)` |
| `submit.go` `CanSeeRequest` | `CanSeeProject`, requester or owner | `In(project.see, id)`, requester or owner |
| `submit.go` `CanReadRequest` | `CanSeeRequest` or an approver of it | unchanged |
| `submit.go` `Cancel` | requester, owner or `ProjectRole == project-admin` | requester, owner or `In(request.manage, id)` |
| `retry.go` `failedRequest` | requester, owner or `ProjectRole == project-admin` | requester, owner or `In(request.manage, id)` |
| `retry.go` `checkDay2Retry` | `visibleDeployment`, day-2 decision | the same, with the day-2 decision above |
| `evaluate.go` `evaluate` | `visibleProject`; `CanRequest`; `on_behalf_of` needs `ProjectRole == project-admin` | `project.see`; `In(request.submit, id)`; `In(request.on_behalf, id)` |
| `evaluate.go` `PolicyVars` | `user.roles` = `GlobalRoles` | plus custom keys for all of rvav; `user.permissions` |
| `catalog_sources.go` `formVars` | `user.roles` = `GlobalRoles` | as `PolicyVars` |
| `drafts.go` `SaveRequestDraft` | `CanRequest` | `In(request.submit, id)` |
| `values.go` `FieldValues` | `previewPrincipal` with `as_user`; `visibleProject`; a version not published needs `canAdminCatalog` | the same; `project.see`; `Everywhere(catalog.manage)` |
| `awx_templates.go` `canAdminCatalog`, `CanAdminCatalog` | `IsAdmin` or `catalog-admin` | `Everywhere(catalog.manage)` |
| `awx_templates.go` `AWXTemplates`, `SyncAWXTemplates` | `canAdminCatalog` | `Everywhere(catalog.manage)` |
| `catalog_admin.go` `adminItem`, `AdminCatalogItems`, `CreateCatalogItem` | `canAdminCatalog` | `Everywhere(catalog.manage)` |
| `catalog_yaml.go` `AdminCatalogCategories`, `Create`/`Update`/`DeleteCatalogCategory`, `importItem` | `canAdminCatalog` | `Everywhere(catalog.manage)` |
| `catalog_git_admin.go` `CatalogGitStatus`, `ReadCatalogGit` | `canAdminCatalog` | `Everywhere(catalog.manage)` |
| `composite_steps.go` `CompositeStepTargets` | `canAdminCatalog` | `Everywhere(catalog.manage)` |
| `secrets.go` `ListSecrets`, `PutSecret`, `DeleteSecret` | `canAdminCatalog` | `Everywhere(catalog.manage)` |
| `designer.go` `previewPrincipal` | `canAdminCatalog`; the user previewed as has a role in the project | `Everywhere(catalog.manage)`; they are given a role in the project |
| `designer.go` `PreviewCatalogItemVersion` | `visibleProject` as the user previewed as | unchanged (`project.see`) |
| `designer.go` `PreviewUsers` | label `ProjectRole`, default member | the names of the roles held there |
| `catalogues_admin.go` `VMTemplates`, `SizePresets` | `SeesEverything` | `Everywhere(infrastructure.read)` |
| `catalogues_admin.go` `Save`/`DeleteVMTemplate`, `Save`/`DeleteSizePreset` | `IsAdmin` | `Everywhere(infrastructure.manage)` |
| `placement_admin.go` `canSeeSettings` | `SeesEverything` or `ProjectRole == project-admin` | `In(project.settings.read, id)`; with no project, `Everywhere` |
| `placement_admin.go` `PlacementRules`, `PlacementPreview` | `canSeeSettings` | `project.settings.read` |
| `placement_admin.go` `SavePlacementRule`, `DeletePlacementRule` | `IsAdmin` | `In(placement.manage, rule's project)` |
| `placement_admin.go` `NetworkMappings` | `SeesEverything` | `Everywhere(infrastructure.read)` |
| `placement_admin.go` `SetNetworkMapping`, `DeleteNetworkMapping` | `IsAdmin` | `Everywhere(infrastructure.manage)` |
| `ipam_reconcile.go` `ReconcileIPAM` | `IsAdmin` | `Everywhere(infrastructure.manage)` |
| `vcenters_admin.go` `ListVCenters` | `SeesEverything` | `Everywhere(infrastructure.read)` |
| `vcenters_admin.go` `SaveVCenter`, `DeleteVCenter`, `TestVCenter` | `IsAdmin` | `Everywhere(integrations.manage)` |
| `policies_admin.go` `ProjectQuotas` | `canSeeSettings` | `In(project.settings.read, id)` |
| `policies_admin.go` `SetProjectQuotas` | `IsAdmin` | `In(project.quotas.manage, id)` |
| `policies_admin.go` `LeasePolicies`, `ApprovalPolicies`, `Day2Policies` | `SeesEverything` | `Everywhere(policies.read)` |
| `policies_admin.go` `SetLeasePolicy`, `DeleteLeaseOverride`, `Save`/`DeleteApprovalPolicy`, `Save`/`DeleteDay2Policy` | `IsAdmin` | `Everywhere(policies.manage)` |
| `policies_admin.go` `day2RoleNames` | owner and the built-in roles | plus custom role keys |
| `policies_admin.go` `ExplainPolicies` | `canSeeSettings`; the user explained has `CanSeeProject`; full detail with `SeesEverything` | `project.settings.read`; their `project.see`; `Everywhere(policies.read)` |
| `policies_admin.go` `explainDay2`, `sampleVars` | `day2Roles`; sample `user.roles` `["user"]` | as `day2Roles`; sample `user.permissions` empty |
| `naming_admin.go` `NamingPolicies` | `SeesEverything` | `Everywhere(policies.read)` |
| `naming_admin.go` `SaveNamingPolicy`, `DeleteNamingPolicy`, `PreviewNaming` | `IsAdmin`; `visibleProject` | `Everywhere(policies.manage)`; `project.see` |
| `projects_admin.go` `presentProject`, `presentAdminProject` | `my_role` | `my_role` (strongest built-in, or null) and `my_permissions` |
| `projects_admin.go` `AdminProjects` | `SeesEverything` | `Everywhere(project.settings.read)` |
| `projects_admin.go` `AdminProject` | `CanSeeProject`, then `SeesEverything` or project admin | `project.see`, then `In(project.settings.read, id)` |
| `projects_admin.go` `CreateProject` | `IsAdmin`; its admins become `project-admin` | `Everywhere(project.create)`; unchanged |
| `projects_admin.go` `UpdateProject` | `IsAdmin` | `In(project.update, id)` |
| `projects_admin.go` `ProjectMembers` | `visibleProject`; strongest by `roleRank` | `project.see`; every role held, direct or through a group |
| `projects_admin.go` `canManageMembers`, `SetProjectMember`, `RemoveProjectMember` | `ProjectRole == project-admin`; roles in `projectRoles` | `In(project.members.manage, id)`; roles given in a project whose permissions the giver holds there, or `access.manage` |
| `projects_admin.go` `keepsAdmin` | the last project admin stays | the last holder of `project.members.manage` given in the project stays |
| `projects_admin.go` `GroupMappings` | `SeesEverything` | `Everywhere(access.read)` |
| `projects_admin.go` `CreateGroupMapping`, `DeleteGroupMapping` | `IsAdmin`; roles in `projectRoles`, `globalRoles`; the last admin, the last project admin | `Everywhere(access.manage)`; any role, within its scope; the last `access.manage`, the last `project.members.manage` |
| `projects_admin.go` `AdminEnvironments` | `SeesEverything` | `Everywhere(environments.read)` |
| `projects_admin.go` `Create`/`Update`/`DeleteEnvironment` | `IsAdmin` | `Everywhere(environments.manage)` |
| `reports.go` `reportFilter` | all with `SeesEverything`, else every project with a role | `Projects(project.see)` |
| `tokens_audit.go` `APITokens` | everyone's with `IsAdmin` | `Everywhere(tokens.manage)` |
| `tokens_audit.go` `RevokeAPIToken` | one's own, or `IsAdmin` | one's own, or `Everywhere(tokens.manage)` |
| `tokens_audit.go` `AuditEvents` | all with `SeesEverything`, else project admins' projects, else 403 | `Projects(audit.read)`; none: 403 |
| `inventory.go` `AnsibleInventory` | admin or auditor | `Everywhere(inventory.read)` |
| `inventory_token.go` `InventoryToken`, `MakeInventoryToken` | `IsAdmin`; its user holds `auditor` | `Everywhere(integrations.manage)`; unchanged |
| `integrations_admin.go` `Integrations`, `SetIntegration`, `TestIntegration` | `IsAdmin` | `Everywhere(integrations.manage)` |
| `identity_provider.go` `TrialIdentityProvider` | `IsAdmin`; shows the roles from `FromGroups` | `Everywhere(integrations.manage)`; every role the groups give |
| `setup.go` `Setup`, `SetSetup` | `IsAdmin` | `Everywhere(system.manage)` |
| `system.go` `System`; `processes.go` `Processes` | `IsAdmin` | `Everywhere(system.manage)` |
| `subscriptions.go` `EventTopics`, `Subscriptions`, `GetSubscription`, `SubscriptionRuns`, `SubscriptionRun` | `SeesEverything` | `Everywhere(extensibility.read)` |
| `subscriptions.go` `SaveSubscription`, `DeleteSubscription`, `RetrySubscriptionRun`, `TestSubscription` | `IsAdmin` | `Everywhere(extensibility.manage)` |
| `workflows.go` `WorkflowRegistrations`, `GetWorkflowRegistration` | `SeesEverything` | `Everywhere(extensibility.read)` |
| `workflows.go` `SaveWorkflowRegistration`, `DeleteWorkflowRegistration` | `IsAdmin` | `Everywhere(extensibility.manage)` |
| `onboarding.go` `OnboardingVMs`, `AdoptVMs` | `IsAdmin`; the owner is a member but not a viewer | `Everywhere(import.manage)`; the owner holds `request.submit` there |
| `vra.go` `vraRvav` | owners: members but not viewers | holders of `request.submit` in the project |
| `vra.go` `AdoptVRADeployments`, `VRAImportStatus`, `ReadVRA`, `VRAPlan`, `MapVRAProject`, `ImportVRA`, `VROTriageMarkdown`, `SetVRACutover` | `IsAdmin` | `Everywhere(import.manage)` |
| `local_admin.go` (made with `admin`), `ChangePassword` (oneself) | | unchanged |
| `retention.go` `system` | `rvav` holds `admin` | unchanged |

### `internal/httpapi`, `internal/events`, `internal/present`, `cmd/rvav`
| Where | Today | Becomes |
|---|---|---|
| `identity.go` `GetMe` | `global_roles`; `projects[]` with the strongest role, all for `SeesEverything` | as "What the web app receives" |
| `identity.go` `ListProjects` | `VisibleProjects`; `my_role` | `Projects(project.see)`; `my_role`, `my_permissions` |
| `identity.go` `visibleProject`, `GetProject`, `GetProjectQuota` | `CanSeeProject`; `my_role` | `In(project.see, id)`; `my_role`, `my_permissions` |
| `catalog.go` `ListCatalogCategories`, `ListCatalogItems`, `entitledItem` | `visibleProject` | `project.see` |
| `catalog.go` `GetRequestForm` | a version not published needs `CanAdminCatalog` | `Everywhere(catalog.manage)` |
| `deployments.go` `ListDeployments` | `VisibleProjects` | `Projects(project.see)` |
| `deployments.go` `GetDeployment`; `day2.go` `ListDeploymentHistory` | `CanSeeProject` or owner | `In(project.see, id)` or owner |
| `requests.go` `ListRequests` | `VisibleProjects`, or requester or owner | `Projects(project.see)`, or requester or owner |
| `requests.go` `visibleRequest` | `CanReadRequest` | unchanged |
| `events.go` `StreamEvents` | `visibleProject`; filter `CanSeeProject`; `request:<id>` by `CanReadRequest` | `project.see`; filter `In(project.see, ·)`; unchanged; ended by `access.changed` |
| `events/hub.go` `Filter.Matches` | `CanSeeProject` given at subscribe | unchanged; the hub ends every stream on `access.changed` |
| `vra.go` `ReadVra` | `IsAdmin` | `Everywhere(import.manage)` |
| `server.go` `authenticate` | an inventory token reaches `/inventory` only | unchanged |
| `local_auth.go` `sessionMayDo` | a password to change first | unchanged |
| `present/present.go` `Project` | `my_role` | `my_role`, `my_permissions` |
| `cmd/rvav/main.go` `run` | `rvav token -scope`; system principals with `admin` | unchanged |

### `internal/store`, `internal/seed`, `internal/vraimport`, `migrations`
| Where | Today | Becomes |
|---|---|---|
| `users.go` `Memberships`, `GroupRoles` | a user's and groups' roles | the assignments with their roles' permissions |
| `users.go` `EnsureSystemUser`; `local_accounts.go` | `global_roles` set on insert | unchanged |
| `projects.go` `ProjectAdmins` | `project-admin` members and groups mapped to it in the project | users and groups given in the project a role holding `approval.project` |
| `projects.go` `ProjectMembers` | anyone with a role in the project | unchanged |
| `admin_projects.go` `CreateProject` | its admins as `project-admin` | unchanged |
| `admin_projects.go` `ProjectMemberRoles`, `SetMember`, `DirectRole`, `RemoveMember` | one role per user and project | several, keyed by user, project and role |
| `admin_projects.go` `OtherProjectAdmins` | other `project-admin` members and groups | other holders of `project.members.manage` given in the project |
| `admin_projects.go` `AdminSources` | users and groups holding `admin` | users and groups holding `access.manage` |
| `admin_projects.go` `GroupMappings`, `CreateGroupMapping` | built-in role names | any role, by foreign key |
| `setup.go` `SetupState` | a group mapped to `admin` | a group given `access.manage` for all of rvav |
| `requests.go`, `deployments.go`, `approvals.go` filters | projects from `VisibleProjects`; viewer, groups | projects from `Projects(project.see)`; unchanged |
| `00001_init.sql` | `global_roles` default `{user}`; `project_members.role`, `group_mappings.role` checked against the built-in names | default kept; a foreign key to `roles` |
| `00005` `day2_policies.roles` | built-in names, checked by `day2RoleNames` | custom keys too; a role named can't be deleted |
| `seed.go` `seedDay2Policies`, members and mappings | built-in roles | unchanged |
| `vraimport/policies.go` `roleKeys` | vRA's roles as built-in ones | unchanged |

### `web/src`
| Where | Today | Becomes |
|---|---|---|
| `components/shell/Sidebar.tsx` `Sidebar`, `admin` | flags from `global_roles` and any `project-admin` | Setup `system.manage`; Catalog items `catalog.manage`; Day-2 actions `extensibility.manage`; Policies `policies.read`; Projects `project.settings.read` anywhere; Templates and networks `infrastructure.read`; Onboarding and vRA import `import.manage`; Integrations `integrations.manage`; Subscriptions and Custom workflows `extensibility.read`; Audit log `audit.read` anywhere; System `system.manage` |
| `components/shell/AppShell.tsx` `NoProject`, `SetupLanding` | `admin` | `project.create`; `system.manage` |
| `features/overview/OverviewPage.tsx` `SetupNotice` | `admin` | `system.manage` |
| `components/shell/roles.ts` `roleLabel`, `accountRole`; `TopBar.tsx` | built-in role labels | the names of the roles held, from `/me` |
| `lib/project.tsx` | the current project's `role`, `viewer` when none | its `roles` and `permissions`, none when none |
| `features/requests/RequestFormPage.tsx` `RequestFormPage` | in the starting project; `canActForOthers` for `project-admin` | `project.see` there; `request.on_behalf` |
| `features/requests/RequestDetailPage.tsx` `canRequestIn` | `member` or `project-admin` | `request.submit` |
| `features/requests/RequestDetailPage.tsx`, `AskPanel.tsx`, `features/approvals/ApprovalsPage.tsx` | `can_decide` from the server | unchanged |
| `features/admin/governance/ProjectsPage.tsx` `Projects` | New project for `admin`; all for admin or auditor; else projects where `project-admin` | `project.create`; `project.settings.read` for all of rvav; else `Projects(project.settings.read)` |
| `features/admin/governance/ProjectsPage.tsx` `ProjectsPage`, `Environments`, `GroupMappings` | shown to admin or auditor, changed by admin; role options fixed | `environments.read`/`manage`, `access.read`/`manage`; roles from the API |
| `features/admin/governance/ProjectPage.tsx` `ProjectPage`, `Details`, `Members`, `Placement`, `Quotas` | `admin`; members for admin or `project-admin`; `roleOptions` fixed | `project.update`; `project.members.manage`; `placement.manage`; `project.quotas.manage`; roles the caller may give |
| `features/admin/AuditPage.tsx` `AuditPage` | all for admin or auditor, else projects where `project-admin` | `Projects(audit.read)` |
| `features/reports/ReportsPage.tsx` `ReportsPage` | all for admin or auditor | `project.see` for all of rvav |
| `features/account/TokensPage.tsx` `TokensPage` | everyone's for `admin` | `tokens.manage` |
| `features/admin/governance/PoliciesPage.tsx` `PoliciesPage` | edit for `admin` | `policies.manage` |
| `features/admin/governance/InfrastructurePage.tsx` `InfrastructurePage` | edit for `admin` | `infrastructure.manage` |
| `features/admin/extensibility/WorkflowsPage.tsx`, `SubscriptionsPage.tsx`; `SubscriptionPage.tsx` `canRetry` | `admin` | `extensibility.manage` |
| `features/admin/OnboardingPage.tsx` | owners: members but not viewers | holders of `request.submit` |
| `features/admin/IdentityProvider.tsx` `TrialResult` | built-in roles from groups | every role the groups give |
| `features/admin/catalog/designer/FormDesigner.tsx` | a preview user's role | the roles they hold there |
| `mocks/handlers.ts` request form, `POST /requests`, `GET /approvals?view=all` | `viewer` refused; admins and auditors | `request.submit`; `approval.read_all` |
| `mocks/day2.ts` `actionsFor`; `mocks/reports.ts` `scopeOf`; `mocks/onboarding.ts` | the role in the project; admin or auditor; not a viewer | the day-2 decision above; `project.see` for all of rvav; `request.submit` |
| `mocks/administration.ts` members, group mappings, `setupOf`, trial | last `project-admin`, last `admin`, built-in roles | last `project.members.manage`, last `access.manage`, any role |
| `mocks/seed.ts` `createDb` | `rvav.mock.as` picks `global_roles` | the persona's roles and permissions, from the built-in fixture |
