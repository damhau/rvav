# ADR-0027: The vRA importer

- Status: accepted
- Date: 2026-10-30

## Context
rvav replaces vRealize Automation 8 (Aria Automation). Going live means
moving what vRA holds, without retyping it:
- projects and their people;
- cloud templates and their custom forms;
- the catalog and who may request what;
- policies;
- the deployments people own.

It also means sorting vRealize Orchestrator's workflows (ADR-0007, groups A
to D). Some of it maps cleanly onto rvav; some doesn't (NSX load
balancers, scripts in forms, vRO actions), and guessing would make items
that look right and build something else.

## Decision

### Read, never write
`internal/vra` reads vRA through its REST API, as a user who may read the
organisation (a refresh token, or a user and password). It reads from
these services:

| What | Service |
|---|---|
| Projects | `iaas/api/projects` |
| Cloud templates, their content and every version | `blueprint/api/blueprints` |
| Request forms | `form-service/api/forms/search` |
| Catalog items, and what is shared with each project | `catalog/api/admin/items`, `catalog/api/admin/entitlements` |
| Policies | `policy/api/policies` |
| Deployments with their resources | `deployment/api/deployments` |
| Event subscriptions | `event-broker/api/subscriptions` |
| Custom day-2 actions | `form-service/api/custom/resource-actions` |
| vRO workflows (inputs, content, runs) and actions (inputs, scripts) | `vco/api/workflows`, `vco/api/actions` |

Nothing in vRA is changed by rvav. What the cutover changes in vRA, such
as no longer sharing an item with a migrated project, vRA's administrators
do by the runbook (P2.6).

`rvav vra-export` writes what was read to a JSON file. The importer works
from vRA directly or from such a file, so the analysis can run where vRA
is reachable and be looked at elsewhere.

### Map what maps; report the rest
The importer turns what it read into rvav's own objects, and lists, for
each vRA object, what it became and what it couldn't carry over and why:
- A **cloud template's released versions** become a catalog item's
  versions.
- A **custom form** becomes the version's form.
- **Sharing** becomes entitlements.
- **Approval, lease, day-2 and quota policies** become rvav's.
- **Projects** map onto rvav's projects: by name, or as an administrator
  says.

Anything it can't map is reported, never approximated: an NSX load
balancer, a form field fed by a vRO action, a script, a constraint tag
with no placement rule.

Catalog items are imported as **drafts**, through the catalog's own YAML
import (ADR-0019): the same checks as any item, and nothing is offered to
anyone until a catalog admin has looked at it and published it.

### Repeatable, with a dry run first
- **The dry run** shows what an import would create, change or leave,
  and nothing is written.
- **The import** records each vRA object and what it became
  (`vra_links`), so running it again updates what it made and creates
  only what is new. A catalog item an admin has since changed in rvav
  gets a new draft, never an overwritten published version.

### Deployments are adopted, not rebuilt
A vRA deployment's vSphere machines are found in rvav's inventory, by
name in the vCenter rvav syncs. They are adopted through onboarding (S13,
ADR-0026) as one deployment of the same name:
- **owner:** the vRA owner, by email;
- **project:** the vRA project's, mapped;
- **lease:** what remains of the vRA lease;
- **item:** the imported item.

Nothing in vCenter or EfficientIP changes. A deployment whose machines
aren't found, or whose owner isn't in rvav, is reported and left in vRA.

### vRO is sorted, not imported
The vRO triage report lists each workflow and action, with:
- its inputs;
- what in vRA uses it (catalog items, subscriptions, form fields);
- when it last ran.

Each gets a suggested group (A native, B AWX, C custom workflow, D
retire) and the reason for it. People decide; the report is where they
start.

### Project by project
A project moves when its items are published, its deployments adopted
and its people told:
- new requests go to rvav;
- vRA keeps what isn't adopted yet;
- vRA's administrators stop sharing the project's items there.

vRA is switched off when no project is left in it (P2.6, with its runbook).

## Consequences
- **A real vRA must confirm the API calls.** They follow vRA 8's
  reference and are tested against a fake that answers them the same way
  (`vra.FakeServer`, with a sample estate); the Lab acceptance runs them
  against the organisation's vRA.
- **Templates that only partly map** still need people. The report says
  what is missing, so the work is known before the cutover, not found
  during it.
- **Reading through the API** needs a vRA account that can read every
  project. Its token is a secret like the integrations' (RVAV_VRA_TOKEN).
