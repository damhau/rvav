# ADR-0065: ServiceNow

- Status: accepted
- Date: 2026-11-22
- Builds on the integrations and trusted certificates of
  [ADR-0020](0020-administration.md) and
  [ADR-0030](0030-settings-and-local-administrator.md), the events of
  [ADR-0021](0021-event-subscriptions.md) and
  [ADR-0049](0049-event-topics.md), the entitlements of
  [ADR-0019](0019-catalog-administration.md) and the audit log of
  [ADR-0058](0058-audit-forwarding-and-sign-ins.md). I1.5 (#178) of the parity
  programme, row D27 of the parity table.

## Context
vRA sites run ServiceNow next to it, through VMware's "Aria Automation
plug-in for ITSM": a scoped application that imports vRA's catalog into
the Service Portal, submits each order to vRA as the person who ordered
it, follows the deployment there, and keeps the deployments and their
machines in tables of its own (refreshed by a daily job). Sites that
wanted VMs in the CMDB itself wrote an extensibility subscription on
`compute.provision.post` that made a `cmdb_ci_vmware_instance` record
through the Table API, and another on `compute.removal.post` that
retired it.

rvav could reach ServiceNow only through generic REST steps, polls,
webhooks and AWX: no connection of its own, no catalog in ServiceNow and
no CMDB records.

## Decision

### The connection
The integration `servicenow` (Administration › Integrations ›
ServiceNow, or `RVAV_SERVICENOW_*`) names the instance's URL, **https
only** (a plain `http://` URL is refused when saved, and by the client),
and how rvav signs in:

- `basic`: a ServiceNow user and its password, for an integration user
  with "Web service access only" ticked;
- `oauth`: an OAuth client of the instance (Application Registry, "OAuth
  API endpoint for external clients"), by the **client credentials**
  grant at `/oauth_token.do`, which ServiceNow offers for the REST APIs
  from its Washington DC release on, the client's "OAuth application
  user" being the integration user. The access token is kept until it
  expires, and asked for again once when ServiceNow refuses it.

The secret, the password or the client's secret, is sealed under
`integration:servicenow` as other integrations' are, and goes only to
the instance it was given for: a new URL, a pin or TLS checks turned off
need it again. The certificate is checked as every connection's: the
system's CAs, the CA bundle, a pin (`cert_sha256`) or `insecure`; the
test shows it. The test signs in and reads one requested item
(`sc_req_item`), the table rvav writes the progress of requests to.
Settings are read on next use: the sync process makes a client for each
pass, and the API for each call.

rvav calls ServiceNow with `net/http`: there is no maintained Go client of
its REST APIs, and rvav needs three calls of two APIs (the Table API's
read and update, and the IRE's one call).

### VMs as CIs
With `cmdb` on, each VM of a deployment of the projects chosen
(`projects`: project IDs; none means every project) is a configuration
item of the class `cmdb_class`, `cmdb_ci_vmware_instance` by default:
the class vCenter discovery uses, whose identification rules ServiceNow
ships. rvav sends it through the **Identification and Reconciliation
API** (`POST /api/now/identifyreconcile`), with `data_source`, `rvav` by
default, as its discovery source: the IRE applies the class's
identification rules (the instance UUID, else the managed object
reference and the vCenter's UUID), so a CI that discovery made already
is updated rather than doubled, and the reconciliation rules decide
which source wins each field. Sending the same VM twice updates the same
CI: there is never a second one.

The values: `name`, `vm_instance_uuid`, `object_id` (the moref),
`vcenter_uuid` (the vCenter's instance UUID, once the inventory sync has
read it), `ip_address`, `fqdn`, `cpus`, `memory` (MB), `disks`,
`disks_size` (GB), `guest_os_fullname`, `state` (`on` or `off`),
`install_status` 1 (installed), `operational_status` 1, 2 when the VM
is missing from vCenter, `correlation_id` (rvav's resource ID) and a
`short_description` naming the deployment, its project and its owner
(`rvav: deployment web-dev-app001 (dep_…) in Web Platform, owned by
Thu Nguyen`). `owned_by` is the ServiceNow user whose email is the
owner's, when there is exactly one. Which vCenter is in the
description's `vcenter_uuid`; a relation to the vCenter's CI is left to
discovery.

**When.** rvav works from its own events: the dispatcher, in the
transaction that marks a batch of events dispatched, queues the
deployment of each VM and request event for ServiceNow
(`servicenow_cmdb_queue`), so no event is missed and none is read twice.
The sync process (`rvav sync`) takes the queue every few seconds, under
an advisory lock of its own so that one process works it, and for each
deployment compares what each VM's CI should hold with what rvav last
sent (`servicenow_cis`): only a VM whose values changed is sent. A
deployment ServiceNow didn't take is tried again with backoff, from 10
seconds to 15 minutes; the queue keeps the last error. A built VM, a
day-2 resize or a reconfiguration, a power change and a drift the
inventory sync found all change the values.

**Retired, never deleted.** A CI rvav made whose VM is gone (deleted
with its deployment, scaled in, removed by an update) is set to
`install_status` 7 (retired) and `operational_status` 6 through the
Table API; its row stays in `servicenow_cis`, retired. ServiceNow's own
retention rules delete it if the site has any.

**Reconcile.** Every `sync_interval` (30 minutes by default) rvav queues
every deployment in scope and every deployment with a CI it keeps, which
catches what an event couldn't (a CI made while ServiceNow was down for
longer than the backoff, a VM changed in vCenter), and reads the CIs
ServiceNow has of its data source that are not retired: those rvav
doesn't keep are reported as CIs rvav no longer has (a VM deleted while
the integration was off, a CI copied by hand). The report, with the
counts and the errors, is on the integration's page and in the API
(`GET /admin/integrations/servicenow/cmdb`). rvav doesn't retire what the
report finds: someone decides.

A project taken out of scope keeps its CIs as they are: rvav no longer
updates them, but still retires a CI it made when its VM goes. Turning
`cmdb` off stops everything, and retires nothing.

### Requests from ServiceNow
ServiceNow calls rvav with an rvav API token that only ServiceNow's
endpoints take, under `/api/v1/servicenow/`:

- `GET /servicenow/catalog`: every item with a published version shared
  in some project, with its inputs, for ServiceNow's import;
- `GET /servicenow/entitlements?user=`: the projects, environments and
  items a user may request in, which ServiceNow's catalog form offers;
- `POST /servicenow/requests`: a request on behalf of a user, for a
  requested item (its number and `sys_id`);
- `GET /servicenow/requests/{ritmSysId}`: the request made for a
  requested item, and where it is.

**The token.** rvav makes it on the integration's page (Make token), for
a user of its own, `usr_rvav-servicenow` (`system:servicenow`, "ServiceNow
(rvav)"), that no one signs in as and that holds no role. The token's
scope is `servicenow`: such a token is refused everywhere but under
`/servicenow/`, and only such a token is taken there (any other token or
session is answered 403). The permission it carries,
`servicenow.request`, is not in the permission catalogue: no role,
built-in or custom, can grant it, not even the administrator's, and
rvav gives it only to a token of that scope held by that user. The token
is shown once, valid a year; making a new one revokes the earlier, and
Revoke cuts ServiceNow off. Making and revoking are audited.

**On whose behalf.** A request names its user by `email`, or by
`user_name` (ServiceNow's `sys_user.user_name`). rvav looks the email up
among its users, whatever the case; a user name is taken as the part
before the `@` of exactly one user's email (two match: refused as
ambiguous). The user must have signed in to rvav at least once, and must
not be the local administrator or one of rvav's own users. The request
is then the user's own: rvav builds the user's principal from the roles
and the groups of their last sign-in, and submits as they would, with
their entitlements, quotas, approval policies, lease rules and soft
policies' warnings (which a request from ServiceNow can only accept by
saying so, `accept_warnings`). Nothing the integration holds is added:
an item the user can't request in that project answers 404, as it would
to them.

The request keeps the requested item's number and `sys_id`
(`servicenow_requests`), its API shows them (`servicenow` on `Request`
and on the deployment it made, with a link to the record), and a second
submission for the same `sys_id` answers the request already made (200)
rather than making another. The audit log has
`servicenow.request`: the actor is ServiceNow's user, the target the
request, the detail the user it was made for (ID, name, email), the
email or user name ServiceNow gave, the requested item, the token and
where the call came from. A refusal (no such user, an item not
entitled, a policy) is audited as `servicenow.request_refused` with why.

**Progress back.** The dispatcher, in the same transaction as for the
CMDB, queues a work note for each event of a request made from
ServiceNow (`servicenow_ritm_notes`): submitted, awaiting approval,
approved or rejected, built or failed, and the deployment's link. The
sync process sends them in order through the Table API, one PATCH of
`sc_req_item` each with its `work_notes` and `state`: 2 (work in
progress) while rvav works, 3 (closed complete) when it succeeded, 4
(closed incomplete) when it failed, was rejected or cancelled. A note
ServiceNow refuses is tried again with backoff, and the ones after it
wait, so the record reads in order.

### Threat model
- **A stolen token** can request, as any user rvav knows, what that user
  may request, and read the catalog and users' entitlements; nothing
  else in rvav. Against it: the token is refused outside `/servicenow/`;
  `allowed_sources` (the instance's outbound addresses, or the MID
  server's) refuses calls from elsewhere, read after the trusted
  proxies; submissions are limited to `requests_per_minute` (30 by
  default) across rvav's API processes, counted in the database, and
  reads to the same rate per process; every request made is audited
  with the token and the address; the token expires in a year, and
  Revoke ends it at once.
- **A forged user name.** rvav believes the user ServiceNow names: it
  can't check who ordered in ServiceNow. ServiceNow's side takes the
  user from the requested item's `requested_for`, which its own ACLs
  guard, and never from a variable the user fills. The user's
  entitlements and approvals still apply: a forged name gets what that
  user could request, with that user's approvers asked, who see the
  request in rvav as the user's, made via ServiceNow. Matching on email
  first, and refusing an ambiguous user name, keeps one ServiceNow user
  from being taken for another rvav user.
- **Rate limits** answer 429 with `Retry-After`, audited once a minute
  at most, so a flood doesn't fill the log.
- **What the audit shows**: who the request was for, what ServiceNow
  said (the email or user name), which requested item, which token,
  which address and user agent; refusals likewise.
- **rvav's account in ServiceNow** holds least privilege: read and write
  on the CMDB class and read on `sys_user` (for `owned_by`), write on
  `sc_req_item`'s `state` and `work_notes`, and the IRE's role
  (`import_transformer`, or `itil` with the IRE's ACL). It needs no
  admin role and no access to anything else.

### ServiceNow's side
`integrations/servicenow/` holds scripts for the instance, not an update
set: a script include (`RvavClient`) that calls rvav through a REST
message with the token in a Connection & Credential alias, a scheduled
job that imports rvav's catalog as catalog items (each keeps its rvav
item ID in its `meta`), and a business rule on `sc_req_item` that
submits each ordered item to rvav on behalf of its `requested_for`, with
the install steps. An update set would be one XML file to import, but we
can't build and check one without an instance: the scripts and the steps
are what we can stand behind, and I1.6's acceptance checks them against a
personal developer instance.

### Events
No new topic: the CMDB and the work notes follow the events rvav already
writes (ADR-0049). Nothing changes in the workflows either.

## Consequences
- Two integrations' worth of work in the sync process: a queue table,
  an advisory lock (`LockServiceNowSync`), a pass every few seconds.
  Its pool is fine since #195: the lock is held on a connection outside
  it.
- The CMDB depends on the IRE: an instance where the class's
  identification rule is changed matches as its rule says. rvav keeps
  the `sys_id` the IRE answered, and retires that one.
- `data_source` must be one of the instance's discovery sources
  (`discovery_source` choices on `cmdb_ci`): the install guide adds
  `rvav`. Until then the IRE refuses, and the queue says why.
- The requested items rvav updates must be readable and writable by its
  account: a site whose ACLs restrict `sc_req_item` gives the role
  access to the items of rvav's catalog items.
- Requests made from ServiceNow can't upload files, and inputs the import
  can't map (objects, arrays, secrets) are left out of the ServiceNow
  form: an item that needs one is requested in rvav.
- I1.6's acceptance checks, against a real instance: the CI class and
  the IRE's matching of a VM discovery found first, the catalog import
  and a request on behalf of a user, the work notes and states on the
  requested item, and that the roles listed are enough.
