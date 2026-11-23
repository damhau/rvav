# Administering rvav

This guide is for administrators and catalog admins: what to set up so
people can request, and how to look after it. Installing and running rvav
is in the [operations guides](../operations/README.md); using it, in the
[user guide](user.md).

**Roles:**
- **Administrator:** everything under **Administration**.
- **Catalog admin:** the catalog only.
- **Auditor:** reads everything and changes nothing.

Roles are given to groups of the identity provider, or to users. You can
also make roles of your own: see [Roles and permissions](#roles-and-permissions).
Every change made here is in the **Audit log**.

**The Administration menu** lists only the pages your roles let you open,
in four groups:
- **Catalog:** Catalog items, Day-2 actions, Custom workflows, Subscriptions.
- **Governance:** Projects, Roles, Policies.
- **Infrastructure:** Integrations, Templates and networks (with the VM
  sizes and the tags of vCenter objects), Onboarding, vRA import.
- **Operations:** Setup, Branding, Notification templates, Audit log,
  Audit forwarding, System.

This guide names a page as **Administration › Page**, such as
**Administration › Roles**. You can also open one from the search box
(Ctrl+K or ⌘K) by typing its name.

**The local administrator,** `admin`, is rvav's only local account
(ADR-0030). It signs in on rvav's sign-in page, below single sign-on:
- at first, before an identity provider is set up;
- when the identity provider is down. A browser that signed in with it
  before goes back to it by itself; when its page doesn't work, open
  `https://<rvav>/sign-in`, which never does.

Its first password is given at installation, or generated and logged
once, and must be changed at the first sign-in (**Account › Change
password** later). From the third failure in a row, sign-in waits, longer
each time, up to 15 minutes. Every sign-in and failure is in the audit
log (**Sign-ins**). A forgotten password is reset on the server:
`rvav admin reset-password`, which ends the account's sessions and
revokes its API tokens, as changing the password does.

## First steps

The order that gets a first request through. On a new install,
**Administration › Setup** lists these steps. It opens when `admin` signs
in, and ticks each step once its page has what it needs. The first step
you can do from there is marked as the one to do now, and its button
opens its page. **Don't open it at sign-in** keeps it for later; it stops
opening by itself once every step is done.

0. **Sign in as `admin`** and choose its password.
1. **The identity provider** (**Integrations › Identity provider**), and
   a group of it mapped to `admin`, or to a role of your own holding
   `access.manage`, for all of rvav (**Projects › Identity provider groups**):
   [the identity provider](#the-identity-provider).
2. **Integrations:** vCenter, AWX and EfficientIP, with rvav's service
   accounts ([integrations.md](../integrations.md)). Each has a test. Its
   password or token is sealed and never shown again.
3. **Infrastructure:**
   - **Templates:** the templates offered, by OS, with their template in
     each vCenter ([templates per vCenter](#templates-per-vcenter)).
   - **Sizes:** the CPU, memory and disk choices.
   - **Networks:** each vSphere network's subnet. The subnets come from
     EfficientIP's class parameters; set one by hand where EfficientIP has
     none. A network that only items on DHCP use (`ipam: dhcp`) needs no
     subnet. For items with `ipv6: true`, a network also needs an IPv6
     subnet: an IPv6 subnet in EfficientIP whose `vsphere_network` and
     `gateway` class parameters name the network and its gateway, or one
     set by hand with the mapping (**IPv6 subnet**, **IPv6 gateway**).
   - **Placement:** which cluster and datastore serve which environment and
     project.
4. **Projects:**
   - each with its members or the groups that map to its roles (viewer,
     member, project admin, or [one of your own](#roles-and-permissions));
   - its quotas, per environment or for the project as a whole, hard or
     soft.
5. **Policies.**
6. **The catalog.**

### Saved settings apply

No process is restarted by hand (ADR-0030):
- the identity provider, the console, vRA, Active Directory, ServiceNow,
  email, Teams, Slack, the allowed hosts and the sync intervals apply on
  next use, within seconds;
- a connection (vCenter, AWX, EfficientIP, NSX, Git) applies as each of
  rvav's processes finishes its running work, exits and is started again
  with it. The panel lists the processes: running with the earlier
  settings, finishing their work, then applied. A VM being provisioned
  meanwhile finishes, its next steps with the new connection.

A save that changes nothing rvav runs with saves nothing: no process
restarts, and the page says the settings are unchanged.

A field that an environment variable sets shows the variable's value and
is locked: change it in the environment of rvav's processes. If the
variable was set after you opened the page, saving is refused at that
field, which then shows the variable's name and value.

Settings saved within a few seconds of each other apply together. With a
single API (Compose, a VM), the page is unavailable for the few seconds it
takes the API to start again; with two or more, they take turns.

**Administration › System** shows, read only:
- the version and the database schema's last migration;
- each of rvav's processes, its version and the settings it runs with
  (two versions at once: an upgrade under way, or a process left behind);
- each setting saved in Administration, by whom, and whether it applies
  on next use, is applying or has applied;
- the install settings, from the environment or their default. A secret
  shows only whether it is set; the database URL is shown without its
  password. They are changed in the environment of each process, which
  is then restarted.

### Where the rest is set

Also in **Administration › Integrations**
([configuration.md](../operations/configuration.md) says where each
setting is set). Its **Connections** tab lists each integration, whether
it is configured and when it was last saved; select one to open its
page:
- **vCenter › Console:** an account that may only open consoles, whose
  clone tickets VMRC links carry (without one, VMRC asks each user to
  sign in), and the WebMKS SDK's scripts, which open consoles in the
  browser. Its password only goes to the vCenter it was given for: give
  it again after changing vCenter's address.
- **vRA:** the vRA the importer reads, with a user and its password, or a
  refresh token.
- **Allowed hosts:** the hosts REST value sources, a composite's REST
  calls and webhooks may call.
  None listed, none is called.
- **Sync intervals:** on each connection's page, how often rvav reads it.
- **Trusted certificates:** see [below](#trusted-certificates).
- **AWX › Inventory token:** the token AWX's inventory source reads rvav's
  VMs with. rvav makes it for a user of its own (an auditor no one signs
  in as), valid a year, and puts it in the source's "rvav API" credential
  with rvav's address as AWX reaches it (empty keeps the one AWX has); the
  one before is revoked and the source synced. On awxnext, whose rvav
  source reads rvav itself, the credential is of awxnext's managed type
  rvav, and its TLS settings are kept. Make a new one before it
  expires. Set the inventory source ID first; `RVAV_INVENTORY_TOKEN` locks
  it.
- **ServiceNow:** the CMDB and requests ordered in ServiceNow: see
  [ServiceNow](#servicenow).

### Trusted certificates

rvav checks the certificate of every server it connects to against the
system's CAs and those of the **CA bundle** (**Integrations › Trusted
certificates**). A connection's test shows its server's certificate
first: who it was issued to and by, until when (in your browser's time
zone), its SHA-256, and whether rvav trusts it. When it doesn't, the test stops there and offers:
- **Trust this certificate** (vCenter, the vCenters added, AWX,
  EfficientIP, NSX, the NSX Managers added, vRA,
  Active Directory): its SHA-256 goes in the
  connection's **Trusted certificate** field, and TLS verification on.
  Save, with the password or token again (a new pin needs it, as a new
  address does), then test.
  For a self-signed certificate. When the server's certificate is
  renewed, test and trust it again.
- **Add it to the CA bundle:** the CA that issued it (or a self-signed
  certificate itself) is added to the bundle, once you confirm that every
  connection will trust what it signs. Prefer it for a private CA: renewed
  certificates stay trusted, and the identity provider and Git, which take
  no pin, need it.

The bundle's **Check** lists its certificates, and says which have
expired. Saved, it applies as a connection does: each process finishes its
work and starts again with it. A connection's test uses it at once.
"Skip TLS verification" remains for labs; its test shows the certificate,
not checked.

## The identity provider

Everyone but the local administrator signs in with the organisation's
identity provider (OpenID Connect), and gets roles from its groups
([ADR-0030](../adr/0030-settings-and-local-administrator.md)). It is set
in **Administration › Integrations › Identity provider**:

1. **Register rvav at the provider** as a single-page application (a
   public client, authorization code with PKCE, no secret):
   - the redirect URI the panel shows, `https://<rvav>/auth/callback`;
   - `https://<rvav>/`, where sign-out comes back.
2. **Fill in the panel:**
   - **Issuer:** as in the tokens' `iss`, and as the browser reaches it.
   - **Discovery URL:** only when rvav reaches the provider at another
     address than the browser (inside a cluster).
   - **Client ID:** the client registered. Access tokens must have it as
     their audience (`aud`).
   - **Scopes:** `openid profile email`, and what the provider needs for
     an access token meant for rvav (below).
   - **Groups claim:** the claim with the user's groups, `groups` by
     default; `roles` for Entra ID's app roles.
   - **Manager claim:** the claim with the user's manager, `manager` by
     default, for approval levels that ask the requester's manager
     ([Who approves](#who-approves)). Its value is the manager's email or
     subject. rvav stores it at each sign-in; a token without it clears
     it.
3. **Test provider:** rvav reads the discovery document and the signing
   keys. The browser fetches tokens at the provider, so its token endpoint
   must be at the issuer's origin.
4. **Try signing in:** a window signs in at the provider with the settings
   in the panel, before they are saved. The panel then shows the token's
   claims, the groups and the manager rvav reads, and the roles the group
   mappings give, or why rvav refuses the token. Nothing is saved, and no user is made.
5. **Map a group to `admin`** (**Projects › Identity provider groups**), and
   others to the projects' roles. Use the values the trial showed.
6. **Save settings.** The provider applies within seconds, without a
   restart. A provider changed or cleared no longer signs anyone in. The
   local administrator still signs in when it is down.

An environment variable (`RVAV_OIDC_*` on the API, `RVAV_WEB_OIDC_*` on the
web container) wins over the panel and locks its field
([configuration](../operations/configuration.md#sign-in)).

### Microsoft Entra ID

One app registration is both the web app and the API its tokens are for.
In **App registrations › New registration**:
- **Authentication:** add the **Single-page application** platform, with
  both redirect URIs. Not **Web**: the browser exchanges the code itself.
- **Expose an API:** set the Application ID URI (`api://<client ID>`), and
  add a scope, `access`, that admins and users can consent to. Without a
  scope of its own, Entra ID gives a token for Microsoft Graph, which
  rvav can't check.
- **Manifest:** set `requestedAccessTokenVersion` to `2`. A v1 token has
  another issuer (`https://sts.windows.net/<tenant>/`) and audience.
- **Groups or app roles:**
  - **Token configuration › Add groups claim:** security groups, in the
    access token. The claim holds the groups' object IDs: map those. A
    user in more than 200 groups gets none in the token: prefer app roles,
    or **Groups assigned to the application**.
  - **App roles** (recommended): add one per rvav role, for example
    `rvav-admins`, and assign groups to them in **Enterprise applications ›
    rvav › Users and groups**. Set the groups claim to `roles`, and map
    the roles' values.

In rvav:
- **Issuer:** `https://login.microsoftonline.com/<tenant ID>/v2.0`.
- **Client ID:** the Application (client) ID.
- **Scopes:** `openid profile email offline_access api://<client ID>/access`.
  `offline_access` gives the refresh token sessions renew with.

### Keycloak

In the realm, **Clients › Create client**:
- **OpenID Connect,** client authentication off, standard flow only.
- **Valid redirect URIs** `https://<rvav>/auth/callback`; **Valid post
  logout redirect URIs** `https://<rvav>/`; **Web origins**
  `https://<rvav>`.
- **Advanced › Proof Key for Code Exchange:** `S256`.
- **Client scopes › rvav-dedicated:**
  - an **Audience** mapper including the client, in the access token:
    Keycloak puts the client in `aud` only with one;
  - a **Group Membership** mapper, claim `groups`, in the access token.
    rvav drops the leading `/` of a full group path.
  - for the requester's manager, a **User Attribute** mapper from a user
    attribute, for example `manager`, holding the manager's email, to the
    claim `manager`, in the access token. On Keycloak 24 and later,
    declare the attribute in **Realm settings › User profile**, or set
    **Unmanaged attributes** to enabled in **Realm settings › General**.

In rvav, the issuer is `https://<keycloak>/realms/<realm>`, as Keycloak's
hostname setting makes it. When rvav reaches Keycloak inside the cluster,
the discovery URL is that address, `http://keycloak.<namespace>.svc:8080/realms/<realm>`.

### Okta

- **Applications › Create App Integration:** OIDC, **Single-Page
  Application**, grant types authorization code (and refresh token).
  Sign-in redirect URI `https://<rvav>/auth/callback`, sign-out redirect
  URI `https://<rvav>/`; rvav's origin is then a trusted origin (CORS).
- **Security › API › Authorization Servers:** Okta's own (org)
  authorization server issues tokens only Okta can check. Use a custom one
  whose **Audience** is the application's client ID. Add a **groups**
  claim to it: in the access token, value type Groups, filtered (for
  example starting with `rvav-`).

In rvav, the issuer is `https://<org>.okta.com/oauth2/<server ID>`.

### Set up the sign-in for rvavctl

[rvavctl](cli.md) signs in with rvav's client, as the web app does,
unless people give another with `rvavctl login --client-id`. rvav takes
a token from either when its audience is rvav's client ID and it has the
same groups claim. The client rvavctl signs in with must allow:
- **The loopback redirect** `http://127.0.0.1/callback`, for the sign-in
  in the browser. rvavctl listens on a free port, which providers ignore
  on a loopback address (RFC 8252). If yours matches ports exactly,
  register one, for example `http://127.0.0.1:8250/callback`, and tell
  people to sign in with `--callback-port 8250`.
- **The device authorization grant,** for `rvavctl login --device`.

We recommend a public client of its own for rvavctl, rather than turning
these on for the web app's client:
- The device grant lets anyone start a sign-in and send someone its code
  to enter, a phishing technique known as device code phishing. On a
  client of its own, you can leave the grant off, or limit it with your
  provider's access policies, without changing the web app's sign-in.
- On Entra ID, **Allow public client flows** turns on the device code
  flow, and also the resource owner password grant (ROPC), which signs
  in with a password sent by the program, without your sign-in page.
  On the web app's registration, that opens the password grant on the
  web app's client too.

People then sign in once with `--client-id <its client ID>`; rvavctl
keeps it in the profile.

By provider:
- **Keycloak:** create an OpenID Connect client for rvavctl, for example
  `rvavctl`, with **Client authentication** off, **Standard flow** on,
  and **OAuth 2.0 Device Authorization Grant** on if people sign in with
  `--device`. Add `http://127.0.0.1/callback` to **Valid redirect URIs**.
  Give it the same mappers as rvav's client: an audience mapper that
  adds rvav's client ID to the access token's audience, and the groups
  mapper. To use rvav's client instead, make the same changes there.
  rvavctl asks for `offline_access`, so people need Keycloak's
  `offline_access` role, which the realm's default roles give: without
  it, Keycloak refuses the sign-in with "Offline tokens not allowed".
- **Entra ID:** register an application for rvavctl. In
  **Authentication**, add the **Mobile and desktop applications**
  platform with `http://127.0.0.1/callback`, and set **Allow public
  client flows** to **Yes** if people sign in with `--device`. In **API
  permissions**, add rvav's API scope (`api://<rvav's client ID>/access`),
  and grant admin consent. Its access tokens are then rvav's, with the
  groups claim that rvav's registration sets.
- **Okta:** the single-page application can't use the device grant. Add
  a **Native Application** for rvavctl, with the authorization code,
  refresh token and device authorization grants, its sign-in redirect URI
  `http://127.0.0.1:8250/callback`, and access to the same authorization
  server, so that its tokens have rvav's audience. Tell people to sign in
  with `--client-id <its client ID> --callback-port 8250`.

`offline_access` is asked for when the provider offers it, so that a
sign-in outlives the browser session. Without it, people sign in again
when the provider's session ends.

`rvavctl logout` revokes the sign-in at providers that offer token
revocation (RFC 7009), such as Keycloak and Okta. Entra ID doesn't: there
a refresh token stays valid until it expires or you revoke the user's
sessions in Entra ID.

### When the trial refuses the token

| The trial says | Change |
|---|---|
| The token is for Microsoft Graph | Add a scope of rvav's API (`api://<client ID>/access`) to the scopes. |
| Entra ID gave a v1 token | `requestedAccessTokenVersion: 2` in the manifest. |
| `expected audience` | Make the provider put the client ID in `aud`: Keycloak's audience mapper, Okta's authorization server audience. |
| The access token is not a JWT | The provider gave a token for itself: ask for a scope of rvav's API, or use a custom authorization server (Okta). |
| The token has no `groups` claim | Add the groups claim to the access token, or set the claim to `roles` for Entra ID's app roles. |
| Roles given: none | No group mapping matches the groups shown: map them. |

A redirect URI error is the provider's (`AADSTS50011` for Entra ID): the
URI registered must be the one the panel shows, exactly.

## Tags on vCenter objects

rvav reads the vSphere tags on clusters, datastores and networks (port
groups), written `category/tag`, for example `rvav-tier/gold`. Placement
rules choose clusters and datastores by them
([ADR-0020](../adr/0020-administration.md)). Within what the rules allow,
a blueprint's constraints choose the clusters, datastores and networks
whose tags it needs, for example a production cluster outside the PCI
zone ([constraint tags](../service-catalog.md#vm-blueprints),
[ADR-0035](../adr/0035-constraint-tags.md)). It reads the tags on hosts,
resource pools and datastore clusters too: a compute constraint is met by
a host or its cluster, and a datastore in a datastore cluster carries the
datastore cluster's tags ([ADR-0036](../adr/0036-placement-targets.md)).

To tag vCenter objects for rvav:
1. In the vSphere Client, open **Tags & Custom Attributes** and create a
   category, for example `rvav-tier`. Under **Associable object types**,
   choose what its tags go on: **Cluster**, **Host**, **Datastore**,
   **Datastore Cluster**, **Distributed Port Group** or **Network**.
2. Create the category's tags, for example `gold` and `bronze`. Keep `/`
   and `:` out of category and tag names: rvav writes a tag as
   `category/tag`.
3. Assign the tags to the clusters, hosts, datastores, datastore clusters
   and port groups.
4. Wait for the next full read of the inventory: tags have no change
   notifications, so they come with the full read every sync interval
   (2 minutes by default).
5. To check what rvav read, open **Administration › Templates and
   networks**, then the **vCenter objects** tab. It lists each cluster,
   host, resource pool, datastore, datastore cluster and network with its
   tags; search by a tag to see which objects carry it.

rvav reads tags through vCenter's tagging service, with the vCenter
account's credentials. When the service can't be read, the sync keeps the
tags it read last, and the vCenter's test in **Integrations** says so
under **Tagging service**.

## Templates per vCenter

A template in **Administration › Templates and networks** is what VM
forms offer in their template picker, for example RHEL 9.4
([ADR-0037](../adr/0037-images-per-vcenter.md)). It has a source in each
vCenter it can be cloned in: the name of a VM template there, or an item
of a content library there. Each site can build the template under a
name of its own.

To give a template a source in another vCenter:
1. Add the vCenter in **Administration › vCenters**, and wait for its
   first inventory sync.
2. Open **Templates and networks**, click the template's **⋯** menu, then
   **Change**.
3. Click **Add a vCenter**, choose the vCenter, and name its template.
   When the template is an item of a content library, name the library
   too.
4. Click **Save template**.

The **In vCenter** column says how many vCenters each template has a
source in, and **Not found** when a vCenter doesn't have its source now.

When a request's form chooses a template, rvav chooses the vCenter and
the cluster among the vCenters the template has a source in, then clones
that vCenter's source. The clusters of the other vCenters aren't used:
the request's preview names those vCenters, and says so when none is
left. A blueprint that names a vCenter template itself, without a
picker, is placed where vCenter has a template of that name.

### Cloud-init and constraint tags of a template

A template can say what its VMs need, whatever the catalog item: an
agent to install, an NTP server, the clusters its operating system is
supported on.

- **Cloud-init user data** (Linux templates only): user data merged with
  the item's, written as a blueprint's: a Go template whose inputs are
  printed through `json`. Start it with `#cloud-config`, or with `#!` for
  a script. A VM of such a template is configured by cloud-init, even when
  its item has no user data, so the template needs no customization
  specification: rvav refuses both on one template.
- **Constraint tags:** tags added to the item's, separated by commas, for
  example `rvav-os/rhel9` under **Compute** or `!rvav-tier/bronze` under
  **Storage** ([constraint tags](#tags-on-vcenter-objects)). They can't
  depend on the request: `${...}` is for blueprints.

When both the template's user data and the item's are `#cloud-config`,
the VM gets one document: maps merged key by key, the template's list
entries then the item's (for example `packages` or `runcmd`), and the
item's value for anything else. When either is a script, the VM gets
both, the template's first. On the request's timeline, the step **Wrote
cloud-init metadata and user data** says whether the template's user data
was used.

Keep a template's user data to what every item offering it can use: a
value it reads that the request doesn't have, such as an input other
items don't ask for, fails the build.

### Linked clones

A template can make its VMs linked clones: vCenter clones them from the
template's current snapshot, and their disks are children of the
template's. A linked clone is made in seconds and takes little space, but
its disks depend on the template's.

To make a template's VMs linked clones:
1. In vCenter, take a snapshot of the template in each vCenter it has a
   source in.
2. In **Templates and networks**, click the template's **⋯** menu, then
   **Change**.
3. Select **Linked clones**, then click **Save template**.

The **Template** column shows **linked clones** for such a template.
Keep these limits in mind:
- A content library item is deployed, not cloned: rvav refuses linked
  clones for a template with a content library source.
- A linked clone keeps the size of the template's first disk. A request
  whose size asks for a larger disk fails at the clone, and says so; add
  disks instead.
- A request fails at the clone when the template has no snapshot in the
  vCenter placement chose.
- Keep the template and its snapshots while VMs cloned from them exist.
  To update the template, take a new snapshot: VMs cloned after it use the
  new one.

A blueprint can say otherwise with `linked_clone` (see the [service
catalog guide](../service-catalog.md)).

### Windows templates with cloudbase-init

By default, rvav configures a Windows VM with a vCenter customization
specification: its name, network, time zone, domain and the
`Administrator` password. A Windows template that has
[cloudbase-init](https://cloudbase-init.readthedocs.io/) can be
configured the way Linux VMs are with cloud-init: rvav writes the VM's
name, network, user and user data in its guestinfo, where cloudbase-init's
VMware GuestInfo service reads them. Its VMs can then get any remote
access a blueprint gives (an SSH key, the deployment's key, or a user
with a password) and user data.

To prepare such a template:
1. In the Windows template, install VMware Tools and cloudbase-init.
2. In `cloudbase-init.conf` and `cloudbase-init-unattend.conf`, set
   `metadata_services=cloudbaseinit.metadata.services.vmwareguestinfoservice.VMwareGuestInfoService`.
   Keep the default plugins, which make the user, set its password and
   keys, and run the user data.
3. For SSH keys, install OpenSSH Server. cloudbase-init writes the keys
   to the user's `.ssh\authorized_keys`: in `sshd_config`, remove the
   `Match Group administrators` block, which reads another file for
   members of Administrators.
4. Generalize the template as cloudbase-init's documentation says, and
   don't give it a customization specification.

Then mark it in rvav:
1. In **Templates and networks**, click the template's **⋯** menu, then
   **Change**. The template's operating system is **Windows**.
2. Select **Uses cloudbase-init**, and clear **Customization spec**.
3. Optional: in **cloudbase-init user data**, give user data every VM of
   the template gets, before the item's: a PowerShell script
   (`#ps1_sysnative`), a batch script (`rem cmd`) or `#cloud-config`.
4. Click **Save template**.

The **Template** column shows **cloudbase-init**. On the request's
timeline, the step **Wrote cloudbase-init metadata and user data** names
the user. A password goes in the VM's metadata: rvav adds a last script to
the user data that writes the metadata again without it, once
cloudbase-init has read it. Keep in mind:
- cloudbase-init doesn't join a domain: an item with `domain_join` is
  refused on such a template. Join with a post-configuration AWX template
  instead.
- A template without cloudbase-init gives remote access only as the
  `Administrator` password, through the customization specification.

## Placement rules

A placement rule says where a project's VMs go in an environment
([ADR-0020](../adr/0020-administration.md)). Requests use the first rule,
lowest priority first, that allows the cluster. To add one, open the
project in **Administration › Projects**, then the **Placement** tab, and
click **New rule**.

| Field | What it says |
|---|---|
| **Clusters**, **Cluster tags** | The clusters allowed, by name pattern (`GVA-DEV-*`) or by tag; a cluster needs every tag. |
| **Resource pool** | The pool VMs go to in each cluster, by its path below the cluster's root pool, for example `rvav/dev`. A cluster without it isn't used. Empty: the root pool. |
| **Hosts** | Host name patterns, for example `esx-gva-dev-0[12]*`. VMs start on the connected hosts out of maintenance that match, in turn. Empty: any host of the cluster. |
| **Datastores**, **Datastore tags** | The datastores allowed, by name pattern or by tag. |
| **Datastore cluster** | A datastore cluster whose datastores are allowed, narrowed by the datastore patterns and tags when you give any. When Storage DRS is on, rvav asks it which datastore each VM goes to. |
| **Strategy** | How rvav chooses the cluster when a blueprint doesn't bind one: **Most free memory** (the default), **Binpack** (the least free memory that still fits the VMs, filling clusters one after the other) or **Spread** (the fewest VMs of rvav). |
| **Networks** | The networks allowed, by name pattern. |
| **VM folder**, **Storage policy**, **vCenter** | Where VMs are filed, their storage policy, and the vCenter, needed when a cluster's name is in two. |

A rule needs datastores, datastore tags or a datastore cluster. The
blueprint's constraint tags choose within what the rules allow
([constraint tags](../service-catalog.md#vm-blueprints)), and the strategy
chooses among the clusters left. A request's preview shows the resource
pool, hosts and strategy, and the timeline says when a strategy other
than **Most free memory** chose the cluster.

The strategies count the VMs of other requests that aren't built yet
([ADR-0036](../adr/0036-placement-targets.md#reservations-189)): each
VM a request places holds its memory on its cluster until the VM exists.
Requests sent at the same time spread over the clusters with room
instead of all choosing the one that looked freest. A VM's hold ends
when the VM exists in vCenter, when its build fails, is rolled back or is
cancelled, or after six hours. To see what's held, open **Administration
› Templates and networks**, then the **vCenter objects** tab: a cluster's
row says how much of its free memory is held for VMs not built yet.

**What the project gets** under the rules lists the clusters, datastores
and networks they allow, and what stops a request, such as a cluster
without the rule's resource pool.

## NSX networks

With an NSX Manager set in **Integrations**
([ADR-0028](../adr/0028-nsx-networking.md)):
- **Existing segments** are port groups in vCenter: placement rules name
  them like any network, and their subnets come from EfficientIP as
  usual.
- **Segments made for a deployment:** an item can ask for its own network
  (`segments` in its blueprint, and `network: segments.<key>`). A
  placement rule then says where they go, under **NSX segments made for
  deployments**:
  - the Tier-1 gateway they connect to;
  - the EfficientIP block their subnets are carved from, and their size;
  - the DNS zone and servers of the VMs on them.

  Each request makes its segment before its VMs, and deleting the
  deployment deletes it after them, with its subnet. rvav only ever
  deletes segments it made: they carry its tag.
- **Security groups:** an item can put its VMs in groups made for each
  deployment (`security_groups`, with the firewall rules into them), and
  in existing groups that select VMs by tag (`groups: [nsx:<ID>]`).
  - rvav makes the groups and a security policy of their rules before the
    VMs, and tags each VM once NSX sees it. A deployment's groups hold the
    VMs with its tag; an existing group is joined by the tags its
    conditions select.
  - Deleting the deployment takes the VMs out first, then deletes the
    rules and the groups. Existing groups are never changed.
  - Day-2 actions add a VM to a group its item offers, or remove it:
    who may is up to the **Day-2** policies (`nsx_group_add`,
    `nsx_group_remove`).
  - A tag removed in NSX shows the deployment as changed outside rvav.

### Several NSX Managers

rvav makes a VM's segment, groups and tags on the NSX Manager that serves
the VM's vCenter ([ADR-0062](../adr/0062-several-nsx-managers.md)). The
NSX integration's NSX Manager is the first. When you upgrade, it serves
every vCenter rvav has, so nothing changes until you add another.

To add an NSX Manager for other vCenters, such as those of another site:

1. In **Administration › Integrations**, open **NSX**.
2. Under **NSX Managers**, click **Add an NSX Manager**.
3. Enter its name (its host name in lower case) and its HTTPS address.
4. Enter the account rvav signs in with and its password, or the paths
   of a principal-identity certificate and its private key on rvav's
   servers. An NSX Manager you switch to a principal identity forgets its
   password: to switch it back to the account, enter the password again.
5. Under **vCenters it serves**, select the vCenters registered with it
   as compute managers. A vCenter that another NSX Manager serves moves
   to this one: the sheet says so before you save.
6. Click **Test connection**. The test signs in, shows the certificate,
   and reads the Tier-1 gateways, segments and groups.
7. Click **Add NSX Manager**.

To change which vCenters an NSX Manager serves, the integration's
included, open its **Actions** menu and select **Link vCenters**. To change
the integration's NSX Manager itself, change the NSX settings above the
list. Its URL can't name an NSX Manager you added: remove that one first.

A vCenter you add while the integration's NSX Manager is the only one
is served by it. Once you add another NSX Manager, a vCenter you add is
served by none until you link it. With several NSX Managers:
- rvav places VMs that need NSX (a segment made for the deployment,
  security groups, or a group to join) only on clusters of a vCenter an
  NSX Manager serves. A request whose VMs need NSX in a vCenter no NSX
  Manager serves is refused before anything is made, and so is a request
  whose VMs are in vCenters of two NSX Managers.
- The placement rule that makes the segments is the first that names no
  vCenter, or a vCenter of that NSX Manager. A rule that names no vCenter
  needs its Tier-1 gateway on every NSX Manager that serves a vCenter.
- Request forms offer the segments and groups of the NSX Manager that
  serves the vCenter of the chosen cluster.
- Linking a vCenter to another NSX Manager doesn't move what deployments
  already have in NSX: rvav changes and deletes it where it made it. An
  update can't add VMs to groups on the NSX Manager the vCenter left.
- An update that adds no VM, such as a resize, doesn't need NSX.
- You can't remove an NSX Manager while a deployment has a segment, a
  group, a policy or VM tags on it, or while a request that waits or runs
  is to make one.
- If you point the integration's URL to another NSX Manager, deleting
  what rvav made on the one before fails, naming both, and rvav keeps
  its records. Point the URL back to delete it, or delete the deployment
  going on past failures and remove the objects from NSX yourself.
- While an NSX Manager doesn't answer, the tag sync still checks the
  other NSX Managers' VMs, and you can still save a catalog item whose
  groups and services another NSX Manager has.

## IP ranges without EfficientIP

On a network that EfficientIP doesn't manage, rvav can give VMs their
addresses from its own IP ranges
([ADR-0063](../adr/0063-ip-ranges.md)). A network with a range takes its
new VMs' addresses from its ranges; every other network keeps
EfficientIP.

rvav writes no DNS records for addresses from its ranges, and doesn't
check a VM's name in DNS. Create the records another way, for example with
an AWX job template on a `compute.provision.post` subscription.

To add a range:

1. In **Administration › Templates and networks**, open the **IP ranges**
   tab.
2. Click **New IP range**.
3. Enter a name, then select the vCenter and the network: a port group,
   or an NSX segment's.
4. Enter the network's subnet, such as `10.51.0.0/24`, and its gateway.
5. Enter the range's first and last addresses.
6. Optional: under **Excluded addresses**, enter the addresses the range
   never gives, one a line: an address, or a span such as
   `10.51.0.50-10.51.0.59`, without spaces.
7. Optional: enter the DNS servers and search domains that VMs get, and
   the DNS domain that they're named in, without its final dot. Without a
   domain, VMs take the blueprint's. A DNS server inside the range must be
   excluded.
8. Click **Create range**.

rvav refuses a range that would give an address a VM already holds: one
that EfficientIP gave a deployment's VM, or one that vCenter reports for
a VM. The message names each address and its VM. Exclude the addresses,
or choose a span apart from them.

A network can have several ranges, all with the same subnet, gateway and
DNS settings; ranges of a vCenter don't overlap. rvav gives the first free address of
the network's ranges, in the order you made them. It skips an address that a
VM got some other way after you saved the range. A range's **Given** meter
counts the addresses it gave to VMs, out of those it gives, and says how
many it skips apart; **Addresses given** says why. The **Networks** tab
shows each network's source: **rvav IP ranges**, **EfficientIP** or **By
hand**.

To see who holds each address, open a range's **Actions** menu and select
**Addresses given**. While VMs hold addresses from a range, you can't
remove it, change its vCenter, network, subnet or gateway, or exclude an
address that a VM holds.

rvav gives an address back when its VM is deleted, when a request fails
before the VM exists or is rolled back, and when a day-2 action moves the
adapter to another network. Unregistering a VM keeps its addresses, as it
does in EfficientIP.

### Reconcile the IP ranges

Under **Reconcile the IP ranges** on the **IP ranges** tab, click **Run
the check**. **EfficientIP reconcile** in **Integrations › Checks** counts
the same rows, and links to this tab. The check lists:

- **Addresses of VMs rvav no longer has**, such as an unregistered VM's.
  If the VM is gone from vCenter, select **Give back** in the row's
  **Actions** menu: the range can then give the address again. While
  vCenter still reports a VM on the address, rvav refuses to give it
  back: delete the VM or change its address first, or exclude the address
  from the range instead.
- **VMs on an address their range didn't give**: vCenter VMs, such as
  adopted ones, on an address of a range. Select **Exclude from** the
  range in the row's **Actions** menu, so that the range never gives the
  address to another VM, or move the VM. When another VM holds the
  address, one of the two must move.

The vRA import doesn't read vRA's network profiles: define their IP
ranges again in rvav before you import deployments on those networks.

## Active Directory

rvav can make each VM's computer object in an OU before the VM is built,
add it to groups, and delete it with the VM
([ADR-0064](../adr/0064-active-directory-computer-objects.md)).

To connect rvav to your domain:

1. Give a service account the rights in
   [integrations.md](../integrations.md#active-directory-permissions-service-account),
   on the OUs rvav will use only.
2. In **Integrations › Active Directory**, under **URL**, enter a domain
   controller's address: `ldaps://dc01.corp.example` for LDAPS, or
   `ldap://dc01.corp.example` for LDAP with StartTLS.
3. Under **Service account**, enter the service account's UPN
   (`svc-rvav@corp.example`) or DN. Under **Service account password**,
   enter its password, and under **Base DN**, the domain's base DN
   (`DC=corp,DC=example`).
4. Click **Test connection**. The test shows the domain controller's
   certificate. If rvav doesn't trust it, trust it or add its CA to the
   CA bundle (see [Trusted certificates](#trusted-certificates)), then
   test again: rvav binds and reads the base DN.
5. Click **Save settings**.

**Allow plain LDAP, without StartTLS** sends the password and every
object in the clear. Use it for a lab only: the form warns you, and
ticking it asks for the password again.

Saving the connection changes nothing yet: rvav makes computer objects
only for the projects and environments you give an OU.

### Give projects and environments an OU

An OU applies to a project in one environment, a project in any
environment, an environment in every project, or every VM. For a VM,
rvav takes the first that exists, in that order. With none, the VM gets
no computer object.

To give a project an OU:

1. In **Projects**, open the project, then the **Placement** tab.
2. Under **Active Directory**, click **New OU**.
3. Under **Environment**, select an environment, or **Any environment**.
4. Under **OU**, enter the OU's DN. A DN without `DC=` parts, such as
   `OU=Servers,OU=Web`, is relative to the base DN.
5. Optional: under **Groups**, enter the groups the computer objects
   join, one DN a line.
6. Optional: select **Blueprints may choose their OU**, to let a
   blueprint's `active_directory` name another OU, or none. Otherwise
   rvav ignores it.
7. Click **Create OU**. rvav checks that the directory has the OU and the
   groups. If the directory can't be reached, the OU isn't saved: try
   again once it is.

A role that manages placement in one project only, rather than for all
of rvav, is held to what you delegate: the project's OU goes under the
OU for every project (in that environment, or in any), and each group
under the project's own OU, or among the groups of that OU for every
project. Give every project an OU first: with none, such a role can't
save one. Groups that an administrator for all of rvav gave the
project's OU stay when the role saves it.

To give an environment an OU in every project, or every VM one, do the
same under **Projects › Environments › Active Directory**. A project's
**Placement** tab shows these too, for reference.

Removing an OU stops rvav making objects there. The objects already made
stay, and rvav deletes them with their VMs.

### What a request does in Active Directory

Before a VM in an OU is cloned, rvav creates its computer object, for
Linux and Windows VMs alike, and adds it to the groups. The request's
timeline shows the step, **Create the computer object**, with the
object's DN. A Windows VM that joins the domain takes the object, in its
OU. The deployment's page lists its objects under **Active Directory**.

- A name Active Directory already has, anywhere in the domain, is passed
  over: the VM gets the next number. A rename to such a name is refused.
- A VM name longer than 15 characters is refused, as Active Directory
  allows no longer computer names. This holds for every VM of a request:
  a later VM's number can make its name longer.
- The object is enabled, with a random password that no one is told. A
  domain join resets it. Over plain LDAP, Active Directory doesn't take a
  password, and the object has none.
- rvav never takes over an object it didn't make. When one with the VM's
  name appears after the request was submitted, the step fails: remove
  the object, then retry the request.
- When the clone fails, rvav deletes the object, and makes it again on a
  retry. A rollback and the deployment's deletion delete it too.
- When Active Directory refuses a delete, usually for the service
  account's rights, the request fails and says so. Fix the rights, then
  retry it.
- Renaming the VM renames its object. Rename a joined Windows VM in
  Windows too, and join a Linux one again. When the object is gone from
  Active Directory, the rename goes on and the deployment no longer lists
  it.
- rvav deletes or renames only an object where it made it, with the
  description it wrote. One that someone moved to another OU or
  described again is left, and the step says so.

## ServiceNow

rvav can keep each VM as a configuration item (CI) in ServiceNow's CMDB,
and take the requests that people order in ServiceNow's Service Catalog
([ADR-0065](../adr/0065-servicenow.md)). Both need the connection.

To connect rvav to ServiceNow:

1. In ServiceNow, make the account rvav signs in with, with the roles in
   [integrations.md](../integrations.md#servicenow-roles-rvavs-account):
   a user with **Web service access only** ticked, or an OAuth client
   (**System OAuth › Application Registry**, **Create an OAuth API
   endpoint for external clients**) whose **OAuth application user** is
   that user. ServiceNow offers OAuth clients the client credentials
   grant from its Washington DC release.
2. In **Integrations › ServiceNow**, enter the **Instance URL**, such as
   `https://corp.service-now.com`. rvav refuses `http://`.
3. In **Sign-in**, enter `basic` and the **User**, or `oauth` and the
   **OAuth client ID**. Then enter the **Password or client secret**.
4. Click **Test connection**. The test shows the instance's certificate,
   signs in and reads one requested item. If rvav doesn't trust the
   certificate, see [Trusted certificates](#trusted-certificates).
5. Click **Save settings**.

### Keep VMs as CIs

To keep VMs as CIs:

1. In ServiceNow, add `rvav`, or the discovery source you set in step 3,
   to the choices of the **Discovery source** field of `cmdb_ci`. Until
   it's there, ServiceNow refuses rvav's CIs, and the CMDB report says
   why.
2. In **Integrations › ServiceNow**, select **Keep VMs as CIs**.
3. Optional: change the **CI class** (`cmdb_ci_vmware_instance` by
   default) or the **Discovery source** (`rvav`). To keep only some
   projects' VMs, enter their IDs or codes in **Projects**, separated by
   commas.
4. Click **Save settings**.

rvav sends each VM through ServiceNow's Identification and
Reconciliation Engine: a CI that discovery already made for the VM, found
by its instance UUID, else by its managed object reference and vCenter,
is updated rather than doubled. rvav updates the CI after day-2 actions,
power changes and drift, and retires it, never deletes it, when the VM
goes. The CI's **Owned by** is the ServiceNow user with the deployment
owner's email, when exactly one has it.

The **CMDB** panel of the integration's page shows the CIs that rvav
keeps and has retired, and lists:

- **Deployments ServiceNow refused**, with ServiceNow's answer. rvav tries
  each again, from 10 seconds to 15 minutes apart.
- **CIs rvav no longer has**: CIs of rvav's discovery source, not
  retired, with no VM in rvav, such as a VM deleted while the CMDB was
  off. rvav doesn't retire them: decide in ServiceNow. When rvav can't
  read ServiceNow's CIs, the panel shows why instead of this list.

rvav reconciles every **Sync interval**, 30 minutes by default. To
reconcile within seconds, click **Reconcile now**.

Removing a project from **Projects** leaves its CIs as they are, and rvav
still retires them when their VMs go. Clearing **Keep VMs as CIs** stops
everything and retires nothing.

### Take requests from ServiceNow

People can order rvav's catalog items in ServiceNow. ServiceNow submits
each order to rvav for the person it's requested for, and rvav writes
the request's progress to the requested item (RITM): its work notes, its
state and the deployment's link. The request is that person's own, with
their entitlements, quotas, approvals and policies. rvav knows a person
from their first sign-in to rvav, and matches them by email, or, when
their ServiceNow user has no email, by their whole user name: their
identity provider's `preferred_username`.

ServiceNow also says who placed the order. To order for someone else,
that person needs `request.on_behalf` in the project, as in rvav, and
rvav's approval policies approve such an order automatically only when
their automatic approval holds for both people. rvav refuses an order
for or by someone whose last sign-in is older than
`RVAV_SCHEDULE_OWNER_IDLE_DAYS` (90 days by default), since their groups
may have changed.

To take requests from ServiceNow:

1. In **Integrations › ServiceNow**, under **ServiceNow's token**, click
   **Create a token**. Copy the token: rvav shows it once.
2. Optional: in **Allowed sources**, enter the instance's outbound
   addresses or networks, or its MID server's, separated by commas. rvav
   then refuses the token from anywhere else. To let ServiceNow submit
   more than 30 requests a minute, change **Requests a minute**. Click
   **Save settings**.
3. Install rvav's scripts in ServiceNow, with the token: follow
   [rvav in ServiceNow](../../integrations/servicenow/README.md).

The token is valid for a year, for a user of rvav's own, **ServiceNow
(rvav)**, and only ServiceNow's endpoints take it. Making a new token
revokes the one before, and **Revoke** cuts ServiceNow off at once. Both
are in the audit log.

A request ordered in ServiceNow shows **From ServiceNow** and the RITM's
number in its header, linked to the RITM, and its deployment shows the
RITM under **Details**. The audit log has each request made for
ServiceNow (`servicenow.request`): who it was for, who ordered it, the
RITM, the token and the address it came from. It also has those rvav refused
(`servicenow.request_refused`) and the calls over the rate
(`servicenow.request_limited`).

## Projects

**Projects** has three tabs:
- **Projects:** each project with its environments, members, deployments
  and quota use: the project-wide limit nearest to being reached. **New
  project** asks for its code, which can't change later, its name,
  description, cost centre, the environments it deploys to and its first
  project admins. Archive a project from its menu: nobody can request
  anything new in it, and its deployments go on running. To put one back
  in use, select **Archived ones too**, then **Put back in use** in its
  menu.
- **Environments:** where projects deploy, whether each is production,
  the snapshots kept per VM, and **Tags users may change**: the vSphere
  tag categories whose tags the users of the environment's VMs change
  with **Edit tags**. The list offers vCenter's categories for VMs, with
  their tags, as the last inventory sync read them. Edit tags never
  touches the tags of other categories, so leave out those that rvav or
  your tools rely on, such as the ones blueprints set. A new
  environment allows none; the sample data allows `backup` in
  Development, Test and Production. You can't remove an environment that
  a project deploys to or that has deployments in it.
- **Identity provider groups:** the roles that groups of the identity provider
  give ([Roles and permissions](#roles-and-permissions)).

A project's page has four tabs:
- **Members:** **Deployment sharing**, then who holds a role in the
  project, and where each role comes from. To add someone, choose the role
  in **As**, then find them in **Add someone**. A member's menu changes
  their roles or removes them.
- **Placement:** each environment's placement rules, lowest priority
  first, and what the project gets there: the clusters, datastores,
  networks and folder a request would use, or what stops it.
- **Quotas:** the limits on vCPU, memory, storage and VMs, for the whole
  project or one environment, with what the project takes now, and
  **Each member's quota**: the most each member may hold in the project.
  A hard limit refuses a request that would pass it; a soft one asks for
  an approval. See [Quotas](#quotas).
- **Details:** name, cost centre, description, environments and custom
  properties; **Archive**. Each VM built in the project gets the custom
  properties as vSphere custom attributes and in its notes. Bindings,
  approval conditions and a composite item's steps read them as
  `project.properties.<key>`. A key has letters, digits, `.`,
  `_` and `-`, and starts with a letter. A blueprint's own properties of
  the same key win. VMs built before a change keep the properties they
  were given.

Each tab is in the page's address, so a link can open it, for example
`/admin/projects?tab=groups`.

Nobody can delete a deployment whose blueprint sets `prevent_delete`: not
its owner, not a project admin, and not the end of its lease. **Delete**
names the VMs it keeps. When its lease ends, rvav tells the owner once, and leaves
the requests waiting on it, such as a lease extension waiting for
approval. To let it be deleted, open the deployment and select
**Allow deletion** in its **Actions** menu. You need `project.update` in
its project, which administrators hold.

### Deployment sharing

By default, everyone who sees a project sees all its deployments. To show
members only their own, open the project's **Members** tab, choose
**Owners only** in **Who sees them**, then select **Save sharing**
([ADR-0045](../adr/0045-deployment-sharing-and-project-policies.md)).
You need `project.members.manage` there, which project admins hold.

With **Owners only**:
- a member sees the deployments they own: those they requested, those
  requested for them, and those handed over to them. They see the
  requests they made, those made for them, and those that built or
  changed a deployment they own;
- whoever holds `deployments.read_all` in the project sees all of them:
  project admins, supervisors, auditors and administrators, and the
  custom roles you give it;
- an approver still reads the request they're asked to approve;
- the rule holds everywhere: lists, search, a deployment's page and
  actions, updates, consoles, reports, pickers in forms, requests and live
  updates. A link to a deployment someone no longer sees answers "not
  found".

To share one deployment with a colleague, hand it over to them. The owner
of a deployment always sees it, even with a custom role that doesn't hold
`project.see`. `PUT /api/v1/projects/{id}/deployment-sharing` with
`{"deployment_sharing": "owner_only"}` does the same, and the audit log
has each change as `project.sharing.set`.

## Roles and permissions

What someone may do comes from the permissions their roles hold
(ADR-0034). `GET /api/v1/permissions` lists them, each held in a project
or only for all of rvav. The built-in roles can't be changed:

| Role | Given | Holds |
|---|---|---|
| Administrator (`admin`) | For all of rvav | Every permission. Day-2 actions are decided by the day-2 policies. |
| Catalog admin | For all of rvav | `catalog.manage`. |
| Auditor | For all of rvav | Every read permission: projects, all their deployments, their settings and audit log, every approval, environments, access, infrastructure, policies, extensibility and the inventory. |
| Project admin | In a project | Seeing it, all its deployments included (`deployments.read_all`), requesting (for others too), its requests, approvals, members, deployment sharing, settings and audit log, and writing its own approval, day-2 and lease policies (`project.policies.manage`, [Project policies](#project-policies)). |
| Member | In a project | Seeing it and requesting. |
| Viewer | In a project | Seeing it. |
| Supervisor | In a project | Seeing it, all its deployments included (`deployments.read_all`), and approving: it is one of the project's approvers (`approval.project`), asked when a lease is extended beyond its policy, and wherever a level names it ([Who approves](#who-approves)). It doesn't request or run day-2 actions. |
| User | To everyone | Nothing. |

To give someone less, or a mix, create a role of your own in
**Administration › Roles** (`access.manage`, which only administrators
hold). Select **New role**, or open a built-in role and select **New
role from it** to start from what it allows. Choose its key, fixed once
created, its name and its permissions, grouped by area; **What it allows**
says in words what you chose. `POST /api/v1/admin/roles` does the same.
- Whoever holds `access.manage` can give themselves any permission: a
  role with it makes its holders administrators in all but name.
- Whoever holds `project.create` names a new project's first project
  admins, themselves included: those admins request for others, approve
  at the project's level and manage its members there, in the
  environments the project is given.
- A role holding a permission that exists only for all of rvav, such as
  `policies.read`, can only be given for all of rvav.
- A role given in a project must hold `project.see`.
- A role that holds `placement.manage` without `project.settings.read`
  opens the project's page in **Administration › Projects**: its
  **Placement** tab, to change the placement rules and OUs, and its
  **Members** and **Details** tabs, read only unless the role holds their
  permissions too. It shows no **Quotas** tab.
- Pages that pick or name projects list every project by name to whoever
  holds the page's permission for all of rvav, without
  `project.settings.read`: offers in **Templates and networks** and
  naming policies, **Policies**, **Custom workflows**, **Map a group** in
  **Identity provider groups**, **Onboarding**, **vRA import**, **What someone
  may do**, and the **Audit log**'s **Project** filter, archived projects
  included (`GET /api/v1/admin/project-names`: ID, code and name only).
  To adopt VMs into a project in **Onboarding**, or to save a plan that
  does, you also need `project.see` there: the environments, owners,
  items and deployments offered are the project's.
- `day2.<group>` allows a group of day-2 actions, `day2.<group>:<action>`
  one of them, for example `day2.power:power_off`. A day-2 policy that
  matches decides first; with none, the roles' day-2 permissions decide.
- A role still given, mapped to a group, named by a day-2 policy, or
  named by a level of an approval or day-2 policy can't be deleted: in its menu, in the list or on its page, **Delete** says
  why, for example "Delete: given 3 times". Otherwise rvav asks before
  deleting it.

To give a role:
- in a project, to a member: on the project's page, **Members**, choose
  it in **As** and add them, or select **Change roles** in their menu to
  give or take away the roles given there
  (`PUT /api/v1/projects/{id}/members/{user}` with `roles`). Someone may hold several roles in a project, each given
  there or through a group. A project admin is offered only the roles
  whose permissions they hold in the project;
- in a project, to a group: in **Projects › Identity provider groups**, select
  **Map a group**, then choose the project and the role;
- for all of rvav, to a user: in **Administration › Roles › For all of
  rvav**, choose the role, then the person in **Give someone**
  (`POST /api/v1/admin/role-assignments`). To take it away, select
  **Take it away** in their row's menu, then confirm;
- for all of rvav, to a group: a group mapping with **For all of rvav**
  instead of a project.

To find out why someone can't do something, open **Administration ›
Roles › What someone may do**, choose them in **Who** and the project in
**Where**: each permission, held or not, lists the roles that grant it
and where each comes from, given to them, through a group or set by rvav
(`GET /api/v1/admin/access/explain`, `access.read`; **Where** lists the
projects of `GET /api/v1/admin/project-names`, which `access.read` is
enough for). Anyone sees the same
about themselves in **What I may do**, in their menu.

rvav refuses a change that would leave no one holding `access.manage`, or
a project with no one holding `project.members.manage`. A change applies
to the user's next request, and their open pages load again.

Policies' conditions see `user.roles`, the keys of the roles held (yours
too), and `user.permissions`, those held in the request's or deployment's
project. The audit log has each change as `role.*`, `role_assignment.*`,
`member.*` or `group_mapping.*`.

### Example: an operator and an auditor

To let someone power a project's VMs on and off, and nothing else:
1. In **Administration › Roles**, select **New role**.
2. Enter the key `vm-operator` and the name `VM operator`.
3. Select `project.see`, then **Power on** and **Power off** among the
   power day-2 actions. **What it allows** says "Run the power day-2
   actions Power on and Power off."
4. Select **Create role**.
5. On the project's page, under **Members**, choose **VM operator** in
   **As** and add the person. To give it to a team, map their OIDC group
   to it in **Projects › Identity provider groups**, with the project.

On the project's deployments, the operator's VM menu offers **Power off**,
or **Power on** once the VM is off. Every other action is disabled, the
console too, with "Your role in <project> does not allow <action>." They
can't request in the project and don't see the others. The seeded day-2
policies still let a deployment's owner run its actions: the role decides
on deployments the operator doesn't own.

To let someone see everything and change nothing, under a name of your
own:
1. In **Administration › Roles**, open **Auditor** and select **New
   role from it**.
2. Enter the key `compliance-auditor` and the name `Compliance auditor`,
   and keep the permissions as they are.
3. Select **Create role**. It holds permissions for all of rvav only, so it
   can only be given for all of rvav.
4. In **For all of rvav**, choose **Compliance auditor** in **The role**,
   then the person in **Give someone**.

The compliance auditor then reads what the built-in auditor reads,
administration pages and audit log included, with no way to change them;
the API answers them as it answers the built-in auditor. To check either
role, open **What someone may do**, choose the person, and the project or
**For all of rvav**.

## Policies

**Policies** has a tab each, in the page's address, for example
`/admin/policies?tab=lease`. **What applies here** shows, for a project
and an environment, which policies would apply to a request and why.

| Tab | What it decides |
|---|---|
| **Approval** | Which requests need approval, from whom (users, groups or project roles, in levels, any or all), and what happens on timeout (escalate, approve, reject). Conditions are CEL over the inputs, environment, project, item, VMs, user and quota, how the request was made, and what an update changes (see [Updates](#updates)). |
| **Lease** | Default and longest leases per environment or project, extensions, the lifetime, the end (power off then delete, delete, or only tell the owner), and the enforcement. Lease rules give another lease to the deployments a condition matches (see [Lease rules and lifetime](#lease-rules-and-lifetime)). |
| **Day-2** | Who may run which action, where, which need approval (deleting in production, by default), and the enforcement. You tick **Roles** by name: **Owner** for the deployment's owner, the built-in roles a day-2 action is given to, and the custom roles. With none ticked, the policy applies to anyone. |
| **Limits** | How many deployments each user or each project may have, and how much vCPU and memory each deployment may take (see [Deployment limits](#deployment-limits)). |
| **Naming** | Name templates (`{project}-{environment}-{inputs.role}{seq:03}`), offered by item, project or environment, with where the counter starts and how much it moves (**Start at** 100 and **Increment** 10 give 100, 110, 120). A counter in use goes on from its last number, and jumps to the start when you raise it past that number. Names are never given twice, and are checked free in vCenter and DNS. A policy wins over the item's own naming; where none applies, the item's names its VMs, and a request for an item without one is refused. |
| **Quotas** | What all of rvav may hold, and what each user may hold across rvav (see [Quotas](#quotas)). |

Each tab is a list. To add a policy, select **New approval policy** (or
the tab's own); to change one, select **Change** in its row's menu. The
form opens in a side panel. The row menu also moves approval and day-2
policies up or down, sets their priority, and turns them, and deployment
limit policies, off or on. Before **Remove** removes a policy, rvav says
what changes, such as which policy decides instead.

Upgrading from a release without **Edit deployment** adds it to the day-2
policies that let only the owner and the project admins delete a
deployment or hand it over, with the condition `true`, as the demo's do.
Other members then can't edit the deployments of others. If you wrote
other policies on those actions, add **Edit deployment** where it should
apply too.

### Quotas

A quota limits vCPU, memory, storage and VMs at five levels
([ADR-0047](../adr/0047-user-and-global-quotas.md)). A request must fit
every level that applies to it:

| Level | Where you set it | What it counts |
|---|---|---|
| The project | The project's **Quotas** tab | The project's deployments, and its requests not yet done. |
| The project in one environment | The project's **Quotas** tab | The same, in that environment. |
| Each member of the project | **Each member's quota**, in the project's **Quotas** tab | What each member owns in the project, and their requests not yet done there. |
| All of rvav | **All of rvav**, in **Administration › Policies › Quotas** | What every project holds together. |
| Each user across rvav | **Each user across rvav**, in **Administration › Policies › Quotas** | What each user owns in all projects together. |

A limit is the same for every member, or every user: rvav has no limit
for one named person. A request made for someone counts for them, and its
message says so, for example "Thomas Nguyen's quota in Web Platform has 2
left". Handing a deployment over checks the new owner's quotas; moving it
to another project checks that project's. When an approver changes a
request's inputs, the change is checked against the quotas of whoever
owns the request, for what it adds to the request's size: a smaller size
always fits. The approval's **Quota after** line shows, for vCPU and
memory, the quota with the least room left once the request is done,
and whose quota it is.

rvav checks requests sent at the same time one after the other, so they
can't all take the last of a quota or of a deployment limit: one fits,
and the others are refused. A handover or a move that isn't done yet,
waiting for approval included, counts for the new owner or in the new
project, both for quotas and for deployment limits.

A request whose rollback finished gave back its quota, its deployments'
place in the deployment limits, and their names. Retrying it takes them
again, so it's refused when they no longer fit or another deployment of
the project took one of the names since. Its approval stands: a soft
quota or limit it now goes over doesn't ask again. A retried day-2
action is checked against hard quotas and limits as if it were asked for
now.

The request form and the update's summary show one meter per resource:
the level that refuses, else the one with the least left, named after its
level ("vCPU · per member").

Who sets each level:
- `project.quotas.manage`, which administrators hold, sets a project's
  levels. Held in every project, it also sets rvav's.
- A project's admins (`project.policies.manage`) set **Each member's
  quota** of their project, within the project's own quota: the side
  panel says how much it allows. They read, but don't change, the lines
  an administrator set.
- The **Quotas** tab of **Administration › Policies** shows to those who
  read every project's settings (`project.settings.read`).

### Deployment limits

A deployment limit policy limits how many deployments each user or each
project may have, and how large each deployment may be
([ADR-0047](../adr/0047-user-and-global-quotas.md)). In
**Administration › Policies › Limits**, a policy sets any of these:

| Limit | What it counts |
|---|---|
| **Deployments per user** | The deployments each user owns in the project, in the policy's environment or in all. |
| **Deployments per project** | The project's deployments, in the policy's environment or in all. |
| **vCPU per deployment** | The vCPU of all of a deployment's VMs together. |
| **Memory per deployment (GB)** | The memory of all of a deployment's VMs together. |

A policy is for every project or one, and for every environment or one,
with a priority, an enforcement, and **Enabled**. A limit left empty
leaves it to other policies. A policy that sets no limit is refused.

Every deployment not deleted counts: a failed one, one still being built,
and one whose request waits for approval. A multi-VM item makes one
deployment with all its VMs; a single-VM item makes one deployment for
each VM; a composite item makes the deployments of its VM steps, and is
as large as the largest of them.

Each limit is decided on its own: the first enabled policy, by priority,
that sets it and applies to the request's project and environment
decides. A global policy comes first on a tie. Every hard global policy
that sets the same limit holds too, each counting in its own environment
or in all, and a request must fit all of them. So a project's policy,
hard or soft, tightens a hard global limit and never loosens it. For
example, with a global limit of 1 deployment per user and a project's
limit of 1 per user in Development, a user who has a deployment in Test
can't request one in Development. A soft global policy gives way to a project's
policy tried before it. To tighten a limit, give the project's policy a
lower priority than the global one: a project's policy tried after the
one that decides doesn't apply. **What applies here** lists each limit
with the policy nearest to being reached, and says why the others don't
decide it.

rvav checks the limits:
- on a new request, as the form is filled and when it's submitted;
- on an update and a resize, against the size the deployment has
  afterwards, only when they grow its vCPU or memory;
- when a deployment moves to another project, against that project's
  limits;
- when a deployment is handed over, against the new owner's count;
- when an approver changes a request's inputs, against the size limits,
  only when the change grows the request's vCPU or memory.

A hard limit refuses, and says how to go on, for example "Deployments in
Web Platform may have at most 8 vCPU under Web limits, and this one would
have 16. Choose a smaller size, or ask an administrator to raise the
limit." A soft limit warns, and the requester may go on: see
[Soft and hard enforcement](#soft-and-hard-enforcement).

Project admins write their projects' own deployment limit policies, as
other project policies (see [Project policies](#project-policies)). The
audit log has `policy.limit.create`, `policy.limit.update` and
`policy.limit.delete`.

### Lease rules and lifetime

A lease rule gives its lease to the deployments its condition matches,
for example GPU servers or small VMs
([ADR-0048](../adr/0048-lease-rules-and-lifetime.md)). The **Lease** tab
lists the rules under the lease policies. A rule has:
- a **Name** and a **Priority**: lower is tried first, and at the same
  priority a rule for every project comes before a project's;
- an **Applies when (CEL)** condition, which reads what approval
  conditions read, for example `item.slug == "gpu-server"` or
  `inputs.size == "S"`;
- an **Environment** and a **Project**, or every one;
- the lease it gives: the default and longest days, extensions, grace
  days, lifetime, end and enforcement.

A request gets the lease of the first rule that matches it. When none
matches, it gets its project's lease in the environment, else the
environment's lease. A rule for every project therefore wins over a
project's lease.

A deployment keeps the rule it got when it was requested, and follows
the rule's changes, except its end: power off then delete, delete, or
notify only stays what it got at request. When you remove the rule, or
the deployment moves to a project the rule isn't for, it gets its
project's or environment's lease. Deployments that rvav takes over from vCenter or vRA get the lease
policies', not a rule's.

When a project admin's rule decides a deployment's lease, the deployment
also keeps the first matching rule an administrator wrote, if any. It
then has the stricter of the two rules' terms, so a project admin who
edits their rule later can't give it more than the administrators' rule
allows. When the project admin's rule is removed, the administrators'
rule decides, not the project's or environment's lease. A deployment
requested before rvav kept the administrators' rule is held to the lease
policies only.

When a rule's condition can't be read, for example because it needs an
input the item's form doesn't have:
- the request form's preview and **What applies here** skip the rule,
  and **What applies here** says why;
- on submit, the rule applies only if it gives no more than the lease
  without rules.

The **Lifetime, in days** caps how long a deployment lives in all,
counted from its creation, or from when rvav took it over. Under a hard
lease, rvav refuses a request for a longer lease, and an extension past
the lifetime asks the project's approvers. Under a soft lease, both warn,
and the requester may go on. The lifetime is apart from the extensions:
once all extensions are used, the next one needs approval even within the
lifetime. A lease's default and longest days can't be longer than its
lifetime. Setting a lifetime doesn't shorten the leases already given.

The request form says which rule or policy gives the lease and the
lifetime. The deployment's **Lease** panel says which one it is under and
how many days it may still live. In **What applies here**, give an item
and **Inputs (JSON)**, such as `{"size": "S"}`, to see which rule decides.

### Project policies

Project admins write the approval, day-2, lease and deployment limit
policies of their projects (`project.policies.manage`,
[ADR-0045](../adr/0045-deployment-sharing-and-project-policies.md)). In
**Administration › Policies**, they see the global policies and their
projects' own, and **What applies here** for their projects. They change
only their projects' policies: the others have no menu. A project admin:
- picks one of their projects in the side panel's **Project**: a policy
  for all projects needs `policies.manage`, which administrators hold;
- reads, but doesn't change or remove, a policy of their project that an
  administrator wrote last: its row says **Set by an administrator** and
  has no menu;
- doesn't see the **Naming** and **Quotas** tabs: naming policies and
  rvav's quotas stay administrators';
- sets a policy's priority instead of moving it up or down, which
  renumbers every project's policies;
- can't write a lease override or a lease rule looser than what
  administrators set: a longer maximum, no lease where the environment
  gives one, more extensions or grace days, a gentler end, a
  longer lifetime or none, or **Soft** over a hard lease. An override is
  held to the environment's lease. A rule is held, in each environment it
  may apply in, to the project's lease an administrator set, else to the
  environment's. rvav says which, for example "Development allows 90 days
  at most."

Every change is in the audit log with its project, as `policy.approval.*`,
`policy.day2.*`, `policy.lease.*`, `policy.lease_rule.*` or `policy.limit.*`.

When you change a project's policy with `policies.manage`, it becomes
yours: the project's admins can no longer change or remove it. A project
policy written before G1.4, by anyone, is an administrator's. To give one
back to the project's admins, remove it and let them write it again.

Project and global policies combine as follows:
- **Approval:** every policy that matches adds its levels. A project's
  policy adds approvals, and never removes a global one's. Levels that ask
  the same approvers become one level only when their policies are all
  global, or all the same project's: a project's level never changes a
  global level's timeout or escalation.
- **Day-2:** the first policy that matches, by priority, decides, global
  or not. When a project's policy decides, the first global policy that
  matches after it still wins if it is hard and stricter: a deny over an
  approval, an approval over a warning, a warning over an allow. A
  project's policy can deny what a global one allows, but can't allow what
  a hard global one denies.
- **Day-2 approval:** when a project's approval and a hard global approval
  both match, the action needs both: the global policy's levels first,
  then the project's.
- **Lease:** the first lease rule that matches decides, global or not.
  When none matches, a project's override replaces the environment's lease
  for that project. A project admin's rule that would give more than the
  first matching rule an administrator wrote, or, without one, than the
  lease an administrator set, is skipped: the next rule, or the lease
  policies, decide.
- **Deployment limits:** each limit is decided on its own, by the first
  policy by priority that sets it. Every hard global policy that sets it
  holds too, so a project's policy only tightens it (see
  [Deployment limits](#deployment-limits)).

**What applies here** names the policy that decides and says why, for
example when a global policy wins over the project's.

### Soft and hard enforcement

Lease, day-2 and deployment limit policies have an **Enforcement**,
**Hard** by default:
- **Hard** refuses, or asks for approval, as the policy says.
- **Soft** warns, and the requester may go on.

A soft lease policy or rule warns when a request asks for a longer lease
than its maximum or lifetime, and when an extension goes beyond its days,
extensions or lifetime; that extension then needs no approval, as the
deployment's **Lease** panel says. A soft day-2 policy with the effect
**Deny** or **Approval** leaves the action enabled, and warns with the
policy's **Reason people see**. A day-2 policy with the effect **Allow**
can't be soft. A soft deployment limit warns when a request, an update
or a move goes beyond it. Approval policies always apply: to warn instead
of asking for approval, write a soft day-2 policy with the effect
**Approval**. The
policy list shows a soft policy as **Soft: warns**, or a soft lease as
**Soft: longer warns**.

The request form, the action's dialog, the update's review and the lease
extension show the warning; the requester selects, for example,
**Submit request anyway**. The request keeps the warnings, its page lists
them under **Policy warnings**, and the audit log has
`request.warnings.accept`. Actions on several deployments at once skip
those whose action warns.

Through the API, previews list the warnings (`warnings`), each with an
`id`. To go on despite the warnings you showed the requester, send their
IDs in `accepted_warnings`. If rvav finds a warning that isn't listed, for
example from a policy saved since the preview, it refuses the request
with 422 and one error per warning, at `/accept_warnings` with the code
`warning` and the `warning` itself. A script that goes on regardless can
send `"accept_warnings": true` instead, without a list.

### Who approves

Each level of an approval policy, or of a day-2 policy with the effect
approval, asks one or more of these
([ADR-0041](../adr/0041-approvers-by-role.md)):
- **An OIDC group** (`group`): its members, as their token says when they
  decide.
- **Users** (`approvers`), by ID.
- **Project roles** (`roles`), built-in or custom, ticked by name (the
  API takes their keys): whoever holds one in the request's project when
  the level opens, given it themselves or through a group mapped to it
  there. A role held for all of rvav
  doesn't count. One policy can then ask every project's supervisors.
- **The requester's manager** (`manager: true`): the user the
  requester's manager claim names, by email or subject, as rvav stored it
  at their last sign-in ([the identity provider](#the-identity-provider)).
  The manager must have signed in to rvav once.

`GET /api/v1/admin/approval-policies` lists the roles a level may name in
`level_roles`: the roles given in projects. A level that names a role
given for all of rvav only, such as `auditor`, or no role at all, isn't
saved.

`GET /api/v1/admin/day2-policies` also lists, in `policy_roles`, the roles
a day-2 policy's `roles` may name, by key and name: `owner` (the
deployment's owner), the built-in roles `viewer`, `member`,
`project-admin`, `supervisor` and `admin`, then the custom roles. Its side
panel ticks them by name, and its list names them.

Both lists also return `users`: the
users their levels ask, as approvers or to escalate to, by ID and name,
without email. A project admin gets the names on the policies they read,
the global ones and their projects'. A user rvav doesn't know isn't in
`users`.

In **Administration › Policies**, the side panel of an approval or day-2
policy gives each level an **OIDC group**, the **Project roles** it asks,
one checkbox per role in `level_roles`, and **The requester's manager**.
The policy list shows whom each level asks by name, for example
"rvav-infra-leads, Marc Keller" or "Supervisor, the requester's manager".
A user rvav doesn't know shows by ID. On a request's page and in the
approvals inbox, a level that asks roles or the manager says so, for
example "Asks whoever holds Supervisor in the project and the
requester's manager".

In mode `all`, every user the level names, who holds one of its roles,
or is the requester's manager must approve. Groups are never required in
full: a level of groups only needs one approval.

A requester without a manager, or whose manager has never signed in, has
none on that level: the level asks its other approvers. When a level
opens and nobody but the requester can decide it, rvav rejects it at
once, even when it would approve itself on time-out, and says why, for
example "Rejected automatically: nobody can approve it, as nobody holds
Supervisor in Web Platform", or "as the requester has no manager". The
requester is told, and the audit log has `approval.unanswerable`. Give the
role in the project, or the manager at the identity provider, and ask
them to request again. A level with a group always waits: its members may
sign in later.

A lease extension beyond the lease policy, its extensions or its
lifetime asks the project's approvers,
in a level named **Project approvers**: whoever holds a role with
`approval.project` in the project, such as Project admin or Supervisor.
When nobody but the requester holds one, rvav refuses the extension.
A role that a level names can't be deleted: change the policy first.

### Updates

An update changes a deployment's inputs, moves it to a version its item
offers (the newest by default), or adds and removes VMs
([ADR-0042](../adr/0042-deployment-updates.md)). It runs as a request of
kind `update`, with the native day-2 action **Update** (`update`), and
goes through the same checks as a new request:
- **Quota** checks the net change in vCPU, memory, storage and VMs. A
  hard limit refuses the update, a soft one asks for approval. Removing
  VMs or making them smaller frees quota once the update has run.
- **Deployment limits** check the deployment's vCPU and memory after the
  update, only when the update grows them: an update that keeps or
  shrinks a deployment already beyond a limit goes on.
- **Approval policies** apply to updates as well as new requests. Their
  conditions read the kind of request, how it was made, and what the
  update changes:

  | Variable | Value |
  |---|---|
  | `request.kind` | `provision` for a new request, `update` for an update. |
  | `request.via` | `rvav`, `schedule` for a schedule's run, or `servicenow` for an order placed in ServiceNow. |
  | `request.ordered_by` | The ID of who ordered it: the requester's own, but for an order placed in ServiceNow for someone else. |
  | `update.version_changed` | `true` when the update moves the deployment to another version. |
  | `update.from_version`, `update.to_version` | The version numbers, such as `3`. |
  | `update.vcpu`, `update.memory_gb`, `update.storage_gb`, `update.vm_count` | The net change, negative when the update frees some. |
  | `update.added`, `update.removed` | The number of VMs added and removed. |
  | `update.changes` | The kinds of change, such as `["resize", "vm_add"]`: `resize`, `disk_add`, `disk_extend`, `disk_remove`, `nic_add`, `nic_move`, `nic_remove`, `vm_add`, `vm_remove`, `job`, and for custom resources `resource_add`, `resource_update`, `resource_remove` ([ADR-0056](../adr/0056-custom-resource-types.md)). |
  | `update.destructive` | `true` when the update deletes a disk, an adapter, a VM or a custom resource. |

  On a new request, the `update` numbers are 0 and its list is empty, so
  conditions you wrote before keep their meaning. For example, to ask for
  approval of updates that add more than four vCPU in production, use
  `request.kind == "update" && environment == "prd" && update.vcpu > 4`.
  For a policy that applies only to new requests, add
  `request.kind == "provision"`.
- **Day-2 policies** decide who may run **Update**, where, and whether it
  needs approval, as for any other action. When an approval policy and a
  day-2 policy both ask, the update waits for both.
- **Approvers can't change the inputs of an update:** they approve or
  reject what the requester saw.

The form's computed fields, such as REST and CEL value sources, are
computed again from the new inputs, and its validations run on submit, as
for a new request. Changes that need a VM built again are refused with
their reason, including another template, cluster, datastore or network.
Users then delete the deployment and request it again.

An update also makes, updates and deletes the deployment's custom
resources with their type's steps
([ADR-0056](../adr/0056-custom-resource-types.md)). Those the new version
adds are made once the VMs are built. Those whose inputs may change are
updated, and when their type has no update step, the update is refused.
Those it
drops are deleted, before any VM the update removes. A resource whose
type changes is made under the new type before the old one is deleted. A
composite's resources stay as they are.

An update matches the extra disks of the deployment's version and the new
one by their `key` when every extra disk of both versions has one, else
by their place. Matched by place, a version that removes extra disks and
changes others is refused, as rvav can't tell which disks go. To remove
some disks and change others in one version, give every extra disk a key
([ADR-0052](../adr/0052-disk-keys.md)):
1. Publish a version that only adds a `key` to each extra disk. Its
   update changes nothing on the VMs.
2. Publish the version that removes and changes disks.

Items the vRA importer makes have their disks keyed already.

To update a deployment through the API, send the same body to
`POST /api/v1/deployments/{id}/update/preview` to read the plan, then to
`POST /api/v1/deployments/{id}/update` with an `Idempotency-Key` header:

```json
{
  "version_id": "ver_…",
  "inputs": { "size": "M", "vm_count": 3 },
  "justification": "More capacity for the release"
}
```

`version_id` is the version to move to: the item's published version,
or another one it offers (`versions` on `GET /api/v1/catalog/items/{id}`).
Send every input of the form: an input left out takes its field's
default, and `null` empties it.

## The catalog

**Catalog items** has four tabs: **Items**, which you can search and
filter by kind, category and state; **Categories**; **Secrets**, for REST
value sources and calls, Windows guests, cloud-init user data
(`{{ secret "name" }}`) and AWX steps (`${secret.name}`); and **AWX
templates**, those imported from AWX. A secret's **Used by** lists the items
that name it, and the templates whose user data reads it
(`template:<key>`): you can't remove it while one does.
To give one project its own value for a secret, store a secret of the same
name and pick the project in **Project**: that project's requests open it
rather than the global one, and other projects never open it. Its row says
**Opened instead of the global one**. rvav refuses a request when its item
opens a secret that neither its project nor the global ones have, and
names the secret. You can remove a project's own secret while items use
it, as long as a global one of its name remains.
A row's menu has what you can do with it, and removing anything asks
first and says what depends on it. An item that was ever requested can't
be deleted: withdraw it instead.

An item is a form and a blueprint
([service-catalog.md](../service-catalog.md)):
- **a VM blueprint:** one VM or several, with their guest settings, and
  with components, custom resources of your
  [resource types](#define-a-resource-type);
- **an AWX job or workflow:** imported from AWX's templates with the
  catalog label, their survey becoming the form;
- **a composite:** VMs, jobs, custom workflows, custom resources, REST calls and waits for a person, a time or a condition in steps;
- **a custom workflow**, run by your own worker.

**New item** makes the item's first draft: a form with one **Name** input,
and a blueprint for its kind. Choose its **Category**, which the catalog
groups it under: rvav refuses an item without one, at that field. A VM blueprint's VM has 2 vCPUs and 4 GB of
memory (`bindings.vcpu`, `bindings.memory_gb`): change them on its
**Definition** tab, or bind them to a size input
([VM blueprints](../service-catalog.md#vm-blueprints)). To publish a VM
item, give it its template, and give each VM its CPU and memory: rvav
refuses one without, or with a plain number below 1, at that binding.
In the YAML, `memory_gb: 4` and `memory_gb: "4"` mean the same. Saving a
draft refuses, at its field, a binding that is a list, and CEL that reads
an input the form doesn't have, such as `inputs.zone` without a `zone`
input.

A VM blueprint's VMs are named `{project}-{environment}-{component}{seq:03}`
(`naming`): `web-dev-001` for its one VM, `web-dev-db001` for a `db`
component's. A naming policy that applies to the project and environment
wins over it ([Policies](#policies)). You can publish a VM item without a
naming, for VMs a naming policy names, but where no policy applies, a
request for it is refused and asks the requester to have you check the
item. Its **Definition** tab warns of it under the YAML, at `naming`, or
at a component that has none when others have one, unless a naming
policy applies everywhere.

An item's page has its main action in its header: **Publish** the
draft, or **New draft** from the published version. Publishing waits
while the form designer saves, or while YAML you typed isn't saved, and
says why beside the button. The **⋯** menu beside it opens the request
form, exports every version as YAML, withdraws the item (every published
version at once), and deletes an item nobody requested. Publishing doesn't change the
deployments made from earlier versions: their owners move them to the new
version with **Update** ([Updates](#updates)).

An item's tabs:
- **Details:** name, category, icon, description.
- **Form designer:** fields, sections, rules, value sources (lists from
  vCenter, AWX, EfficientIP or a REST call), and fields approvers may
  change. Preview the form as any user. Choose how its sections show in
  **Layout**:
  - **One page:** every section on one page, one after the other.
  - **Wizard:** a page per section, with **Back** and **Next**, then a
    review of what was entered before submit. **Next** checks the page's
    inputs first.
  - **Tabs:** a tab per section.

  The field list then names the sections pages or tabs. To order them,
  use their up and down arrows. The preview shows the layout you chose,
  on the page or tab of the field or section you select. A page or tab
  whose fields a `visible` rule all hides is left out, and its inputs
  aren't checked. The layout changes only how the form shows: rvav
  checks a request the same way whatever it is.

  To fill a field from other inputs and leave it editable, give it a
  **Default** rule, in CEL, such as `inputs.app + "-" + environment`.
  The form shows the default until the requester changes the field, and
  updates it as the inputs it reads change. Once the requester changes
  the field, even to empty it, the field keeps their value. A read-only
  field always shows its default, which the request gets. On a cluster,
  datastore or network dropdown, the default rule takes the place of the
  option rvav suggests. A request
  that leaves the field out gets the default, and a wizard's review
  lists it. A **Computed value** rule instead gives a value the
  requester can't change. On a text, number, date or toggle field, a
  REST or CEL **Value source** gives the field its default: for REST,
  set **Item** and **Value** to the JSONPaths of the value in the
  answer; for CEL, write a **Value** expression. On a computed field,
  the source gives its value. A field takes one default: rvav refuses a
  default rule beside an initial value, a computed value or a source
  that gives a default. When a default can't be worked out, the field's
  properties say why, under **Rules**.

  Each field type has its own settings
  ([field types](../service-catalog.md#field-types)):
  - a number can **Allow decimals**, with a minimum, a maximum and a step;
  - a date, or a date and time, takes the **Earliest** and **Latest** it
    accepts, a time in your browser's time zone;
  - tags take **Suggestions** and the **Fewest tags** and **Most tags**;
  - a table takes the **Fewest rows** and **Most rows**, and its
    **Columns**: each has a key (the property of its rows), a label, a
    widget (text, number, toggle or dropdown, with its **Choices**) and
    whether every row needs it;
  - a group of fields takes its **Sub-fields**, set as a table's columns
    are: each edits one property of the object;
  - an image takes its **Address**, its **Text alternative** (what it
    shows, for those who can't see it) and a **Caption**; a link takes
    its **Address**, and its label is the link's text. An address starts
    with `https://` or `http://`, or is a path on this site. To publish,
    give the address, and an image's text alternative. An image from
    another site loads only if that site is in `RVAV_WEB_IMAGE_SOURCES`.
    To show a picture rvav keeps instead, select **Upload a picture** and
    choose a PNG, JPEG, GIF or WebP file of 2 MB at most: its address
    becomes `upload:<id>`;
  - a multiple choice dropdown, a checklist or a dual list takes its
    choices from its options or a value source, as a dropdown does; a
    dual list also takes the **Fewest picks** and **Most picks**, and
    keeps the order the requester gives;
  - a search field finds its choices in a value source as the requester
    types. If a REST or CEL source searches by the text itself, select
    **The source searches by the typed text**: its URL or body reads
    `${search}`, or its options expression reads `search`. Otherwise rvav
    keeps the options that contain the text. On submit, rvav searches the
    source for the value picked and refuses it unless the source finds
    it, so the source must find an option by its value too;
  - a tree shows its options as a hierarchy: each option names the value
    of the option it sits under. In **Options**, write one per line as
    `value | label | parent`; a REST source maps **Parent** with a
    JSONPath, and a CEL source's options give `parent`. With the
    `vsphere.folders` source, a tree shows each VM folder under its
    parent folder. On submit, rvav refuses a node the field doesn't offer;
  - a file upload takes **Accepted types**, comma-separated extensions or
    MIME types such as `.csv, .pdf, image/*`, which it needs to be
    published, and its **Largest file (MB)**, from 1 to 25 (10 if empty).
    rvav refuses a file over that size, of another type, or whose content
    isn't what its name says, and never takes HTML or SVG. The request
    keeps the file; its requester, its approvers and the project's
    administrators download it from the request's page. AWX gets the
    file's record (`id`, `name`, `size`, `content_type`, `sha256`): a
    playbook that needs the content downloads `GET /api/v1/files/{id}`
    with a token of an account allowed to, such as one that is the
    project's administrator. A file field can't be secret, editable by
    approvers, or have an initial value
    ([ADR-0040](../adr/0040-file-fields.md));
  - a vSphere folder lists the VM folders of the datacenters the
    project's placement uses: bind the blueprint's `folder` to it;
  - a project, user, deployment or vSphere VM lists only what the
    requester may see, from rvav itself. A deployment can keep one
    catalog item's (**Catalog item**), and a VM one deployment's
    (**Deployment**, such as `${inputs.server}`).
- **Definition:** the whole version as YAML, to review or paste. A VM
  blueprint's or a composite's graph is beside it (below it on a narrow
  screen), drawn from the YAML as you type: a VM item's components (how
  many VMs, template, disks, kept apart), an edge to each component one
  is built after (it names it in `depends_on`, or its tags or cloud-init
  read it), the networks and NSX segments they are on, the security
  groups they are in and the rules between them, drops and rejects in
  red; a composite's steps in the order they
  run, those that run at the same time in one frame, a condition on a
  dashed edge, what each step runs, uses and undoes. Problems the graph
  finds (a segment or group nobody declared, a step using a later one)
  are on its nodes and listed with their lines. While the YAML doesn't
  parse, the graph stays as it last parsed and says so. **Graph** and
  **List** show the same thing; the arrow keys move between nodes, Enter
  shows one's details. Requesters see the same graph on the request form
  of an item with more than one VM or step, without what only you see:
  no guest settings (domain, join credential, administrator password,
  cloud-init), sizes, naming, tags or placement, of a composite step's
  bindings only the earlier steps they use, and in a condition or a
  binding the graph shows, its quoted values as `'…'`
  (`inputs.token == '…'`). A binding that is only a value, such as a
  network's name, shows whole.
- **Writing a VM blueprint:** a component's tags and cloud-init can read
  each VM's index and what the other components built (their names,
  FQDNs and addresses), and any binding can go through CEL's string
  functions (`inputs.app.trim().lowerAscii()`); a name reads such a
  binding as `{bindings.<key>}`
  ([service-catalog.md](../service-catalog.md#vm-blueprints)). A
  component that reads another is built after it. When you save, rvav
  refuses a reference to a component that doesn't exist and components
  that would each be built after the other, with the line in the YAML.
- **Designing a VM blueprint:** in a VM item's draft, the graph edits the
  YAML beside it, and the YAML the graph
  ([ADR-0066](../adr/0066-vm-designer.md)). A published version's graph
  is read-only.
  - To add a part, drag it from **Add to the topology** onto the graph, or
    select it: a **VM**, a **Network**, a **Segment**, a **Security
    group**, an **NSX group**, a **Disk** or a **Custom resource**. Its
    dialog asks for what the part needs: a key, a network's CEL
    (`inputs.network`, or a quoted name such as `"dvpg-web"`), a disk's
    size in GB, a resource's type among those turned on. A network, an
    NSX group or a disk goes on the VM you drop it on, or the one in
    **Connected to**, the selected VM by default. To add a second VM to an
    item with one, give the item's VM a key in **Key of the existing VM**:
    it becomes the first of `components`, and the blueprint's settings,
    such as its template and guest settings, stay the defaults of both.
  - To connect two nodes, drag from one node's dot to the other, or select
    a node and pick the other in **Connect to**, which says what
    **Connect** will write, or why it can't, before you click. A VM
    connected to a network or a segment gets an adapter there (the first
    in `bindings.network`, the others in `nics`), to a security group or
    an NSX group puts its VMs in it, and to a disk takes the disk. A VM
    connected to another builds the other after it (`depends_on`). rvav
    refuses other connections and says what to do instead: write a
    firewall rule, with its services, under `security_groups` in the
    YAML; bind a custom resource's inputs to a component's, such as
    `components.web.addresses`, to make it after that component.
  - A selected node's details edit its fields: a blueprint's one VM's
    template, naming, vCPUs, memory and OS family; a component's key,
    title, count (a number, or CEL over the inputs), template, naming,
    vCPUs, memory and anti-affinity; a network's CEL; a disk's key, size,
    storage policy and datastore; a segment's, a group's or a resource's
    key and title. When you rename a key, what reads it follows
    (`depends_on`, adapters, groups, rules and CEL). The other guest
    settings, a rule's services and a resource's inputs stay in the YAML.
  - **vCPUs** and **Memory in GB** write `bindings.vcpu` and
    `bindings.memory_gb`: a whole number of 1 or more, or CEL over the
    inputs, such as `sizes[inputs.size].vcpu` for a size the requester
    picks. A component's blank takes the blueprint's, shown in the field.
    Publishing needs both for every VM, and says so on the VM's node.
  - **Disconnect**, beside each of a node's connections, removes what
    writes it. **Remove** removes the node and what names it: a network's
    adapters, a group's members and rules, a VM's place in the others'
    `depends_on`. An item keeps at least one VM.
  - A VM without its own disks, adapters or groups takes the blueprint's.
    When you edit those of one VM, it gets its own copy first, and the
    others keep the blueprint's.
  - **Undo** and **Redo** (Ctrl+Z, Ctrl+Shift+Z) go back through the
    graph's edits and your typing alike.

  rvav refuses an edit the definition can't hold before it writes
  anything, and says why under the field or in the dialog: a key that is
  taken or malformed, more than 20 VMs, 4 segments or 8 security groups,
  a disk without a size, a VM built after itself. A refusal of a save is
  on the node it is about. When the YAML has something the graph can't
  tell apart, such as two components with the same key or `components`
  that isn't a list, the graph is read-only and says why: fix it in the
  YAML, and the graph is editable again.

  Your comments in the YAML stay through the graph's edits, except those
  on a node you remove, but not through a save, as for a composite. A
  text indented otherwise than rvav writes it is re-indented on the
  graph's first edit, and keys the graph adds go in rvav's order.
- **Designing a composite:** in a composite's draft, the graph edits the
  YAML beside it, and the YAML the graph. Your comments in the YAML stay
  through the graph's edits, but not through a save: rvav stores the
  definition, not its text, and the editor shows it again without them
  (as an export does). Put what a reader needs in step titles and the
  item's description. Steps that share bindings through a YAML anchor
  (`with: *common`) don't change with the one you edit: it gets its own
  copy, and the others keep theirs, even when you remove or move the
  step holding the anchor. Select a step (click it, or the arrow keys and
  Enter) and its editor opens under the graph:
  - **Add a step** runs a published item (not another composite), an AWX
    template imported from AWX (or any template, by its name), a
    custom workflow registered for composite steps, a REST call, a
    person, a wait for a time, a wait for a condition, or a wait for a
    signal or an event. It goes after the selected step, at the same time
    as it, or at the end. Its inputs
    named as the form's are bound to them. An AWX template picked from
    the list is written with its ID in AWX (`template_id`) beside its
    name, so it is launched even if renamed; picking another drops the
    previous one's ID and title.
  - **Move earlier**, **Move later**, **Run with the step before** and
    **Remove step** change the order; a step in a group of steps that run
    at the same time is moved out of it, before or after.
  - **Runs**, **When** (a CEL condition: the step is skipped when it is
    false) and **Undone by** (an AWX template, custom workflow or REST
    call run when a later step fails and **Undo what was built when a step
    fails** is on, on the Done node).
  - Each **input** is bound to the form's inputs, the request's
    environment or project, or an output of a step that runs before it
    (`steps.nodes.fqdn`, `steps.monitor.job_id`), picked from what exists
    there, or written: a number, `true`, a quoted string, or a CEL
    expression. Steps that run at the same time can't use each other's
    outputs.
  - A **REST call** has no inputs: its **Method**, **URL** and **Body**
    read the form and earlier steps themselves. In the URL, `${...}` fills
    the path or query, escaped; the host is fixed, and must be in
    Integrations › Allowed hosts. The body is a CEL expression, sent as
    JSON. **Secret** picks a stored secret, sent as it is stored (bearer,
    basic or a header). **Accepted statuses** are those that don't fail the
    step (any 2xx if empty), and **Keep** is a JSONPath picking what later
    steps read as `steps.<key>.json` (all of it if empty), beside
    `steps.<key>.status`. A POST or a PATCH that fails isn't tried again
    by itself, as it may have done its work; the others are, on network
    errors, 5xx, 429 and 408. Its undo is another call, which can read this
    step's outputs (`steps.cmdb.json.id`). See
    [the catalog's reference](../service-catalog.md#a-rest-call-step).
  - **A person** stops the run until someone answers, and has no inputs or
    undo. **Who is asked:** an **OIDC group**, **Approvers** (user IDs),
    **The requester** and **The owner**, told as for an approval. Nobody
    answers their own request unless the step asks the requester, or the
    owner when that is them; a request that would leave nobody is refused.
    An administrator answers only if named. **What they answer:** a
    **Message**, fixed text, and **Add a question** for each thing to ask:
    its **Key**, its **Question**, a **Type** (text, a number, yes or no, or
    a choice with its **Options**), a **Default** and whether **An answer
    is required**. No questions means they approve or reject, and a reject
    fails the step. A form can't ask for a secret. **If nobody answers:** a
    **Timeout**, and **Then** the step fails, it goes on with no answer, or
    it escalates to a group or users for one more timeout and then fails.
    Later steps read `steps.<key>.answered`, `by`, `comment` and
    `answer.<key>`; on a timeout that goes on, `answered` is false and each
    answer its default or null. See
    [the catalog's reference](../service-catalog.md#a-step-that-waits-for-a-person).
  - **A wait for a time** stops the run for a while, and has no inputs or undo:
    **For** a duration, or **Or until** a CEL expression giving a time (RFC 3339,
    or a date; with no zone, UTC). Filling in one clears the other. A time already
    passed goes on at once, and one that can't be read fails the step. Later steps
    read `steps.<key>.until`.
  - **A wait for a condition, polling a REST API** calls an API again and again
    until a condition holds, and has no inputs or undo. **The call it repeats:** a
    REST call's fields, with GET, PUT or DELETE only, since a POST or a PATCH may
    do something each time. **What it waits for:** **Every**, how often it checks,
    at least 1m, and **Until**, CEL on the call's answer, `steps.<key>.json` or
    `.status`. **If it never holds:** a **Deadline**, required, and **Then** the
    step fails or it goes on. No answer, a 5xx, a 429 or a 408 only waits for the
    next check. Another status it doesn't accept fails the step, so list a 404 in
    **Accepted statuses** if it means "not yet". Later steps read `status`, `json`
    and `met`; on a deadline that goes on, `met` is false, with the last answer or
    null. See
    [the catalog's reference](../service-catalog.md#a-step-that-waits-for-a-time-or-a-condition).
  - **A branch** runs the steps of one side of a condition, or of a switch's
    cases. **Its cases:** each has a **Title**, shown on the graph and the timeline,
    a **When**, a CEL condition, and its steps: **Add a step to...** adds one, and
    selecting it edits it as any other. **Add a case** adds another, and the first
    whose condition holds is taken, so their order matters: **Earlier** and **Later**
    move them. **Otherwise** has what runs when no case holds. A case with no steps
    goes on. It chooses once, and a Retry keeps its choice. Nothing nests: a branch
    isn't offered on a side or in a parallel group, a side's steps move within it and
    don't run together, and a VM item can't go on a side. Later steps read
    `steps.<key>.case`, and a side's step only if it ran: `has(steps.x)`. A side's
    step can't read another side's. See
    [the catalog's reference](../service-catalog.md#a-branch).
  - **A message to people** tells **The requester**, **The owner**, **OIDC groups**
    or **People**, through the notification centre and the channels each of them
    keeps on. It has no inputs or undo. Its **Message** is text with `${...}`, on the
    form's inputs and earlier steps. What a requester typed reaches those people, and
    each channel escapes it. A secret input is refused. It is sent once: a Retry
    doesn't send it again. A channel that fails doesn't fail the step, and neither
    does nobody to tell; a text it can't fill does. Later steps read
    `steps.<key>.told`, never what it said. See
    [the catalog's reference](../service-catalog.md#a-step-that-sends-a-message).
  - **A wait for a signal or an event** stops the run until something comes,
    and has no inputs or undo. **What it waits for:** **Waits for** is **A signal
    sent to this request**, with its **Signal name**, or one of rvav's events by
    its topic, with a **Condition**, CEL on the event, that picks the one it
    takes (the first of its topic in the request's project if empty). **If nothing
    comes:** a **Timeout**, at most 90 days, and **Then** the step fails or it
    goes on. A project manager's API token sends the signal with
    `rvavctl requests signal` or `POST /requests/{id}/signal/{name}`; the
    requester can't. Later steps read what came, the signal's body or the
    event's data, as `steps.<key>.<field>`. See
    [the catalog's reference](../service-catalog.md#a-step-that-waits-for-a-signal).
  - **Loop:** **Repeat it for each item of a list** runs the step once for each
    item of **For each item of**, a CEL list of at most 100 items, and **At a
    time** runs up to 10 passes together (one by one if empty). The step reads
    `each` and `index` in its inputs, its condition, and a REST call's URL and
    body; later steps read `steps.<key>.items`, each pass's outputs in order,
    and `count`. **Run it once** removes the loop. A VM or custom resource
    step, a wait, a branch, a step with an undo and a step on a catch path
    can't loop. See [the catalog's reference](../service-catalog.md#a-loop).
  - **Catch path:** **Add a step to the catch path** adds a step that runs only
    if this one fails, with `error.step` and `error.message`; the request then
    goes on. On a step in a group of steps that run at the same time, the path
    is the group's, for any of its steps. A caught step stays failed on the
    timeline, and later steps read `steps.<key>.caught`. A catch path holds AWX
    jobs, workflows, REST calls and messages. A VM or custom resource step, a
    group with one, and a branch have none, since what a VM or a resource
    built is the rollback's to delete.
  - **Error handler:** on the Done node, **Add a step to the error handler**
    adds a step that runs on any failure no catch path caught, before the undo
    steps and the rollback; the request still fails. It holds the same steps
    as a catch path. See
    [the catalog's reference](../service-catalog.md#a-catch-path-and-an-error-handler).

    On the graph, a loop is its step's **For each** and **At a time** lines, a
    catch path the steps after what it catches, by a dashed **if it fails**
    edge, and the error handler the steps after Done.
  - A VM item's step is planned when the request is submitted: its inputs
    come from the form only, and it has no condition or undo (a failure
    deletes its VMs).
  - **Undo** and **Redo** (Ctrl+Z, Ctrl+Shift+Z) go back through the
    graph's edits and your typing alike.

  What the server would refuse is on the step it is about as you design:
  an item that isn't published, a workflow that isn't registered for
  composite steps, a workflow (a step's, an undo's or a workflow item's)
  not offered to every project the composite is offered to (a request
  from one of the others is refused), a VM step with a condition, an
  undo or an input from a step, or on a side of a branch or a catch path, a
  VM step that loops or catches, a step using a later one or another side's,
  a message showing a secret input, a REST call whose host isn't allowed or
  whose secret is gone. Save the draft, publish it, and
  request it to try it: the request's page draws its run on the graph of
  the version requested, each step with its live status.
- **Versions:** each version's menu
  ([ADR-0046](../adr/0046-deployment-names-and-versions.md#several-versions-requestable)):
  - **Publish** a draft, or **Roll back** to a deprecated version: it
    becomes the only version people can request. Requests and deployments
    keep the version they were made with;
  - **Publish *n*, keeping *m* requestable** publishes a draft beside the
    versions offered now, and **Offer *n* again, beside *m*** offers a
    deprecated version beside them. Requesters then pick a version on the
    form, the newest preselected, and updates move to any of them;
  - **Retire** a published version when several are: requests and updates
    can't use it any more, and the newest of the others is the default.
    A form still open on it is refused as out of date;
  - **Withdraw** the only published version: nobody can request the item
    until a version is published again. The **⋯** menu's **Withdraw**
    retires every published version at once;
  - **Export** it as YAML;
  - **Discard** a draft.

  **Compare** shows what changed between two versions.
- **Sharing:** who may request it.
  - **Projects:** every project, those made later included, or the
    projects you choose. Click **Save projects**.
  - **Who may request it:** in each project, all its members by default.
    To share the item there with chosen people only, click **Share**, pick
    the project, choose **Chosen people**, and add users, OIDC groups (as
    the identity provider's token names them) and project roles. Before
    you save, the sheet lists who can request it in the project and why,
    who is left out, and anyone named who has no role that may request
    there. Pick **Every project** to name who may request it in each
    project, when it is shared with every project. Shares add up: someone
    requests it when any share covers them, and the sheet's list counts
    the every-project share too. Choosing **Every project** under
    **Projects** keeps each project's own share.

  Sharing doesn't replace the roles: only people whose role in the
  project lets them request see the item there, as before. Administrators
  and catalog admins see only what is shared with them, like anyone. An API
  token carries no groups, so it requests only what its user is named for,
  holds a role for, or what is shared with all members.

  When you narrow an item's sharing, saved drafts stay, but their submit is
  refused for those left out. Requests already submitted, waiting for
  approval or scheduled, run as they were submitted. A role an item names
  can't be deleted until you change the item's sharing.

Items can be exported and imported as YAML, between installations.

### Property groups

A property group holds inputs and constants that several items share, such
as a cost centre every VM asks for or the DNS domain of a site. Manage them
in **Property groups**, under **Catalog**; you need `catalog.manage`.

- **Inputs:** each has a name, a label, a type (text, whole number,
  number, yes or no), and can be required or have a default and a help
  text, shown under the field on the request form. Text inputs can list
  options, separated by commas, for requesters to choose from, or be
  secret (a secret input has no default and no options). Changing an
  input's type away from text clears **Secret** and its options. **A host
  variable in the AWX inventory** puts the input in the inventory; a
  secret input can't be one.
- **Constants:** values the item's bindings, properties, folders, form
  rules and composite steps read as `propgroup.<key>.<name>`, such as
  `${propgroup.network.dns_domain}`. Each has a type: **Text**,
  **Number**, or **Yes or no**.

Saving a group makes its next version; the versions before it don't
change. To use a group, name it and its version in the item's blueprint,
on its **Definition** tab:

```yaml
blueprint:
  kind: vm
  property_groups:
    - {key: network, version: 2}
```

The group's inputs show on the item's request form in a section of their
own, titled with the group's name, and go wherever the item's own inputs
go:

- Bindings, properties and cloud-init read them as `inputs.<name>`.
- An AWX item's job gets them as extra variables, and the events sent to
  webhooks carry them under `inputs`.
- The AWX inventory has an input among a host's `rvav_inputs` only when
  the input is a host variable in the AWX inventory.
- A secret input is sealed like the item's own secret inputs: only an
  AWX item's job gets its value, at launch. Events, the inventory and the
  API never show it, a custom property that reads it shows `••••`, and
  saving an item refuses a folder that reads it.

An item keeps the version it names: to use a newer one, change the version in a
new draft and publish it. Saving the item refuses a group or version that
doesn't exist, and an input that is both in the item's form and in a
group, or in two groups.

You can't delete a group while a published or deprecated version of an
item names it. A draft that names a deleted group is refused the next time
you save it.

### Form library

The form library holds value sources and checks that several items' forms
share, such as the CMDB's applications or a check that a cost centre is
open ([ADR-0043](../adr/0043-form-library.md)). It's where vRO actions go
when you move from vRA. Manage it in **Form library**, under **Catalog**;
you need `catalog.manage`.

To add an entry:

1. Select **New entry**.
2. Give it a **Key**, which forms name it by and which doesn't change, a
   **Name** and a **Description**, which the form designer shows.
3. Choose its **Purpose**:
   - **Value source** gives a field its choices, or its value on a field
     that takes one, such as a text or a number.
   - **Validation** checks the whole form when the requester submits it,
     before approval.
4. Choose its **Kind**:
   - **REST call** calls a URL on a host that REST value sources may call
     (**Administration › Integrations › Allowed hosts**). A source maps
     the answer with JSONPath: **Items**, **Value**, **Label**, **Detail**,
     and **Parent** for a tree, where each item names its parent's value.
     A validation reads **Valid at**, **Message at** and **Errors at**
     (`$.valid`, `$.message` and `$.errors` when empty). **Secret** names a
     stored secret that rvav sends as a bearer token.
   - **CEL expression** works the options, the value or the check out in
     CEL. A check gives `{}` or `true` to pass, and text, `false` or a map
     of parameter to message to refuse.
5. Under **Parameters**, add what the entry needs from the form, each with
   a name, a label and whether it's required. The entry reads them as
   `${params.<name>}` in a URL or body and `params.<name>` in CEL. It
   doesn't read the form's inputs, so forms with different inputs can
   share it.
6. Under **Test**, give the parameters values and select **Run test** to
   see the options, the value, or what the check says. To test it as a
   form of a project would run it, choose the **Project** and the
   **Environment**: the entry then reads `project` and `environment`, and a
   REST call opens the project's secret of that name before the global one.
7. Select **Create entry**.

To change an entry, open its row's menu and select **Change or test**.
Saving makes the entry's next version; the versions before it don't
change. What the sheet doesn't show, such as a `cache_seconds` set through the
API, stays as it was unless you change the kind. Each item that names an earlier version gets a draft that names
the new one, so the change reaches requesters only when you publish those
drafts. **Used by** lists the items' versions that name the entry, and
which of its versions each names.

To use an entry in an item's form, in the form designer:

- For a field's choices or value, set the field's **Value source** to
  **Form library**, then choose the **Entry** and its **Version**.
- For a check of the whole form, select **Form validations**, then **Add
  one from the form library**, and choose the **Entry** and its
  **Version**.

Then give each parameter a CEL expression over the form's inputs, such as
`inputs.app` or `environment`. A message the check gives for a parameter
shows on the input that the parameter's expression reads alone. **Add a
validation** writes a REST or CEL check into the form itself instead.

A validation refuses the request when it can't run, such as when its
service is down or answers an error: a requester can't submit until it
answers. In a wizard, it runs when the requester moves to the review step.
Each run is in the audit log as `form_validation.run`, with its outcome,
and each change to an entry as `form_library.create`, `form_library.update`
or `form_library.delete`.

You can't delete an entry while a published, deprecated or retired version
of an item names it: its menu says how many items use it. A draft that
names a deleted entry is refused the next time you save it.

### Shared variables

A shared variable is a named value that isn't secret, such as a site's DNS
servers, which composite steps read as `vars.<group>.<name>`
([ADR-0057](../adr/0057-run-data-variables-schedules-and-loops.md)). Manage
them in **Variables**, under **Catalog**. To add, change or remove one, you
need `catalog.manage` for all of rvav. With `extensibility.read`, as an
auditor, you see the same page and what reads each variable, without **New
variable**, **Change** or **Remove**.

- A change reaches the steps that haven't run yet, in requests already
  running too: you don't publish the items again.
- To publish a version, each variable its steps read must exist. The
  composite designer marks a step that reads one that doesn't, and a
  publish refused for it shows the problem on the step and on its line.
- You can't remove a variable that a draft or a published version reads,
  or that a request of an older version reads while it's still to run or
  running. **Read by** lists those items and requests.

For what a value may be and where steps read it, see
[Shared variables](../service-catalog.md#shared-variables).

### A run's step data

A request's page shows what each step was given and what it gave. To read
it, choose **Show data** on a step, in the timeline or in the selected
node's details: the **Step data** panel lists the step's inputs and
outputs, with secrets masked. You need `catalog.manage` or
`extensibility.read` for all of rvav, so catalog admins and auditors read
it; a requester sees each step's status and error only. For what each kind
of step shows, see [A run's data](../service-catalog.md#a-runs-data).

### The catalog in Git

The catalog can be kept in a Git repository
([ADR-0029](../adr/0029-catalog-in-git.md)), so it is reviewed in merge
requests and the same in every installation. Set the repository in
**Integrations › Git** (configuration.md lists the settings): an HTTPS
URL with a token, or an SSH URL with a deploy key and the server's host
keys (put the token in its own field, never in the URL). **Test
connection** signs in and reads the branch. rvav needs to push to the
branch, never to force a push. Saved, the settings apply once each of
rvav's processes has finished its running work and started again, which
the panel shows: until then, Catalog items shows no repository and
nothing is synced.

Each item is a file, `catalog/<slug>.yaml`, as the export writes it with
its published version. **rvav publishes, the repository proposes:**
- **What you do in rvav is committed:** publishing, deprecating, rolling
  back, changing an item's details or slug, deleting it. You are the
  commit's author, the changelog its message. rvav commits in the
  background: publishing never waits for Git.
- **What changes in the repository becomes a draft**, never published by
  itself. Review it in **Versions** and publish it (rvav commits it back),
  or discard it (rvav commits its own file again). A new file becomes a new
  item, a draft. The item's details (name, summary, category) stay as rvav
  has them.
- **Never in the repository:** drafts, sharing, secrets' values, and
  items imported from AWX. A sharing block pushed to the repository is
  put back by a commit, as the item's details are.

Above the items, **Catalog items** shows the repository, when it was
last read, and how many files need you. **Details** lists those files,
and **Read the repository now** reads it at once (otherwise every 5
minutes, or when the Git server calls rvav's webhook). An item's **Git**
tab shows its file, its last commit, and how the repository's file
differs from rvav's:
- **Waiting to be committed:** at the next read.
- **Commit failing:** the error, and when it is tried again (**Read the
  repository now** tries at once). Check the repository's settings and
  token.
- **Draft from Git:** the repository changed the file.
- **Conflict in Git:** both changed it (a draft open in rvav, or a change
  not committed yet). **Keep rvav's** commits rvav's file over the
  repository's change; **Take the repository's** makes a draft of it,
  discarding rvav's draft after asking.
- **Refused from Git:** the file can't be imported: the problems, by line.
  Fix it in the repository.
- **Deleted in Git:** rvav keeps the item; its next change adds the file
  back.

To have the repository read as soon as it changes, add a webhook on the
Git server to `https://<rvav>/api/v1/integrations/git/webhook` (push
events) with the secret of `RVAV_GIT_WEBHOOK_SECRET`: GitHub and Gitea
sign it, GitLab sends it as its token.

## Automation

- **Day-2 actions:** AWX templates with the day-2 label become actions on
  VMs, their survey becoming the action's form
  ([ADR-0018](../adr/0018-awx-day2-actions.md)), unless you
  [design the action's form](#design-a-day-2-actions-form).
- **Subscriptions:** events sent to webhooks, AWX or custom workflows
  ([ADR-0021](../adr/0021-event-subscriptions.md)).
  - The **Topic** list groups the topics by what their events are about
    ([ADR-0049](../adr/0049-event-topics.md)): a request; each of its VMs,
    from its placement to its deletion, with vRA's names
    (`compute.provision.post`); each of its custom resources, made and
    deleted (`custom.create.post`,
    [ADR-0056](../adr/0056-custom-resource-types.md)); each of its
    approval levels, asked and decided; a deployment's lease; a project,
    created or changed.
  - A condition reads the event: on a VM's topic, the VM
    (`machine.placement.cluster == "GVA-DEV-CL01"`); on a custom
    resource's, the resource
    (`custom_resource.type.key == "load-balancer"`); on an approval's, the
    level (`approval.status == "rejected"`); on a project's, what changed
    (`"cost_center" in changes`).
  - A **blocking** subscription holds a request until it answers, and can
    stop it. Only subscriptions on `provisioning.pre`,
    `compute.allocation.pre`, `network.configure`, `day2.pre` and
    `delete.pre` can block. On `provisioning.pre` the answer can add
    properties to the deployment; on a VM's topics it can
    [rename, place or address the VM](#let-a-subscription-name-place-or-address-vms).
  - Each subscription's runs, their errors and retries are on its page.
- **Custom workflows:** your own Temporal workflows, on your own task
  queue, offered as items or composite steps
  ([custom-workflows.md](../custom-workflows.md)).
- **Resource types:** what rvav doesn't build itself, such as load
  balancers, made and deleted by your custom workflows or AWX templates,
  and kept in deployments beside their VMs
  ([define a resource type](#define-a-resource-type)).

Each of these pages is a list. A row's menu changes the record in a side
panel and turns it off or on (a day-2 action is offered or not). A
subscription, custom workflow or resource type can also be removed from
its menu: rvav first says what stops. A subscription's row opens its page, where
**Change subscription** is the main action and a menu has the rest.

### Let a subscription name, place or address VMs

A CMDB, an IPAM or a capacity tool can decide a VM's name, placement or
address before rvav builds it
([ADR-0054](../adr/0054-subscriptions-that-change-the-request.md)). To
let one decide:

1. Go to **Administration › Subscriptions** and select **New
   subscription**.
2. Under **Topic**, select `compute.allocation.pre` to change the VM's
   name and placement, or `network.configure` to change its adapters'
   networks and addresses.
3. Choose the target, then select **Blocking**. The form says what the
   answer may change and how each kind of target answers: a webhook with
   JSON, an AWX job with the `rvav_answer` artifact, a custom workflow
   with its result.
4. Under **When it fails, doesn't answer or asks for a change rvav
   refuses**, select **Stop the request** to fail the request, or **Let
   the request go on** to build the VM as planned.
5. Optional: to tell another system when the subscription fails, select
   **Tell a recovery target when it fails** and choose the recovery
   target. If it answers with an outcome, that decides instead of the
   previous setting.
6. Select **Create subscription**.

rvav checks each change again before building, and refuses:

- a name without the naming policy's prefix, or one that a deployment, a
  VM in vCenter, a DNS record or an EfficientIP address already has;
- a cluster, resource pool, datastore, folder or network that the
  environment's placement rule doesn't allow, or without room for the VM;
- an address outside the adapter's subnet, or one that is already taken.

The request's timeline shows each VM's checks under **Allocation checks**
and **Network checks**, with what changed. The subscription's runs show
it too, and its recovery target's runs have a **Recovery** badge. A
request retried after a failure keeps what was changed, and doesn't ask
again.

### Design a day-2 action's form

An AWX action asks for its template's survey, and a workflow action for
the form its input schema gives. To give an action a form of its own,
with sections, value sources and rules, as a catalog item's form has
([ADR-0050](../adr/0050-designed-day2-forms.md)):

1. Go to **Administration › Day-2 actions**. The **Form** column says
   which form each action asks for.
2. In the action's row menu, select **Design the form**. The form
   designer opens on the survey's or the input schema's form, to start
   from.
3. Change the fields, the layout and the rules as in a catalog item's
   form designer. Each change is saved as a draft: people keep getting
   the survey's form.
4. In **Preview**, choose a deployment and, for an action on VMs, the VM
   under **Runs on**. The preview works the form out there, as you.
5. Select **Turn on**. From its next run, the action asks for this form.

Once the form is on, each change you save reaches people from the action's
next run: the page says **Saved: people get this form from their next
run.** To work on the form without them getting your changes as you go,
select **Turn off** first, which gives them the survey's or the schema's
form meanwhile, and **Turn on** again when it's ready.

An action's form differs from a catalog item's in these ways:

- It shows in a dialog: lay it out as one page or as tabs, not as a
  wizard.
- It takes no files and no secrets. Ask for an address to fetch a file
  from, and keep a secret in AWX or in rvav's secrets.
- Its rules and sources also read what the action runs on:
  - `resource`: on a VM, `name`, `power_state`, `cpu_count`,
    `memory_gb`, `disk_gb`, `guest_os`, `os_family`, `fqdn`, `ip`,
    `moref`, `cluster`, `folder`, `tags` and `custom_properties`; on a
    whole deployment, `name`, `os_family` and `vms`, each with `name`,
    `fqdn` and `ip`; on a custom resource, `id`, `type`
    (`custom.<type key>`), `name`, `key` and `properties`, such as
    `resource.properties.ttl`.
  - `deployment`: `id`, `name`, `description`, `owner_id` and `item`.

  For example, the default
  `resource.os_family == 'windows' ? 'telegraf' : 'node_exporter'`
  picks an agent for the VM.
- The survey or the input schema stays what AWX or the workflow checks.
  When the form doesn't ask for what they require, or asks for it as
  another type, the page lists where they differ: AWX or the workflow may
  refuse such a run. A survey changed in AWX doesn't change the form.

If rvav refuses to turn the form on, the designer marks the fields to
fix. To go back to the survey's form, select **Turn off**. To delete the
form, select **Drop the form** in the page's menu.

### Define a resource type

A resource type lets blueprints and composites make what rvav doesn't
build itself, such as a load balancer or a DNS alias, with your custom
workflows or AWX templates
([ADR-0056](../adr/0056-custom-resource-types.md)). Its resources sit in
their deployment beside its VMs, with their own properties and day-2
actions, and deleting the deployment deletes them. Administrators define
types; auditors read them.

Before you start, have what makes and deletes a resource ready: custom
workflows registered with the usage **Resource type**
([custom-workflows.md](../custom-workflows.md#4-register-it)), or AWX job
templates. To define a type:

1. Go to **Administration › Resource types** and select **New resource
   type**.
2. Enter a **Key**, the type's name in blueprints, such as
   `load-balancer`, and a **Title**. You can't change the key later.
3. Under **Its life**, choose what runs each step, a **Custom workflow**
   or an **AWX template**, and which one:
   - **Creates it** and **Deletes it** are required.
   - **Updates it** is optional. Without it, an update of a deployment
     that changes a resource's inputs is refused.
   - **Reads it again** is optional. It runs after each day-2 action on
     a resource, and what it returns replaces the resource's properties.
4. In **Property schema (JSON Schema)**, describe the properties that
   **Creates it** returns, as an object. The deployment's page labels
   them by their `title`.
5. Under **Day-2 actions on its resources**, select the actions to offer
   on its resources.
6. Select **Create resource type**.

What each step gets and returns:

- `create` and `update` get the inputs the blueprint or the composite
  step binds; `delete` and `read` get the resource's properties. When
  `create` is a custom workflow whose input schema lists its inputs,
  saving an item refuses a `with` that binds another, except `name`,
  which names the resource.
- A custom workflow gets them in its context's `inputs`, and the
  resource in `resource`. Its result is the resource's properties.
- An AWX template gets them as extra variables, and the resource in
  `rvav.resource`. It runs on its own inventory, with no limit. It
  returns the properties with `set_stats`:

  ```yaml
  - name: Give rvav the load balancer's properties
    ansible.builtin.set_stats:
      data:
        vip: "{{ lb.vip }}"
        pool: "{{ lb.pool }}"
  ```
- A `name` in the result names the resource; else its `name` input does,
  else rvav names it `<deployment>-<key>`.

An action the type lists that is already offered on VMs or deployments
is offered on its resources too. To offer an action only on the type's
resources, list a custom workflow registered and on as a day-2 action,
with **Offer it on deployments** cleared. An AWX action is offered on the
VMs or deployments it is set for as well; one that is off, or whose
template AWX no longer has, is offered nowhere.

A workflow action gets the resource in its context's `resource`, an AWX
action in `rvav.resource`. An AWX action on a custom resource runs on its
template's own inventory, with no limit.

A change to a type applies to the resources made before it: they keep
their properties, and the type's steps as they are now update, read and
delete them. To stop new requests and updates of the items that use a
type, clear **Items may use it**, or select **Turn off** in its row's
menu: its resources keep their actions and are still deleted with their
deployments. **Remove** is unavailable while a deployment has a resource
of the type or an item version names it.

## Existing VMs and EfficientIP

- **Onboarding:** adopt VMs rvav didn't build. On the **VMs to adopt**
  tab, search and filter the list (by folder too), select the VMs (a
  selection outlives a change of filters), then select **Adopt N VMs…**
  in the bar above the list. In the side panel, choose a project and
  **Where they go**:
  - **Each into a deployment of its own** or **All into one new
    deployment**: give an environment, an owner, a catalog item, and
    optionally a lease and the **Inputs** the deployments record, as if
    they had been requested with them. A later **Update** starts from
    those inputs; the others are the form's defaults. Without inputs, it
    starts from the form's initial values, and accepts the cluster,
    datastore and network the VMs are on. It doesn't grow or remove an
    adopted VM's disks: rvav doesn't know which are the blueprint's.
    When a live deployment of the project already has a new deployment's
    name, whatever its case, rvav numbers it: `erp-app02 2`, then
    `erp-app02 3`. Quotas and deployment limits don't refuse adopted VMs,
    which run already, but they count from then on.
  - **Into an existing deployment**: choose a ready deployment of the
    project. Its owner, lease and inputs stay. rvav refuses a VM that
    doesn't fit, with why: another OS family, a name the deployment has
    already, or more VMs than the component's count allows. When the
    count is an input, such as `web_nodes`, rvav raises it, within the
    form's maximum. While a request on the deployment runs, or waits for
    approval or its start time, rvav refuses the VMs: adopt them once it
    ends.

  Give the **Component** when the item's blueprint has several, such as
  `web`. rvav finds the VMs' addresses and DNS records in EfficientIP,
  and nothing is changed there or in vCenter
  ([ADR-0026](../adr/0026-onboarding.md),
  [ADR-0044](../adr/0044-onboarding-plans.md)).

  To keep the VMs from being deleted, as a blueprint's `prevent_delete`
  does, select **Keep from deletion**: the deployment's **Delete** is
  refused until an administrator runs its **Allow deletion**. Select it
  for VMs vRA keeps from deletion: the vRA import adopts its own
  deployments with their protection, and its plan names the VMs left for
  you to adopt here
  ([Delete protection](../operations/vra-import.md#delete-protection)).
- **Onboarding plans:** adopt the VMs a filter matches, again and again,
  as they appear. **Deployments › Adopt existing VMs** opens them, as does
  the **Plans** tab of **Onboarding**. To make one:
  1. Select **New plan**.
  2. Name it, and give **Which VMs** at least one criterion: name or
     address, folder (with the folders under it), vSphere tag as
     `category/tag`, cluster, vCenter or power. The panel says how many
     VMs rvav doesn't manage match now.
  3. Choose **Where they go**, as for adopting by hand. **All into one
     new deployment** makes the deployment on the first run that adopts
     a VM, and the next runs add to it.
  4. Optionally, set **Run every (hours)**, from 1 to 168. Without it, the
     plan runs only when you select **Run now**.
  5. Select **Create plan**. Its page opens.

  Each run adopts, one VM at a time, the matching VMs no deployment has,
  and skips those a deployment has already, so no VM is adopted twice.
  A VM that fails doesn't stop the others. Select a run on the plan's
  page to see each VM's outcome: **Adopted**, **Skipped** or **Failed**,
  and why. rvav keeps a plan's last 20 runs. While a run goes on, you
  can't start another or delete the plan. Deleting a plan keeps the
  deployments it made. The **Audit log** records each plan change, each
  run, and each VM adopted.
- **Unregister:** the day-2 action that does the opposite, on a VM or on
  a whole deployment. rvav forgets them and changes nothing elsewhere:
  - the VMs stay in vCenter as they are, running or not, with their
    folder, tags and DRS rules;
  - their IP addresses stay reserved and their DNS records stay in
    EfficientIP;
  - their NSX group memberships, and the deployment's NSX groups,
    firewall rules, segments and subnets, stay in NSX and EfficientIP.

  The VMs no longer count in the project's quota, their lease ends, and
  **Onboarding** lists them again: adopting a VM finds its address and
  records again, but not its NSX memberships. A VM goes with its own
  addresses and records; an address another VM of the deployment has
  stays with that VM. When the VM's component counts its VMs with an
  input, such as `web_nodes`, rvav lowers it, within the form's minimum,
  so the next update doesn't build the VM again. A deployment's only VM
  is unregistered with the deployment, which then shows as deleted, its
  history saying it was unregistered. To delete what stays, do it in vCenter, EfficientIP and
  NSX. Only those who may change the project (administrators) are
  offered it; day-2 policies can still refuse it or ask for approval.
- **EfficientIP reconcile** (**Integrations › Checks**): addresses and records
  tagged as rvav's that no deployment owns, left by failures or restores,
  or by a deletion that went on past failed steps.
  Delete them there, or adopt their VM. The same check counts what rvav's
  IP ranges have to reconcile, which you do on the **IP ranges** tab (see
  [Reconcile the IP ranges](#reconcile-the-ip-ranges)).
  Adopting a VM on a network with ranges gives it no address from them:
  exclude its address from the range.

## Branding and notification templates

**Administration › Branding** and **Administration › Notification
templates** need the `system.manage` permission (administrators). Every
change on them is in the **Audit log**
([ADR-0059](../adr/0059-notification-templates-and-branding.md)).

### Brand the portal

The portal's name, accent colour and logos show in the menu, on the
sign-in page, in the browser's title and in emails.

1. Go to **Branding**.
2. In **Portal name**, enter up to 40 characters. Left empty, the portal
   is called rvav. rvav trims spaces, tabs and line breaks at either
   end and turns each run of them inside into one space. It refuses any
   other control character.
3. In **Accent colour**, enter a colour as `#rrggbb`, or pick one. The
   note under the field says how it reads:
   - Text in the colour must reach a contrast of 4.5:1 in the light
     theme. If it doesn't, the note gives the closest darker colour that
     does: to take it, click **Use #…**.
   - The dark theme takes the colour lightened as much as its text
     needs. The swatches show a button, a link and the current page in
     each theme.
4. Click **Save branding**. Your pages take the name and colour at once;
   other people's take them when they next open rvav.
5. Under **Logos**, click **Upload** for the light theme. If that logo
   doesn't read on a dark background, upload one for the dark theme
   too; without one, the dark theme shows the light theme's logo.

A logo is a PNG, JPEG or SVG of 256 KB and 2048 pixels a side at most.
rvav refuses an SVG, rather than cleaning it, if it has scripts, event
handlers, links, animations, `foreignObject`, a DOCTYPE or entities, a
CSS escape (`\`), or refers to anything outside itself. Export it again as plain SVG, or as a
PNG. Emails carry the light theme's logo only when it is a PNG or a JPEG,
since many mail clients block SVG; they show the portal's name otherwise.

The menu shows a logo 22 pixels high and 96 wide at most. When the name
doesn't fit beside it, the name goes under it, on as many lines as it
needs. In a narrow window, where the menu shows icons only, the logo
stands alone and its tooltip gives the name.

To go back to rvav's own colours, click **Use rvav's colours**, then
**Save branding**. To remove a logo, click **Remove**.

The sign-in page reads the branding from `GET /api/v1/branding`, which
anyone can read without signing in: the name and logos are public.

### Edit a notification template

Each notification, such as **Approval needed** or **Request failed**,
has a built-in template for each channel (email, Teams, Slack) and
language (English, French). An edited template replaces the built-in one
for its channel and language. rvav still writes what goes around it: an
email's greeting, facts, button and footer, and a card's facts and
buttons, so an approval's **Approve** and **Reject** keep working.

1. Go to **Notification templates**. The list shows when rvav sends each
   notification and which of its templates are edited.
2. Select a notification, then its **Channel** and **Language**.
3. Edit its parts:
   - **Subject** (email) or **Title** (Teams and Slack): one line of
     plain text.
   - **Text:** the email's text part, the Teams card's text in Markdown,
     or the Slack message in `mrkdwn`.
   - **HTML** (email only): what the email shows between the greeting
     and the facts.

   **Preview** writes the template with sample data as you type: the
   email as HTML, with scripts off, and as text; the Teams card or the
   Slack message, and their JSON.
4. Click **Save template**.

Templates are [Go templates](https://pkg.go.dev/text/template).
`{{.Request.Number}}` writes a variable, and `{{if .Approval.Name}}…{{end}}`
writes what it holds only when the variable has a value. **Variables**
lists what the notification can write, and **Functions** the functions
besides Go's own, such as `{{date .Lease.ExpiresAt}}` and
`{{join .Approval.Reasons "; "}}`. Values are escaped for the channel
(HTML in an email, Markdown in Teams, `&`, `<` and `>` in Slack), so a
title that a requester typed can't add links or markup.

Saving refuses a template, with the part and the line, when:

- it doesn't parse;
- it names a variable the notification doesn't have, in any branch, or
  a field of what a function gives, such as `{{(or .Request .Project).Name}}`:
  name the variable itself;
- it uses `define`, `block` or `template`, a `range` over anything but
  a list, such as `.Approval.Reasons`, or a `range` inside another;
- its subject or text is empty, or a part is too long: 1 KB for the
  subject, 20 KB for the text, 200 KB for the HTML;
- it fails, or writes nothing, with the sample data. `printf` pads to
  100 characters at most, and fails past that.

To go back to the built-in template, click **Reset to built-in**. An
edited template doesn't take the improvements later releases make to the
built-in one: reset it to get them. If an edited template fails when a
notification is sent, rvav logs a warning and sends the built-in one. A
template fails when it writes more than 64 KB in its subject or text, or
512 KB in its HTML, such as over a long list.

The audit log records `settings.branding.update`,
`settings.branding_logo.set`, `settings.branding_logo.remove`,
`settings.notification_template.update` and
`settings.notification_template.reset`. Audit forwarding sends them with
the **Settings, secrets, integrations** events.

## Watching

- **Reports:** requests by period and item, success rate, provisioning
  time, approval lead time, quota use, leases ending
  ([reports.md](../reports.md)).
- **Audit log:** every administrative change and approver edit, and every
  sign-in, with who made it. Search and filter it by what changed,
  project and dates; the address keeps the filters, so you can bookmark a
  search. Select an event's time to see its detail in a side panel.
  **Export CSV** keeps it for the archive.
- **What an event was on:** **On** names the target as it was called
  when the event was recorded, so a role renamed or deleted since keeps
  the name it had. The name opens the target's page when rvav still keeps
  it and you may open that page; a target deleted since says **(no longer
  exists)**. A deleted deployment keeps its page, with its history. A
  target without a name, such as a setting or an integration, shows its
  ID, and the side panel shows the ID under the name. Events recorded
  before rvav kept names take the target's name now, and show the ID once
  the target is deleted. The CSV export ends each row with a
  `target_name` column, after `detail`.
- **Secrets:** stored secrets (domain-join accounts, webhook signing
  secrets, credentials for value sources) are sealed with the secrets key.
  They are written, never read back.

### Sign-ins in the audit log

The audit log records each sign-in to the web app and to rvavctl, and
each failed one, with the address and the browser or program it came
from ([ADR-0058](../adr/0058-audit-forwarding-and-sign-ins.md)). To see
them, in **Audit log**, set **What** to **Sign-ins** or **Failed
sign-ins**.

| Action | What it records |
|---|---|
| `auth.sign_in` | A sign-in: the user, their roles and groups, the client (`web` or `rvavctl`) and the method (`oidc`, `device`, `token`, `local`, or `dev` in development mode). |
| `auth.sign_in_failed` | A refusal. **Who** is **Unknown** when the identity provider refused before rvav knew who it was. The detail says the stage (`state`, `provider` or `token`), the reason, and the provider's error and description, such as `access_denied` for a refused consent. |
| `auth.sign_out` | A sign-out from the web app, or `rvavctl logout`. |
| `auth.token_used` | An API token's first use of the day (UTC), with its name. An expired token's use is a failure, once a day. |

- **Where from:** the detail's `ip` and `user_agent`. Behind a reverse
  proxy, `ip` is the client's address only if the proxy is in
  `RVAV_TRUSTED_PROXIES`; otherwise it is the proxy's
  ([configuration.md](../operations/configuration.md)).
- **Reported by the client:** a failure marked `reported_by: client`
  happened in the browser or rvavctl, before rvav saw a token. rvav can't
  check it, and keeps at most 10 a minute from one address (an IPv6
  /64) and 300 in all. The same limits apply to the local administrator's
  sign-ins with an unknown username, recorded as `unknown` rather than
  what was typed.
- **Refused at the provider:** a refusal that the identity provider
  shows on its own page doesn't reach rvav, since the browser never comes
  back. Keycloak refuses a disabled user and a wrong password this way.
  To see those attempts, read the provider's own sign-in log: in
  Keycloak, turn on the realm's user events (**Realm settings** ›
  **Events**).
- **No secrets:** the audit log never records a token, a code or a
  password.

### Forward the audit log to a SIEM

rvav sends a copy of each audit event to the syslog servers and webhooks
you add. Events wait in rvav until they are delivered, and an outage of
the destination delays them without losing any. You need the
`integrations.manage` permission; auditors see the destinations and
their delivery without changing them.

1. If the server asks rvav for a client certificate, store its private
   key, in PEM, as a secret: **Catalog items › Secrets**. For a webhook,
   store the secret its calls are signed with the same way.
2. For a webhook, allow its host: **Integrations › Allowed hosts**.
3. Go to **Audit forwarding**, then click **New destination**.
4. Give it a **Name**, and choose its **Kind**:
   - **Syslog server:** its **Host** and **Port** (6514 for TLS, 514
     otherwise, when empty), the **Transport**, the **Facility** and the
     **App name**. With TLS, paste the **CA certificates** that sign the
     server's certificate unless the system or the CA bundle already
     trusts them, and, for mutual TLS, the **Client certificate** with
     the stored secret that holds its **Client key**.
   - **Webhook:** its **URL** and the **Signing secret**. If the SIEM
     takes a credential in a header, such as Splunk's
     `Authorization: Splunk <token>` or Datadog's `DD-API-KEY`, give the
     header's name in **Authentication header** and its value in
     **Header's value**. rvav keeps the value sealed and never
     shows it again; to keep it when you change the destination, leave
     the value empty. A value isn't kept for a URL with another host.
5. Under **Events**, tick the categories to send, such as **Sign-ins and
   sign-outs**. To send every event, tick none.
6. Click **Create destination**.
7. To check it, click the destination's **⋯** menu, then **Send a test
   event**. The dialog says what the server answered.

Each row shows how delivery goes: **Up to date**, **Sending** while
events wait, or **Failing**, with the last error, how many events wait,
and when rvav tries again. rvav tries after 5 seconds, 30 seconds and
1 minute, then every 5 minutes, until the destination answers; saving
the destination tries it again at once. **Turn off** stops sending and
queuing; **Remove** drops the events waiting for it. The audit log keeps
every event either way.

Don't put a credential in a webhook's URL: rvav refuses a user name or
password there, and shows the value of each query parameter as
`REDACTED`, to auditors, in the audit log and in the destination's last
error. rvav keeps the values it stored when you save the URL as shown.

An event the destination refuses for good waits aside, and the events
after it go on: a webhook that answers 413 for that event alone, or an
event longer than 64 KB written for syslog over TCP or TLS. The row
shows how many events are set aside and why. To send them again, raise
the receiver's limit, then save the destination.

What a destination receives:

- **Syslog:** one RFC 5424 message per event, framed by octet counting
  over TLS (RFC 5425) or TCP, or one per datagram over UDP. HOSTNAME is
  the host of rvav's address, MSGID the event's action cut at a dot to
  32 characters, and MSG the event in JSON: `id`, `at`, `category`,
  `action`, `actor`, `target_type`, `target_id`, `target_name` when
  rvav has a name for the target, `project` and `detail`. Failed sign-ins have the warning severity, other events
  notice. Use TCP and UDP for labs only: they send in clear.
- **Webhook:** a POST of `{"contract": "rvav.audit.v1", "events": [...]}`
  with up to 100 events, the oldest first, signed in `X-Rvav-Signature`
  as subscriptions' webhooks are
  ([integrations.md](../integrations.md)). Any 2xx answer delivers them.

An event can arrive twice, after a restart in the middle of a delivery,
or after a syslog server closed the connection while rvav sent: drop an
`id` you already have. A syslog server doesn't confirm what it receives.
If it loses events it took, or closes the connection without reading
them more than a quarter of a second after rvav wrote them, those
events are lost. To receive every event, use a webhook.
