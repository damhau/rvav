# Cutover from vRA

How projects move from vRA to rvav one at a time, how vRA and rvav run
side by side meanwhile, and when vRA can be switched off
([ADR-0027](../adr/0027-vra-importer.md)). The importer is in
[vra-import.md](vra-import.md).

rvav never writes to vRA. What changes in vRA, vRA's administrators do,
by this runbook. rvav reads vRA again to check it was done.

The **Cutover** tab of Administration › vRA import tracks each vRA
project:

| Check | Done when |
|---|---|
| rvav project | It maps to an rvav project. |
| Catalog items | Every cloud template vRA offers it is imported and published in rvav. |
| Shared in rvav | Those items are offered to its rvav project. |
| vRO and ABX items | By hand: replaced as the vRO triage says. |
| Deployments adopted | Every deployment of it is adopted. |
| Leases in vRA | No adopted deployment still has a lease in vRA (see below). |
| People | Everyone vRA names in it has signed in to rvav once. |
| Policies | By hand: set in rvav from the plan. |
| vRA offers it nothing | Once moved: vRA no longer offers it any item. |
| Nothing new in vRA | Once moved: no deployment was made in vRA since. |

A project is **ready** when it is mapped and its items are published and
shared. Its deployments can follow after it moves.

## Two things vRA still does to adopted deployments

Adopting a deployment changes nothing in vRA. vRA still has its own
record of it, and still acts on it:

- **vRA deletes it when its vRA lease ends**, after the grace period,
  even though rvav now manages it. For each adopted deployment, remove
  its lease in vRA (or extend it past the day vRA is switched off). The
  check *Leases in vRA* lists those still leased.
- **Deleting it in vRA deletes its VMs.** Never delete an adopted
  deployment in vRA. Leave it there until vRA is switched off, or remove
  it from vRA's records only, if your vRA version can do that without
  deleting the machines.

## Before a project moves

1. **Read vRA** (or upload a fresh `rvav vra-export`).
2. **Map the project** (Projects tab), if its rvav project has another
   name. Create the rvav project first if there is none, with its
   environments, placement rules, quotas and members or groups.
3. **Import its catalog items** and review each draft:
   - fix what the notes say to check;
   - replace what was left out: a value source for a vRO dropdown, an
     AWX job for an NSX load balancer;
   - publish it.
   Import again to share the items with the project.
4. **Set its policies** in rvav from the Policies tab: approvals, leases,
   day-2 and quotas.
5. **Replace its vRO and ABX items** as the vRO triage says. Usually that
   means AWX job templates as catalog items.
6. **Tell its people:**
   - the date;
   - rvav's address;
   - that they sign in with their usual account;
   - that their deployments will appear in rvav.
   The *People* check lists who hasn't signed in yet.
7. **Adopt its deployments** (Import… › Deployments). Do it before the
   move or after; vRA keeps those not adopted yet. For each one that
   stays in vRA, the Deployments tab says why.
8. **Remove the vRA leases** of adopted deployments (see above).

The Cutover tab shows the project **Ready** when steps 2 and 3 are done.

## Moving it

On the day, in this order:

1. **In vRA, vRA's administrators:**
   - stop offering the project's items: remove its content sharing, or
     its catalog item entitlements;
   - make its members viewers, so nothing is changed from vRA any more.
2. **In rvav:** Cutover › the project › **Mark as moved…**, with a note
   if useful (the change ticket, say).
3. **Read vRA again.** On the project:
   - *vRA offers it nothing* should be done;
   - *Nothing new in vRA* lists any deployment made in vRA since the
     move. Adopt those.

New requests now go to rvav. Its people find their adopted deployments
there, with their day-2 actions.

## The parallel run

Until every project has moved, vRA and rvav both run:

- **Projects not yet moved** request in vRA as before. Each new read and
  import picks up what changed in vRA. Run them as often as needed: an
  import changes only what changed.
- **Moved projects** request in rvav. Their deployments still in vRA stay
  there until adopted; day-2 actions on those happen in vRA meanwhile.
- **A template changed in vRA** after it was imported gives its rvav item
  a new draft at the next import, to review and publish.

## Moving a project back

**Not moved** on the project undoes the mark, and vRA's administrators
offer its items again. Its adopted deployments stay in rvav: rvav can't
give them back to vRA, which still has its own record of them.

## Switching vRA off

The Cutover tab says when this can happen: every project moved, and
every deployment adopted.

1. **Deal with what is left.** For each deployment still in vRA, its
   reason says what to do:
   - its VMs are gone: delete it in vRA;
   - its owner has left: give it a new owner in vRA, then adopt it;
   - otherwise, adopt it by hand from Onboarding.
2. **Keep a record.** Run `rvav vra-export -o vra-final.json` and keep the
   file with the organisation's records. Do the same with the vRO triage
   report.
3. **Switch vRA off**, by the organisation's procedure for it. vRA mustn't
   come back with its leases running.
4. **Remove vRA from rvav's configuration:** `RVAV_VRA_URL` and its
   credentials. Revoke vRA's account for rvav. The importer's page keeps
   the last estate read, for reference.
