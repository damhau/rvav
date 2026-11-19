# ADR-0060: FIPS 140-3 mode

- Status: accepted
- Date: 2026-11-19
- Builds on the stored secrets of [ADR-0019](0019-catalog-administration.md),
  the webhooks of [ADR-0021](0021-event-subscriptions.md), the console of
  [ADR-0012](0012-vm-console.md), the catalog in Git of
  [ADR-0029](0029-catalog-in-git.md) and the local administrator and
  System page of [ADR-0030](0030-settings-and-local-administrator.md).
  O1.6 (#172), D21 of the parity programme.

## Context
vRA's appliances have a FIPS mode, and agencies and regulated customers
ask for cryptography from a FIPS 140-3 validated module. Since Go 1.24,
Go's own cryptography is such a module, the Go Cryptographic Module:
v1.0.0 has CMVP certificate #5247, and Go 1.27 also carries v1.26.0,
still in process. A program built with `GOFIPS140=v1.0.0` uses the
v1.0.0 snapshot for every algorithm of the standard library, and
`GODEBUG=fips140=on` turns FIPS 140-3 mode on as the process starts: the
module checks its integrity and runs its self-tests, `crypto/rand` uses
its DRBG, and TLS negotiates only approved versions, suites, groups and
certificate keys. `fips140=only` also makes every non-approved algorithm
fail: an error, or for most a panic. Go calls `only` best effort: it
can't see an algorithm a dependency carries itself.

A survey of rvav under `fips140=only`, every unit and end-to-end test,
found what fails: AES-GCM with nonces rvav chooses (the stored secrets),
SHA-1 in go-jose for an identity provider's keys that carry `x5c`, MD5 in
pgx and in go-mail's CRAM-MD5, SHA-1 in go-mail's SCRAM-SHA-1 and NTLM, the
SHA-1 thumbprint of an ESXi host, X25519 and the hashed host keys of
`known_hosts` (HMAC-SHA-1) in x/crypto's SSH, HMAC keys under 112 bits for
any secret a person chooses, and argon2id, which Go doesn't refuse but
FIPS 140 doesn't approve. A panic in a request is answered with a 500;
one in go-oidc's key fetch ends the whole process.

## Decision

### One image, the mode set when a process starts
rvav's image builds `rvav` with `GOFIPS140=v1.0.0` (the Dockerfile's
`GOFIPS140` argument, the Makefile's variable), and `//go:debug
fips140=off` in its main package keeps the mode off unless asked: one
image serves installs with and without it, and a fault in the mode can be
undone without another image. `RVAV_FIPS` (`off`, `on`, `only`; the
chart's `fips.mode`) asks for it. Go reads `GODEBUG` once, before `main`,
so `main` first starts the process again with `GODEBUG=…,fips140=<mode>`
when it runs in another, an `exec` in place that keeps its PID; a
`GODEBUG` that already asks for it and still doesn't give it is an error,
not a loop. `GODEBUG` alone also works, for a build without `RVAV_FIPS`.
`rvav-vcsim`, `rvavctl` and the Terraform provider are development or
client tools and stay out of the module.

Each process logs its mode and module at start, records them with
itself (`processes.fips_mode`, `fips_module`), and the System page shows
the API's and each process's, saying when they differ. The install
settings show `RVAV_FIPS` as the mode in effect.

### Approved algorithms where rvav chooses (on and only)
- **The local administrator's password** is hashed with
  PBKDF2-HMAC-SHA-512, 210,000 iterations, a 16-byte salt and a 32-byte
  key (`$pbkdf2-sha512$i=…$salt$key`, OWASP's figure for SHA-512), instead
  of argon2id. A password still kept as argon2id is checked, and hashed
  again with PBKDF2 at its next sign-in, in `on`.
- **Stored secrets** are sealed by `cipher.NewGCMWithRandomNonce`: the
  module draws the 12-byte nonce, as the approved construction requires.
  rvav keeps it in its own column as before, so the format is the same,
  in every mode: secrets sealed before open, and a rollback reads those
  sealed after.

### What rvav refuses in only mode
Where only mode would fail, rvav checks first and says what to change,
with `RVAV_FIPS=only` in the message, rather than a panic:

- **An argon2id hash**: a sign-in or password change is answered `409`,
  audited as a failed sign-in without counting as an attempt; `rvav admin
  reset-password` sets a PBKDF2 one, or a sign-in in `on` rehashes it.
  The other way, PBKDF2 hashes keep working when the mode is turned off.
- **PostgreSQL's MD5 password authentication**: pgx is told to refuse it
  (`RequireAuth=!md5`), and the error says to use SCRAM-SHA-256, which
  pgx does with the module's PBKDF2.
- **HMAC secrets shorter than 14 bytes**: the signing secret of a
  subscription's webhook or an audit webhook (the delivery fails, saying
  so), `RVAV_GIT_WEBHOOK_SECRET` for GitHub and Gitea signatures (GitLab's
  token is compared, not used as a key), and Slack's signing secret.
- **An ESXi host vCenter gives a SHA-1 thumbprint only**: the console
  checks the host's certificate by its SHA-256 thumbprint only.
- **SSH to the catalog's Git server** offers only ECDH on P-256, P-384
  and P-521, AES-CTR and HMAC-SHA-2 (x/crypto would offer X25519 and
  ML-KEM hybrids first and fail), and refuses hashed `known_hosts` lines.
- **SMTP sign-in** picks SCRAM-SHA-256, or PLAIN or LOGIN over TLS, and
  says so when the server offers neither; go-mail would pick CRAM-MD5,
  SCRAM-SHA-1 or NTLM.

An identity provider's keys lose their `x5c`, `x5t` and `x5u` before
go-jose reads them, in `on` and `only`: rvav verifies tokens with the key
itself, never with its certificate, so nothing is refused.

### What stays outside the module
Git names objects by SHA-1, which go-git computes with its own code: a
name, not a protection. The console's WebSocket handshake hashes with
SHA-1 between the browser and ESXi, through rvav untouched. The web
container's nginx, PostgreSQL, Temporal and the browser have their own
cryptography and their own FIPS modes.

### Tests
The tests that need a mode run themselves again in a process with
`GODEBUG=fips140=…` (`internal/fips/fipstest`), so CI runs them without a
workflow of their own: each refusal above, a token verified with an `x5c`
key set, the SSH handshake, and `TestFIPSOnly`, which runs end-to-end
tests in only mode against the run's database: the processes starting,
local sign-in, an identity provider's tokens, a request provisioned in
vcsim, webhooks signed with HMAC, audit events forwarded to syslog over
mutual TLS, API tokens and the System page.

## Consequences
- In `on` and `only`, every TLS peer must speak TLS 1.2 or 1.3 with an
  approved suite and certificate key (RSA of 2048 bits or more, ECDSA on
  P-256, P-384 or P-521, Ed25519): an old vCenter, AWX or SMTP server
  that doesn't fails its handshake.
- `only` is for proving that nothing on rvav's paths uses a non-approved
  algorithm; `on` is what a FIPS 140-3 requirement usually needs, and
  what Go recommends for production.
- A new dependency or path that uses SHA-1, MD5, short HMAC keys or GCM
  with chosen nonces fails in only mode: `TestFIPSOnly` and the
  only-mode tests are where it shows.
- Moving to module v1.26.0 once certified is a build argument
  (`GOFIPS140=v1.26.0`).
