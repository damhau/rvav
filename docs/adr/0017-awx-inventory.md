# ADR-0017: AWX reads rvav's VMs from rvav, and rvav's jobs run on that inventory

- Status: accepted
- Date: 2026-10-27

## Context
S6 ([implementation plan](../implementation-plan.md#s6-awx-integration))
asks for a dynamic inventory: `GET /api/v1/ansible/inventory`, and an
`rvav.platform` Ansible collection with an inventory plugin and a custom
credential type ([ADR-0003](0003-awx-for-ansible-native-vsphere.md),
[integrations.md](../integrations.md#dynamic-inventory-awx-pulls-from-rvav)).
Post-configuration jobs (S1) and AWX day-2 actions (S6) run on rvav's VMs
with `limit=<VM name>`, after rvav refreshes the inventory source given by
`RVAV_AWX_INVENTORY_SOURCE_ID`. The design assumed those templates use the
rvav inventory as their own.

## Decision

### The endpoint
`GET /api/v1/ansible/inventory` answers in the format of
`ansible-inventory --list`, for an API token of a user with the auditor
role (the seeded `svc-awx`; `rvav api-token` makes one). It lists every VM
of a deployment that is not being deleted. A failed deployment's VMs stay
in it, so a retried post-configuration step can reach them.
- `ansible_host`: the address rvav reserved, else the FQDN.
- Groups: `rvav`, `project_<code>`, `env_<key>`, `item_<slug>`,
  `tag_<item tag>` and `os_<family>`.
- Host variables: rvav's context (`rvav_deployment_id`, `rvav_project`,
  `rvav_owner`, `rvav_fqdn`, `rvav_moref`, …). `rvav_inputs` holds the
  inputs whose form field is marked `expose_to_inventory`, never secret
  ones.

### The collection
`ansible/ansible_collections/rvav/platform`:
- The inventory plugin `rvav.platform.rvav` takes `RVAV_URL` and
  `RVAV_TOKEN`, a file ending in `rvav.yml`, and `compose`, `groups` and
  `keyed_groups`.
- Since ansible-core 2.19, what the plugin returns is data and never a
  template (`ansible-inventory` prints it as `__ansible_unsafe`). That
  matters: rvav's values include what users typed in forms.
- AWX installs the collection from this repository
  (`collections/requirements.yml`, git) when it syncs the project.
- Publishing to Ansible Galaxy or an Automation Hub is an outward-facing
  step left to the organisation.
- `make test-ansible` builds the collection and runs the plugin through
  `ansible-inventory` against a fake rvav (uv, ansible-core, pytest). It is
  part of `make check` and CI.

### In AWX
- A credential type "rvav API" (kind cloud, fields `url` and `token`)
  injects `RVAV_URL` and `RVAV_TOKEN`.
- An inventory "rvav" has a source from the project
  (`deploy/awx/inventory/rvav.yml`) using a credential of that type.
- `rvav awx-bootstrap -rvav-url … -rvav-token …` sets this up in the test
  AWX and syncs the source once to show whether AWX reaches rvav.

### Jobs on rvav's VMs run on rvav's inventory
A job with a limit, which is a job on rvav's VMs, is launched with
`inventory` set to the inventory of `RVAV_AWX_INVENTORY_SOURCE_ID`. That is
the inventory rvav has just refreshed, whatever inventory the template has
by default. The template must therefore prompt for an inventory on launch.
rvav checks this first (`GET …/launch/`) and refuses, without launching,
a template that doesn't: run on its own inventory, the limit could match
another host of the same name. Jobs without a limit (AWX catalog items)
keep the template's inventory.

This replaces "the templates use the rvav inventory". Templates can then
keep a default inventory for other uses; the contract tests launch them
on `localhost`. And rvav's jobs always run on the inventory it refreshed,
however the AWX admins set the templates.

## Consequences
- Templates used for post-configuration and AWX day-2 actions need
  "Prompt on launch" for Inventory, as well as Limit and Variables
  (integrations.md, launch contract).
- One extra call per launch, to read the template's prompts.
- AWX must reach rvav's API to sync the inventory. In the lab, the AWX
  container must be able to reach rvav's `api` service.
- Not covered: the inventory lists all of rvav's VMs. Limiting it per AWX
  organisation or project waits for a real need.
