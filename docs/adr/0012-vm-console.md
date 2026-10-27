# ADR-0012: VM console

- Status: accepted
- Date: 2026-10-27

## Context
Users expect to open a VM's console from its deployment, as in vRA. vSphere
offers two ways in:

- **VMRC**, the VMware Remote Console application, opened with a link
  `vmrc://clone:<ticket>@<vcenter>/?moid=<vm>`. The clone ticket comes from
  `SessionManager.AcquireCloneTicket` and is valid once, for a short time.
  The user's machine must reach vCenter (443) and the ESXi host (902), and
  have VMRC installed.
- **WebMKS**, the HTML console: `VirtualMachine.AcquireTicket("webmks")`
  returns a host, port and ticket, and a JavaScript client (VMware's HTML
  Console SDK, `wmks`) connects to `wss://<esxi>:<port>/ticket/<ticket>`. The
  browser must reach the ESXi host on 443, which firewalls often prevent.

The plan (S4) asked for a spike before building, and a WebSocket proxy in
rvav if browsers can't reach the hosts.

## Decision
- `POST /deployments/{id}/resources/{resourceId}/console` checks the day-2
  policy for the `console` action, records it in the audit log, and returns:
  - a **VMRC link** with a fresh clone ticket, always;
  - a **WebMKS session**: a short-lived, single-use rvav URL
    (`/api/v1/console/{token}`) that rvav proxies, over WebSocket, to the
    ESXi ticket URL. The browser only ever talks to rvav.
- The web app offers **Open in VMRC** everywhere, and **Open web console**
  when the organisation provides the WMKS client: rvav does not bundle
  VMware's SDK; its scripts (jQuery, jQuery UI, `wmks.min.js`) are
  configured, in load order, with `RVAV_WMKS_SDK_URLS` (for example the
  copies served by vCenter).
- Tickets are never stored in the database; tokens live in the API
  process and expire after one minute or one use (several API replicas need
  sticky sessions for the WebSocket until tokens move to the database).
- rvav checks the ESXi host's certificate against the thumbprints vCenter
  returns with the ticket, and refuses WebSockets opened from other sites.
- The `console` action is in the actions list and decided by day-2 policies
  like the others, but it is not a request: opening it is an audit entry.

## Consequences
- VMRC works wherever the network allows it; the web console works wherever
  the browser reaches rvav, at the cost of console traffic going through rvav.
- What only a real vCenter can confirm: that the ESXi hosts accept the
  proxied WebSocket, the SDK version that matches the ESXi release, and the
  firewall path for VMRC. vcsim has no WebMKS tickets; tests use a fake host.
