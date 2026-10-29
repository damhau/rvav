# Administering rvav

This guide is for administrators and catalog admins: what to set up so
people can request, and how to look after it. Installing and running rvav
is in the [operations guides](../operations/README.md); using it, in the
[user guide](user.md).

**Roles:**
- **Administrator:** everything under **Administration**.
- **Catalog admin:** the catalog only.
- **Auditor:** reads everything and changes nothing.

Roles are given to groups of the identity provider. Every change made
here is in the **Audit log**.

## First steps

The order that gets a first request through:

1. **Integrations:** vCenter, AWX and EfficientIP, with rvav's service
   accounts ([integrations.md](../integrations.md)). Each has a test. Its
   password or token is sealed and never shown again.
2. **Infrastructure:**
   - **Templates:** the vCenter templates offered, by OS.
   - **Sizes:** the CPU, memory and disk choices.
   - **Networks:** each vSphere network's subnet. The subnets come from
     EfficientIP's class parameters; set one by hand where EfficientIP has
     none.
   - **Placement:** which cluster and datastore serve which environment and
     project.
3. **Projects:**
   - each with its members or the groups that map to its roles (viewer,
     member, project admin);
   - its quotas, per environment or for the project as a whole, hard or
     soft.
4. **Policies.**
5. **The catalog.**

## Policies

**Policies** has a tab each. **What applies here** shows, for a project
and an environment, which policies would apply to a request and why.

| Tab | What it decides |
|---|---|
| **Approval** | Which requests need approval, from whom (users or groups, in levels, any or all), and what happens on timeout (escalate, approve, reject). Conditions are CEL over the inputs, environment, project, item, VMs, user and quota. |
| **Quotas** | Projects' limits, also set on each project. A hard limit refuses; a soft one asks for approval. |
| **Lease** | Default and longest leases per environment or project, extensions, and the end: power off then delete, delete, or only tell the owner. |
| **Day-2** | Who may run which action, where, and which need approval (deleting in production, by default). |
| **Naming** | Name templates (`{project}-{environment}-{inputs.role}{seq:03}`), offered by item, project or environment. Names are never given twice, and are checked free in vCenter and DNS. |

## The catalog

**Catalog items** lists the items, their versions and who may request
them. An item is a form and a blueprint
([service-catalog.md](../service-catalog.md)):
- **a VM blueprint:** one VM or several, with their guest settings;
- **an AWX job or workflow:** imported from AWX's templates with the
  catalog label, their survey becoming the form;
- **a composite:** VMs, jobs and custom workflows in steps;
- **a custom workflow**, run by your own worker.

An item's tabs:
- **Details:** name, category, icon, description.
- **Form designer:** fields, sections, rules, value sources (lists from
  vCenter, AWX, EfficientIP or a REST call), and fields approvers may
  change. Preview the form as any user.
- **Definition:** the whole version as YAML, to review or paste.
- **Versions:**
  - publish a draft;
  - compare two versions;
  - deprecate one: requests keep the version they were made with.
- **Entitlements:** which projects may request it.

Items can be exported and imported as YAML, between installations.

## Automation

- **Day-2 actions:** AWX templates with the day-2 label become actions on
  VMs, their survey becoming the action's form
  ([ADR-0018](../adr/0018-awx-day2-actions.md)).
- **Subscriptions:** events (requests submitted, finished, VMs changed)
  sent to webhooks or AWX ([ADR-0021](../adr/0021-event-subscriptions.md)).
  - A **blocking** subscription holds a request until it answers, and can
    add properties to it.
  - Each subscription's runs, their errors and retries are on its page.
- **Custom workflows:** your own Temporal workflows, on your own task
  queue, offered as items or composite steps
  ([custom-workflows.md](../custom-workflows.md)).

## Existing VMs and EfficientIP

- **Onboarding:** adopt VMs rvav didn't build. Choose them in vCenter and
  give each a project, an owner and an item. rvav finds their addresses
  and DNS records in EfficientIP, and nothing is changed there
  ([ADR-0026](../adr/0026-onboarding.md)).
- **EfficientIP reconcile** (under Integrations): addresses and records
  tagged as rvav's that no deployment owns, left by failures or restores.
  Delete them there, or adopt their VM.

## Watching

- **Reports:** requests by period and item, success rate, provisioning
  time, approval lead time, quota use, leases ending
  ([reports.md](../reports.md)).
- **Audit log:** every administrative change and approver edit, with who
  made it. Search it, and export it as CSV for the archive.
- **Secrets:** stored secrets (domain-join accounts, webhook signing
  secrets, credentials for value sources) are sealed with the secrets key.
  They are written, never read back.
