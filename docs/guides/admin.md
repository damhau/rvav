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
- **Operations:** Setup, Audit log, System.

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
- the identity provider, the console, vRA, email, Teams, Slack, the
  allowed hosts and the sync intervals apply on next use, within seconds;
- a connection (vCenter, AWX, EfficientIP, NSX, Git) applies as each of
  rvav's processes finishes its running work, exits and is started again
  with it. The panel lists the processes: running with the earlier
  settings, finishing their work, then applied. A VM being provisioned
  meanwhile finishes, its next steps with the new connection.

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

### Trusted certificates

rvav checks the certificate of every server it connects to against the
system's CAs and those of the **CA bundle** (**Integrations › Trusted
certificates**). A connection's test shows its server's certificate
first: who it was issued to and by, until when, its SHA-256, and whether
rvav trusts it. When it doesn't, the test stops there and offers:
- **Trust this certificate** (vCenter, the vCenters added, AWX,
  EfficientIP, NSX, vRA): its SHA-256 goes in the connection's **Trusted
  certificate** field, and TLS verification on. Save, with the password
  or token again (a new pin needs it, as a new address does), then test.
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

To give someone less, or a mix, make a role of your own in
**Administration › Roles** (`access.manage`, which only administrators
hold). Select **New role**, or open a built-in role and select **Make a
role from it** to start from what it allows. Choose its key, fixed once
made, its name and its permissions, grouped by area; **What it allows**
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
4. Select **Make role**.
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
1. In **Administration › Roles**, open **Auditor** and select **Make a
   role from it**.
2. Enter the key `compliance-auditor` and the name `Compliance auditor`,
   and keep the permissions as they are.
3. Select **Make role**. It holds permissions for all of rvav only, so it
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
| **Approval** | Which requests need approval, from whom (users, groups or project roles, in levels, any or all), and what happens on timeout (escalate, approve, reject). Conditions are CEL over the inputs, environment, project, item, VMs, user and quota, and what an update changes (see [Updates](#updates)). |
| **Lease** | Default and longest leases per environment or project, extensions, the lifetime, the end (power off then delete, delete, or only tell the owner), and the enforcement. Lease rules give another lease to the deployments a condition matches (see [Lease rules and lifetime](#lease-rules-and-lifetime)). |
| **Day-2** | Who may run which action, where, which need approval (deleting in production, by default), and the enforcement. |
| **Limits** | How many deployments each user or each project may have, and how much vCPU and memory each deployment may take (see [Deployment limits](#deployment-limits)). |
| **Naming** | Name templates (`{project}-{environment}-{inputs.role}{seq:03}`), offered by item, project or environment, with where the counter starts and how much it moves (**Start at** 100 and **Increment** 10 give 100, 110, 120). A counter in use goes on from its last number, and jumps to the start when you raise it past that number. Names are never given twice, and are checked free in vCenter and DNS. |
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
memory, the quota with the least room left once the request is done.

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
limit of 1 per user in `dev`, a user who has a deployment in `tst` can't
request one in `dev`. A soft global policy gives way to a project's
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
- **Project roles** (`roles`), built-in or custom, by key: whoever holds
  one in the request's project when the level opens, given it themselves
  or through a group mapped to it there. A role held for all of rvav
  doesn't count. One policy can then ask every project's supervisors.
- **The requester's manager** (`manager: true`): the user the
  requester's manager claim names, by email or subject, as rvav stored it
  at their last sign-in ([the identity provider](#the-identity-provider)).
  The manager must have signed in to rvav once.

`GET /api/v1/admin/approval-policies` lists the roles a level may name in
`level_roles`: the roles given in projects. A level that names a role
given for all of rvav only, such as `auditor`, or no role at all, isn't
saved.

In **Administration › Policies**, the side panel of an approval or day-2
policy gives each level an **OIDC group**, the **Project roles** it asks,
one checkbox per role in `level_roles`, and **The requester's manager**.
The policy list shows whom each level asks, for example "Supervisor, the
requester's manager". On a request's page and in the approvals inbox, a
level that asks roles or the manager says so, for example "Asks whoever
holds Supervisor in the project and the requester's manager".

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
  conditions read the kind of request and what the update changes:

  | Variable | Value |
  |---|---|
  | `request.kind` | `provision` for a new request, `update` for an update. |
  | `update.version_changed` | `true` when the update moves the deployment to another version. |
  | `update.from_version`, `update.to_version` | The version numbers, such as `3`. |
  | `update.vcpu`, `update.memory_gb`, `update.storage_gb`, `update.vm_count` | The net change, negative when the update frees some. |
  | `update.added`, `update.removed` | The number of VMs added and removed. |
  | `update.changes` | The kinds of change, such as `["resize", "vm_add"]`: `resize`, `disk_add`, `disk_extend`, `disk_remove`, `nic_add`, `nic_move`, `nic_remove`, `vm_add`, `vm_remove`, `job`. |
  | `update.destructive` | `true` when the update deletes a disk, an adapter or a VM. |

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
- **a VM blueprint:** one VM or several, with their guest settings;
- **an AWX job or workflow:** imported from AWX's templates with the
  catalog label, their survey becoming the form;
- **a composite:** VMs, jobs, custom workflows, REST calls and waits for a person, a time or a condition in steps;
- **a custom workflow**, run by your own worker.

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
    person, a wait for a time, or a wait for a condition. It goes after the
    selected step, at the same time as it, or at the end. Its inputs
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
  undo or an input from a step, or on a side of a branch, a step using a later
  one or another side's, a message showing a secret input, a REST call whose
  host isn't allowed or whose secret is gone. Save the draft, publish it, and
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
7. Select **Add**.

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
  ([ADR-0018](../adr/0018-awx-day2-actions.md)).
- **Subscriptions:** events (requests submitted, finished, VMs changed)
  sent to webhooks or AWX ([ADR-0021](../adr/0021-event-subscriptions.md)).
  - A **blocking** subscription holds a request until it answers, and can
    add properties to it.
  - Each subscription's runs, their errors and retries are on its page.
- **Custom workflows:** your own Temporal workflows, on your own task
  queue, offered as items or composite steps
  ([custom-workflows.md](../custom-workflows.md)).

Each of these pages is a list. A row's menu changes the record in a side
panel and turns it off or on (a day-2 action is offered or not). A
subscription or custom workflow can also be removed from its menu: rvav
first says what stops. A subscription's row opens its page, where
**Change subscription** is the main action and a menu has the rest.

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
  5. Select **Save plan**. Its page opens.

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
  Delete them there, or adopt their VM.

## Watching

- **Reports:** requests by period and item, success rate, provisioning
  time, approval lead time, quota use, leases ending
  ([reports.md](../reports.md)).
- **Audit log:** every administrative change and approver edit, with who
  made it. Search and filter it by what changed, project and dates;
  select an event's time to see its detail in a side panel. **Export
  CSV** keeps it for the archive.
- **Secrets:** stored secrets (domain-join accounts, webhook signing
  secrets, credentials for value sources) are sealed with the secrets key.
  They are written, never read back.
