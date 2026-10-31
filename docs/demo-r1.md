# R1 demo script

About 45 minutes, on the Docker Compose lab stack with Keycloak sign-in
(vcsim, the fake SOLIDserver and the fake AWX), or on the pilot environment
once vCenter, EfficientIP, AWX and the identity provider are connected. It
covers what R1 adds to R0 ([demo-r0.md](demo-r0.md)): AWX (S6), EfficientIP
(S7), catalog administration (S8), and projects, placement and governance
(S9). Each step says what to show and what to point out.

## Before the demo

```sh
RVAV_AUTH_MODE=oidc RVAV_REST_SOURCE_HOSTS=http://rvav-cmdb \
  docker compose -f deploy/docker-compose.yml up -d --build --wait
# A stand-in CMDB for the REST dropdown in step 5:
docker run -d --rm --name rvav-cmdb --network rvav_default \
  -v "$PWD/deploy/cmdb:/usr/share/nginx/html:ro" nginx:1.27-alpine
```

| What | Where |
|---|---|
| Portal | http://localhost:3000 |
| Keycloak (users `lea`, `thomas`, `marc`; password `rvav`) | http://localhost:8081 |
| Temporal UI (workflow histories) | http://localhost:8233 |

Who is who: **Thomas** is a developer, member of Web Platform; **Léa** is
the project admin of Web Platform and a catalog admin; **Marc** is an
Infrastructure lead and an administrator. Use one private browser window
per person. Their roles come from their Keycloak groups (OIDC group
mappings, step 1.6).

## 1. A new project, from the UI (Marc, then Léa)
The S9 acceptance: a project set up end to end without SQL or YAML.

1. Administration → **Projects** → **New project**: code `pay`, name
   *Payments*, environments Development and Test, Léa as project admin.
   Point out: the code is in VM names and can't change later.
2. The project's **Placement** tab, Development → **New rule**: no cluster
   names, cluster tag `rvav-tier/bronze`, datastores `vsan-*`, networks
   `dvpg-app-dev-*`. The preview says what a request would get (the bronze
   cluster GVA-DEV-CL01, its vSAN datastore, the network with its subnet
   from EfficientIP, the folder) and what would stop one. Tags are read
   from vCenter.
3. **Quotas** → a hard limit of 2 VMs in Development, and 16 vCPU for the
   whole project; a soft limit sends requests over it to approval instead.
4. **Policies** → **Naming** → *Payments names*,
   `{project}{environment}{role}{seq:02}`, offered to Payments. **Preview
   names**: the next names, and the ones skipped because vCenter, DNS or an
   earlier deployment has them. rvav never gives a name twice.
5. **Templates and networks** → a template (Ubuntu 24.04,
   `tpl-ubuntu24.04-2026.10`) and a size offered to Payments only. A
   template missing from vCenter is flagged.
6. **Projects** (the same page) → **OIDC group mappings**: `rvav-fin` →
   Member of Payments. Everyone in that Keycloak group is a member at their
   next sign-in. Removing the last mapping to *admin* is refused.
7. Léa signs in: **Projects** shows her projects only. In Payments →
   **Members**, she adds Thomas (found by name), and can't remove herself
   while she is its only project admin.
8. Léa, as catalog admin: **Catalog items** → *RHEL 9 server* →
   **Entitlements** → Payments.

## 2. Requesting in the new project (Thomas)
1. Project switcher → *Payments* → Catalog → **RHEL 9 server**. Only the
   bronze cluster and its vSAN datastore are offered; the name is
   `paydevapi01` for the role `api`.
2. Submit: the steps run as in R0, the VM lands in `/GVA/vm/rvav/pay/dev`.
3. A third VM in Development is refused, saying what is left.
4. Marc: **Policies** → **What applies here**, Payments, Development,
   *RHEL 9 server*: which approval, lease and naming policy decide and why,
   and the quota lines with what is used.

## 3. EfficientIP (Thomas, Marc)
1. In the request from step 2, the steps **Reserved 10.31.10.x** and the A
   and PTR records come from EfficientIP (the fake SOLIDserver speaks the
   same API as a real one). Deleting the deployment releases them.
2. Marc: **Templates and networks** → **Networks**: the subnet of each
   vCenter network comes from EfficientIP's subnet class parameters; a
   mapping by hand wins until removed.
3. **Integrations** → **EfficientIP reconcile** → **Run the check**:
   addresses and records tagged for this rvav instance (`RVAV_INSTANCE_ID`)
   that no deployment owns, and the reverse. Nothing is changed.

## 4. AWX (Thomas, Marc)
1. Thomas: Catalog → **Create database user**: an AWX job template whose
   survey became the form. Submit: the job's tasks and output follow live.
2. On a VM: actions menu → **Automation** → the action made from the
   `monitoring-agent` template (its `day2` label). It runs on rvav's own
   AWX inventory with the VM as the limit.
3. Marc: **Day-2 actions**: the AWX actions, their name, what they run on,
   a CEL condition, and whether they are offered. The day-2 policies decide
   who may run them.
4. On the test AWX only: a template's survey changed in AWX is flagged in
   **Catalog items** with what changed; the form stays as it was.

## 5. Catalog administration (Léa)
The S8 acceptance.
1. **Catalog items** → *RHEL 9 server* → export a version as YAML, change its
   slug and name, **Import YAML**: a new item with a draft. Import never
   publishes.
2. **Form designer**: add a *Backup policy* dropdown with a REST value
   source on the stand-in CMDB (`http://rvav-cmdb/policies.json`, a stored
   secret as its token). The preview runs on the server, as Thomas in Web
   Platform.
3. **Publish** 1.0, open it to Web Platform, request it as Thomas.
4. Change a label, publish 1.1; **Roll back** to 1.0. Thomas's deployment
   keeps the version it was made with.

## 6. Governance and settings (Marc)
1. **Policies**: approval and day-2 policies in the order they are tried
   (move them up and down); a condition that isn't valid CEL is refused when
   saved, under its field. Lease policies, with Web Platform's override.
2. **Integrations**: vCenter, AWX and EfficientIP settings. A field set by
   an environment variable is locked and says which; the password or token
   is write-only. **Test connection** with the settings on screen. After a
   save, the panel lists rvav's processes finishing their running work and
   starting again with the settings, then "Applied", with no restart by
   hand.
3. **API tokens** (account menu): make one for a pipeline, scope and
   expiry (a year at most); the value shows once. Marc sees everyone's and
   can revoke them.
4. **Audit log**: search *Payments*; every step of section 1 is there, who
   and when. Filter by kind, **Export CSV**. Léa sees her projects' events
   only.

## What the demo does not show yet
The lab vCenter itself, a real SOLIDserver, the organisation's AWX, the
corporate identity provider and the web console through the ESXi hosts:
the [Lab acceptance milestone](https://github.com/damhau/rvav/milestone/9)
and the [R1 go/no-go report](release-r1.md).
