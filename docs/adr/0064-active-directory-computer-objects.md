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
environment, or an environment in any project. One row applies to a VM,
the first of:

1. the row for its project and environment;
2. the row for its project, any environment;
3. the row for its environment, any project.

None means no computer object: placement is opt-in, so a site that turns
the integration on changes nothing until it says where. The row that
applies gives everything: its OU, its groups, its override. An OU or
group is a DN, or a DN relative to the base DN (`OU=Servers`, as vRA's
relative DNs are), and must be under the base DN; both are checked when
the row is saved and again when a request is evaluated, as the base DN
may have changed.

A project's rows are on its page, an environment's on its settings.
Those who manage placement (`placement.manage`) manage them: in the
project for its rows, for all of rvav for an environment's. The API is
`/admin/ad-placements`. There is no Terraform resource yet.

A blueprint's guest may set `active_directory: { ou, skip }`. The row's
`allow_blueprint_ou` makes them count; otherwise they are ignored, as
vRA does, and the plan shows the OU used.

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
it replay. Its activity records an `ad.computer` resource first, then
adds the object: an activity tried again finds the object it made by its
DN and description and goes on. An object of the same name that rvav
didn't make, whoever's it is, fails the step without retry, and rvav
deletes nothing it didn't make.

### When a name is taken
A name is checked when the request is evaluated, as its DNS name is
(ADR-0038): a computer object of that name anywhere under the base DN
makes it taken, so a naming rule skips to the next number and a name
given by the requester is refused, with the reason `ad`. A name longer
than 15 characters, NetBIOS's limit, is refused when an object is to be
made. A Windows domain join naming another domain than the base DN's is
refused.

There is no "reuse": rvav can't tell a stale account from a live one of
another machine, and taking over an object resets its trust with the
machine that has it. A stale object is deleted by the directory's
administrators, and the request is evaluated again.

### Removal
The object goes when its VM does:

- deleting the deployment: `ad.computer.delete`, after the VMs and
  before the DNS records, retried as a failed delete is, so the
  deployment stays to be deleted again;
- a build that fails after the object was made: the rollback deletes it
  with the VM;
- scaling in, or an update that removes a VM: its own object;
- an object already gone counts as deleted.

With rollback off, a failed build keeps its object for the retry, as it
keeps the VM.

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
The vRO triage maps `std-ad-computer` and `createComputerAD` workflows to
this feature rather than to AWX. The importer reads vRA's Active
Directory integration and its projects' relative DNs when the export has
them, and suggests the placements; a blueprint's
`activeDirectory.relativeDN` and `ignoreActiveDirectory` become
`active_directory.ou` and `skip`.

## Consequences
- The service account needs, on the OUs the placements name only: create
  and delete computer objects, write their `cn`, `sAMAccountName`,
  `dNSHostName` and `description`; write `member` on the groups listed;
  read the base DN to find taken names. Nothing on the domain itself.
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
