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
- [ ] S7.5 Reconcile report: addresses and records tagged by rvav that no deployment owns, and rvav records missing in SOLIDserver (API, `rvav ipam-reconcile`, admin page)
- [ ] S7.6 Slice review, docs and roadmap status
- [-] S7.x Acceptance against a real SOLIDserver

## S6 AWX integration

The user's test AWX (24.6.1) runs on this machine; rvav works in its own
organisation `rvav-test` there.

- [ ] S6.1 to be broken down when S7 is done

## S8 Catalog administration

- [ ] S8.1 to be broken down

## S9 Projects, placement and governance

- [ ] S9.1 to be broken down

## Decisions taken

| Decision | Choice | Where |
|---|---|---|
| EfficientIP API | The `/rest/` and `/rpc/` services, as EfficientIP's SOLIDserverRest library calls them; the fake is an HTTP SOLIDserver | ADR-0015 |

## Needs real infrastructure

| Item | Needs |
|---|---|
| S7 acceptance | A SOLIDserver (trial appliance or a non-production one) with a test subnet and DNS zone, and an account limited to them |
