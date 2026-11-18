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
