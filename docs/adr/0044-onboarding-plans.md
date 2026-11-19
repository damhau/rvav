# ADR-0044: Onboarding plans, and VMs that join existing deployments

- Status: accepted
- Date: 2026-11-19
- Extends the onboarding of [ADR-0026](0026-onboarding.md), the updates of
  [ADR-0042](0042-deployment-updates.md) and the multi-VM blueprints of
  [ADR-0024](0024-multi-vm-blueprints.md). X1.5 (#145), vRA parity B22.

## Context
ADR-0026 adopts the VMs an administrator selects, once, each into a
deployment of its own or all into one new deployment. vRA's onboarding
plans go further: a plan is saved, runs again, and says what it onboarded.
Estates keep VMs appearing outside rvav (built by hand, by scripts, by
vRA during a migration), and adopting them meant coming back to the
Onboarding page and selecting them again. A VM built next to an existing
deployment, a third web server by hand, could only become a deployment of
its own.

## Decision

### A plan is a filter and where its VMs go
- **The filter** is the Onboarding page's, plus the VM's **folder** and
  **tags**: name or address (part of the name or host name, or the start
  of an address), folder (a whole folder path, with the folders under it:
  `/GVA/vm/erp` takes `/GVA/vm/erp/prd`, not `/GVA/vm/erp-old`), tag
  (`category/tag`), cluster, vCenter, power state. The inventory sync
  reads the folders and tags of every VM (migration 00077). A filter needs
  a criterion at least: without one, a plan would adopt every VM rvav
  doesn't manage.
- **Where they go** (its target), one of:
  - **each into a deployment of its own**, named after the VM;
  - **all into one new deployment** of a given name: the first run that
    adopts a VM makes it, later runs add to it, and a run after it was
    deleted makes it again;
  - **into an existing deployment**: a ready deployment of a VM item; its
    owner, environment, lease and inputs stay.

  A new deployment takes a project, an environment, an owner, a catalog
  item, a lease and inputs, as ADR-0026's adoption does. A component is
  given when the item's blueprint has several.
- **The target is checked when the plan is saved, and again at each
  run.** A plan whose target stopped working (an owner who left the
  project, an item no longer offered, a deployment deleted) saves no
  more, and its runs fail with why, adopting nothing.
- **Plans are rvav-wide and admin-only,** under `import.manage`, as
  onboarding is. A name is unique.

### Runs
- **A run is a Temporal workflow** (`OnboardingRun`, workflow ID
  `onboarding-<run ID>`) with one activity. It lists the VMs the filter
  matches, then takes them one at a time, in name order, and records what
  it did with each:
  - **adopted**, with the deployment;
  - **skipped**: `managed` (a deployment has it: "*name* has it."),
    `building` (a request is building it);
  - **failed**: `not_found` (gone from vCenter), `upstream` (vCenter or
    EfficientIP couldn't be read), `taken` (an address or DNS record of
    its belongs to a deployment), `no_fit` (it doesn't fit the deployment
    it joins, below), `busy` (that deployment isn't ready).

  A run **succeeded** when nothing failed, is **partial** when a VM
  failed, and **failed** when its target or filter doesn't work, with why.
  A VM failing doesn't stop the others; what was adopted stays.
- **Each VM is adopted in its own transaction,** with ADR-0026's checks:
  a run isn't all or none, unlike adopting by hand, so one VM whose
  address another deployment holds doesn't hold back the others.
- **Retries go on where the run was.** The activity heartbeats per VM and
  retries 3 times; a retry skips the VMs the run already recorded. When
  it can't go on, the run ends failed with the error.
- **One run per plan at a time** (a partial unique index): starting
  another, or deleting the plan, is refused with 409 while one runs.
- **The last 20 runs of a plan are kept,** with what each did to each VM.
  Deleting a plan deletes its runs; the deployments stay.

### No VM is adopted twice
- A VM a deployment has is skipped, not adopted again, whichever plan or
  person adopted it, and so are VMs a request is building.
- Two runs, or a run and someone adopting by hand, that reach the same VM
  at once: a VM's resource is unique by its vCenter and MoRef, so the
  second transaction to record it fails, and the run skips the VM
  ("Another deployment took it meanwhile.").
- A VM that was unregistered (X1.3) is no deployment's: the next run
  adopts it again.

### Schedule: by hand, and every N hours
- **Run now** starts a run (`POST /admin/onboarding/plans/{id}/runs`,
  202 with the run).
- **Every N hours** (1 to 168), optional. Housekeeping, which runs every
  15 minutes, claims the plans whose next run is due and starts their
  runs. Claiming moves the next run on by the interval in one statement,
  so each due run starts once, whichever worker claims it. A run is
  skipped, not queued, when the previous one still runs. Changing the
  interval starts it from the change; a plan without one runs by hand
  only.
- Housekeeping's histories from before X1.5 replay unchanged
  (`GetVersion` "onboarding-plans").
- Not a cron expression: plans follow VMs that appear, and an interval is
  enough to say how soon. A cron can come with a need for one.

### VMs joining an existing deployment
- **By hand too:** `POST /admin/onboarding/into` adopts the selected VMs
  into an existing deployment, all or none as ADR-0026's adoption is.
- **The deployment must be ready,** of a VM item, with no request running
  on it, its project not archived, and the version it had when the
  adoption started (an update in between refuses it: "try again").
- **The component:** a single-VM blueprint takes no component. A
  multi-VM one needs the component the VMs join, unless it has only one.
  An unknown component is refused, naming those it has.
- **What must fit,** or the VM is refused with why (`no_fit`):
  - **its OS family** is the component's (Linux or Windows), when vCenter
    knows the guest's OS;
  - **its name** is no other VM's of the deployment;
  - **the count:** a component has at most 20 VMs. When the component's
    count is a constant, the VMs it has and those joining must fit in it
    ("Application has 1 VM (its count), and has 1 already."). When it is
    a plain input (`count: inputs.web_nodes`), the input is raised to
    the new number of VMs, within the form's maximum, unless raising it
    changes another component's count too. A count that is an
    expression is refused when it is too small.
  - A single-VM item's deployment takes any number of VMs, within 20:
    its count is the VMs it has, and an update keeps them all.
- **The index is the name's order:** VMs joining take the places after
  the component's own, as an update counts them. Nothing is renamed, in
  rvav or in vCenter.
- **What joins with it:** its addresses and DNS records, found in
  EfficientIP as ADR-0026 finds them, and its resources' rows; NSX
  memberships aren't read. The deployment's DRS rule isn't changed.

### The inputs a plan sets are recorded
- A plan, or an adoption by hand, can give the **inputs** the new
  deployments record, as if they had been requested with them: a later
  update (ADR-0042) compares from them rather than from the form's
  defaults. Only the form's fields are taken, validated as a request's
  are, and not secret fields (rvav keeps no secret of a VM it didn't
  build) or read-only ones (rvav works them out).
- A count input raised for VMs joining is recorded the same way.
- **Not read back from the VMs:** sizes, disks, networks and the rest
  stay the form's defaults unless given. An update that differs from what
  a VM has changes it; read its plan before confirming.

### Audit
- `onboarding_plan.created`, `.updated`, `.deleted`, with the filter and
  target.
- `onboarding_plan.run_started`, with the trigger (`manual` or
  `schedule`) and who started it (none for the schedule), and
  `onboarding_plan.run_finished`, with the status, the counts and why it
  failed.
- Each adoption: `deployment.adopted` for a new deployment and
  `deployment.vm_adopted` for each VM a deployment takes, with the plan
  and run when a plan did it.

### The pages
- **Onboarding** has two tabs, **VMs to adopt** (ADR-0026's list, with
  the folder filter) and **Plans**, in the address (`?tab=plans`).
  Adopting by hand chooses **Where they go** as a plan does.
- A plan's page (`/admin/onboarding/plans/{id}`) shows its settings, how
  many VMs it matches now, and its runs; a run opens in a side panel with
  each VM's outcome and why.
- **Deployments** offers **Adopt existing VMs** to those who may onboard,
  which opens the plans.

### vRA
- vRA's onboarding plans aren't imported. What they onboarded are vRA
  deployments, which the importer adopts as it adopts any other
  (ADR-0027); a plan that should go on adopting is made again in rvav,
  where filters replace vRA's lists of machines.

## Consequences
- An estate can be brought under rvav in batches, by folder or tag, and
  kept there: VMs that appear outside rvav are adopted within a run's
  interval.
- Runs adopt VM by VM: a run that stops half-way leaves what it adopted,
  and says so, unlike adopting by hand.
- A deployment can grow by adoption, which needs a count it can raise:
  items whose count is a constant or an expression take no more VMs than
  the count says.
- A component isn't offered from a list: no API lists an item's
  components. The field is free text, and a wrong key is refused, naming
  the right ones.
- Not done: removing a plan's VMs when they leave its filter (a plan only
  adopts), post-configuration of adopted VMs, reading inputs back from
  VMs, and a dry run of a plan beyond how many VMs it matches now.
