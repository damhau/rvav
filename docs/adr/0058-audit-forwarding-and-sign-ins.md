# ADR-0058: Audit forwarding and sign-ins audited

- Status: accepted
- Date: 2026-11-19
- Builds on the audit log of [ADR-0020](0020-administration.md), the
  webhooks of [ADR-0021](0021-event-subscriptions.md), the local
  administrator of [ADR-0030](0030-settings-and-local-administrator.md),
  the stored secrets of [ADR-0038](0038-names-groups-secrets-properties.md)
  and rvavctl's sign-in of [ADR-0051](0051-command-line.md). O1.4 (#170),
  vRA parity D19; amended by O1.7 (#173).

## Context
vRA sends its audit log to syslog (Log Insight) and records who signed
in. rvav's audit log stayed in its database, where its own database
administrators could change it, and it recorded only the local
administrator's sign-ins: an OIDC sign-in happens between the browser (or
rvavctl) and the identity provider, so the API saw tokens arrive, not
people sign in, and nothing at all of a sign-in that failed.

Security teams want both in their SIEM: every change, and every sign-in
with where it came from and why it failed. Two constraints shape the
design. Sending must never hold up or fail the action audited, and must
lose nothing while a SIEM is down. And the audit log must never hold what
would let its readers sign in: no token, code or password.

## Decision

### Destinations
Administrators (`integrations.manage`) add destinations in Administration
› Audit forwarding (`/api/v1/admin/audit-destinations`); auditors and
administrators (`audit.read` for all of rvav) see them. A destination is
one of:

- **a syslog server:** RFC 5424 messages over TLS with RFC 5425
  octet-counting framing (port 6514 by default), with the same framing in
  clear over TCP, or one message per datagram over UDP (RFC 5426, port
  514), the last two for labs. Its CA certificates are trusted besides the
  system's and the CA bundle, and the server's name is checked. A client
  certificate is presented when the server asks, its private key held in a
  stored secret (sealed, ADR-0038), never in the destination;
- **a webhook:** a POST of `{"contract": "rvav.audit.v1", "events": [...]}`
  signed as subscriptions' webhooks are (`X-Rvav-Signature`, HMAC-SHA256
  under a stored secret), to a host webhooks may call, without following
  redirects. A 2xx answer delivers the batch.

A webhook's credential, for a SIEM that wants one (Splunk's HEC,
Datadog's API key), goes in an authentication header: its name
(`auth_header`, not one rvav sets itself) is shown, its value
(`auth_value`) sealed in the destination as integrations' passwords are
(ADR-0019) and never shown. The value is kept when a change doesn't give
it, unless the URL's scheme, host or port changes: a value isn't sent to
a server it wasn't given for. A URL with a user name or password is
refused, since auditors see the URL. Its query's values, where some SIEMs
take a token, are shown as `REDACTED`, in the API, in the audit events
of the destination's changes and in its last error; a URL sent back so
keeps the values stored, and a changed one with `REDACTED` in it is
refused.

A destination gets every event, or those of the categories it names. The
category is the action's first word, grouped: `sign_in` (`auth.*`),
`token`, `access`, `catalog`, `policy`, `infrastructure` (with IP ranges
and Active Directory placement), `extensibility` (with shared
variables), `settings`, `request` (with schedules) and `other`.
`TestEveryAuditedActionHasACategory` finds every action the code audits
and fails on one that would be `other`.

### Durable, in order, at least once
`store.Audit` writes the event and a row in `audit_deliveries` for each
enabled destination whose categories take it, in one statement: inside
the audited action's transaction when it has one. An event is never
audited without its deliveries, and sending is no part of the action.

A forwarder runs in every worker replica. Every 2 s it leases the
destinations that are due, one replica each (`lease_until`, 2 minutes),
and sends each one's backlog in order of the events' IDs, 100 at a time,
deleting a batch once it is delivered. A failure keeps the batch,
records the error and tries again after 5 s, 30 s, 1 min, then every
5 min, without end: an event waits until it is delivered or its
destination is removed. Changing a destination tries it again at once.
A batch delivered just before a crash is sent again: receivers drop an
event `id` they already have.

The lease is taken with `FOR NO KEY UPDATE SKIP LOCKED`, after a look at
which destinations have events waiting, so that nothing an audited
action does waits on it. A worker ends only its own lease: one that ran
out and was taken by another worker stays theirs. A worker that stops
finishes the batch it is sending, records it and ends its leases before
it exits.

`audit_deliveries` has no foreign key to its destination: an action
audited while a destination was removed waited on the removal's lock,
then failed on the key, and the action with it. Removing a destination
removes only the destination; each round of the forwarder sweeps what
waited for destinations gone, those of the removal and of actions
audited while it ran, 10,000 rows at most.

An event a destination refuses for good is set aside, with why, and the
events after it go: a webhook's 413 for the event alone (a batch answered
413 is sent again one event at a time), or an event longer than 64 KiB
written for syslog over TCP or TLS, the default `log-msg-size` of
syslog-ng, which closes the connection past it. Saving the destination
tries what was set aside again.

The state each destination shows, and the API returns, is what waits
(`backlog`), what was set aside and why, how many were delivered, the
failures in a row, the last error and when, the last success, and the
next attempt. `rvav_queue_depth{queue="audit"}` counts what waits for the
enabled destinations, without what was set aside: a disabled
destination's backlog doesn't age into the alert.

A test (`POST /admin/audit-destinations/{id}/test`) sends one made-up
event, `settings.audit_destination.test` with the ID `test`, at once and
outside the backlog, and says what the server answered. The test itself
is audited.

### The syslog message
`<PRI>1 TIMESTAMP HOSTNAME APP-NAME - MSGID - MSG`:

- the severity is notice, warning for `auth.sign_in_failed`; the facility
  is the destination's (`local0` by default);
- HOSTNAME is the host of rvav's public URL, the same whichever replica
  sent; APP-NAME is the destination's (`rvav` by default); MSGID is the
  action;
- MSG is the event as the webhook has it, in JSON: `id`, `at`,
  `category`, `action`, `actor`, `target_type`, `target_id`, `project`,
  `detail`. A UDP datagram over 8 KiB goes with `{"truncated": true}` as
  its detail.

### Sign-ins
The API records what it sees, and the clients report what only they see:

| Action | When | Recorded by |
|---|---|---|
| `auth.sign_in` | The web app or rvavctl got a token from the provider (browser, device code or loopback), or the local administrator signed in | The API, from the token the client reports with, or from the password checked |
| `auth.sign_in_failed` | The provider redirected back with an error (a disabled or unknown user, a refused consent, a group the client requires), the state didn't match, or the code couldn't be exchanged | The client, before it has a token: `reported_by: client` |
| `auth.sign_in_failed` | The client reported with a token rvav refuses (expired, signed by another key, a reserved subject), an expired API token was used, or the local administrator's password was wrong | The API |
| `auth.sign_out` | The web app signed out, or `rvavctl logout` ran | The API, from the token it sent |
| `auth.token_used` | An API token's first use of the day (UTC) | The API |

Each row says how (`client`: `web` or `rvavctl`; `method`: `oidc`,
`device`, `dev`, `token` or `local`), and where from: `ip` and
`user_agent`. A failure says its `stage` (`state`, `provider` or
`token`), a `reason`, and the provider's `error` code and `description`.
A success has the user as its actor and their roles and groups.

Clients report to `POST /api/v1/auth/sign-ins`, which needs no sign-in.
A success is recorded only with the token the client got, which the API
checks as any other: a client can't claim someone signed in. A failure
can't be checked, so anyone could report one. It is marked
`reported_by: client`, its fields are checked (an error code of at most
64 printable ASCII characters, a description of at most 500 without
control characters), and at most 10 a minute from one address, and 300
in all, are kept in each API process; so are 10 successes and sign-outs
a minute per user. A report past those limits is answered as if it were
kept.

rvav has no gate of its own for disabled users or required groups: the
provider refuses them, and the client reports its error. A token that
reaches rvav for someone rvav doesn't know signs them in, as before.

### No secrets in the audit log
Clients send neither the code, the state, the device code nor any token:
only the provider's error code and description, which describe the
refusal. A refused token is recorded by the verifier's reason, not the
token. An API token is named by its ID and name. `TestSignInsAudited`
and `TestCLIOIDC` search the audit log for every code and token they
used.

### Where a request came from
`ip` is the peer's address, unless the peer is a trusted proxy
(`RVAV_TRUSTED_PROXIES`, addresses or networks): then it is the
rightmost address of `X-Forwarded-For` that isn't one, so a client can't
put an address of its choice in front. The web container's nginx and
the ingress controller add theirs. `user_agent` keeps 256 characters.

### API tokens
An API token's use is recorded once a day, its first, in the same
transaction that records `last_used_at`, so concurrent first uses record
one row. An expired token's use is recorded once a day too, as a failure
(`token_expired`, `last_refused_at`). An unknown token isn't recorded:
it names no one, and a scanner would fill the log.

## Consequences
- A SIEM has the audit log within seconds, and catches up after an
  outage; rvav's database administrators can no longer change the record
  unseen.
- Removing a destination drops what waits for it; the audit log keeps
  every event.
- An event set aside reaches its destination only once someone saves the
  destination again, after raising the receiver's limit; the audit log
  has it meanwhile.
- A destination that stays down grows its backlog by every event it
  takes: its state, the queue metric and its alert say so.
- A failure a client reports is a claim, not a fact: SIEM rules should
  treat `reported_by: client` as such. Successes, sign-outs and token
  use are facts.
- `rvavctl login --with-token` with an expired token records both the
  expired token's use and the token refused.
- Without `RVAV_TRUSTED_PROXIES`, every request seems to come from the
  proxy in front of the API.
