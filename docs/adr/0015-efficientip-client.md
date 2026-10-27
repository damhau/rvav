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
| Release | `DELETE /rest/ip_delete?ip_id` |
| Create a record | `POST /rest/dns_rr_add?rr_name&rr_type&value1&dns_name[&dnsview_name][&dnszone_name]&add_flag=new_only` |
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
- Reserving first looks for an address with the hostname in the subnet.
  An address taken by someone else between the search and the add is
  skipped.
- Creating a record adopts one with the same name, type and value, which
  also covers SOLIDserver creating records itself from the address. A
  record pointing elsewhere is refused.
- Releasing and deleting succeed when the object is already gone.

### What rvav needs to know
- Subnets are found by their EfficientIP ID when known, or else by start
  address and size, within `RVAV_EFFICIENTIP_SPACE` when the same CIDR
  exists in several spaces. The subnet gives the space an address is added
  to.
- Records are created on `RVAV_EFFICIENTIP_DNS_SERVER` (a server or smart
  architecture), in `RVAV_EFFICIENTIP_DNS_VIEW` when set. A records name
  their zone; PTR records let SOLIDserver find the reverse zone.
- Every address and record rvav creates has the class parameter
  `rvav_managed=1`.

### The fake is a SOLIDserver
`efficientip.FakeServer` answers the same calls over HTTP, with their
mandatory parameters, `add_flag` rules and result shapes. Tests and the
development stack use the real client against it.

## Consequences
- What only a real SOLIDserver can confirm, in order of risk: the status
  codes and `errno` values of refusals; whether free addresses honour pools
  and reserved ranges; whether DNS records are created on the smart
  architecture or on each server; whether the class `rvav_managed` is
  accepted on the classes used for addresses and records; and the exact
  `WHERE` syntax on these fields. All of it is in `client.go`; the rest of
  rvav only sees the `Client` interface.
- Moving to the object-style API later stays inside the client.
