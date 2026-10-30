# Implementation plan: from slice 1 to 80% of vRA

This plan takes rvav from what exists today (slice 1) to a production 1.0 that
covers at least 80% of what a vSphere-only vRA installation is used for, and
then to switching vRA off. It replaces the milestone list that was in the
roadmap.

Read it in this order: the [assumptions](#1-assumptions), the
[target](#3-what-80-of-vra-means), the [releases](#4-releases), then the
[slices](#6-slices) when you need detail.

## 1. Assumptions

Change these, and the estimates and calendar change with them.

| Topic | Assumption |
|---|---|
| Scope | One organisation, VMware vSphere only, Ansible through AWX, IPAM and DNS in EfficientIP SOLIDserver ([ADR-0002](adr/0002-single-organisation.md), [ADR-0003](adr/0003-awx-for-ansible-native-vsphere.md)). |
| vRA usage | A typical enterprise setup: Service Broker catalog with custom forms, VM blueprints, approvals, leases, day-2 actions, event subscriptions calling vRO, a few XaaS items. Re-weight [section 3](#3-what-80-of-vra-means) once the real usage is known. |
| Reference team | 2 backend developers (Go), 1.5 frontend developers (React), 0.5 platform engineer, and a product owner who decides scope. Each person delivers about 80% of their time on the plan. |
| Environments | By the end of month 1: a lab vCenter (not production), a test AWX, a test SOLIDserver (or vendor sandbox), Keycloak or the corporate OIDC provider, and a place to run Temporal. |
| vRA | Keeps running until cutover. rvav runs next to it, project by project. |
| Estimates | Engineer-weeks (ew) of focused work, ±30%. A 25% contingency is added at the end. |

## 2. Where we are

Built ([roadmap status](roadmap.md#status)): S1 to S14 (1.0, stretch items
included), P2 (the vRA importer and cutover) and N1 (NSX networks and
security groups).

Proven: end-to-end tests against PostgreSQL, vcsim, a Temporal dev server
and fakes of AWX, EfficientIP, vRA and NSX; browser tests on the mock API;
the compose stack with the real images, Temporal and Keycloak; a test AWX.

Not proven yet, each a Lab acceptance issue (#76 to #91): the lab vCenter,
the organisation's AWX, SOLIDserver, identity provider, mail server, Teams
and Slack, vRA and NSX Manager.

## 3. What "80% of vRA" means

vRA features are listed below with a weight for how much a typical vSphere
shop relies on them (H = 3, M = 2, L = 1). Coverage is the weighted share of
features delivered in 1.0.

**Result: 83% in the core 1.0 scope, 86% with the four stretch items
(all built); 88% (97 / 110) with P2's importer and half of NSX (N1, load
balancers out).** Features vRA has but a vSphere-only shop rarely uses
(other clouds, Kubernetes, pipelines) are counted against us, so the figure
is conservative.

**After N1** (decided 2026-10-30, in this order): N2 NSX gaps, C1 the
catalog in Git, D1 one canvas (an item's topology, the composite
designer, a request's run), A1 custom roles, D2 composite step kinds
([section 6](#n2-nsx-gaps-after-n1)). With D1 and D2 counting half of
features 17 and 41, and A1 the other half of 44, 100 / 110 = 91%.
Kept out, decided: 7, 26, 28, 29, 36, 42, 48, and NSX load balancers
(half of 27); Terraform runs as an AWX item for those who need it.

### Service Broker

| # | vRA feature | Weight | rvav | Slice |
|---|---|---|---|---|
| 1 | Self-service catalog, search, categories | H | 1.0 | S1 (done) |
| 2 | Custom forms: layout, conditions, validation | H | 1.0 | S8 |
| 3 | Dynamic form values (vRO actions, external sources) | H | 1.0 | S1, S8 |
| 4 | Cloud templates as catalog items | H | 1.0 | S1, S8 |
| 5 | vRO workflows as catalog items (XaaS) | M | 1.0, as AWX items and custom workflows | S6, S10 |
| 6 | Ansible Tower / AWX job templates as catalog items | M | 1.0 | S6 |
| 7 | Terraform, marketplace and CloudFormation content | L | No | – |
| 8 | Content sharing (entitlements) | H | 1.0 | S8 |
| 9 | Approval policies | H | 1.0 | S3 |
| 10 | Lease policies | H | 1.0 | S5 |
| 11 | Day-2 action policies | H | 1.0 | S4 |
| 12 | Resource quota policies | M | 1.0 | S5 |
| 13 | Deployment limit policies | L | 1.0 | S5 |
| 14 | Email notifications | M | 1.0 | S11 |
| 15 | Request on behalf, scheduled requests, request count | L | 1.0 (stretch) | S11 |

### Cloud Assembly

| # | vRA feature | Weight | rvav | Slice |
|---|---|---|---|---|
| 16 | Cloud templates with versions | H | 1.0 | S8 |
| 17 | Visual template designer (canvas) | M | After N1: an item's topology on a canvas, beside its YAML and form designer (half) | D1 |
| 18 | Multi-machine templates, extra disks and networks | H | 1.0 | S12 |
| 19 | Image and flavour mappings | H | 1.0, as template and size catalogues | S9 |
| 20 | Cloud zones, placement, capability tags | H | 1.0 | S9 |
| 21 | Network profiles with IPAM | H | 1.0 | S7, S9 |
| 22 | Storage profiles (SPBM) | M | 1.0 | S12 |
| 23 | Projects, members, roles | H | 1.0 | S1, S9 |
| 24 | Custom naming | H | 1.0 | S1, S9 |
| 25 | Several vCenters | M | 1.0 | S12 |
| 26 | AWS, Azure, GCP, VMC endpoints | L | No | – |
| 27 | NSX networking, security groups, load balancers | M | After 1.0: networks and security groups native, built ([ADR-0028](adr/0028-nsx-networking.md)); load balancers not | N1 |
| 28 | Kubernetes and Tanzu namespaces | L | No | – |
| 29 | Terraform integration | L | No | – |
| 30 | Onboarding existing VMs | M | 1.0 | S13 |
| 31 | Native day-2: power, resize, disks, snapshots, console | H | 1.0 | S4 |
| 32 | Custom day-2 actions | H | 1.0 | S6, S10 |
| 33 | Deployments view, history | H | 1.0 | S1, S4 |
| 34 | Drift, changes made outside vRA | L | 1.0 (stretch) | S13 |
| 35 | Git integration for templates | L | 1.0 (stretch): YAML import and export; after N1, the catalog in Git | S8, C1 |
| 36 | Code Stream pipelines | L | No | – |
| 37 | Action-based extensibility (ABX) | M | 1.0, as AWX and custom workflows | S10 |
| 38 | Event broker subscriptions | H | 1.0 | S10 |

### Orchestrator and platform

| # | vRA feature | Weight | rvav | Slice |
|---|---|---|---|---|
| 39 | Workflow engine and custom workflows | H | 1.0, with Temporal ([ADR-0007](adr/0007-extensibility-model.md)) | S1, S10 |
| 40 | Plugins: AD, SQL, REST, PowerShell, SSH | M | 1.0, as Ansible modules | S6 |
| 41 | vRO visual workflow designer | M | After N1: composite items on a canvas, then step kinds for waits, people and REST, logic in CEL; no scripts (half) | D1, D2 |
| 42 | Multi-tenancy (organisations) | L | No ([ADR-0002](adr/0002-single-organisation.md)) | – |
| 43 | Single sign-on | H | 1.0 | S1, S2 |
| 44 | Role-based access, custom roles | M | Half: fixed roles in 1.0; custom roles after N1 | S9, A1 |
| 45 | API and CLI | H | 1.0 | S1, S9, S14 |
| 46 | Audit log | M | 1.0 | S9 |
| 47 | Reports and dashboards | L | 1.0 (stretch) | S13 |
| 48 | Cost and showback | L | No (decided) | – |
| 49 | High availability, backup and restore | H | 1.0 | S14 |
| 50 | Migration from the previous system | L | After 1.0: vRA importer | P2 |

Total weight 110. Not in 1.0: 15 (items 7, 17, 26, 27, 28, 29, 36, 41, 42, 48,
50, and half of 44). Stretch: 4 (items 15, 34, 35, 47). Core 1.0 covers
91 / 110 = 83%; with stretch, 95 / 110 = 86%.

## 4. Releases

| Release | When (reference team) | Contents | Who uses it |
|---|---|---|---|
| **R0 Lab** | End of month 3 | Slice 1 plus S2 to S5: running for real, approvals, day-2 actions, leases and quotas | The platform team, in the lab |
| **R1 Pilot** | End of month 6 | Plus S6 to S9: real AWX and EfficientIP, catalog and admin UI | One pilot project in dev and tst; vRA still serves production |
| **R2 1.0** | Month 10 | Plus S10 to S14: extensibility, notifications, advanced provisioning, inventory, production readiness. 83% coverage. | Production use alongside vRA, project by project |
| **R3 vRA off** | Month 13 | Phase 2: vRA importer, vRO triage, parallel run, cutover | Everyone; vRA is switched off |

Each release ends with a demo on the lab or pilot environment and a go/no-go
by the product owner.

## 5. Calendar (reference team)

Lanes are people, not strict assignments. Frontend work for a slice follows
its API by a few weeks.

| Month | Backend A | Backend B | Frontend | Platform |
|---|---|---|---|---|
| 1 | S2 Make it real | S3 Approvals | S3 Approvals UI, slice 1 polish | Lab environments, CI, Temporal |
| 2 | S4 Day-2 actions | S5 Leases and quotas | S4 Day-2 UI | Keycloak or corporate OIDC |
| 3 | S4 Day-2 (console, rename, delete) | S7 EfficientIP | S5 UI, start S8 | **R0 Lab** |
| 4 | S6 AWX | S9 Admin backend | S8 Catalog admin, form designer | Test AWX content |
| 5 | S6 AWX (inventory plugin) | S8 Catalog backend | S8 form designer | Pilot environment |
| 6 | S12 Advanced provisioning | S9 Admin, audit | S9 Admin UI | **R1 Pilot** |
| 7 | S10 Subscriptions | S12 Advanced provisioning | S6 AWX items and actions UI, S10 UI | |
| 8 | S10 Custom Temporal workflows | S11 Notifications | S11 and S13 UI | Helm chart |
| 9 | S13 Inventory, onboarding | S14 HA, observability | S13 Reports | Backups, monitoring |
| 10 | S14 Security, performance | S14 Upgrade tests | Accessibility audit, docs | **R2 1.0** |
| 11 | Contingency, pilot feedback | Contingency | Contingency | |
| 12–13 | P2 vRA importer | P2 parallel run support | P2 importer UI | Cutover |

### Effort

| Slice | Backend | Frontend | Platform | Total (ew) |
|---|---|---|---|---|
| S2 Make it real | 2 | 0.5 | 1.5 | 4 |
| S3 Approvals | 3 | 2 | | 5 |
| S4 Day-2 actions | 5 | 3 | | 8 |
| S5 Leases and quotas | 2.5 | 1.5 | | 4 |
| S6 AWX integration | 5 | 2 | | 7 |
| S7 EfficientIP integration | 2.5 | 0.5 | | 3 |
| S8 Catalog administration | 4 | 7 | | 11 |
| S9 Projects, placement, governance | 4 | 3.5 | | 7.5 |
| S10 Extensibility | 8 | 2.5 | | 10.5 |
| S11 Notifications and request options | 3 | 1.5 | | 4.5 |
| S12 Advanced provisioning | 6 | 1.5 | | 7.5 |
| S13 Inventory and reporting | 4 | 3 | | 7 |
| S14 Production readiness | 4 | 1.5 | 5 | 10.5 |
| **1.0 subtotal** | **53** | **30** | **6.5** | **89.5** |
| P2 vRA importer and cutover | 7 | 2 | 4 | 13 |
| **Total** | **60** | **32** | **10.5** | **102.5** |
| With 25% contingency | 75 | 40 | 13 | **128** |

Backend work sets the pace: 53 ew plus contingency, at 1.6 ew a week for two
developers, is about 41 weeks to 1.0.

| Team | 1.0 | vRA off |
|---|---|---|
| 1 backend, 1 frontend, part-time platform | ~19 months | ~23 months |
| **Reference: 2 backend, 1.5 frontend, 0.5 platform** | **~10 months** | **~13 months** |
| 3 backend, 2 frontend, 1 platform | ~7–8 months | ~10 months |

Adding people beyond the third case shortens the calendar less, because slices
depend on each other (section 7).

## 6. Slices

Every slice follows [ADR-0006](adr/0006-contract-first-vertical-slices.md):
spec first, then backend and frontend in parallel, finished end to end.

**Definition of done, for every slice**
- `api/openapi.yaml` updated and linted, generated code current (`make gen-check`, `pnpm gen:api`).
- Backend unit tests, plus end-to-end test cases for the new flows against PostgreSQL and vcsim.
- Frontend unit tests, and a Playwright test for the main path in mock mode.
- Mock handlers in `web/src/mocks` follow the new spec.
- Audit entries and live events for every state change.
- User-facing copy reviewed; empty, loading and error states present.
- Docs updated (`docs/`, READMEs), and a demo on the lab environment.

### S1 Catalog to running VM (done)
Catalog, API-driven request form, preview, submit, provisioning workflow with
EfficientIP, vCenter and AWX steps, live timeline, deployments, event stream,
Docker Compose stack.

### S2 Make it real
**Goal:** everything from slice 1 works on real infrastructure, and every change is checked by CI.
- **CI** (GitHub Actions): Go vet, tests and the end-to-end test with PostgreSQL and vcsim; frontend lint, typecheck, tests, build and Playwright; spec lint; generated-code check; Docker image builds.
- Run the Docker Compose stack for real; fix image tags, start order and health checks.
- A real Temporal server: worker connection, retries, workflow history in the UI. Decide self-hosted or Temporal Cloud.
- OIDC sign-in end to end with Keycloak, then the corporate provider; group-to-role mapping.
- First run against the lab vCenter with a real template, customisation spec and network.
- Refuse to start in dev auth mode unless an explicit development flag is set.

**Acceptance:** a RHEL 9 request submitted in the browser reaches Ready on the lab vCenter, with the steps visible in the Temporal UI; CI is green and required on pull requests.
**Depends on:** lab environments.
**Risks:** lab access late (mitigate: ask in week 1); Temporal operations are new to the team (mitigate: short training, runbook from the start).

### S3 Approvals
**Goal:** production requests go through approval with the experience in the mockup.
- **API:** list approvals waiting for me, approval detail, approve or reject with a comment, approve with edited inputs (within the field's limits), delegate, approval history on the request.
- **Policies:** several levels, any or all approvers per level, CEL conditions (already used in preview), auto-approve rules, timeout with auto-approve, auto-reject or escalation.
- **Workflow:** the provisioning workflow waits for the decision signal; timeouts use Temporal timers.
- **UI:** approvals inbox and detail (from the mockup), approval step in the request timeline, sidebar badge.
- Replace the `rvav approve` command-line workaround.

**Acceptance:** a 48 GB production VM waits for the Infrastructure leads; Marc lowers RAM to 32 GB and approves; the requester sees who approved, what changed and why; a two-level policy needs both levels; an unanswered approval escalates after the configured time.
**Depends on:** S2.

### S4 Day-2 actions
**Goal:** the native actions vRA users rely on every day.
- **Actions:** power on, off, shut down guest, restart guest; resize CPU and RAM (hot-add aware); add or extend disks; add, remove or move NICs; snapshots (create, revert, delete, retention policy); web console; rename (vCenter and DNS); mount ISO; change owner or project (with quota check); delete deployment (VM, IP and DNS released).
- **Day-2 policies:** which roles may run which action, on which resources, under which conditions (for example no delete in production without approval).
- **API:** list actions available for a resource (already filtered by policy), run an action with its form, action runs with their own timeline, deployment history, retry or roll back a failed request.
- **UI:** action menus and dialogs from the mockup, bulk actions, History tab.
- **Spike in week 1:** web console. vCenter's HTML console (WebMKS) needs the browser to reach the ESXi hosts. If that isn't allowed, add a WebSocket proxy in rvav.

**Acceptance:** each action works on the lab vCenter, shows in the timeline and the audit log, respects day-2 policies, and a failed request can be retried from its failed step.
**Depends on:** S2; S3 for actions that need approval.
**Risks:** console access through firewalls (spike first; fallback: VMRC link).

### S5 Leases and quotas
**Goal:** resources don't live forever, and projects stay within their quota.
- **Lease policies:** default and maximum per project and environment, self-service extension up to a limit, extension through approval, expiry action (power off, then delete after a grace period).
- **Design:** leases live in the database. A Temporal schedule runs every 15 minutes to send reminders and start a short expiry workflow per deployment. No workflow sleeps for months, so upgrades stay safe.
- **Quotas:** per project and environment, hard or soft (soft can mean "needs approval"), checked on submit, resize and change of project.
- **Reminders:** 7 days and 1 day before expiry (email arrives in S11; in-app notifications now).
- **UI:** lease section on deployments, extend dialog, quota on the overview and in the form summary (partly done).

**Acceptance:** a dev VM with a 1-day lease gets a reminder, is powered off at expiry and deleted after the grace period; an extension beyond the maximum asks for approval; a request over a hard quota is refused with a clear message.
**Depends on:** S3, S4.

### S6 AWX integration
**Goal:** AWX becomes the automation layer described in ADR-0003 and ADR-0007.
- **Content source:** connect to AWX, choose templates by organisation or label, sync every few minutes, flag changed surveys with a diff.
- **Catalog items:** AWX job templates and workflow job templates with forms generated from their surveys, which admins can then extend.
- **Dynamic inventory:** `/api/v1/ansible/inventory`, and the `rvav.platform` Ansible collection with the inventory plugin and a custom credential type, published to Ansible Galaxy or an internal Automation Hub.
- **Custom day-2 actions** backed by AWX templates, bound to resource types and conditions.
- Value sources `awx.inventories` and `awx.credentials`.
- Validate the AWX client against the test AWX: launch prompts, job events, cancel, workflow jobs, errors.

**Acceptance:** an AWX template imported from the test AWX is requestable with its survey as a form; "Install monitoring agent" runs as a day-2 action on a VM using the rvav inventory; a changed survey is flagged in the catalog admin.
**Depends on:** S2, S4; test AWX.
**Risks:** AWX release changes (pin and test the version in CI against a containerised AWX if possible).

### S7 EfficientIP integration
**Goal:** the real SOLIDserver replaces the fake.
- Confirm the current SOLIDserver REST API and implement the client: free address in a subnet, reserve, release, A and PTR records, rename.
- Map vSphere networks to subnets from class parameters on the subnet, with a manual override in placement rules.
- Form pickers `ipam.subnets` and `dns.zones`; check that a hostname is free in DNS.
- Find and report orphaned addresses and records left by failed runs.

**Acceptance:** a request reserves a real address and creates both records; deleting the deployment removes them; a reconcile report lists orphans.
**Depends on:** S2; test SOLIDserver.

### S8 Catalog administration
**Goal:** catalog admins build and publish items without touching the database.
- **API:** create and edit items, versions (draft, published, deprecated, compare, roll back), entitlements, categories and icons, YAML import and export.
- **Form designer:** three panes (fields, live preview, properties) with a YAML view, value sources and dependencies, CEL rules, preview as a chosen user and project.
- Value sources `rest` (URL template, auth, JSONPath mapping, caching) and `cel`.
- Per-field rule results in the preview response, so the browser no longer evaluates CEL itself.
- Checklist options with descriptions (gap found in slice 1).

**Acceptance:** an admin builds an Ubuntu item with a REST-backed dropdown, previews it as a member of Web Platform, publishes v1, edits it into v2 and rolls back; existing deployments keep their version.
**Depends on:** S3 for approval settings on items.
**Risks:** the form designer can grow without end (mitigate: YAML first, the visual designer covers the common 80%).

### S9 Projects, placement and governance
**Goal:** the platform team administers rvav from the UI.
- Projects, members and roles; OIDC group mappings; environments.
- Placement rules: vCenter, cluster or resource pool, folder, datastores, networks and subnets, tag-based selection.
- Template catalogue (vRA image mappings) and size presets (flavour mappings) per project.
- Naming policies with counters and uniqueness checks.
- Integration settings: vCenter, AWX and EfficientIP connections with a "test connection" button; secrets in Vault or encrypted in the database.
- API tokens for automation; audit log with filters and CSV export.

**Acceptance:** a new project is set up end to end from the UI (members, placement, quota, naming, templates) and can request a VM without any SQL or YAML.
**Depends on:** S5.

### S10 Extensibility
**Goal:** the three levels in [ADR-0007](adr/0007-extensibility-model.md).
- **Event subscriptions:** topics (request, provisioning, day-2, delete, lease), CEL conditions, blocking or not, timeouts, priority; targets are an AWX template, a webhook (HMAC-signed) or a custom workflow; delivery through the outbox with retries; run history.
- **Composite catalog items:** ordered and parallel steps combining native steps, AWX templates, custom workflows and other items.
- **Custom Temporal workflows:** registration, child-workflow invocation, the `RvavContext` input, `ReportStep`, `AppendLog` and `RequestApproval` activities, versioning rules, Go and Python helper packages, an example repository with tests.
- **UI:** subscriptions admin, run history, composite item editor (YAML first).

**Acceptance:** a blocking subscription calls a CMDB webhook before provisioning and can stop it; a composite "VM plus database user" item runs both parts in one timeline; a custom Python decommission workflow runs over several days with an approval and shows its steps in rvav.
**Depends on:** S6, S8.
**Risks:** the custom workflow contract becomes hard to change once used (mitigate: version it, start with workflows written by the platform team only).

### S11 Notifications and request options
**Goal:** people hear about what concerns them, where they work.
- Email (SMTP) with templates per event; Microsoft Teams and Slack, including approve and reject buttons.
- Notification preferences per user; notification centre in the UI.
- Request on behalf of someone, scheduled requests, request count, save as draft, request again (stretch).

**Acceptance:** an approver approves from Teams; the requester gets an email when the VM is ready and seven days before the lease ends.
**Depends on:** S3, S5.

### S12 Advanced provisioning
**Goal:** the blueprint features that real vRA templates use.
- Multi-VM blueprints with counts and anti-affinity (DRS rules), several disks and NICs at provisioning, vSphere tags.
- Windows customisation specs with domain join; cloud-init for Linux (partly done).
- Content library templates; storage policies (SPBM).
- Several vCenters, with placement choosing between them.
- The PostgreSQL cluster composite item as the reference example.

**Acceptance:** a three-VM application blueprint with anti-affinity, two disks each and a Windows node joined to the domain provisions on the lab, across two clusters.
**Depends on:** S9. The PostgreSQL composite example also needs S10, so it comes last.

### S13 Inventory and reporting
**Goal:** rvav matches vCenter at all times and shows how the platform is used.
- Inventory sync from vCenter change notifications (property collector) instead of polling.
- Drift: VMs changed or deleted outside rvav are flagged on their deployment (stretch).
- Onboarding: adopt existing vCenter VMs into deployments with owner, project and lease. The vRA importer builds on this.
- Reports: requests over time, success rate, provisioning time, usage against quota, upcoming lease expiries, approval lead time, CSV export (stretch).

**Acceptance:** a VM powered off in vCenter shows as off in rvav within seconds; 50 existing VMs are adopted into deployments in one operation.
**Depends on:** S12.

### S14 Production readiness
**Goal:** pass the 1.0 gate in [section 8](#8-10-production-gate).
- High availability: two or more API and worker replicas, sync leader election, Temporal in HA or Temporal Cloud.
- Helm chart and a documented VM-based alternative; configuration and secrets reference.
- Backups and tested restores for PostgreSQL (point in time) and Temporal.
- Upgrades: migration policy, upgrade test with workflows in flight, worker versioning.
- Observability: OpenTelemetry traces, Prometheus metrics, dashboards, alerts, log retention.
- Security: threat model, external penetration test, dependency and container scanning, SBOM, session and token review.
- Performance: load test (k6) with 200 concurrent users and 500 requests an hour.
- Accessibility audit to WCAG 2.2 AA (axe in CI plus manual checks).
- Documentation: user guide, admin guide, API reference, runbooks.

**Acceptance:** the 1.0 gate checklist is complete and signed off.
**Depends on:** everything before.

### P2 vRA importer and cutover (after 1.0)
- Read vRA 8 / Aria Automation through its API: projects, cloud templates, custom forms, catalog items and sharing, policies, deployments.
- Map templates to blueprint versions and custom forms to rvav forms; report what can't be mapped instead of guessing.
- Adopt vRA deployments with owner, project and lease (uses S13 onboarding).
- vRO triage report (groups A to D in ADR-0007) from the vRO API.
- Dry run with a diff report, then an import that can be repeated safely.
- Parallel run: project by project, new requests go to rvav; vRA keeps existing deployments until they are adopted. Then vRA is switched off.

**Acceptance:** every project is migrated, every active vRA deployment is adopted or retired, and vRA receives no new requests for 30 days.

### N1 NSX networking (after 1.0)
Decided after 1.0, instead of AWX playbooks ([ADR-0028](adr/0028-nsx-networking.md)); load balancers are left out.
- An NSX client for the Policy API, an NSX Manager in Integrations, a fake NSX for tests and the compose stack.
- Networks: existing segments by placement, and segments made for a deployment on a Tier-1, their subnets from EfficientIP.
- Security groups: existing ones by tag, and groups made for a deployment with their distributed firewall rules; day-2 actions add and remove VMs.
- NSX in item YAML and the form designer; the vRA importer maps `Cloud.NSX.Network` and `Cloud.SecurityGroup` (N1.4).

**Acceptance** (`TestN1Acceptance`, `TestMapNSXTemplate`; the organisation's NSX: [#91](https://github.com/damhau/rvav/issues/91)): a three-tier item builds its VMs on a new segment of the project's Tier-1, each tier in its own security group with the firewall rules between them; deleting the deployment removes the segment, groups and rules; a day-2 action moves a VM into another group; a vRA template with NSX networks and security groups imports without them left out.

### N2 NSX gaps (after N1, built)
- NSX segments and security groups in composite items' VMs.
- A VM's added adapters on a segment made for its deployment.

**Acceptance** (`TestN2Acceptance`, `TestCompositeNSX`, `TestSegmentAddedAdapter`; the organisation's NSX: [#91](https://github.com/damhau/rvav/issues/91)): a composite whose VM step is on its own segment and in a
security group builds, and deletes, as a VM item does; a VM with two
adapters, the second on the deployment's segment, gets an address there.

### C1 The catalog in Git (after N1)
- Catalog items as YAML files in a Git repository (one per item, as the
  export writes them), over HTTPS with a token or SSH with a deploy key.
- Publishing in rvav commits the item; a change merged in the repository
  becomes a draft in rvav, with the diff, for a catalog admin to publish.
- What differs between the two is shown, never overwritten silently.

**Acceptance:** an item changed in rvav appears in the repository; one
changed by a merge request appears in rvav as a draft; a conflict is shown.

### D1 One canvas (after N1)
The canvas rvav's catalog and requests share (React Flow, laid out
automatically; YAML stays the source of truth), used three ways:
- **an item's topology:** a VM item's components, disks, networks and
  segments, security groups and the rules between them, read only;
- **the composite designer:** steps in order and in parallel, conditions,
  undo, and each step's inputs bound to the form's or earlier steps'
  outputs through a picker that knows them; the server's checks on the
  step itself; the YAML tab edits the same document;
- **a request's run:** the same graph with each step's live status.

UX borrowed from Windmill (typed step inputs, bindings), Kestra (code and
graph side by side) and n8n (the run on the graph); no code from them.
ADR-0007's "no visual designer" is revised in this slice's ADR.

**Acceptance:** the seeded PostgreSQL composite edited on the canvas
(a step added, its inputs bound, a condition set) and published; its
request followed on the same graph; a three-tier item's topology shown;
keyboard and axe in both themes.

### A1 Custom roles (after N1)
- Roles as sets of permissions (request, run each kind of day-2 action,
  approve, see, administer...), at the project or the whole of rvav; the
  fixed roles become built-in ones.
- A role editor and assignment to users and groups; day-2 and approval
  policies keep deciding within what a role allows.

**Acceptance:** an operator who may only power VMs on and off, and an
auditor who sees everything and changes nothing, made and used.

### D2 Composite step kinds (after N1)
Sized first from twelve typical enterprise vRO workflows
(`rvav vro-triage -standard`; D2.1): a REST call in 5, wait for a person
in 3, wait for a time in 3 (one polls a ticket until it is approved),
a branch in 2, a notification in 1, wait for an event in none. The vRO
triage of the organisation's vRA (#85) confirms or reorders them.
- New steps: wait for a person (a form or an approval mid-run, with its
  notification), wait for a time, a REST call (allowed hosts, a stored
  secret), a notification, a branch; the logic between them in CEL.
- No scripting language: work that needs one is an AWX playbook or a
  custom workflow.

**Acceptance:** a vRO workflow with a user interaction and a REST call,
rebuilt as a composite on the canvas, run end to end.

## 7. Dependencies

| Slice | Needs |
|---|---|
| S2 Make it real | Lab environments |
| S3 Approvals | S2 |
| S4 Day-2 actions | S2 (S3 for actions that need approval) |
| S5 Leases and quotas | S3, S4 |
| S6 AWX integration | S2, S4, test AWX |
| S7 EfficientIP integration | S2, test SOLIDserver |
| S8 Catalog administration | S3 |
| S9 Projects, placement, governance | S5 |
| S10 Extensibility | S6, S8 |
| S11 Notifications | S3, S5 |
| S12 Advanced provisioning | S9 (S10 for the composite example) |
| S13 Inventory and reporting | S12 |
| S14 Production readiness | All of the above |
| P2 vRA importer | 1.0, S13 onboarding |
| N1 NSX networking | 1.0 (S9, S12, S13), P2 |
| N2 NSX gaps | N1 |
| C1 The catalog in Git | S8 |
| D1 One canvas | S10 (composites), S12, N1 |
| A1 Custom roles | S9 |
| D2 Composite step kinds | D1, the vRO triage of the real vRA (#85) |

The longest chain is S2 → S3 → S5 → S9 → S12 → S13 → S14. A delay there moves
1.0; most other slices have a few weeks of slack.

External prerequisites, needed by month 1: lab vCenter, test AWX, test
SOLIDserver, an OIDC client in the corporate identity provider, and a decision
on where Temporal runs.

## 8. 1.0 production gate

- **Service levels:** API p95 under 300 ms; pages usable in under 1.5 s; provisioning succeeds at least 98% of the time when vCenter, AWX and EfficientIP are healthy.
- **Resilience:** no single point of failure; losing one API or worker node interrupts nothing; restore tested with RPO 15 minutes and RTO 1 hour.
- **Security:** SSO only (dev auth impossible to enable in production), penetration test with no open high findings, secrets never in logs or responses, full audit trail.
- **Operations:** dashboards and alerts in place, runbooks for the top 10 failure cases, an upgrade done with requests in flight.
- **Quality:** CI green, end-to-end tests for every slice, accessibility audit passed.
- **People:** user guide and admin guide published; the pilot project's feedback addressed.

## 9. Cross-cutting work in every slice

| Track | What it means in practice |
|---|---|
| Testing | Unit, end-to-end against vcsim, Playwright; contract tests against the test AWX and SOLIDserver from S6 and S7. |
| Security | Authorisation checked on every new endpoint; secrets reviewed; dependency updates monthly. |
| Observability | Every new workflow step and external call has a trace span and a metric. |
| UX | New screens follow the mockup and `docs/ux.md`; each has empty, loading and error states. |
| Docs | User-facing behaviour in the user guide; settings in the admin guide. |
| Performance | No list without pagination; heavy queries checked with `EXPLAIN` before merge. |

## 10. Risks

| Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|
| Lab environments arrive late | Medium | High | Request them in week 1; keep working against vcsim and the fakes meanwhile. |
| Real AWX or SOLIDserver behave differently from the docs | High | Medium | Contract tests from S6 and S7; keep the fakes in sync with what the real systems do. |
| Web console blocked by network rules | Medium | Medium | Spike at the start of S4; WebSocket proxy in rvav; VMRC link as fallback. |
| Temporal operations are new to the team | Medium | High | Training in S2; consider Temporal Cloud; runbooks; worker versioning before 1.0. |
| Frontend capacity becomes the bottleneck (S8 is heavy) | Medium | Medium | Start the form designer early; YAML first; 1.5 frontend developers minimum. |
| Users expect vRA behaviour that isn't in scope | High | Medium | Agree the parity table in section 3 with the stakeholders before R1. |
| The custom workflow contract has to change after teams use it | Medium | Medium | Versioned contract and SDK; platform team only at first. |
| Long-waiting workflows block upgrades | Low | High | Leases handled by schedules, not sleeping workflows; approval waits use worker versioning. |
| vRO workflows are more numerous or complex than expected | Medium | High | Run the vRO triage report early in P2, or by hand during R1, to size the work. |

## 11. Decisions needed

| Decision | Needed by | Options |
|---|---|---|
| Where Temporal runs | S2 | Self-hosted on Kubernetes or VMs, or Temporal Cloud |
| Hosting for rvav | S2 (choice), S14 (production) | Kubernetes with Helm, or VMs with Compose and systemd |
| Corporate identity provider and group names | S2 | Keycloak, Entra ID, Okta… |
| Web console approach | S4 | Direct WebMKS, proxy in rvav, VMRC only |
| Secret storage | S9 | HashiCorp Vault, or encrypted in PostgreSQL |
| Notification channels first | S11 | Teams, Slack, email only |
| Languages for custom workflows | S10 | Go and Python first, TypeScript later |
| Which vRA features are actually used | Before R1 | Re-weight section 3 |

## 12. Keeping this plan current

- Update the status of a slice in the [roadmap](roadmap.md) when it is done.
- Re-estimate the remaining slices after each release, using actual effort.
- Change scope through the parity table in section 3, so the coverage figure
  stays honest.
