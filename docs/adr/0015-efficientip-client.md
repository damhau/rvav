# ADR-0015: The EfficientIP client follows EfficientIP's own library

- Status: accepted
- Date: 2026-10-27

## Context
S7 ([implementation plan](../implementation-plan.md#s7-efficientip-integration))
replaces the fake IPAM with SOLIDserver. No SOLIDserver is available yet, and
slice 1's client guessed its endpoints. [integrations.md](../integrations.md)
assumed the `/rest/` API was legacy.

Two Python libraries show the API as used today, and agree:
[SOLIDserverRest](https://pypi.org/project/SOLIDserverRest/) 2.14.0,
EfficientIP's own, with an API reference generated from SOLIDserver 9.0.1a;
and [eip-pydantic](https://pypi.org/project/eip-pydantic/) 0.0.1 (July
2026). Both call services under `/rest/` and the free-address search under
`/rpc/`. EfficientIP also describes an object-style API
(`<module>/<object>/<action>`, JSON bodies); neither library uses it.

## Decision

### Calls
| Need | Call |
|---|---|
| Find a subnet by ID, or by start address among terminal subnets | `GET /rest/ip_block_subnet_list` with `WHERE` |
| Next free addresses | `OPTIONS /rpc/ip_find_free_address?subnet_id&max_find`; 204 when full |
| Reserve | `POST /rest/ip_add?site_id&hostaddr&name&ip_class_parameters&add_flag=new_only` |
| Find an address | `GET /rest/ip_address_list` with `WHERE` |
| Rename an address | `PUT /rest/ip_add?ip_id&name&add_flag=edit_only` |
| Release | `DELETE /rest/ip_delete?ip_id` |
| Create a record | `POST /rest/dns_rr_add?rr_name&rr_type&value1&dns_name[&dnsview_name][&dnszone_name]&add_flag=new_only` |
| Change a record's value | `PUT /rest/dns_rr_add?rr_id&value1&add_flag=edit_only` (the library's `dns_rr_update`) |
| Find records, check a name | `GET /rest/dns_rr_list` with `WHERE` |
| Delete a record | `DELETE /rest/dns_rr_delete?rr_id` |

Parameters go in the query string; results are a JSON array of objects whose
fields are strings; 204 is an empty result; errors carry `errno` and
`errmsg`. A 429 is retried after its `Retry-After`, three times at most.

### Signing in
`RVAV_EFFICIENTIP_AUTH`: `native` (the default: user name and password,
base64, in `X-IPM-Username` and `X-IPM-Password`), `basic`, or `token` (an
API token: `Authorization: SDS <key id>:<signature>` and `X-SDS-TS`, the
signature a SHA3-256 of the secret, the time, the method and the URL).

### Safe to repeat
Workflow activities are retried, so every call can run twice:
- Reserving first looks for an address with the hostname in the subnet,
  and takes it back only when rvav reserved it for the same deployment
  (class parameter `rvav_owner`); an address of that name reserved by
  anyone else fails the step. An address taken by someone else between the
  search and the add is skipped.
- Creating a record adopts one with the same name, type and value, which
  also covers SOLIDserver creating records itself from the address. A
  record pointing elsewhere is refused.
- Releasing and deleting succeed when the object is already gone. An
  address whose ID rvav doesn't know is found by IP and released only when
  it still has the name rvav gave it.
- SOLIDserver keeps a deleted DNS record listed, with
  `delayed_delete_time` set, until the DNS server has dropped it (the
  library waits on it). Such records count as gone everywhere: name checks,
  adopting a record, the reconcile report.
- A rename adds the new A record before deleting the old one, and points
  the PTR record to the new name in place; the new name is checked again
  just before the rename, since an approval may have taken days.

### What rvav needs to know
- Subnets are found by their EfficientIP ID when known, or else by start
  address and size, within `RVAV_EFFICIENTIP_SPACE` when the same CIDR
  exists in several spaces. The subnet gives the space an address is added
  to.
- Records are created on `RVAV_EFFICIENTIP_DNS_SERVER` (a server or smart
  architecture), in `RVAV_EFFICIENTIP_DNS_VIEW` when set. A records name
  their zone; PTR records let SOLIDserver find the reverse zone.
- Every address and record rvav creates has the class parameter
  `rvav_managed=1`; addresses also have `rvav_owner` (the deployment).
- SOLIDserver knows nothing of the `gateway` class parameter, so it may
  offer the gateway as free when it isn't registered: rvav never reserves
  the gateway, the network or the broadcast address.
- A subnet ID rvav has stored is used only while that subnet still has the
  mapping's range; otherwise the subnet is looked up by its range again.
- Redirects are not followed (the sign-in headers would go along), and a
  plain `http://` URL is logged as a warning.

### Network mappings
The sync service reads the terminal subnets every
`RVAV_IPAM_SYNC_INTERVAL` (5 minutes). A subnet maps the vSphere networks
named in its class parameter `vsphere_network` (several, separated by
commas), with `gateway`, `domain` (the DNS zone) and `dns_servers`. A
subnet without a gateway or a zone is left out, and so is a network two
subnets claim; both are logged. Mappings an administrator set
(`source = manual`: the seeded ones, and the placement screens from S9)
win, and the sync only fills in their subnet's ID. A synced mapping whose
subnet lost the parameter is removed. A network whose subnet has a problem
(no gateway or zone, a second subnet claiming it) keeps its mapping until
it is fixed, and an answer that maps no network at all changes nothing:
both more likely come from a mistake or a permission change than from the
network. Lists are read page by page until a page comes back empty.

### Names and pickers
A VM name is free when vCenter has no VM of that name, DNS has no A, AAAA
or CNAME record for it in the subnet's zone, and no IPAM address has it as
its name. The name preview and
submit skip taken names; when EfficientIP can't be asked, the preview
carries on and submit is refused. Forms can use `ipam.subnets` (the
mapped subnets, of one network with the `network` parameter) and
`dns.zones` (the forward zones of rvav's DNS server); a value picked
from `dns.zones` is checked against those zones on submit.

### Reconcile report
`GET /api/v1/admin/ipam/reconcile` (administrators; also `rvav
ipam-reconcile`, and the Integrations page) lists addresses and records
with `rvav_managed=1` that no deployment owns, and addresses and records a
deployment owns that EfficientIP no longer has (looked up by ID, since an
adopted record has no tag). It changes nothing: deleting an orphan is a
person's decision. The tagged objects are found with `like` on
`ip_class_parameters` and `rr_class_parameters` and checked again on
rvav's side. Two rvav instances sharing a SOLIDserver list each other's
objects as orphans.

### The fake is a SOLIDserver
`efficientip.FakeServer` answers the same calls over HTTP, with their
mandatory parameters, `add_flag` rules and result shapes, case-sensitive
`WHERE` comparisons, delayed DNS deletes, and no idea of the gateway.
Tests and the development stack use the real client against it.

## Consequences
- What only a real SOLIDserver can confirm, in order of risk: whether
  addresses accept free class parameters (`rvav_managed`, `rvav_owner`)
  without a class; the status
  codes and `errno` values of refusals; whether free addresses honour pools
  and reserved ranges; whether DNS records are created on the smart
  architecture or on each server; whether the class parameter `rvav_managed` is
  accepted on the classes used for addresses and records; and the exact
  `WHERE` syntax on these fields (`like` on the class parameters
  especially; eip-pydantic filters on `tag_ip_<name>` with `TAGS` instead). All of it is in `client.go`; the rest of
  rvav only sees the `Client` interface.
- The class parameter names are fixed for now; they become settings once
  the real SOLIDserver's classes are known.
- Moving to the object-style API later stays inside the client.
