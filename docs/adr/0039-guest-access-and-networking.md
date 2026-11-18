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
  not `root`), Windows (for now; see below) and a Linux guest with a
  `customization_spec`. A request is refused when the catalogue template
  its `template` binding chose is Windows or names a customization
  specification, with the template's name.
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
