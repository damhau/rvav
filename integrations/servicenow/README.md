# rvav in ServiceNow

These scripts put rvav's catalog in ServiceNow's Service Catalog, and send
each order of one of its items to rvav, on behalf of the person it is
requested for. rvav then writes the request's progress to the requested
item (RITM): its work notes, its state and the deployment's link. The
design and its threat model are in
[ADR-0065](../../docs/adr/0065-servicenow.md).

There is no update set: you create a few records and paste the scripts
into them. An update set is one XML file to import, but it can only be
exported from an instance it was built on, and these scripts haven't run
on a real instance yet: they are checked under Node against rvav's API
(`TestServiceNowScripts`), with a stand-in for the Glide APIs.

| File | ServiceNow record |
| --- | --- |
| `script-includes/RvavClient.js` | Script Include `RvavClient`: calls rvav |
| `script-includes/RvavOrder.js` | Script Include `RvavOrder`: submits a requested item |
| `script-includes/RvavCatalogImport.js` | Script Include `RvavCatalogImport`: imports the catalog |
| `script-includes/RvavAjax.js` | Script Include `RvavAjax`, client callable: the order form's projects |
| `business-rules/rvav_order.js` | Business rule `rvav: order` on `sc_req_item` |
| `script-actions/rvav_submit.js` | Script action `rvav: submit` on the event `rvav.order` |
| `scheduled-jobs/rvav_catalog_import.js` | Scheduled job `rvav: catalog import` |
| `scheduled-jobs/rvav_retry.js` | Scheduled job `rvav: retry` |
| `catalog-client-scripts/rvav_projects_onload.js` | Catalog client script `rvav: projects` |
| `catalog-client-scripts/rvav_environments_onchange.js` | Catalog client script `rvav: environments` |

## What ServiceNow users get

- Each item rvav offers in at least one project is a catalog item in the
  catalog you pick, in a category named after its rvav category. Its
  variables are the variable set `rvav` (project, environment, deployment
  name, justification, and a box to submit despite policy warnings) and
  one variable per input: a select box for a fixed list, a checkbox for a
  boolean, a line of text otherwise. An input whose options rvav finds
  when the request is made (a vSphere cluster, for example) is a line of
  text that rvav checks. Secrets, files, lists and objects aren't offered:
  an item that needs one is requested in rvav.
- The project list holds the projects where the person ordering may
  request the item, with each project's environments.
- rvav submits the order as the RITM's **Requested for**, with that
  person's entitlements, quotas, approvals and policies, and says who
  placed it, the RITM's **Opened by**. rvav matches each person by email,
  or, when their ServiceNow user has no email, by their whole user name,
  which must be their rvav user name: the `preferred_username` their
  identity provider gives rvav. If rvav refuses the order, the RITM is
  closed incomplete with rvav's reason in its work notes.
- Someone who orders for another person needs, in rvav, the permission
  to request for someone else in that project (`request.on_behalf`,
  which project admins have), as in rvav itself. rvav's approval
  policies then ask for approval unless their automatic approval holds
  for both people.
- rvav writes to the RITM as the request goes: submitted (state Work in
  Progress), waiting for approval and by whom, approved or rejected, then
  Closed Complete with the deployment's link, or Closed Incomplete.

## Before you begin

- rvav's ServiceNow integration is set and its test passes
  (Administration › Integrations › ServiceNow). Its account in ServiceNow
  needs the roles in
  [The account rvav signs in with](#the-account-rvav-signs-in-with).
- The instance reaches rvav's URL over HTTPS, directly or through a MID
  server, and checks the certificates of the servers it calls: the
  system properties `com.glide.communications.trustmanager_trust_all`
  is `false`, its default, and
  `com.glide.communications.httpclient.verify_hostname` is `true`.
  Otherwise anyone on the way to rvav can read rvav's token. A MID
  server checks them with its own Java trust store: rvav's certificate
  authority must be in it.
- You have the `admin` role on the instance, in the global scope.
- People who order rvav's items, and those they order for, have an rvav
  user with the same email. rvav knows a person from their first sign-in
  to rvav, and refuses an order for or by someone whose last sign-in is
  older than 90 days (`RVAV_SCHEDULE_OWNER_IDLE_DAYS`): rvav would go by
  groups they may have left.

## Install

1. In rvav, go to **Administration › Integrations › ServiceNow** and
   click **Make a token**. Copy the token: rvav shows it once. Making a new
   one later revokes this one.
1. Optional: in the same page, set **Allowed sources** to the instance's
   outbound addresses, or the MID server's, so that rvav refuses the
   token from anywhere else. Set **Requests a minute** if 30 orders a
   minute isn't enough.
1. In ServiceNow, go to **Connections & Credentials › Connection &
   Credential Aliases** and click **New**. Name it `rvav`, of type
   **Connection and Credential**, and save it. Note its **ID**, for
   example `global.rvav`.
1. In the alias's **Connections** list, click **New** and choose **HTTP(s)
   Connection**:
   - **Connection URL**: rvav's URL, for example `https://rvav.corp.example`.
   - **Credential**: a new **API Key Credentials** record whose **API
     Key** is the token.
   - **Use MID server**: leave it off, and set `rvav.mid_server` instead
     if rvav is reached through one.
1. Go to **System Properties** (`sys_properties.list`) and create the
   properties that differ from their default, each of type `string`:

   | Property | Default | Value |
   | --- | --- | --- |
   | `rvav.alias` | `global.rvav` | The alias's ID |
   | `rvav.catalog` | the catalog titled Service Catalog | The `sys_id` of the catalog rvav's items go in |
   | `rvav.mid_server` | empty | The name of the MID server that reaches rvav |
   | `rvav.wait_for_approval` | `false` | `true` to send an order only once ServiceNow approved it |

1. Go to **System Definition › Script Includes** and create
   `RvavClient`, `RvavOrder`, `RvavCatalogImport` and `RvavAjax`, in the
   global scope, each with the script of its file. Tick **Client
   callable** for `RvavAjax` only.
1. Go to **System Policy › Events › Registry** and create the event
   `rvav.order`, on the table **Requested Item [sc_req_item]**, fired by
   the business rule `rvav: order`.
1. Go to **System Policy › Events › Script Actions** and create `rvav:
   submit`: event name `rvav.order`, **Active** ticked, the script of
   `script-actions/rvav_submit.js`.
1. Go to **System Definition › Business Rules** and create `rvav: order`
   on **Requested Item [sc_req_item]**: **When** after, **Insert** and
   **Update** ticked, **Advanced** ticked, the script of
   `business-rules/rvav_order.js`.
1. Go to **System Definition › Scheduled Jobs**, click **New**, choose
   **Automatically run a script of your choosing**, and create:
   - `rvav: catalog import`, run daily, with
     `scheduled-jobs/rvav_catalog_import.js`;
   - `rvav: retry`, run periodically every 5 minutes, with
     `scheduled-jobs/rvav_retry.js`.
1. Open `rvav: catalog import` and click **Execute Now**. The system log
   says `rvav: imported N catalog items`, and the variable set `rvav`
   exists.
1. Go to **Service Catalog › Catalog Variables › Variable Sets**, open
   `rvav`, and create two catalog client scripts in its **Catalog Client
   Scripts** list, **UI Type** All and **Applies on a Catalog Item view**
   ticked:
   - `rvav: projects`, type **onLoad**, with
     `catalog-client-scripts/rvav_projects_onload.js`;
   - `rvav: environments`, type **onChange** on the variable
     `rvav_project`, with
     `catalog-client-scripts/rvav_environments_onchange.js`.
1. If your instance asks for ACLs on client-callable script includes,
   create one of type **client_callable_script_include** for `RvavAjax`
   that requires the role `snc_internal`. `RvavAjax` only answers for the
   person signed in.
1. rvav does the fulfilment and its own approvals, so give rvav's catalog
   items no flow or workflow that creates fulfilment tasks. If your site
   approves orders in ServiceNow first, set `rvav.wait_for_approval` to
   `true`: the order then goes to rvav once its approval is **Approved**.

To check the installation, order an rvav item as someone with an rvav
user. Within a minute its RITM's work notes say `rvav: request REQ-…
(<item name>) submitted`, and rvav's audit log has a `servicenow.request`
event naming that person.

## The account rvav signs in with

rvav calls ServiceNow with the user or OAuth client set in its
integration. Give that account no admin role, only:

- read on `sc_req_item`, and write on its `state` and `work_notes`, for
  the requested items of rvav's catalog items;
- for the CMDB, the role that may call the Identification and
  Reconciliation API (`import_transformer`, or `itil` with the API's
  ACL), read and write on the CI class (`cmdb_ci_vmware_instance` by
  default), and read on `sys_user` for the CIs' **Owned by**;
- **Web service access only** ticked on the user.

## When something goes wrong

- **The project list is empty, with a message.** The message is rvav's:
  no rvav user has the person's email, or no project of theirs offers
  the item.
- **The RITM is Closed Incomplete.** Its work notes have rvav's reason:
  an input it refused, a quota, an item the person can't request, a
  policy warning that the order didn't accept, an order for someone else
  by a person who may not request for others in rvav, a last sign-in to
  rvav too old, or a rejection, with the approval level and who rejected
  it. "Not from ServiceNow's
  addresses" means that the call came from outside rvav's **Allowed
  sources**: add the instance's address, then order again.
- **The RITM stays Open.** rvav couldn't be reached, refused the token
  (401, a revoked or expired token), answered 429, answered an error of
  its own (5xx), or answered a conflict that passes (409 with `retry`:
  rvav's ServiceNow connection isn't set, or orders placed at the same
  moment took the room counted on). The system log has a warning
  starting with `rvav:`, and `rvav: retry` submits the RITM again every
  5 minutes for 3 days.
- **A work note is missing.** ServiceNow refused it five times, for
  example through a data policy or a business rule on `sc_req_item`, and
  rvav gave it up so that the later notes still reach the RITM. rvav's
  log has `servicenow work note refused, given up` with ServiceNow's
  answer.
- **An item doesn't show up.** rvav offers an item when it has a
  published version shared with at least one project. Run `rvav: catalog
  import` again after publishing it.

## Limits

- The project list shows the projects of the person ordering. When they
  order for someone else, rvav checks the person it is requested for,
  and refuses if that person can't request the item there, or if the
  person ordering may not request for others there.
- Files can't be uploaded with an order.
- An input whose name doesn't fit in a variable's name is left out, and
  takes its initial value. The system log says which.
