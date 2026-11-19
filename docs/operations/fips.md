# FIPS 140-3 mode

rvav's image is built with the Go Cryptographic Module v1.0.0, Go's
FIPS 140-3 validated module. FIPS 140-3 mode is off until you turn it on
with `RVAV_FIPS`; the same image runs either way. The design is in
[ADR-0060](../adr/0060-fips-mode.md).

## The modes

| `RVAV_FIPS` | What rvav's processes do |
|---|---|
| `off` (the default) | Go's cryptography as usual, from the module's code, without its self-tests or restrictions. |
| `on` | The module checks its integrity and runs its self-tests at start. Random numbers come from its DRBG. TLS uses approved versions, cipher suites, key exchanges and certificate keys only. Where rvav chooses an algorithm, it chooses an approved one: the local administrator's password is hashed with PBKDF2-HMAC-SHA-512 instead of argon2id. |
| `only` | As `on`, and every algorithm the module doesn't approve fails. rvav refuses the features that would need one, with an error that says what to change instead ([what only mode refuses](#what-only-mode-refuses)). |

Use `on` to meet a FIPS 140-3 requirement. Use `only` to show that
nothing rvav does needs a non-approved algorithm: Go describes it as best
effort, because it can't see an algorithm a library implements itself.

## Turn it on

Give every rvav process the same mode, `rvav migrate` included: the API,
the worker and the sync each check the module as they start.

- **Kubernetes:** set `fips.mode` in the chart's values. Quote the value,
  or YAML reads `on` and `off` as booleans.

  ```yaml
  fips:
    mode: "on"
  ```

- **VMs:** add `RVAV_FIPS=on` to `/etc/rvav/rvav.env`, then restart the
  rvav services.
- **The local stack:** start it with the mode in the environment.

  ```sh
  RVAV_FIPS=only docker compose -f deploy/docker-compose.yml up --build -d --wait
  ```

To check the mode, open **Administration › System**: **This install**
shows the API's mode and module, and **Processes** shows each process's.
A warning says when processes run in different modes. Each process also
logs its mode as it starts:

```json
{"time":"2026-11-19T09:12:03Z","level":"INFO","msg":"FIPS 140-3","process":"api","mode":"on","module":"v1.0.0"}
```

`GODEBUG=fips140=on` or `GODEBUG=fips140=only` also sets the mode, for a
binary you build yourself. To check that a binary carries the module, run
`go version -m rvav`: it lists `GOFIPS140=v1.0.0-c2097c7c` and
`DefaultGODEBUG=fips140=off`.

## Before you turn it on

- **TLS peers:** in `on` and `only`, every server rvav connects to must
  offer TLS 1.2 or 1.3 with an AES-GCM cipher suite (or AES-CBC with
  SHA-256 in TLS 1.2), an ECDHE or ML-KEM key exchange, and a certificate
  with an RSA key of 2048 bits or more, an ECDSA key on P-256, P-384 or
  P-521, or an Ed25519 key. This covers vCenter, ESXi hosts, AWX,
  EfficientIP, NSX, vRA, the identity provider, Temporal, PostgreSQL over
  TLS, the SMTP server, syslog servers, Microsoft Teams, Slack, and the
  hosts of webhooks and REST sources. A server that offers none of these
  fails its handshake, and the connection's test says so.
- **PostgreSQL:** keep rvav's password as a SCRAM-SHA-256 verifier, not
  MD5 (`password_encryption = scram-sha-256`, then set the password again),
  and give rvav's role `scram-sha-256` in `pg_hba.conf`. PostgreSQL 14 and
  later keep new passwords as SCRAM-SHA-256 verifiers by default.
- **The local administrator:** its password is kept as an argon2id hash
  until it is hashed again. Sign in once with `RVAV_FIPS=on`, which hashes
  it again with PBKDF2, before you move to `only`.

## What only mode refuses

| Feature | Refused when | Do instead |
|---|---|---|
| Local administrator sign-in | Its password is still kept as an argon2id hash. The sign-in page shows the error (`409`), and the audit log records a failed sign-in. | Run `rvav admin reset-password`, or sign in once with `RVAV_FIPS=on`. |
| The database | PostgreSQL asks for MD5 password authentication. The process doesn't start. | Use SCRAM-SHA-256, as described in [before you turn it on](#before-you-turn-it-on). |
| Webhooks | A subscription's signing secret, or an audit webhook's, is shorter than 14 bytes. Its deliveries fail, saying why. | Give a stored secret of 14 bytes or more (32 random bytes is better). |
| The catalog's Git webhook | `RVAV_GIT_WEBHOOK_SECRET` is shorter than 14 bytes and GitHub or Gitea signs with it. GitLab's token isn't affected. | Give a secret of 14 bytes or more, in rvav and in the Git server. |
| Slack interactions | Slack's signing secret is shorter than 14 bytes. Slack's own are 32 characters. | Give the app's signing secret as Slack shows it. |
| The VM console | vCenter gives only a SHA-1 thumbprint for the ESXi host. | Upgrade vCenter to API version 7.0.3.1 or later (7.0 Update 3), whose console tickets also list the host's SHA-256 thumbprint. |
| The catalog's Git repository over SSH | The server offers no ECDH key exchange on P-256, P-384 or P-521, no AES-CTR cipher or no HMAC-SHA-2 MAC; or a `known_hosts` line is hashed (`|1|…`). | Enable those algorithms on the Git server; give `known_hosts` lines unhashed, as `ssh-keyscan` prints them without `-H`. HTTPS needs nothing. |
| Email | The SMTP server offers no SCRAM-SHA-256, and no PLAIN or LOGIN over TLS (CRAM-MD5, SCRAM-SHA-1 and NTLM need SHA-1 or MD5). The connection's test says so. | Offer SCRAM-SHA-256, or PLAIN or LOGIN with STARTTLS or TLS. |

The identity provider's keys need nothing: rvav reads its signing keys
without the certificates (`x5c`) some providers add to them, whose SHA-1
thumbprint only mode would refuse.

## What the mode doesn't cover

- **Git object names** are SHA-1 hashes, computed by go-git's own code:
  they name commits and files, and protect nothing.
- **The console's WebSocket handshake** hashes with SHA-1 between the
  browser and the ESXi host. rvav passes it through without hashing.
- **The web container** (nginx), **PostgreSQL**, **Temporal** and the
  **browser** use their own cryptography. Run them in their own FIPS
  modes where your requirement covers them.
- **rvavctl, the Terraform provider and `rvav-vcsim`** aren't built with
  the module.

## The module's certificate

The Go Cryptographic Module v1.0.0 has CMVP certificate
[#5247](https://csrc.nist.gov/projects/cryptographic-module-validation-program/certificate/5247).
Its security policy and the versions in process are on
[Go's FIPS 140-3 page](https://go.dev/doc/security/fips140). Go 1.27
also carries v1.26.0, still in process; to build with it, run
`docker build --build-arg GOFIPS140=v1.26.0 .` or
`make build GOFIPS140=v1.26.0`.
