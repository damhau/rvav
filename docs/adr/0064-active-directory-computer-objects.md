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
answers 502, and the row isn't saved (I1.6: it answered 422, as if the
row were wrong). A request's evaluation checks the DNs again, as the base
DN may have changed.

A project's rows are on its Placement tab, with those of every project
that may apply to it; every project's rows are under Projects ›
Environments. Those who manage placement (`placement.manage`) manage
them: in the project for its rows, for all of rvav for every project's.
Removing a row leaves the objects made: they go with their VMs. The API is
`/admin/ad-placements`. There is no Terraform resource yet.

**Placement managed in one project only** (a custom role; the built-in
ones hold `placement.manage` for all of rvav). A group gives its
members' rights to the VMs' computer accounts, so such a role is held to
what an administrator for all of rvav delegated (I1.6), with nothing to
store: the rows of every project, which only they save, are the
delegation. A project's row's OU goes under the OU of the row for every
project in its environment, else of the one in any environment; each of
its groups goes under the row's own OU, or is one of that row's groups,
the allow-list. Without such a row, the role saves none. What the row had
already, as an administrator for all of rvav saved it, stays when the
role saves it again. The OU had to be held too: a role choosing its OU
freely could choose `CN=Users` and so name Domain Admins "under its own
OU". The cost is that delegating needs a row for every project, in an
environment or in all, which puts every project's VMs of it in the
directory; a site that wants only some projects' VMs there keeps
placement with administrators for all of rvav.

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

**Its password** (I1.6). An enabled account (4096) made with no
password is refused by a domain controller whose password policy sets a
minimum length, and `Refused` made that permanent. The choices were:
4128 (4096 with `PASSWD_NOTREQD`), which is enabled with an empty
password, an account anyone who knows its name may try; disabled
(4098), which a join must enable; or a random password. rvav sets
`unicodePwd` to 120 random characters with upper and lower case letters,
digits and symbols, as a complexity policy wants, which it keeps nowhere
and tells no one: the account is enabled and can't be signed in to.
Active Directory sets a password only over an encrypted connection, so
over plain LDAP (labs only) the object has none, as before. The service
account needs **Reset password** on the objects it makes for it.

**What a Windows join onto it needs.** The customization's domain join
names the object's OU (`DomainOU`), so the join finds the account where
rvav made it. Windows reuses an account of the computer's name instead
of making one, and sets a new password with the join account's rights:
rvav's random one never has to be known. Since KB5020276 the join
account must be allowed to reuse it: the object's owner (rvav's service
account, which made it), a member of Domain Admins, or an account the
policy "Domain controller: Allow computer account re-use during domain
join" names. It needs **Reset password** and the validated writes to
`dNSHostName` and `servicePrincipalName` on the object, which its
owner has. `realm join` and `adcli join` reset the password the same
way. I1.6's acceptance checks both against a real domain controller,
which the tests' in-process directory isn't.

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
and leaves one that is no longer at the DN rvav recorded, or whose
description isn't the one it wrote, saying so in the step. An object an
administrator moved to another OU is theirs now (I1.6: only the
description was compared). The resource, `ad.computer`, shows on the deployment's page
under Active Directory, with its DN and groups. The activities reach the
directory through a `Directory` interface, read from the integration on
every use.

### When a name is taken
A name is checked when the request is evaluated, as its DNS name is
(ADR-0038): a computer object of that name anywhere in the domain makes
it taken, searched from the DC parts of the base DN, since a
`sAMAccountName` is unique in the domain and a base DN may be one OU of
it (I1.6: only the base DN was searched, and a name taken outside it
failed the step instead of being passed over), so a naming rule skips to the next number, as it does
past a name vCenter or DNS has (ADR-0020), with the reason `ad`. Every
VM name rvav gives comes from a naming rule with a counter, so a request
is never refused for it; a name the requester gives, a day-2 rename's,
is refused when it is submitted ("Active Directory already has a
computer NAME$."), and checked again when the rename runs (`ad.check`),
before anything is renamed. A name longer than 15 characters, NetBIOS's
limit, is refused when an object is to be made, for a request and a
rename: every name a request gives, since a later VM's number may be
longer than the first's (`-99`, then `-100`), and again when the names
are given for good, in the submission or an update adding VMs, as
another request may have taken those the evaluation counted on (I1.6:
only the first name was checked). A Windows domain join naming another domain than the base DN's
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
and a Linux one joined again. When the object is gone, an administrator
having deleted it, the rename goes on: vCenter has renamed the VM
already, and its addresses and records follow. The step says so and the
deployment no longer holds the object (I1.6: the step failed for good,
leaving the VM renamed and the rest not).

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
  and delete computer objects, and delete their subtree (Windows may put
  a cluster's or BitLocker's objects under one), rename them, write
  their `cn`, `sAMAccountName`, `userAccountControl`, `dNSHostName` and
  `description`, reset their password; write `member` on the groups
  listed; read the domain to find taken names, which every authenticated
  user may. Nothing else on the domain itself.
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
