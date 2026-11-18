# ADR-0039: Guest access and networking

- Status: accepted
- Date: 2026-11-18
- Extends the guests of [ADR-0024](0024-multi-vm-blueprints.md), the
  templates and user data of [ADR-0037](0037-images-per-vcenter.md), the
  secrets in templates of
  [ADR-0038](0038-names-groups-secrets-properties.md) and the importer of
  [ADR-0027](0027-vra-importer.md). T1.6 (#139).

## Context
A vRA cloud template's machine says how people sign in to it
(`remoteAccess`: a public key, a key pair vRA makes, or a user name and
password). rvav's only way was user data written by hand (parity C22).
Its first adapter can take its address from DHCP or add an IPv6 one;
rvav's always had a static IPv4 address from EfficientIP (parity C23).

## Decision

### Remote access
- `guest.remote_access` is `{kind, username, public_key, password,
  secret}`. It lives under `guest`, so a blueprint says it once for every
  component and a component's own wins, as the other guest fields do.
- `ssh_key`: the public key is written in the blueprint, read from one
  input (`${inputs.ssh_key}`, the whole value) or from a stored secret.
  A literal key is parsed when the item is saved; a key from an input or
  a secret when the VM is built, which fails the step permanently if it
  isn't one.
- `password`: from a secret input (`${inputs.x}` where `x` is secret) or
  a stored secret. A plain input is refused: its value is kept with the
  request and shown in its inputs.
- `generated_key`: below.
- Saving the item refuses fields that don't apply to the kind, an unknown
  input, a user name the OS refuses (on Linux: `^[a-z_][a-z0-9_-]{0,31}$`,
  not `root`; on Windows: up to 20 letters, digits, `.`, `-`, `_`) and a
  Linux guest with a `customization_spec`; Windows has its own rules,
  below. A request is refused, with the template's name, when the
  catalogue template its `template` binding chose doesn't allow it.
- On Linux, the user is a cloud-init `users` entry: a shell, `sudo`
  without a password (vRA's remote access user can administer the
  machine), and either `ssh_authorized_keys` with the password locked or
  `plain_text_passwd` with `ssh_pwauth: true`. It is merged with the
  template's and the blueprint's user data as ADR-0037 merges those: one
  `#cloud-config` document when all of them are, the user appended to
  their `users`; else a multipart part of its own with cloud-init's
  `merge_how: list(append)+dict(no_replace,recurse_list)+str()`. When no
  part lists `users`, `default` comes first so the distribution's default
  user stays.
- A remote access forces the cloud-init path: a Linux VM with one gets
  guestinfo metadata and user data even without `cloud_init`.
- The values are worked out in the `Customize` activity, from the inputs,
  the secret inputs and the stored secrets it opens there. Neither the
  plan, the activity's input or result, the step's detail (the user and
  the key's SHA-256 fingerprint only) nor its log carries a key or a
  password. With a password, the metadata asks cloud-init to clear
  `guestinfo.userdata` once read (`cleanup-guestinfo`), so it doesn't stay
  in the VM's configuration in vCenter.
- Each component with remote access gets a `guest.access` resource:
  component, kind, user and fingerprint, which the deployment page shows.
  Migration 00062 adds the type.

### Windows and cloudbase-init
- A catalogue template can be marked `cloudbase_init` (migration 00063):
  a Windows template with cloudbase-init and its VMware GuestInfo service,
  without a customization specification (refused together, as user data
  and a specification on Linux). A request picks it up from the template
  its `template` binding chose, or the one whose source is the
  blueprint's own `template`, and the plan's VM says `cloudbase_init`.
- Such a VM isn't customized by vCenter. `Customize` writes
  `guestinfo.metadata` and `guestinfo.userdata` as for cloud-init: the
  same metadata (cloudbase-init parses network config version 2, `to:
  default` included), with `admin-username`, `admin-password` and
  `public-keys-data`, the keys its service reads. The remote access's
  user is the admin user; with `admin_password` and no remote access, it
  is `Administrator`. The user data is the template's then the item's, as
  for cloud-init: one `#cloud-config` when both are, else a multipart
  document whose scripts are named `part-NNN.ps1`, `.cmd`, `.sh` or `.py`,
  because cloudbase-init runs a part by its file name's extension.
- cloudbase-init doesn't clear guestinfo, as cloud-init's
  `cleanup-guestinfo` does. With a password, rvav adds a last part,
  `#ps1_sysnative`, that runs VMware Tools' `rpctool.exe "info-set
  guestinfo.metadata …"` with the metadata without the password.
  cloudbase-init keeps the metadata it loaded for the rest of its run, so
  its password plugin, after the user data plugin, still sets it.
- Without cloudbase-init, a Windows VM's remote access is `password` for
  `Administrator` only: the customization specification's administrator
  password, as `admin_password` is. Anything else (a key, another user,
  user data) is refused when the request is made; with a
  `customization_spec` in the blueprint, when the item is saved.
- `remote_access` and `admin_password` are refused together on Windows:
  cloudbase-init makes one admin user from the metadata, and on the
  specification path both would set the same password.
- `domain_join` with cloudbase-init is refused: cloudbase-init's GuestInfo
  service has no domain to join. A post-configuration AWX template joins
  instead.

### Generated keys
- rvav makes one ed25519 key pair per deployment, on the first VM that
  needs it; every component with `generated_key` in the deployment gets
  its public key. The pair is made inside `Customize`, so the private key
  is never in an activity's input or result and so never in Temporal's
  history.
- The private key (OpenSSH PEM) is sealed with the secret key as stored
  secrets are and kept in `deployment_keys` (migration 00062), with the
  user, the public key and its fingerprint. A retried `Customize` reads
  the pair it stored. Rotating the secret key reseals it.
- Deleting the deployment's `guest.access` resources forgets the private
  key: the row keeps the public key and fingerprint, without the sealed
  key.
- The deployment's owner downloads the private key once:
  `POST /deployments/{id}/private-key`. It is a POST because it changes
  something: in one transaction the API process locks the row, opens the
  sealed key, forgets it (the row keeps who downloaded it and when) and
  writes the audit entry `deployment.private_key.download` with the
  fingerprint; the answer has `Cache-Control: no-store`. Anyone else,
  administrators included, gets 403 naming the owner: the key signs in to
  the owner's VMs, and an administrator who needs in has vCenter's
  console. A second download, or one after the VMs were deleted, gets 409
  saying which.
- `GET /deployments/{id}` carries the users people sign in as
  (`remote_access`, from the `guest.access` resources) and the generated
  key's state (`generated_key`: user, fingerprint, `available`, who
  downloaded it and when). The web shows them in a **Remote access**
  panel, and the key in a sheet that shows it once, with **Save as** and
  **Copy**; the key never goes into the query cache.
- The key isn't kept for its owner to download again: a key rvav can
  hand out twice goes as well to whoever has the owner's session or one
  of their API tokens, and the owner sees nothing. Handed out once, with
  who and when on the page, a second taker shows.

### DHCP on the first adapter
- `ipam: dhcp` on the blueprint or a component (a component's wins), as
  added adapters already had in `nics[].ipam`, puts the first adapter on
  DHCP. Without it, or with `efficientip`, nothing changes.
- At request time the network needs no subnet, and a subnet it has is
  not used; the FQDN is the VM's name in `guest.domain`, with no
  EfficientIP DNS check. A segment made for the deployment has no DHCP
  server, so DHCP on one is refused.
- The plan's VM carries `dhcp`. Its reserve and DNS steps are left out
  when nothing is reserved; with an added EfficientIP adapter they stay
  and cover only it. Old plans have no `dhcp`, so their workflows replay
  as before.
- vCenter's customization gives the first adapter the DHCP generator and
  no DNS servers; cloud-init and cloudbase-init network data give it
  `dhcp4`. `.vm.ip` is empty in the VM's own user data: the address isn't
  known when it is written.
- `PowerOn` waits for the guest's IP as before and returns it. For a VM
  on DHCP, the workflow uses it as the VM's address: AWX's extra vars,
  and what later components read. A retried request reads it back from
  the VM's properties.
- The VM's properties say `dhcp: true`; its address is the first of the
  first adapter's `ip_addresses`, as vCenter reports it. The deployment's
  page shows it under **Network and addresses**, and AWX's inventory
  falls back to it. The deployment has no `ipam.address` or `dns.record`
  for the first adapter, so deleting it releases nothing in EfficientIP.

### IPv6 dual stack
- `ipv6: true` on the blueprint or a component (a component's wins)
  gives the first adapter an IPv6 address beside its IPv4 one. It needs
  the IPv4 address from EfficientIP too: `ipv6` with `ipam: dhcp` is
  refused when the item is saved. Added adapters stay IPv4.
- A network's IPv6 subnet is a SOLIDserver IPv6 subnet (`ip6_*`
  services) whose `vsphere_network` class parameter names it, with a
  `gateway` parameter; the sync sets it on the network's mapping when
  the network has an IPv4 subnet and only one IPv6 subnet claims it, and
  says why otherwise. An administrator can set it by hand on the mapping
  too (`subnet6`, `gateway6`).
- At request time a VM with `ipv6` on a network without an IPv6 subnet is
  refused (`no_subnet6`), and so is one on a segment made for the
  deployment: its subnet is carved from an IPv4 block.
- The plan's VM carries the IPv6 subnet, its ID, gateway and prefix
  length, all omitted when empty, so old plans replay as before. No
  activity or workflow command is added: `ReserveIP` reserves the IPv6
  address too, under the same FQDN, as an `ipam.address` resource of its
  own; `CreateDNS` adds an AAAA record; `Customize` reads the address
  back from the request's resources. The activity inputs don't carry it,
  so histories stay as they were.
- rvav makes no IPv6 PTR record: reverse zones for IPv6 are rarely
  delegated per subnet, and SOLIDserver's DNS rules can make them.
- vCenter's customization gives the first adapter a fixed IPv6 address
  with its prefix length and gateway (`ipV6Spec`); cloud-init and
  cloudbase-init network data list both addresses and add a `::/0` route
  through the IPv6 gateway. User data reads `.vm.ip6`.
- The VM's address stays its IPv4 one wherever rvav names one address:
  the deployment's, AWX's inventory, components' references. The
  deployment's page lists both under **Network and addresses**.
- Renaming the VM renames its IPv6 address and AAAA record with the rest;
  deleting it releases both. SOLIDserver numbers IPv4 and IPv6 addresses
  apart, so the IPAM reconciliation keys an address by its family and
  ID.
- A SOLIDserver without the IPv6 module, or an account without the right
  to it, answers the `ip6_*` services with a 4xx: the sync, the
  reconciliation and the listings go on with IPv4 only.

### The importer
- A vRA machine's `remoteAccess` is its component's
  `guest.remote_access`: `publicPrivateKey` is `ssh_key` (the `sshKey`
  written in, or an input the form has, `${input.x}` as `${inputs.x}`),
  `generatedPublicPrivateKey` is `generated_key` and `usernamePassword`
  is `password` when the password is an `encrypted` input. The importer
  applies the checks saving does, so an imported item saves: what they
  would refuse (a password written in the template or from a plain
  input, a user name from an input or one the OS refuses, a
  customization specification with anything but Windows' `Administrator`
  and a password) is reported and left out, and a password is never
  repeated in a note.
  vRA's `keyPairName` names a key pair of a public cloud: it has no
  vSphere meaning, and is reported.
- On a machine's first adapter, `assignment: dynamic` is `ipam: dhcp`
  and `assignIPv6Address: true` is `ipv6: true`, unless the adapter is on
  a segment made for the deployment, or both are asked together. A fixed
  `address` and the adapters' other settings are reported.
- A Windows image's `cloudConfig` in an image mapping stays unmapped:
  only an administrator knows whether the vCenter template runs
  cloudbase-init, and marking it changes how every VM of the template is
  customized.

## Consequences
- People sign in to rvav's VMs as they did to vRA's, without user data
  written by hand, and parity rows C22 and C23 are covered on vSphere.
- A generated private key is handed out once: an owner who loses it gets
  in another way, as the user guide says, rather than downloading it
  again.
- Windows VMs get keys, users other than `Administrator` and user data
  only on templates with cloudbase-init, which administrators prepare and
  mark; the others keep vCenter's customization and its `Administrator`
  password.
- The VM's address is still one IPv4 address. What reads it (AWX's
  inventory, outbox events, components' references) is unchanged, and a
  service that needs the IPv6 one reads the deployment's resources.
- A DHCP VM has no DNS record from rvav: its name resolves only where the
  DHCP server registers it.
- vcsim doesn't run a guest: cloudbase-init's run, a real guest's DHCP
  lease and IPv6 address, and SOLIDserver's IPv6 services are checked on
  a real vCenter and SOLIDserver (#140).
