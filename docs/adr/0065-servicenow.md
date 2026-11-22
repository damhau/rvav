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
- Revised in I1.6 (#179), after I1's review: ServiceNow sends who placed
  the order as well as who it's for, checked as in rvav; a user name is
  a user's whole user name; a sign-in too old to go by is refused; a
  rejection closes the requested item.

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
(`projects`: project IDs or codes; none means every project) is a configuration
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
`disks_size` (GB), `guest_os_fullname`, `state` (`on`, `off` or `paused`),
`install_status` 1 (installed), `operational_status` 1, 2 when the VM
is missing from vCenter, `correlation_id` (rvav's resource ID) and a
`short_description` naming the deployment, its project and its owner
(`rvav: deployment web-dev-app001 (dep_…) in Web Platform, owned by
Thu Nguyen`). `owned_by` is the ServiceNow user whose email is the
owner's, when there is exactly one, looked up only for an email that
fits in an encoded query as it is (no `^`, `=`, or `javascript:`, which
ServiceNow would run). Which vCenter is in the
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
ServiceNow has of its data source that are not retired, in pages ordered
by `sys_id` (pages of a read with no order may repeat or skip rows):
those rvav
doesn't keep are reported as CIs rvav no longer has (a VM deleted while
the integration was off, a CI copied by hand). The report, with the
counts and the errors, is on the integration's page and in the API
(`GET /admin/integrations/servicenow/cmdb`; `POST
/admin/integrations/servicenow/cmdb/reconcile` has the next pass, within
seconds, reconcile). rvav doesn't retire what the report finds: someone
decides.

A project taken out of scope keeps its CIs as they are: rvav no longer
updates them, but still retires a CI it made when its VM goes. Turning
`cmdb` off stops everything, and retires nothing; turned on again, the
next pass reconciles, since events went unqueued meanwhile.

### Requests from ServiceNow
ServiceNow calls rvav with an rvav API token that only ServiceNow's
endpoints take, under `/api/v1/servicenow/`:

- `GET /servicenow/catalog`: every item with a published version shared
  in some project, with the inputs a ServiceNow variable can hold
  (strings, numbers, booleans, choices from a fixed list; `dynamic` when
  rvav finds the options at request time; secrets, files, lists and
  objects left out), for ServiceNow's import;
- `GET /servicenow/entitlements?user=`: the projects, environments and
  items a user may request in, which ServiceNow's catalog form offers;
- `POST /servicenow/requests`: a request on behalf of a user, for a
  requested item (its number and `sys_id`);
- `GET /servicenow/requests/{ritmSysId}`: the request made for a
  requested item, and where it is.

**The token.** rvav makes it on the integration's page (Make a token), for
a user of its own, `usr_rvav-servicenow` (`system:servicenow`, "ServiceNow
(rvav)"), that no one signs in as and that holds no role. The token's
scope is `servicenow`: such a token is refused everywhere but under
`/servicenow/`, and only such a token is taken there (any other token or
session is answered 403). The permission it carries,
`servicenow.request`, is not in the permission catalogue: no role,
built-in or custom, can grant it, not even the administrator's, and
rvav gives it only to a token of that scope held by that user. The token
is shown once, valid a year; making a new one revokes the earlier, and
Revoke cuts ServiceNow off (`GET`, `POST` and `DELETE
/admin/integrations/servicenow/token`, with `integrations.manage`).
Making and revoking are audited (`servicenow.token`,
`servicenow.token_revoked`).

**On whose behalf.** A request names its user in `user`: an email, or,
without an `@`, a user name (ServiceNow's `sys_user.user_name`). rvav
looks the email up among its users, whatever the case; a user name is
an rvav user's whole user name, whatever the case: the identity
provider's `preferred_username`, which rvav keeps at each sign-in
(`users.username`), and the seed's names. Two users with it are refused
as ambiguous; the part of an email before the `@` is never taken for a
user name, so that `thomas` is no one rather than whichever Thomas's
email starts so. No match is refused (422 on `/user`, 404 for the
entitlements). Never the local administrator or one of rvav's own users.
The request is then the user's own: rvav builds the user's principal from
their roles and the groups of their last sign-in (none for a user who
hasn't signed in yet, who then has their direct memberships only). Those
groups are refused once older than `RVAV_SCHEDULE_OWNER_IDLE_DAYS` (90 by
default), as a schedule's run is (ADR-0057): a user whose last sign-in,
or their creation for one who never signed in, is older is answered 422
on `/user` (`idle`), and the entitlements likewise, saying to sign in to
rvav once. A limit, rather than taking only recent groups, because a
request with fewer groups than the person has fails in ways that don't
say why, while a refusal does. Within the limit, rvav submits as they
would, with their entitlements, quotas, approval
policies, lease rules and soft policies' warnings (which a request from
ServiceNow can only accept by saying so, `accept_warnings`). Nothing the
integration holds is added: an item the user can't request in that
project, or a project they can't see, answers 404, as it would to them.
Inputs ServiceNow leaves out take the form's initial values (ADR-0055);
the default version is requested; the justification ServiceNow sends is
kept, followed by "Requested in ServiceNow, RITM…". A submission needs
the connection set (409 otherwise): rvav couldn't tell the requested
item how it goes.

The body is held to the API's schema: a field it doesn't list answers
400, and a `sys_id` that isn't 32 lower-case hexadecimal digits, a
number out of its pattern or a name longer than 254 characters 422,
before anything is looked up or kept, since a `sys_id` is stored and
named in the paths both sides build. A 409 says in `retry` whether it
passes: the connection not set yet, requests placed at the same moment
taking the room counted on, or a soft quota a request asked for at the
same moment crossed. ServiceNow keeps such an item open and orders it
again later; any other 409 is final and closes it.

**Who ordered.** A request also names in `ordered_by` who placed the
order, the requested item's `opened_by`, matched as `user` is (a
sign-in too old included, 422 on `/ordered_by`). When it's the same
person, nothing more. When it's someone else, they order for the user
as in rvav, where a request for someone else takes `request.on_behalf`
in the project (ADR-0034): rvav refuses unless the orderer is an rvav
user who holds it there (422 on `/ordered_by`, `not_allowed` or
`unknown_user`). The request stays the user's, with their entitlements
and quotas, as the requester; the orderer is kept with the requested
item (`servicenow_requests.ordered_by_id`, and the name ServiceNow gave),
shown as `ordered_by` on `servicenow` and in ServiceNow's answer, and
audited. Approval policies read `request.via` (`rvav`, `schedule` or
`servicenow`) and `request.ordered_by` (the orderer's ID, the
requester's own when they ordered). For an order placed for someone
else, a policy applies when its condition holds for the requester or
for the orderer (each as `user`), and approves automatically only when
its `auto_approve` holds for both: never on the requester's rights
alone, so that someone who may order for an infrastructure lead doesn't
get production VMs without approval through the lead's
`auto_approve`. We kept the user as the requester rather than make the
orderer the requester and the user the owner, as rvav's own form does,
because the order is the user's in ServiceNow: ServiceNow's approvals
and its catalog form's projects are the user's, and the requester's
manager is the one a level with `manager: true` asks.

The request keeps the requested item's number and `sys_id`
(`servicenow_requests`), its API shows them (`servicenow` on `Request`
and on the deployment it made, with a link to the record), and a second
submission for the same `sys_id` answers the request already made (200)
rather than making another. The audit log has
`servicenow.request`: the actor is ServiceNow's user, the target the
request, the detail `via: servicenow`, the user it was made for
(`on_behalf_of`: ID, name, email), who ordered it (`ordered_by`, the
same fields), the emails or user names ServiceNow gave (`user`,
`ordered_by_given`), the requested item, the token, and the caller's address and user
agent. A refusal (no such user, an item not entitled, a policy) is
audited as `servicenow.request_refused` with why. The request itself is
the user's: they are its requester, and its page says it came from
ServiceNow, with the requested item's number.

**Progress back.** The dispatcher, in the same transaction as for the
CMDB, queues a work note for each event of a request made from
ServiceNow (`servicenow_ritm_notes`): submitted, awaiting approval,
approved or rejected, built or failed, and the deployment's link. The
sync process sends them in order through the Table API, the note's
text written from the event when it is sent, one PATCH of
`sc_req_item` each with its `work_notes` and `state`: 2 (work in
progress) while rvav works, 3 (closed complete) when it succeeded, 4
(closed incomplete) when it failed, was cancelled or was rejected. rvav
writes no `provisioning.post` for a rejected request, so the rejection
that closes the item is the `approval.decided` of a policy's level whose
status is `rejected`: an approver's, or rvav's own when nobody can
approve or nobody answered in time, even after escalation. A custom
workflow's approval leaves the item to the workflow. A note ServiceNow
can't take is tried again with backoff, and the ones after it wait, so
the record reads in order; one it refuses with a 4xx (but 401, 404, 408
and 429) five times, about five minutes, is given up with its error
kept in `servicenow_ritm_notes.last_error`, so that a data policy or a
business rule that refuses one note doesn't hold back the item's later
notes for good.

### Threat model
- **A stolen token** can request, as any user rvav knows, what that user
  may request, and read the catalog and users' entitlements; nothing
  else in rvav. Against it: the token is refused outside `/servicenow/`;
  `allowed_sources` (the instance's outbound addresses, or the MID
  server's) refuses calls from elsewhere, read after the trusted
  proxies; submissions are limited to `requests_per_minute` (30 by
  default) across rvav's API processes, counted by the minute in the
  database (`servicenow_rate`), and reads to ten times that rate in each
  process, a requested item submitted again being a read, so that the
  retry job's asking again doesn't hold back new orders; every request
  made is audited with the token and the
  address; the token expires in a year, and Revoke ends it at once.
- **A forged user name.** rvav believes the people ServiceNow names: it
  can't check who ordered in ServiceNow. ServiceNow's side takes the
  user from the requested item's `requested_for` and the orderer from
  its `opened_by`, which its own ACLs guard and ServiceNow sets to the
  person whose session ordered, never from a variable the user fills.
  Anyone who can set **Requested for** gets no more than they could in
  rvav: ordering for someone else takes `request.on_behalf` in the
  project, as in rvav, and the requester's `auto_approve` alone doesn't
  approve it. The user's entitlements and approvals still apply, and
  their approvers see the request as the user's, made via ServiceNow
  and ordered by the orderer. What remains is a forged `opened_by` or a
  stolen token, which can request as any user what that user may: rvav
  can't tell them from a real order. An email matches only that email,
  and a user name only a whole user name, so one ServiceNow user isn't
  taken for another rvav user; ambiguous names are refused.
- **Rate limits** answer 429 with `Retry-After` (the seconds to the
  next minute), audited as `servicenow.request_limited` once a minute at
  most in each process, so a flood doesn't fill the log.
- **What the audit shows**: who the request was for and who ordered
  it, what ServiceNow said (the emails or user names), which requested
  item, which token, which address and user agent; refusals likewise.
- **rvav's account in ServiceNow** holds least privilege: read and write
  on the CMDB class and read on `sys_user` (for `owned_by`), write on
  `sc_req_item`'s `state` and `work_notes`, and the IRE's role
  (`import_transformer`, or `itil` with the IRE's ACL). It needs no
  admin role and no access to anything else.

### ServiceNow's side
`integrations/servicenow/` holds scripts for the instance, global scope,
and its README the install steps:

- `RvavClient` calls rvav with `sn_ws.RESTMessageV2`, the URL and the
  token from a Connection & Credential alias (an HTTP connection and an
  API key credential), through a MID server if a property names one.
- `RvavCatalogImport`, from a daily scheduled job, makes a catalog item
  of each item rvav offers, which keeps its rvav item ID in its `meta`
  (`rvav:itm_…`), and deactivates the ones rvav no longer offers. Each
  has the variable set `rvav` (project, environment, deployment name,
  justification, accept warnings) and a variable `rvav_in_<input>` per
  input: a select box for a fixed list, a checkbox for a boolean, else a
  line of text, a `dynamic` input included, which rvav checks when the
  request is submitted.
- Two catalog client scripts on the variable set fill the project and
  environment choices through `RvavAjax` (client callable), which asks
  rvav's entitlements for the user signed in, never for a user the
  browser names.
- A business rule on `sc_req_item` queues the event `rvav.order` when an
  item of rvav's is ordered (or, with `rvav.wait_for_approval`, once
  ServiceNow approved it); its script action, `RvavOrder`, submits it on
  behalf of `requested_for`, ordered by `opened_by`, each by email, else
  user name. ServiceNow's
  variables are strings: rvav takes them as the form's types, and an
  empty one as the initial value. A refusal closes the requested item
  incomplete with rvav's problem in its work notes; rvav down, a 401, a
  429, a 5xx or a 409 with `retry` leave it open, and a job every 5
  minutes submits again, for 3 days, the open ones rvav has no request
  for. It asks rvav about 50 at most a run, oldest first; those waiting
  for ServiceNow's approval cost no call, and one rvav has already but
  whose first work note didn't land is set to Work in Progress, so that
  neither holds back the items behind them.

There is no update set. An update set is one XML file to import, but it
is exported from an instance the records were made on, and we have none:
a hand-written one we can't check would be worse than steps that say
what each record is. The scripts are checked under Node, with a stand-in
for the Glide APIs, against rvav's real answers (`TestServiceNowScripts`,
`TestServiceNowRetryJob`): the import, the projects, the draft rvav
takes, a refusal and a retry.
I1.6's acceptance runs them on a personal developer instance.

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
