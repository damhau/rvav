# ADR-0064: Active Directory computer objects

- Status: accepted
- Date: 2026-11-22
- Builds on the integrations and trusted certificates of
  [ADR-0020](0020-administration.md) and
  [ADR-0030](0030-settings-and-local-administrator.md), the names of
  [ADR-0038](0038-names-groups-secrets-properties.md), the events of
  [ADR-0049](0049-event-topics.md) and the vRA importer of
  [ADR-0027](0027-vra-importer.md). I1.3 (#176) of the parity programme.

## Context
vRA's Active Directory integration makes a VM's computer object in an OU
before the VM is built, and deletes it with the VM: the OU is set per
project (a DN relative to the integration's base DN), which a blueprint
may override (`activeDirectory.relativeDN`) or skip
(`ignoreActiveDirectory`) when the project allows it. Sites without it
ran the standard vRO workflow (`std-ad-computer`) on a subscription.
rvav had neither: a Windows VM joined its domain from the customization
specification, making its account wherever the domain's default puts
computers, and nothing removed it when the VM went. A Linux VM joined in
post-configuration, if at all.

## Decision

### One directory, over TLS
The integration `ad` (Administration › Integrations › Active Directory,
or `RVAV_AD_*`) names a domain controller, a service account and its
password, and the domain's base DN. rvav speaks LDAP with
`github.com/go-ldap/ldap/v3`:

- `ldaps://` is LDAPS (636 by default), `ldap://` is LDAP upgraded with
  StartTLS (389) before the bind;
- plain LDAP only when `plain_ldap` is ticked, which the form warns
  about, as the password and every object go in the clear; ticking it
  needs the password again, as a new address or pin does;
- the certificate is checked as every connection's: the system's CAs, the
  CA bundle, a pin (`cert_sha256`) or `insecure`. The connection's test
  shows it, read with StartTLS for `ldap://`, before any password is sent;
- the password is sealed under `integration:ad`, as other integrations'
  secrets.

The test binds and reads the base DN. Settings apply from the next
computer object made or deleted: each one binds anew, as the integration
is read on every use.

**One domain.** Several directories would need what several NSX Managers
needed ([ADR-0062](0062-several-nsx-managers.md)): a table, its CRUD, a
form, and a way to say which projects use which. Sites with several
domains in one forest can use one domain's controllers if the service
account may write the other's OUs; otherwise this waits for a site that
needs it.

### Where an object goes
`ad_placements` rows hold an OU, groups and whether a blueprint may
choose its own OU, for a project and an environment, a project in any
environment, an environment in any project, or every VM. One row for
each; one row applies to a VM, the first of:

1. the row for its project and environment;
2. the row for its project, any environment;
3. the row for its environment, any project;
4. the row for any project in any environment.

None means no computer object: placement is opt-in, so a site that turns
the integration on changes nothing until it says where, and rows count
only while the integration is set. The row that
applies gives everything: its OU, its groups, its override. An OU or
group is a DN, or a DN relative to the base DN (`OU=Servers`, as vRA's
relative DNs are: one with no `DC=` part), and must be under the base
DN. Saving a row checks this and, once the integration is set, that the
directory has the OU and the groups; a directory that can't be reached
refuses the row. A request's evaluation checks the DNs again, as the base
DN may have changed.

A project's rows are on its Placement tab, with those of every project
that may apply to it; every project's rows are under Projects ›
Environments. Those who manage placement (`placement.manage`) manage
them: in the project for its rows, for all of rvav for every project's.
Removing a row leaves the objects made: they go with their VMs. The API is
`/admin/ad-placements`. There is no Terraform resource yet.

A blueprint's guest may set `active_directory: { ou, skip }` (one or the
other; the OU a DN as the rows' are). The row's `allow_blueprint_ou`
makes them count; otherwise they are ignored, as vRA does, and the plan
shows the OU used: the step's detail is the object's DN.

### The object
Before the VM is cloned, after its DNS records, rvav adds
`CN=<NAME>,<OU>` with `sAMAccountName` `<NAME>$`,
`userAccountControl` 4096 (a workstation or server account),
`dNSHostName` the VM's FQDN (its name and the domain of the base DN when
it has none) and the description `rvav: request <id>, deployment <id>`.
It then adds the object to the row's groups. Windows and Linux alike:
a Windows VM joining the domain takes the existing object (its domain
join's OU becomes the object's), and `realm join` or `adcli join` on
Linux do too.

The step is `ad` in the plan, after DNS and before the clone, in every
place a plan is built (a VM, a component's VMs, a composite's own VMs),
behind `workflow.GetVersion("ad-computers")` so that runs started before
it replay; a component's VMs, in a multi-VM item or a composite's step
that is one, behind a change of their own, `ad-computers-components`
(I1.6: until then their plan showed the step but their build made no
object). Its activity records an `ad.computer` resource first, then
adds the object: an activity tried again finds the object it made by its
DN and description and goes on. An object of the same name that rvav
didn't make, whoever's it is (one made since the request was evaluated),
fails the step without retry and is forgotten, and rvav deletes nothing
it didn't make: before deleting or renaming an object it reads it again,
and leaves one whose description isn't the one it wrote, saying so in
the step. The resource, `ad.computer`, shows on the deployment's page
under Active Directory, with its DN and groups. The activities reach the
directory through a `Directory` interface, read from the integration on
every use.

### When a name is taken
A name is checked when the request is evaluated, as its DNS name is
(ADR-0038): a computer object of that name anywhere under the base DN
makes it taken, so a naming rule skips to the next number, as it does
past a name vCenter or DNS has (ADR-0020), with the reason `ad`. Every
VM name rvav gives comes from a naming rule with a counter, so a request
is never refused for it; a name the requester gives, a day-2 rename's,
is refused when it is submitted ("Active Directory already has a
computer NAME$."), and checked again when the rename runs (`ad.check`),
before anything is renamed. A name longer than 15 characters, NetBIOS's
limit, is refused when an object is to be made, for a request and a
rename. A Windows domain join naming another domain than the base DN's
is refused. A directory that can't be reached refuses the submission,
as EfficientIP's DNS does; the preview only logs it.

There is no "reuse": rvav can't tell a stale account from a live one of
another machine, and taking over an object resets its trust with the
machine that has it. A stale object is deleted by the directory's
administrators, and the request is evaluated again.

### Removal
The object goes when its VM does:

- deleting the deployment: `ad.computer.delete`, after the VMs and
  before the DNS records, retried as a failed delete is, so the
  deployment stays to be deleted again;
- a clone that fails after the object was made: there is no VM, so the
  object is deleted at once and forgotten, rollback or not, and the
  step says so ("Removed computer object NAME$"); a retry makes it
  again. When the directory refuses, the step fails with why and the
  deployment keeps the object, to be deleted with it;
- a build that fails later, with rollback on: the rollback deletes it
  with the VM, after the VM and before the DNS records;
- scaling in, or an update that removes a VM: its own object;
- an object already gone counts as deleted.

With rollback off, a build that fails once the VM exists keeps its
object for the retry, as it keeps the VM. A delete the directory
refuses fails the request without retry, and says so; the request is
retried once the rights are fixed.

**Rename.** The day-2 rename renames the object too (`ad.rename`): its CN,
`sAMAccountName` and `dNSHostName`. The guest's own name and its secure
channel are its own: a joined Windows VM must be renamed in Windows too,
and a Linux one joined again.

### Events
No new topic. The object is made between `network.configure` and
`compute.provision.pre`, and deleted with `compute.removal.*`: a VM's
topics already bracket it, and a subscription that renames a VM on
`compute.allocation.pre` (ADR-0054) does so before the object is made.

### vRA import
The vRO triage puts a workflow that makes or deletes computer objects
with vRO's Active Directory plug-in (`createComputerAD`, `ComputerAD`)
and reaches nothing but vCenter and Active Directory, `std-ad-computer`
among the standard ones, in group A with no steps: this feature replaces
it, and its subscription retires. One that also does something else
stays in its group.

A template machine's `activeDirectory.relativeDN` becomes its guest's
`active_directory.ou` and `ignoreActiveDirectory` its `skip`; neither is
a custom property. The importer reads vRA's Active Directory
integrations (`/iaas/api/integrations`, type `activedirectory`) and
shows each project's relative DN, whether its templates may override
it, and whether it ignores Active Directory, in the plan's projects. It
writes no placement: the integration's credentials aren't readable, and
an administrator connects rvav's own first. vRA documents no schema for
an integration's projects, so the reader takes several spellings and
leaves out what it doesn't find: check the column against vRA's
integration page.

## Consequences
- The service account needs, on the OUs the placements name only: create
  and delete computer objects, rename them, write their `cn`,
  `sAMAccountName`, `userAccountControl`, `dNSHostName` and
  `description`; write `member` on the groups listed; read the base DN
  to find taken names. Nothing on the domain itself.
- The in-process directory of the tests can't serve a modify DN request:
  the rename's is checked against a real directory in I1.6's acceptance,
  as are a Windows join and a `realm join` onto the objects made.
- A Windows domain join onto a pre-made object needs the join account to
  be allowed to reuse it (since KB5020276, its owner, or the policy
  "Domain controller: Allow computer account re-use during domain
  join"): the join account is the service account, or one the policy
  names.
- The directory is called for every VM made and deleted: a domain
  controller that is down holds the request, retried with backoff, as
  EfficientIP does.
- Objects made before this, by vRA or by hand, are not rvav's to delete:
  onboarding a VM doesn't adopt its object.
