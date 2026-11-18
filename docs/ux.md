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
Sidebar:  Overview · Catalog · Deployments · Requests (running) ·
          Approvals (badge) · Reports
          ── Administration
             Catalog:        Catalog items · Day-2 actions · Custom workflows ·
                             Subscriptions
             Governance:     Projects · Roles · Policies
             Infrastructure: Integrations · Templates and networks ·
                             Onboarding · vRA import
             Operations:     Setup · Audit log · System
Top bar:  ⌘K search · project switcher · notifications · theme · user menu
```

- Every entry has an icon. The main menu's entries are larger and darker
  than the administration's, whose groups each have a small heading.
- Each person sees only the pages they may open; a group with none is left
  out. An address for an administration page someone may not open says
  **You can't open this page** and links to **What I may do**; an address
  that matches no page says **Page not found** and offers **Go to the
  overview**.
- A menu entry's label is its page's title.
- Pages below a top-level one start with breadcrumbs: links to the pages
  above, such as **Deployments** above a deployment or **Catalog items**
  above an item. Top-level pages have none.
- From 1024 px wide, the sidebar shows labels; from 768 px, icons with
  their labels in tooltips; narrower, it's a drawer. The menu button in the
  top bar opens the drawer, with labels, on the rail too.
- Keyboard: the skip link comes first, then the menu in order; the
  groups' headings aren't stops. The current page's entry has
  `aria-current="page"`, and every entry shows its focus. The drawer opens
  on the current page's entry, and closing it, the command palette or a
  side panel gives the focus back to what opened it.

## Key screens

### Home
Greeting, "Continue" (drafts and running requests), approvals waiting for me,
leases expiring soon, featured catalog items, recent activity.

### Catalog
A grid of cards, with search and the category and kind filters in a toolbar
above it. A card shows the item's icon (or its kind's), its kind, its one-line
summary and a Request button, for those whose roles in the project let them
request; for others, the page says why there is none. Clicking a card opens
the item's page: its Markdown description, the version requests use, what it
builds (the topology, or a composite's steps), and Request as the primary
action.

### Request form
- Wizard or single page, depending on the form layout. Each section of the
  form, then Lease and reason and Options, is a panel of fields.
- A sticky right-hand summary: name preview, placement, quota impact,
  approval path, lease. Save as draft and the primary Submit request (or
  Submit for approval) are at its foot, on the right.
- Inline validation; dynamic fields show loading states rather than freezing the
  page.

### Request timeline
A request's page has its status, number, project, requester and progress in
the header, beside Cancel request, Request again and Open deployment. The
steps (a graph, for a composite) come first; the AWX job output and what was
asked for are beside them. The steps are a vertical stepper with nested steps:
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
- A table with its filters in the toolbar, kept in the address; each row's
  ⋯ menu holds its actions, and selected rows get bulk actions.
- Detail page: its status and owner in the header, beside an **Actions** menu
  with every action that applies (a submenu per VM when there are several),
  each it can't run with the reason. Tabs in the address: Overview (VMs live,
  network, security groups, details, lease, placement), History, Inputs.
- VM row: power indicator, IP and FQDN (click to copy), console button, actions menu.

### Approvals inbox
A table in two tabs, Waiting for you and Decided, kept in the address. A row
opens its approval in a side panel (`?id=`): request summary, inputs
(editable where allowed), policy reason ("RAM > 32 GB"), and Approve or
Reject with a comment. A row's ⋯ menu approves as asked, or opens the panel
to reject.

### Admin: form designer
Three panes: field palette | live form preview | field properties (including
value source, dependencies and rules). A toggle switches to the YAML view.
"Preview as user" lets the admin pick a project and role.

## Design system

- Radix primitives and Tailwind tokens for colour and type, in a light and a
  dark theme. [frontend.md](frontend.md#design-system) lists the tokens and the
  building blocks every page is made of.
- IBM Plex Sans for text; IBM Plex Mono only for identifiers, addresses and
  code.
- Lucide icons, plus custom catalog item icons (SVG upload).
- Report charts are plain SVG in the `--chart-*` colours, and every value a
  chart shows is in a table beside it.

### Type scale

| Name | Size | For |
|---|---|---|
| Title | 20 px, semibold | The page's title |
| Section | 15 px, semibold | Panel, section and side panel titles |
| Body | 14 px | Text, tables and controls |
| Secondary | 13 px | Descriptions, help, a row's second line |
| Caption | 12 px | Table headers and labels |
| Badge | 11 px | Badges only |

Secondary text has at least 4.5:1 contrast in both themes. Labels are
sentence case.

### Rules

- **One primary action per page.** The page header holds it; a panel has at
  most one of its own. Other actions are secondary or quiet buttons. An
  empty list offers its New … in the empty state only.
- **Edit in a side panel.** Creating and editing a record opens a side panel
  (`Sheet`) over the page, so the list stays in view. Dialogs are for
  questions.
- **Actions on the row.** A record's actions are in its row's ⋯ menu; the
  panel header keeps the action that adds one.
- **Empty states give a next step.** An empty list says what goes there and
  offers the step that adds it, such as "New template".
- **Removing asks first.** The confirmation names what goes and what depends
  on it, such as the projects a template is offered to. It waits while the
  change runs, and its way out says what it keeps when "Cancel" would be
  ambiguous: **Keep it running** beside **Cancel request**.
- **Off says why.** A disabled button or menu item says why beside it, such
  as "Waits for the change running" or "It comes first already". An action
  that needs a selection appears with the selection, in a bar above the
  table.
- **Failures offer a retry.** A list, a panel or a menu that couldn't load
  says so and offers **Try again**.
- **Nothing that comes later.** A feature that doesn't work yet isn't shown,
  not even as a disabled button.
