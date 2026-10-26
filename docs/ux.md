# UX principles and key screens

## Principles

1. **Fast**: optimistic updates, prefetching, skeleton loaders. The goal is
   under 100 ms for UI interactions and under 1 s for every page.
2. **Live**: no refresh buttons. Requests, power state and approvals update in
   place over SSE.
3. **Keyboard-first**: ⌘K command palette for navigation and actions; shortcuts
   on lists (`j`/`k`, `/` to search, `?` for help).
4. **Explain before acting**: show quota, approval path and what will
   change before the user submits.
5. **Context over menus**: actions live on the object (row menu, detail header),
   not in a separate "actions" area.
6. **Progressive disclosure**: simple by default; advanced options are collapsed.
7. **Accessible**: WCAG 2.2 AA, full keyboard support, respects reduced motion,
   light and dark themes.

## Navigation

```
Sidebar:  Home · Catalog · Deployments · Requests · Approvals (badge) · Reports
          ── Admin: Catalog items · Content sources · Policies · Projects ·
                    Integrations · Subscriptions · Audit · Settings
Top bar:  ⌘K search · project switcher · notifications · user menu
```

## Key screens

### Home
Greeting, "Continue" (drafts and running requests), approvals waiting for me,
leases expiring soon, featured catalog items, recent activity.

### Catalog
A grid of cards with search, category and tag filters. Clicking a card opens a
side panel with the description and a "Request" button, without leaving the page.

### Request form
- Wizard or single page, depending on the form layout.
- A sticky right-hand summary: name preview, placement, quota impact,
  approval path, lease.
- Inline validation; dynamic fields show loading states rather than freezing the
  page.

### Request timeline
A vertical stepper with nested steps:
```
✓ Submitted                         10:02:11
✓ Approved by Alice (Infra lead)    10:05:40
● Provisioning
  ✓ Reserve IP 10.1.2.34 (EfficientIP)
  ✓ Create DNS web-prod-web001.example.com
  ✓ Clone from tpl-rhel9
  ● Waiting for guest IP…            00:42
  ○ Configure (AWX: linux-baseline)
○ Ready
```
AWX steps expand into play, task and host results. "View raw log" opens the full
stdout in a drawer. If something fails, the step shows the error, a link to the
AWX job, and Retry or Rollback buttons (permission-dependent).

### Deployments
- A table or card view with saved filters and bulk actions.
- Detail page with tabs: Overview (resources, live state), Actions, History
  (timeline), Inputs, Lease, Audit.
- VM row: power indicator, IP and FQDN (click to copy), console button, actions menu.

### Approvals inbox
List and detail view: request summary, inputs (editable where allowed), policy
reason ("RAM > 32 GB"), Approve or Reject with a comment, bulk approve.

### Admin: form designer
Three panes: field palette | live form preview | field properties (including
value source, dependencies and rules). A toggle switches to the YAML view.
"Preview as user" lets the admin pick a project and role.

## Design system

- shadcn/ui on Radix primitives, Tailwind tokens for colour, spacing and radius.
- Lucide icons, plus custom catalog item icons (SVG upload).
- Data tables: TanStack Table (virtualised, column visibility, saved views).
- Charts: a lightweight library (for example Recharts) for reports.
