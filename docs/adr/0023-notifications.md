# ADR-0023: Notifications

- Status: accepted
- Date: 2026-10-28

## Context
Until S11 rvav only told people things in the app. It pushed a toast over
the live stream when an approval was needed, a request ended or a lease
was ending, and kept nothing. People work in their mail, Microsoft Teams
or Slack. They miss an approval waiting in an app they don't have open,
and a lease reminder seen once in a toast is easy to lose.

## Decision

### One notification, kept
Each thing that concerns someone is a **notification** for that user:

- kept in `notifications`: kind, title, body, level, link, the request or
  deployment it is about, read or not;
- listed in the app's **notification centre** (`/me/notifications`), and
  pushed live as before;
- then sent on the **channels** the user chose for its kind.

A notification has a **dedupe key**. The same key for the same user makes
no second notification. Activity retries, housekeeping running twice and
a day-2 action finishing twice therefore don't notify twice. Examples:
`lease_expiring:<deployment>:<expiry>:7` for the 7-day reminder, the
activity's run key for an outcome.

| Kind | Who | When |
|---|---|---|
| `request_submitted` | the requester, and the owner when on behalf | a request was submitted |
| `approval_needed` | each approver of the level | a level opened, was delegated or escalated to them, or a custom workflow asked them |
| `request_approved`, `request_rejected` | the requester | the last level was decided |
| `request_succeeded`, `request_failed` | the owner, and the requester when on behalf | the request ended |
| `request_rolled_back` | the owner | a failed request was rolled back |
| `lease_expiring` | the owner | 7 days and 1 day before the lease ends |
| `lease_expired` | the owner | the lease ended: the deployment is powered off |
| `owner_changed` | the new owner | a deployment was handed over to them |

### Preferences
Each user chooses, per kind, the channels besides the app: email, Teams,
Slack. In the app is always on. Without a choice of their own:

- **Email:** `approval_needed`, `request_succeeded`, `request_failed`,
  `request_rejected`, `lease_expiring` and `lease_expired`.
- **Teams or Slack:** `approval_needed`, when that channel is set up.

A user also chooses a **locale** (`en`, `fr`) for what is sent.
Preferences say which channels can reach the user and why not: no email
address, not found in Teams or Slack, channel not set up.

### Delivery: an outbox, as for subscriptions
- **Rows.** A notification and one **delivery** row per channel are
  written in one transaction. `(notification, channel)` is unique.
- **Claims.** A notifier in rvav's worker claims due deliveries with a
  lease (`for update skip locked`), as many as it has free senders.
- **Sending.** It renders the template, sends, and records the attempt
  under that lease.
- **Retries.** A failure is retried after 30 s, 2 min, 10 min, 30 min, then
  every hour. After a day the delivery is dropped as failed: an approval
  is still in the inbox, and a reminder that late has lost its use.
- **Pruning.** Delivered and failed rows, and read notifications, are
  pruned after 90 days.

### Templates
Templates are Go `text/template` and `html/template` files embedded in
rvav, one per kind and locale (`en`, `fr`):

- `subject`, and `text` and `html` bodies for email;
- `title`, `text` and facts for Teams and Slack cards.

They get the notification and what it is about (request, item, project,
environment, deployment, lease, approval level and reasons), plus the
link into rvav. Administrators don't edit them yet. A missing locale falls
back to `en`.

### Channels
Channels are integrations ([ADR-0020](0020-administration.md)): their
settings are on the Integrations page, an environment variable wins, and
secrets are sealed.

- **Email (SMTP).** Host, port, TLS (STARTTLS, implicit or none), From,
  user and password. Messages are plain text and HTML. Email has links
  only, no buttons: approving still needs a sign-in.
- **Microsoft Teams** (an Azure bot: an Entra app registration with the
  Teams channel):
  - rvav signs in as the app with client credentials, finds the user's
    Entra object ID through Microsoft Graph by their email (kept once
    found), opens a one-to-one conversation through the Bot Connector and
    sends an Adaptive Card.
  - An approval card has the request's facts, a comment box and
    **Approve** / **Reject** (`Action.Execute`, carrying only the approval
    ID and the decision).
  - Teams calls rvav's messaging endpoint
    (`/api/v1/integrations/teams/messages`) with a Bot Framework token.
    rvav checks its signature against the Bot Framework keys, its issuer,
    its audience (the app ID) and its expiry, and that the service URL is
    the one in the token.
  - The caller's `aadObjectId` must be the user rvav sent the card to.
  - rvav answers with the card updated: who decided, or why it couldn't.
- **Slack** (a Slack app with a bot token and its signing secret):
  - rvav finds the user by email (`users.lookupByEmail`, kept once found)
    and sends a direct message in Block Kit.
  - An approval message has **Approve** and **Reject**. Reject opens a
    dialog that asks for the comment.
  - Slack's calls to `/api/v1/integrations/slack/interactions` are checked
    with the signing secret: HMAC v0 over `v0:<timestamp>:<body>`, with
    the timestamp within five minutes.
  - The Slack user must be the one rvav sent the message to. rvav updates
    the message with the outcome.

### Deciding from Teams or Slack
- **Who.** A button decides as the user the message was sent to, matched
  by the channel identity rvav recorded when it sent it. Nothing in the
  card is a credential: a forwarded card does nothing for anyone else.
- **Rights.** The user acts with the groups from their last sign-in, as
  API tokens do. The decision goes through the inbox's own code, so the
  same rules apply:
  - only an approver of the level (named, in its group, or delegated);
  - a comment is required to reject;
  - a level decided meanwhile is refused;
  - every decision is audited, with the channel.
- **Inputs.** Approvers can't change inputs from a card: that needs the
  inbox.

### Testing
Teams, Slack and SMTP are tested against fakes speaking their protocols:
- an SMTP server;
- a Bot Connector, Graph and token endpoint, with Bot Framework tokens
  signed by a test key;
- a Slack Web API, with requests signed with a test secret.

A real tenant and workspace are lab acceptance.

## Consequences
- People are told where they work, once per occasion, and a missed
  message is still in the app's centre.
- Teams and Slack need an administrator of the tenant or workspace to
  register the app. Until then email and the app work alone.
- A user's Teams or Slack identity comes from their email address. Someone
  whose address differs between the identity provider and Teams or Slack
  isn't reachable there, and preferences say so.
- Decisions from a card use the groups of the user's last sign-in: someone
  removed from a group keeps deciding from cards until they sign in again,
  as with API tokens.
