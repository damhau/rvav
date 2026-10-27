# R1 progress

Working file for building release R1 (slices S6 to S9 of the
[implementation plan](implementation-plan.md)), the same way as
[R0](progress.md): each item ends with a commit on `main`, and a slice is
done when every item is checked, the slice's definition of done holds, and
an independent review of the slice diff has been addressed.

Status: `[ ]` to do, `[~]` in progress, `[x]` done, `[-]` needs real
infrastructure (see the last section).

The gate for every item and how tests run are as in [R0](progress.md#gate-for-every-item).

## S7 EfficientIP integration

No SOLIDserver is available yet, so the client follows EfficientIP's own
Python library, [SOLIDserverRest](https://pypi.org/project/SOLIDserverRest/)
2.14.0, and the API reference it ships (generated from SOLIDserver 9.0.1a).
The fake becomes an HTTP server speaking the same API, so every test and the
compose stack run the real client.

- [x] S7.1 Client on the documented API ([ADR-0015](adr/0015-efficientip-client.md)): calls, methods and parameters as in SOLIDserverRest, confirmed by a second library (eip-pydantic, July 2026) that uses the same `/rest/` and `/rpc/` services, so the integrations doc's "legacy `/rest/`" was wrong and is corrected; native, basic or token authentication; 204 empty, 401, 429 retried after `Retry-After`, `errno` and `errmsg`; reserving looks for the hostname first and skips an address taken since the search, records with the same value are adopted, releases and deletes accept objects already gone; subnets found by CIDR (and space) when their ID is not known; addresses and records tagged `rvav_managed=1`; addresses named with the VM's FQDN. `FakeServer` answers the same calls over HTTP and the client's tests check the exact calls sent
- [x] S7.2 The real client everywhere: `RVAV_FAKE_EFFICIENTIP=1` runs a `FakeServer` in the process, the end-to-end tests run one per test, and the compose stack has an `efficientip` service (`rvav fake-efficientip`) that api, worker and sync share, so the API's DNS name checks now see the records the worker created. The fake holds the subnets of the network mappings (with their class parameters) and never offers an address rvav has recorded. The old in-memory fake is gone
- [x] S7.3 Network mappings from subnet class parameters (`vsphere_network`, one or several, `gateway`, `domain`, `dns_servers`), read by the `sync` service every `RVAV_IPAM_SYNC_INTERVAL` (5 minutes; migration 00007 adds `source`, `subnet_name`, `synced_at`): mappings set by hand win and get their subnet's ID, subnets without a gateway or zone and networks claimed twice are logged and left out, synced mappings whose subnet lost the parameter go, an empty answer changes nothing. Pickers `ipam.subnets` (mapped subnets, optionally of one network) and `dns.zones` (forward zones of rvav's DNS server; the DNS record item uses it). VM names are skipped when DNS has the name in the subnet's zone, in the name preview (which now depends on the network) and on submit; submit is refused when EfficientIP can't be asked. Editing mappings by hand comes with the placement screens (S9)
- [x] S7.4 Rename also renames the address in EfficientIP (`PUT ip_add` with `add_flag=edit_only`), a step of its own before the DNS records; an address EfficientIP no longer has fails the step
- [x] S7.5 Reconcile report: addresses and records tagged `rvav_managed=1` that no deployment owns, and what deployments own that EfficientIP no longer has (checked by ID, so adopted records count), from `GET /api/v1/admin/ipam/reconcile` (administrators), `rvav ipam-reconcile` (JSON) and the admin Integrations page (run on demand, nothing changed). Mock handler and Playwright test
- [ ] S7.6 Slice review, docs and roadmap status
- [-] S7.x Acceptance against a real SOLIDserver

## S6 AWX integration

The user's test AWX (24.6.1) runs on this machine; rvav works in its own
organisation `rvav-test` there and leaves the rest alone. Its playbooks and
the inventory plugin come from this repository (public on GitHub). The VMs
are vcsim's, so test playbooks run on the execution environment
(`connection: local`) against the hosts the rvav inventory gives.

- [x] S6.1 AWX test content ([deploy/awx](../deploy/awx/README.md): playbooks, templates with surveys among them `install-monitoring-agent`, a workflow), set up in `rvav-test` by `rvav awx-bootstrap` (repeatable, syncs the project from GitHub). Client: templates by organisation and label, surveys, workflow nodes, AWX's error messages; lookups kept to `RVAV_AWX_ORGANIZATION`; workflow jobs followed node by node (they used to be read as playbook jobs). Contract tests against the test AWX (opt-in, `RVAV_AWX_TEST_URL`) pass: templates and labels, surveys, launch with limit and variables, events, a failed task's message, a refused survey answer, cancel, a workflow and its nodes. Found: AWX can't filter workflow templates on labels; a survey question needs a default. The fake plays workflows (the seeded firewall-flow item now runs; `TestAWXWorkflowItem`)
- [x] S6.2 Content source ([ADR-0016](adr/0016-awx-catalog.md)): templates of `RVAV_AWX_ORGANIZATION` with a label of `RVAV_AWX_CATALOG_LABELS` (`catalog`), synced by the `sync` service every `RVAV_AWX_SYNC_INTERVAL` (5 minutes) or at once by an admin; new ones become published items open to every project, with forms from their surveys (text, numbers, choices, multiselect, required, defaults; passwords left out), or are linked to the AWX item already launching them (the seeded ones keep their forms); a changed survey is flagged with a line per change, launch settings that break rvav's launch are warnings, templates no longer chosen are flagged and their items stay (migration 00008). Admin API and the Catalog items page; mock and Playwright test
- [ ] S6.3 Dynamic inventory `GET /api/v1/ansible/inventory` (read-only API token): VMs ready for configuration, groups by project, environment, item and OS, rvav host variables; the `rvav.platform` collection (inventory plugin and credential type) in `ansible/`, tested with ansible-core; not published (outward-facing)
- [ ] S6.4 Custom day-2 actions backed by AWX templates, bound to resource types and a CEL condition, their form from the survey, running against the rvav inventory with `limit`
- [ ] S6.5 Value sources `awx.inventories` and `awx.credentials`
- [ ] S6.6 Acceptance on the test AWX: an imported template requested with its survey as a form; "Install monitoring agent" as a day-2 action on a VM through the rvav inventory; a changed survey flagged
- [ ] S6.7 Slice review, docs and roadmap status

## S8 Catalog administration

- [ ] S8.1 to be broken down

## S9 Projects, placement and governance

- [ ] S9.1 to be broken down

## Decisions taken

| Decision | Choice | Where |
|---|---|---|
| AWX in the catalog | Templates with the catalog label are published to every project; surveys become forms; a changed survey is flagged, the form stays | ADR-0016 |
| EfficientIP API | The `/rest/` and `/rpc/` services, as EfficientIP's SOLIDserverRest library calls them; the fake is an HTTP SOLIDserver | ADR-0015 |

## Needs real infrastructure

| Item | Needs |
|---|---|
| S7 acceptance | A SOLIDserver (trial appliance or a non-production one) with a test subnet and DNS zone, and an account limited to them |
