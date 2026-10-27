# R0 demo script

About 30 minutes, on the Docker Compose lab stack (vcsim, fake EfficientIP
and AWX, Keycloak), or on the lab vCenter once it is connected. Each step
says what to show and what to point out.

## Before the demo

```sh
RVAV_AUTH_MODE=oidc docker compose -f deploy/docker-compose.yml up -d --build
```

| What | Where |
|---|---|
| Portal | http://localhost:3000 |
| Keycloak (users `lea`, `thomas`, `marc`; password `rvav`) | http://localhost:8081 |
| Temporal UI (workflow histories) | http://localhost:8233 |

Who is who: **Thomas** is a developer, member of Web Platform; **Léa** is
the project admin of Web Platform; **Marc** is an Infrastructure lead (and
a global admin). Use one private browser window per person.

## 1. Request a VM (Thomas)
1. Catalog → **RHEL 9 server**. The form fills in cluster, datastore,
   network and the next free IP for Development; the name follows the
   naming rule (`web-dev-app00N`). Change the role and watch the name.
2. The summary shows the size, the quota after the request, no approval
   needed in Development, and a 30-day lease. Set the lease to 1 day.
3. Submit. The request page shows every step live: IP reserved in
   EfficientIP, A and PTR records, clone, customisation, power on, AWX
   inventory refresh, then the AWX job with its log. It ends **Ready**.
4. Point out: Temporal UI → the request's workflow and its history.

## 2. Approval (Thomas, then Marc)
1. Thomas requests the same item in **Production** with size **XL** (48 GB).
   The summary says it needs approval and why (two policies, one level).
2. Marc sees it in **Approvals** (sidebar badge), with the facts, lowers the
   size to 32 GB (an approver-editable field) and approves.
3. Thomas's request picks up the new size and runs to Ready.

## 3. Day-2 actions (Thomas)
On the dev deployment from step 1:
1. The VM row: power button, console button, actions menu. Disabled actions
   say why ("The VM is already powered on", "VMware Tools is not running").
2. **Power off** (confirmation, it is like pulling the plug), then
   **Power on**. Each one is a request with its own timeline.
3. **Take a snapshot**, then **Resize** to 4 vCPU (without hot add it asks to
   allow a restart), **Add a disk**.
4. **History** tab: the creating request and every action, newest first.
5. **Console**: a VMRC link (the user signs in to VMRC). The web console
   appears when the organisation configures VMware's WMKS client.
6. Deployments list: select two deployments → **Shut down guest** for both
   (one request per VM; a results list).

## 4. Policies (Léa, Thomas)
1. Thomas can't **Delete** Léa's deployment ("Only the owner and the
   project admins…"), but can operate its VMs.
2. In Production, deleting asks for the Infrastructure leads' approval.

## 5. Leases and quotas
1. Thomas's 1-day lease: the deployment's **Lease** panel shows when it
   ends and what happens then. **Extend the lease** by 10 days: done at
   once. By 120 days: it waits for Léa (beyond the 90-day maximum).
2. Expiry, without waiting a day: shorten the lease and run housekeeping
   now:
   ```sh
   docker compose -f deploy/docker-compose.yml exec postgres psql -U rvav -c \
     "update deployments set lease_expires_at = now() + interval '1 minute' where name = 'web-dev-app001'"
   docker compose -f deploy/docker-compose.yml exec temporal sh -c \
     'temporal schedule trigger --schedule-id rvav-housekeeping --address $(hostname -i | cut -d" " -f1):7233'
   ```
   The first run sends the "ends within a day" reminder (bell); after the
   minute, the next run powers the VM off (a request by **rvav**) and the
   Lease panel shows the deletion date. To show the deletion, set
   `lease_expired_at = now() - interval '8 days'` and trigger again.
3. Quota: as an admin, set a hard Development limit just above what Web
   Platform uses (`insert into quotas …`, see `TestQuotas`), then request
   another VM: refused with what is left. A soft limit asks for approval
   instead.

## 6. When something fails
Make the fake AWX fail the IdM join, then request a VM with **idm-join**:

```sh
RVAV_FAKE_AWX_FAIL_TASK="idm : join realm" RVAV_AUTH_MODE=oidc \
  docker compose -f deploy/docker-compose.yml up -d worker
```

The request fails at that step, which shows the error. Restart the worker
without the variable, then **Retry from the failed step**: only what is
left runs. Or **Roll back**: the VM, its address and DNS records are
deleted.

## What the demo does not show yet
The lab vCenter itself (guest operations with VMware Tools, hot add, the
web console through the ESXi hosts), real EfficientIP and AWX, and the
corporate identity provider: see "Needs real infrastructure" in
[progress.md](progress.md).
