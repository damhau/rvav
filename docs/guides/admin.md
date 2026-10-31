# Administering rvav

This guide is for administrators and catalog admins: what to set up so
people can request, and how to look after it. Installing and running rvav
is in the [operations guides](../operations/README.md); using it, in the
[user guide](user.md).

**Roles:**
- **Administrator:** everything under **Administration**.
- **Catalog admin:** the catalog only.
- **Auditor:** reads everything and changes nothing.

Roles are given to groups of the identity provider. Every change made
here is in the **Audit log**.

**The local administrator,** `admin`, is rvav's only local account
(ADR-0030). It signs in on rvav's sign-in page, below single sign-on:
- at first, before an identity provider is set up;
- when the identity provider is down.

Its first password is given at installation, or generated and logged
once, and must be changed at the first sign-in (**Account › Change
password** later). From the third failure in a row, sign-in waits, longer
each time, up to 15 minutes. Every sign-in and failure is in the audit
log (**Sign-ins**). A forgotten password is reset on the server:
`rvav admin reset-password`.

## First steps

The order that gets a first request through:

0. **Sign in as `admin`** and choose its password.
1. **The identity provider** (**Integrations › Identity provider**), and
   a group of it mapped to `admin` (**Projects › OIDC group mappings**):
   [the identity provider](#the-identity-provider).
2. **Integrations:** vCenter, AWX and EfficientIP, with rvav's service
   accounts ([integrations.md](../integrations.md)). Each has a test. Its
   password or token is sealed and never shown again.
3. **Infrastructure:**
   - **Templates:** the vCenter templates offered, by OS.
   - **Sizes:** the CPU, memory and disk choices.
   - **Networks:** each vSphere network's subnet. The subnets come from
     EfficientIP's class parameters; set one by hand where EfficientIP has
     none.
   - **Placement:** which cluster and datastore serve which environment and
     project.
4. **Projects:**
   - each with its members or the groups that map to its roles (viewer,
     member, project admin);
   - its quotas, per environment or for the project as a whole, hard or
     soft.
5. **Policies.**
6. **The catalog.**

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
3. **Test provider:** rvav reads the discovery document and the signing
   keys. The browser fetches tokens at the provider, so its token endpoint
   must be at the issuer's origin.
4. **Try signing in:** a window signs in at the provider with the settings
   in the panel, before they are saved. The panel then shows the token's
   claims, the groups rvav reads, and the roles the group mappings give,
   or why rvav refuses the token. Nothing is saved, and no user is made.
5. **Map a group to `admin`** (**Projects › OIDC group mappings**), and
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

## Policies

**Policies** has a tab each. **What applies here** shows, for a project
and an environment, which policies would apply to a request and why.

| Tab | What it decides |
|---|---|
| **Approval** | Which requests need approval, from whom (users or groups, in levels, any or all), and what happens on timeout (escalate, approve, reject). Conditions are CEL over the inputs, environment, project, item, VMs, user and quota. |
| **Quotas** | Projects' limits, also set on each project. A hard limit refuses; a soft one asks for approval. |
| **Lease** | Default and longest leases per environment or project, extensions, and the end: power off then delete, delete, or only tell the owner. |
| **Day-2** | Who may run which action, where, and which need approval (deleting in production, by default). |
| **Naming** | Name templates (`{project}-{environment}-{inputs.role}{seq:03}`), offered by item, project or environment. Names are never given twice, and are checked free in vCenter and DNS. |

## The catalog

**Catalog items** lists the items, their versions and who may request
them. An item is a form and a blueprint
([service-catalog.md](../service-catalog.md)):
- **a VM blueprint:** one VM or several, with their guest settings;
- **an AWX job or workflow:** imported from AWX's templates with the
  catalog label, their survey becoming the form;
- **a composite:** VMs, jobs and custom workflows in steps;
- **a custom workflow**, run by your own worker.

An item's tabs:
- **Details:** name, category, icon, description.
- **Form designer:** fields, sections, rules, value sources (lists from
  vCenter, AWX, EfficientIP or a REST call), and fields approvers may
  change. Preview the form as any user.
- **Definition:** the whole version as YAML, to review or paste.
- **Versions:**
  - publish a draft;
  - compare two versions;
  - deprecate one: requests keep the version they were made with.
- **Entitlements:** which projects may request it.

Items can be exported and imported as YAML, between installations.

### The catalog in Git

The catalog can be kept in a Git repository
([ADR-0029](../adr/0029-catalog-in-git.md)), so it is reviewed in merge
requests and the same in every installation. Set the repository in
**Integrations › Git** (configuration.md lists the settings): an HTTPS
URL with a token, or an SSH URL with a deploy key and the server's host
keys (put the token in its own field, never in the URL). **Test
connection** signs in and reads the branch. rvav needs to push to the
branch, never to force a push. Saved settings apply when rvav's processes
restart: until then, Catalog items shows no repository and nothing is
synced.

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
- **Never in the repository:** drafts, entitlements, secrets' values, and
  items imported from AWX.

**Catalog items** shows the repository as last read and the files that
need you; **Read the repository now** reads it at once (otherwise every 5
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
  discarding rvav's draft.
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

## Existing VMs and EfficientIP

- **Onboarding:** adopt VMs rvav didn't build. Choose them in vCenter and
  give each a project, an owner and an item. rvav finds their addresses
  and DNS records in EfficientIP, and nothing is changed there
  ([ADR-0026](../adr/0026-onboarding.md)).
- **EfficientIP reconcile** (under Integrations): addresses and records
  tagged as rvav's that no deployment owns, left by failures or restores.
  Delete them there, or adopt their VM.

## Watching

- **Reports:** requests by period and item, success rate, provisioning
  time, approval lead time, quota use, leases ending
  ([reports.md](../reports.md)).
- **Audit log:** every administrative change and approver edit, with who
  made it. Search it, and export it as CSV for the archive.
- **Secrets:** stored secrets (domain-join accounts, webhook signing
  secrets, credentials for value sources) are sealed with the secrets key.
  They are written, never read back.
