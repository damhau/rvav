# R1 Pilot: report for the go/no-go

For the product owner's decision on R1 (slices S6 to S9), as the
[implementation plan](implementation-plan.md) asks of each release. The
demo script is [demo-r1.md](demo-r1.md).

## In short

R1 is built and accepted on the lab stack: AWX (S6), EfficientIP (S7),
catalog administration (S8), and projects, placement and governance (S9).
A new project can be set up from the UI and request a VM without SQL or
YAML, and catalog admins build and publish items without a developer.

What R1 promises beyond the lab ("real AWX and EfficientIP") is verified
only in part. AWX was accepted against a test AWX 24.6.1, not the
organisation's. EfficientIP runs against a fake SOLIDserver that speaks the
same API as the real one. vCenter is the simulator (vcsim), and sign-in is
Keycloak, not the corporate identity provider. The five issues of the
[Lab acceptance milestone](https://github.com/damhau/rvav/milestone/9)
close that gap; none needs new code that is known today.

**Recommendation:** go for installing the pilot environment and running
the Lab acceptance there. No-go for pilot users until the lab vCenter
(#76), a real SOLIDserver (#78), the corporate identity provider (#79) and
the organisation's AWX (#80) have passed. The web console (#77) doesn't
block: the VMRC link works without it.

## What R1 delivers

| Slice | Built | Accepted |
|---|---|---|
| S6 AWX | AWX templates with the catalog label become catalog items, their surveys forms, a changed survey flagged ([ADR-0016](adr/0016-awx-catalog.md)); AWX reads rvav's VMs through the `rvav.platform` inventory plugin ([ADR-0017](adr/0017-awx-inventory.md)); templates with the `day2` label become day-2 actions ([ADR-0018](adr/0018-awx-day2-actions.md)); `awx.inventories` and `awx.credentials` pickers | On a test AWX 24.6.1 with the compose stack: inventory synced, post-configuration and a day-2 action run on rvav's inventory, a template requested with its survey, a changed survey flagged ([progress-r1.md](progress-r1.md) S6.6) |
| S7 EfficientIP | The client follows EfficientIP's own library ([ADR-0015](adr/0015-efficientip-client.md)); addresses and A/PTR records per request; network mappings from subnet class parameters; renames reach EfficientIP; a reconcile report; each rvav instance tags its own objects (`RVAV_INSTANCE_ID`) | Against the fake SOLIDserver only; a real one is #78 |
| S8 Catalog administration | Items and versions (draft, publish, roll back), entitlements, YAML export and import, a form designer with a live preview as a chosen user, REST and CEL value sources, secrets encrypted in PostgreSQL ([ADR-0019](adr/0019-catalog-administration.md)) | On the compose stack in the browser: an Ubuntu item with a REST dropdown built, previewed, published, requested, changed and rolled back ([milestone S8](https://github.com/damhau/rvav/milestone/1)) |
| S9 Projects, placement and governance | Projects, members and OIDC group mappings, environments; placement by name patterns and vCenter tags with a preview; templates, sizes and naming policies offered per project and environment; quotas, lease, approval and day-2 policies edited and explained; vCenter, AWX and EfficientIP settings from the UI; API tokens; the audit log ([ADR-0020](adr/0020-administration.md)) | On the compose stack in the browser: a project set up from the UI requests a VM, named by its policy, placed by its rule, stopped by its quota ([milestone S9](https://github.com/damhau/rvav/milestone/2)) |

## How it was verified

- `make check` on every commit, and CI on every push to `main`: spec lint
  and generated code, Go vet and tests, end-to-end tests on PostgreSQL with
  vcsim (through Temporal's test environment and a real Temporal dev
  server), the acceptance scenarios of S5, S8 and S9, the Ansible inventory
  plugin, and the web app's typecheck, lint, unit tests, build and 29
  Playwright flows on mock data (what project admins and auditors see
  included).
- The Docker Compose stack, locally (CI doesn't run it): migrations up to
  00018, `deploy/smoke.sh`, and `pnpm e2e:stack`'s six browser flows with
  Keycloak sign-in (request to Ready and power off, an approval with a
  changed size, sign-in and roles from Keycloak groups for two users, the
  S8 catalog flow, the S9 project setup); the three that don't need
  Keycloak also with dev authentication, after the review fixes.
- Each slice ended with an independent review of its diff; its findings
  were fixed or answered in the slice's last issue. S9's review (backend
  and web, separately) found no high-severity issue. Its medium findings
  were fixed in 89da0fc:
  - test connection could send the saved vCenter, AWX or EfficientIP
    credentials to any URL;
  - an administrator who was also a project member lost their admin rights
    there;
  - unknown values made full-access API tokens or approval policies that
    never applied;
  - two counters sharing a name prefix could give one name twice;
  - a failed quota load could lead to wiping the quotas.

  The low findings were fixed with them ([#22](https://github.com/damhau/rvav/issues/22)).

## Still needs real infrastructure

| Issue | Needs | Blocks pilot users |
|---|---|---|
| [#76](https://github.com/damhau/rvav/issues/76) First run against the lab vCenter | A vCenter with a real template, customisation spec, network and an account; then guest operations with VMware Tools and hot add (S2, S4) | Yes |
| [#78](https://github.com/damhau/rvav/issues/78) A real SOLIDserver | A test subnet and DNS zone and an account limited to them; then addresses, records, deletion and the reconcile report (S7) | Yes |
| [#79](https://github.com/damhau/rvav/issues/79) Corporate identity provider | An OIDC client and test users in groups; then sign-in, groups to roles, sign-out | Yes |
| [#80](https://github.com/damhau/rvav/issues/80) The organisation's AWX | Its AWX (version, execution environment) and an organisation for rvav; then the contract tests, inventory source, post-configuration and a day-2 action | Yes |
| [#77](https://github.com/damhau/rvav/issues/77) Web console | The ESXi hosts' firewall openings and VMware's WMKS client | No: the VMRC link works |

## Risks and limitations

- **Hosting.** Production is planned on Kubernetes with Helm
  ([ADR-0009](adr/0009-hosting.md)); the chart comes with S14. Until then
  the pilot would run the Docker Compose stack on a VM.
- **Settings apply on restart.** vCenter, AWX and EfficientIP settings
  saved from the UI take effect when rvav's processes restart; the page
  says so. An environment variable always wins over a saved setting.
- **User identity follows the provider's subject.** On the lab stack,
  re-importing Keycloak's realm gave users new subjects, and rvav made new
  user records with the same names and emails (the people picker now marks
  the signed-in person). rvav links a sign-in by email only to a user the
  seed pre-created, which is the safe rule; with the corporate provider,
  subjects must stay stable, and there is no merge of user records yet.
  People appear in the member picker once they have signed in; before
  that, an OIDC group mapping gives them their role.
- **What the simulator can't show.** Guest operations with VMware Tools,
  CPU and memory hot add, and the web console through the ESXi hosts are
  verified on vcsim and fakes only (#76, #77).
- **Long-lived test stacks fill up.** Stack tests leave their VMs; on a
  stack used for weeks, a project's quota needs raising from the
  administration pages.

## Decisions for the product owner

1. Go or no-go, as recommended above.
2. Pilot hosting: the Compose stack on a VM now, or wait for the Helm chart
   (S14).
3. The pilot project, its environments (dev and tst, per the plan), and
   who on its team takes part.
4. Who provides the lab vCenter account, the SOLIDserver test subnet and
   account, the OIDC client and the AWX organisation, so the Lab
   acceptance can run.

## After R1

R2 (slices S10 to S14: extensibility, notifications, advanced
provisioning, inventory and reporting, production readiness) is planned in
[GitHub milestones](https://github.com/damhau/rvav/milestones), issue by
issue; the [roadmap](roadmap.md) has the releases.
