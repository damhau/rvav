# ADR-0048: Lease rules and a deployment's lifetime

- Status: accepted
- Date: 2026-11-19
- Extends the lease policies of [ADR-0014](0014-leases-and-quotas.md) and
  the project policies of
  [ADR-0045](0045-deployment-sharing-and-project-policies.md). C2.2 (#158).

## Context
vRA's lease policies apply to the deployments their criteria match: an
item, a size, an input, a project. They also bound a deployment's total
lifetime (`leaseTotalTermMax`), whatever extensions it got. rvav had one
lease per environment, which a project could override for one environment,
and counted extensions only. The vRA importer reported a policy's criteria
as unmapped and turned the total term into a number of extensions.

## Decision

### Lease rules
A lease rule (`lease_rules`) gives a lease to the deployments its CEL
condition matches. The condition reads what approval conditions read:
`inputs`, `environment`, `project`, `item`, `vm`, `vms`, `user`, `quota`,
`request` and `update`. A rule may be for one environment, for one
project, or for every one of them. It has the terms of a lease policy
(`default_days`, `max_days`, `max_extensions`, `grace_days`,
`expiry_action`, `enforcement`, `max_lifetime_days`), a `priority` (100
when absent) and `set_by_admin`.

### Which lease applies
1. The rules are tried by `priority`, lower first, global and the
   project's rules together. On a tie, global rules come first, then by
   name. The first rule whose environment, project and condition match
   decides.
2. With no such rule, the project's override in the environment decides,
   else the environment's lease policy, as before.

A global rule beats a project's unconditional override. An administrator
who wants an exception for one project writes a project rule with an
earlier priority.

### Project admins can't loosen
A project admin (`project.policies.manage`) writes rules for their
project only, as with the other project policies (ADR-0045).

- **At save:** a project admin's rule can't give more than the lease that
  administrators set, in each environment it may apply in. That lease is
  the project's override when an administrator set it, else the
  environment's lease policy. Each field that gives more is refused with
  the code `looser`. "More" is what `looser` already checked for an
  override: no default where the reference has one, a longer `max_days`
  or none, more extensions, more grace days, a milder expiry action, or
  soft where the reference is hard. A longer `max_lifetime_days`, or none
  where the reference has one, also counts. An environment without a lease
  can't be loosened.
- **At request:** a project admin's matching rule is skipped when it
  gives more than the administrators' lease. That lease is the first
  matching rule that administrators wrote (a global rule, or a project
  rule marked `set_by_admin`), else what the save checks against. The
  next rule is tried. This catches a change to the administrators'
  policies made after the save. An administrator's project rule may be
  looser.

### A condition that can't be evaluated
In the preview and in **What applies here**, the rule is skipped, as an
approval policy is: an input it needs isn't filled in yet. On submit, the
rule applies when it gives no more than the lease without rules (the
project's override, or the environment's), and is skipped otherwise. The
failure is logged. An approval policy then requires approval, and a day-2
policy denies. Applying a lease that is no looser has the same intent:
a failure never gives more.

### The deployment keeps its rule
The rule that decides is recorded on the deployment (`lease_rule_id`) at
submit, because its condition reads the requester, the VMs and the
request, which only the request has. The rule's current terms apply, as
a lease policy's do now: a change to the rule changes the limits of the
deployments it decided. The deployment falls back to the project's
override or the environment's lease when:

- someone deletes the rule;
- the deployment moves to a project the rule isn't for.

Updates keep the rule. Onboarded and vRA-imported deployments get the
override or the environment's lease.

### Lifetime
`max_lifetime_days`, on a lease policy or a rule, is how long a deployment
lives in all, counted from its `created_at`. Existing deployments count
from when rvav created them. Imported and onboarded ones count from when
rvav took them over.

- **At request:** a lease longer than the lifetime is refused, or warns
  when the policy is soft. `max_days` and `default_days` can't be longer
  than the lifetime.
- **Extensions:** an extension whose new end is past the lifetime needs
  the approval of the project's approvers, or warns when soft. This is
  independent of `max_extensions`: whichever limit is reached first asks.
  The extension form offers the days left as its default and says how many
  are left.
- **A new or shorter cap** doesn't shorten leases already given: it
  decides the next extension.

### API, audit, What applies here
- `GET` and `POST /admin/lease-rules`, `PUT` and `DELETE
  /admin/lease-rules/{id}`. They're audited as
  `policy.lease_rule.create`, `update` and `delete`, with the target type
  `lease_rule`. The audit prefix isn't `policy.lease.` so that a rule
  doesn't stop the seed keeping `lease_policies` current.
- A request's preview gives the lease's `policy` (the rule's name,
  "<project> in <environment>" or the environment), its `max_extensions`
  and its `max_lifetime_days`. A deployment's lease gives `policy`,
  `max_lifetime_days` and `lifetime_ends_at`.
- **What applies here** lists the rules with why each applies or not,
  read with the item and inputs given, then the override and the
  environment's lease.

### Storage
Migration 00082 adds `lease_rules`, `lease_policies.max_lifetime_days` and
`deployments.lease_rule_id` (set to null when the rule is deleted).

### vRA import
A lease policy's criteria, when the importer can translate them to CEL,
become a lease rule with that condition. Criteria it can't translate are
reported, asking for the condition. `leaseTotalTermMax` becomes the
lifetime.

## Consequences
- Several leases in one environment, by item, size or input, as vRA has.
- Every request evaluates the rules' conditions: one query for the rules,
  and CEL per rule.
- A deployment's limits follow the rule that decided them; the rule's name
  shows where it applies.
- Lifetime and extensions are two limits. An owner may run out of either.
