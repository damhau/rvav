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

## NSX networks

With an NSX Manager set in **Integrations**
([ADR-0028](../adr/0028-nsx-networking.md)):
- **Existing segments** are port groups in vCenter: placement rules name
  them like any network, and their subnets come from EfficientIP as
  usual.
- **Segments made for a deployment:** an item can ask for its own network
  (`segments` in its blueprint, and `network: segments.<key>`). A
  placement rule then says where they go, under **NSX segments made for
  deployments**:
  - the Tier-1 gateway they connect to;
  - the EfficientIP block their subnets are carved from, and their size;
  - the DNS zone and servers of the VMs on them.

  Each request makes its segment before its VMs, and deleting the
  deployment deletes it after them, with its subnet. rvav only ever
  deletes segments it made: they carry its tag.
- **Security groups:** an item can put its VMs in groups made for each
  deployment (`security_groups`, with the firewall rules into them), and
  in existing groups that select VMs by tag (`groups: [nsx:<ID>]`).
  - rvav makes the groups and a security policy of their rules before the
    VMs, and tags each VM once NSX sees it. A deployment's groups hold the
    VMs with its tag; an existing group is joined by the tags its
    conditions select.
  - Deleting the deployment takes the VMs out first, then deletes the
    rules and the groups. Existing groups are never changed.
  - Day-2 actions add a VM to a group its item offers, or remove it:
    who may is up to the **Day-2** policies (`nsx_group_add`,
    `nsx_group_remove`).
  - A tag removed in NSX shows the deployment as changed outside rvav.

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

### The catalog in Git

The catalog can be kept in a Git repository
([ADR-0029](../adr/0029-catalog-in-git.md)), so it is reviewed in merge
requests and the same in every installation. Set the repository in
**Integrations › Git** (configuration.md lists the settings): an HTTPS
URL with a token, or an SSH URL with a deploy key and the server's host
keys (put the token in its own field, never in the URL). **Test
connection** signs in and reads the branch. rvav needs to push to the
branch, never to force a push. Saved settings apply when rvav's processes
restart: until then, Catalog items shows no repository and nothing is
synced.

Each item is a file, `catalog/<slug>.yaml`, as the export writes it with
its published version. **rvav publishes, the repository proposes:**
- **What you do in rvav is committed:** publishing, deprecating, rolling
  back, changing an item's details or slug, deleting it. You are the
  commit's author, the changelog its message. rvav commits in the
  background: publishing never waits for Git.
- **What changes in the repository becomes a draft**, never published by
  itself. Review it in **Versions** and publish it (rvav commits it back),
  or discard it (rvav commits its own file again). A new file becomes a new
  item, a draft. The item's details (name, summary, category) stay as rvav
  has them.
- **Never in the repository:** drafts, entitlements, secrets' values, and
  items imported from AWX.

**Catalog items** shows the repository as last read and the files that
need you; **Read the repository now** reads it at once (otherwise every 5
minutes, or when the Git server calls rvav's webhook). An item's **Git**
tab shows its file, its last commit, and how the repository's file
differs from rvav's:
- **Waiting to be committed:** at the next read.
- **Commit failing:** the error, and when it is tried again (**Read the
  repository now** tries at once). Check the repository's settings and
  token.
- **Draft from Git:** the repository changed the file.
- **Conflict in Git:** both changed it (a draft open in rvav, or a change
  not committed yet). **Keep rvav's** commits rvav's file over the
  repository's change; **Take the repository's** makes a draft of it,
  discarding rvav's draft.
- **Refused from Git:** the file can't be imported: the problems, by line.
  Fix it in the repository.
- **Deleted in Git:** rvav keeps the item; its next change adds the file
  back.

To have the repository read as soon as it changes, add a webhook on the
Git server to `https://<rvav>/api/v1/integrations/git/webhook` (push
events) with the secret of `RVAV_GIT_WEBHOOK_SECRET`: GitHub and Gitea
sign it, GitLab sends it as its token.

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
