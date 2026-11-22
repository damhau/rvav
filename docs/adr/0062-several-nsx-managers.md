# ADR-0062: Several NSX Managers

- Status: accepted
- Date: 2026-11-22
- Builds on the NSX networking of [ADR-0028](0028-nsx-networking.md) and
  the several vCenters of [ADR-0024](0024-multi-vm-blueprints.md).
  I1.1 (#174), C2 of the parity programme.

## Context
rvav talks to one NSX Manager, the NSX integration's (ADR-0028). An NSX
Manager sees the VMs of the vCenters registered with it as compute
managers, and a vCenter is registered with one NSX Manager. Organisations
with a site per data centre often have an NSX Manager per site, each with
its own vCenters. vRA links an NSX cloud account to each vCenter cloud
account; its deployments' networks and security groups are made on the
NSX of the VMs' vCenter.

With one NSX Manager, rvav makes a VM's segment and its groups where the
VM's vCenter may not be: the segment never becomes a port group there,
and NSX never sees the VM to tag it. The request fails half-way, after
its subnet and DNS records are made.

## Decision

### A list of NSX Managers, each serving vCenters
Following the several vCenters of ADR-0024:

- **The first is the NSX integration's.** Its settings stay where they
  are (Integrations, `RVAV_NSX_*` or a settings file), unchanged. The API
  shows it by the host name of its URL, with `primary: true`. Inside
  rvav (plans, resources, the database), its name is `""`.
- **Administrators add others** under Administration › Integrations ›
  NSX: a name, an HTTPS address, an account and its password, or a
  principal-identity certificate and its key by their paths on rvav's
  servers, and the certificate checked, pinned or not checked, as for the
  integration's. The password is sealed under `nsx:<name>` and resealed
  by `rvav secrets rotate`. **Test** signs in, shows the certificate and
  reads Tier-1 gateways, segments and groups, as the integration's test
  does.
- **Each vCenter is served by one NSX Manager at most** (the
  `vcenter_nsx` table; `""` is the vCenter integration's vCenter).
  Linking a vCenter to an NSX Manager moves it from the one that served
  it. An administrator links vCenters in the same panel, the
  integration's NSX Manager too.
- **Upgrading changes nothing.** The migration links every vCenter that
  exists, the integration's included, to the integration's NSX Manager.
  A vCenter added later is served by none until it is linked: in another
  site it is most often under another NSX Manager, and a segment made on
  the wrong one fails half-way.
- **The API** is `/admin/nsx-managers`, as `/admin/vcenters` is.
  `/admin/integrations/nsx` keeps working as before. The CLI, the
  Terraform provider and the PowerShell module manage neither vCenters
  nor integrations, so they don't change.

### NSX objects follow the VM's vCenter
- **At evaluation**, a request's segments and security groups are on the
  NSX Manager of its VMs' vCenter. A request whose VMs need NSX (a
  segment made for the deployment, security groups, or an NSX group to
  join) in a vCenter no NSX Manager serves is refused there (`no_nsx`),
  naming the vCenter, before anything is made. One request's NSX objects
  are on one NSX Manager: VMs in vCenters of two NSX Managers are refused
  too (`nsx_managers`).
- **The placement rule that makes segments** is the first that does and
  names no vCenter, or a vCenter of that NSX Manager. With one NSX
  Manager, that is the first rule that makes segments, as before.
- **The plan carries the NSX Manager's name** (`nsx` on segments,
  security and VMs, omitted for the integration's), so that workflow
  histories from before replay as they ran.
- **Resources record it** (`nsx_manager` on `nsx.segment`, `nsx.group`,
  `nsx.policy` and `nsx.membership`). Deletes, day-2 group changes and
  tag drift go to the NSX Manager recorded; a resource without one is the
  integration's, where every NSX object was made before.
- **The tag sync** reads each NSX Manager's VMs.
- **A vRA deployment adopted** (ADR-0027) takes its NSX objects from the
  NSX Manager of its VMs' vCenter.
- **An NSX Manager can't be removed** while a deployment not deleted has
  an NSX object on it.

### Where segments and groups are chosen
- The **`nsx.*` value sources** offer the segments, Tier-1 gateways,
  groups and services of the NSX Manager serving the vCenter of the
  `cluster` or `vcenter` parameter; without them, of those serving the
  vCenters the project's placement uses in the environment; without an
  environment, of every NSX Manager.
- **Saving a catalog item** checks that each NSX group and service it
  names is on at least one NSX Manager: the environment, and so the NSX
  Manager, is chosen when it is requested.
- **Saving a placement rule that makes segments** checks its Tier-1 on
  the NSX Manager of the vCenter it names. A rule that names none needs
  the Tier-1 on every NSX Manager that serves a vCenter.

## Consequences
- **C2 of the parity programme** moves from Partial towards Full: NSX-V
  stays out.
- **A multi-VM item across sites with different NSX Managers** is
  refused rather than split. Splitting its NSX objects by NSX Manager is
  possible later: the plan already carries the NSX Manager per segment
  and per VM.
- **Moving a vCenter to another NSX Manager** doesn't move what existing
  deployments have in NSX: it is changed and deleted where it was made.
- **A principal-identity certificate is read when rvav connects**: a
  certificate replaced on disk is read again when the NSX Manager's
  settings change or rvav restarts, as the integration's is.
- **A real NSX must confirm** that two NSX Managers each see only their
  vCenters' VMs and port groups; the fake NSX of the tests sees every VM
  it is told about.
