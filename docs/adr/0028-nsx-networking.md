# ADR-0028: NSX networking

- Status: accepted
- Date: 2026-10-30

## Context
vRA places VMs on NSX networks it creates or reuses, and puts them in
security groups whose firewall rules it writes (`Cloud.NSX.Network`,
`Cloud.SecurityGroup`). The implementation plan left NSX out of 1.0
(feature 27: "AWX playbooks later"), and ADR-0007 sent everything outside
vCenter to AWX. The vRA importer (ADR-0027) therefore reports NSX
resources as left out, so templates that use them can't move.

A playbook can create a segment, but it can't do what rvav's own steps do:
- run before the VMs are cloned and after they are deleted, in the same
  timeline;
- be undone when a request fails;
- show in the deployment's page;
- be checked for drift.

Teams that used vRA's NSX resources expect them to work the same way.

Load balancers are left out by decision: requests don't make them, and
the importer keeps reporting them.

## Decision

### Native, through the Policy API
rvav talks to the NSX Manager itself, through NSX's **Policy API**
(`/policy/api/v1`). That API is declarative:
- **Writes:** a `PATCH` of an object by its ID creates or updates it,
  so a step that runs twice does no harm.
- **Deletes:** a `DELETE` of something already gone is not an error.

NSX 4.x is the target. NSX-T 3.x has the same Policy API for what rvav
uses.

`internal/integrations/nsx` holds:
- **The client:** a user and password (basic authentication), or a client
  certificate (a principal identity), which NSX recommends for
  automation.
- **The fake:** an in-memory NSX answering the same calls, for the tests
  and the compose stack (`rvav fake-nsx`).

The NSX Manager is set in Administration › Integrations, like vCenter and
EfficientIP (ADR-0020): its password is sealed, and an environment
variable wins.

### What rvav owns, and what it only reads
rvav **reads**, and refers to:
- Tier-1 gateways;
- existing segments and groups;
- services (HTTPS, SSH...);
- transport zones.

It never changes them. It **creates** only these, each for one
deployment:
- **segments**, on a Tier-1 its placement names;
- **groups**;
- **a security policy** holding the deployment's firewall rules;
- **tags on its VMs.**

Every object rvav creates:
- has an ID derived from the deployment's;
- is tagged with rvav's instance and the deployment (scope `rvav`);
- is only ever deleted by rvav if it carries that tag.

Gateways, Tier-0s, NAT, VPN and the gateway firewall are not rvav's.
An "outbound" network relies on the source NAT its Tier-1 already has.

### NSX objects follow the deployment
- **Steps in the request's workflow:**
  - segments and groups are made before the VMs are cloned;
  - the VMs' tags are set once NSX sees the VMs;
  - all of it is deleted after the VMs, when the deployment is.
- **Failures:** each step is a Temporal activity with retries, undone
  with the request when it fails (ADR-0013).
- **Group membership is by tag, not static.** A group made for a
  deployment holds the VMs carrying its tag. An existing group is joined
  by the tag its own conditions select. So membership survives a VM
  being rebuilt or moved, and drift shows when someone changes a tag in
  NSX.
- **Addresses:** EfficientIP stays rvav's IPAM (ADR-0015). A
  per-deployment segment gets its subnet from an EfficientIP block named
  in the placement rule. VMs keep their static addresses from guest
  customization, so segments need no DHCP.
- **vCenter:** an NSX segment appears there as a port group. A VM joins
  it like any network, once NSX has realised the segment in vCenter.
- **Day-2 actions** add a VM to a group, or remove it, decided by day-2
  policies (ADR-0013).

### The account
rvav needs an NSX role that can:
- read the inventory;
- write segments, groups and security policies (under the `default`
  domain);
- tag VMs.

It needs nothing on gateways. `docs/operations/configuration.md` lists the
role.

## Consequences
- **Plan coverage:** NSX (feature 27, weight M) moves from "No" to
  built, about 89% of vRA by the plan's count. The importer maps
  `Cloud.NSX.Network` and `Cloud.SecurityGroup` instead of reporting
  them.
- **One more integration to run and watch:** its own metrics, and its
  checks under Integrations.
- **A real NSX Manager must confirm the calls.** They follow NSX 4.x's
  API reference and are tested against the fake; the Lab acceptance runs
  them against the organisation's NSX.
- **Load balancers stay out.** Templates with one import with it
  reported. An AWX job in a composite item can make one, as before.
