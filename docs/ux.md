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
Sidebar:  Overview · Catalog · Deployments · Resources ·
          Requests (running) · Approvals (badge) · Reports
          ── Administration
             Catalog:        Catalog items · Day-2 actions · Custom workflows ·
                             Subscriptions
             Governance:     Projects · Roles · Policies
             Infrastructure: Integrations · Templates and networks ·
                             Onboarding · vRA import
             Operations:     Setup · Branding · Notification templates ·
                             Audit log · Audit forwarding · System
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
- The top bar is one row at every width. On a phone, the search box and
  the project switcher become icon buttons beside the notifications, the
  theme and the account menu; each opens what it stands for and gets the
  focus back when it closes.
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
  ⋯ menu holds its actions, and selected rows get bulk actions. Filters:
  search, status (with **Lease ended**), environment (the project's own),
  catalog item and VM template (the values some deployment has), owner (a
  people picker), Only mine, lease ending within 7 days.
- A column header sorts the whole list on the server (`?sort=`), not the
  rows loaded: the table is `presorted`.
- **All my projects**, a segmented toggle beside the header's actions,
  lists every project the person is in (`?project=all`) and adds a
  **Project** column; the project switcher still names the project they
  work in.
- **Table** and **Cards**, an icon-only segmented toggle (`?layout=`), the
  choice remembered per person in the browser. A card shows the name (the
  link), catalog item, status, environment, VMs on, address, owner, lease
  and the ⋯ menu; cards have no selection, and a **Sort by** select stands
  in for the headers.
- **Views**, a menu: the person's saved views, **Save as a new view…**,
  **Update** the view last opened once its filters change, and **Rename**
  or **Delete** the view shown. The button names the view the address
  matches. Views are the person's own, kept on the server: the address
  already shares a list with someone else.
- Detail page: its status and owner in the header, beside an **Actions** menu
  with every action that applies (a submenu per VM when there are several),
  each it can't run with the reason. Tabs in the address: Overview (VMs live,
  network, security groups, details, lease, placement), Topology (the canvas,
  read only, drawn from what the deployment holds; a VM's node opens its
  properties), History, Inputs.
- VM row: power indicator, IP and FQDN (click to copy), console button, actions menu.
  Its name opens the VM's properties in a read-only side panel: guest, adapters,
  disks, snapshots, ISO, placement, custom properties, and a link into the
  vSphere Client (or why there is none yet).

### Resources
- The VMs of the deployments the person sees, one row per VM, in a table:
  name (a button that opens the VM's properties in the side panel, in
  place) with its FQDN, power, addresses (reserved first, IPv4 before
  IPv6), deployment (a link), project (with **All my projects**),
  environment, vCenter and cluster, vCPU, memory, disks (count and total)
  and networks. It scrolls sideways under its width.
- Filters in the toolbar, kept in the address: search (name, IP, FQDN),
  environment, power, vCenter, cluster, network. vCenters, clusters and
  networks are those the VMs use. Headers sort on the server; **Load more**
  pages.
- **All my projects** and **Views** as on Deployments; no cards.
- VMs only, not tabs: the disks and networks are the VMs' own (columns and
  a filter), NSX objects belong to a deployment's topology, and other kinds
  of resource would each need their own columns. A tab bar with one tab
  would only add a click.
- ⌘K offers **Find … in Resources** once two characters are typed.

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
- **One filter toolbar.** A list's search and filters sit in one row above
  it, a group named **Filters**: the search box first, then the filters,
  then **Clear filters** once one is set, and at the far end how many
  rows the filters leave, "12 deployments", or "50 shown" while more pages
  remain; a screen reader hears it change. A select reads "All …" when it
  filters nothing;
  a box for a value of its own, such as a day or a folder, has its label
  beside it. The filters live in the address, so a filtered list can be
  shared and Back leaves the page. **Clear filters** keeps the tab, sort
  and layout, and puts the focus on the search box; changing the tab
  keeps what is chosen in the address, for the tab that reads it. A list
  that nothing matches says so and offers **Clear filters**. Narrower
  than 640 px, the bar shows the search, then a **Filters** button that
  says how many are set, "Filters (2)", and shows or hides the rest; it
  says whether they show (`aria-expanded`), and the focus stays on it.
  **Clear filters** and the count stay in view.
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
  says so and offers **Try again**. A check run again that fails shows
  only its error, not the result before it.
- **An address naming nothing goes back to the list.** A page opened from
  an address naming something that doesn't exist, such as a new role
  started from a role that doesn't, goes back to the list and says so.
- **Phones get cards, not a sideways page.** On a phone, a list's rows
  stack as cards: the first column as the title, the others as label and
  value, the checkbox and ⋯ menu at the top, and the sort and **Select
  all** above the cards. A table that only reads side by side scrolls
  within itself, its edge fading where there's more; the page never
  scrolls sideways.
- **Long names are cut, not broken.** An FQDN or a path in a table stays
  on one line, cut with an ellipsis, and shows whole in a tooltip on hover
  and keyboard focus.
- **A count past a page shows a plus.** A count made from one page of a
  list, such as the requests running in the menu, reads "50+ running"
  once there are more, not 50.
- **Nothing that comes later.** A feature that doesn't work yet isn't shown,
  not even as a disabled button.

### Text

Every text a person reads is in English and in French
([frontend.md](frontend.md#languages) has the conventions and the glossary).

- **Whole sentences.** A text is a sentence with its values in it, so a
  translation can reorder them: "Revoke GitLab pipeline?", not "Revoke" and
  a name. Counts choose their words: "1 VM", "3 VMs".
- **French says vous**, and uses vRA's French words: demande, déploiement,
  bail, approbation. A no-break space goes before `: ; ! ?` and between a
  number and its unit.
- **Dates, numbers and lists in the language shown:** "12 mars 2027",
  "1 234,5", "a, b et c".
- **Left as they are:** names (rvav, vCenter, AWX), identifiers (REQ-12, a
  VM's name), what people typed, and the server's messages, which are in
  English until the API speaks the person's language (ADR-0061).
- **One set of verbs for making things.** Three verbs, each with one job:

  | Verb | What it does | Examples |
  |---|---|---|
  | **New** *thing* | Opens the form for something rvav keeps on its own. The form has the same title. | **New project**, **New VM template**, **New token**, **New role from it**, **New draft** |
  | **Create** *thing* | Confirms that form. A form for something that exists says **Save** *thing* instead. | **Create project**, **Create template**, **Create token**; **Save template** in **Change RHEL 9** |
  | **Add** *thing* | Puts a part into what you edit, or connects something that exists elsewhere. Its form, if it has one, is titled and confirmed with **Add**. | **Add a field**, **Add a step**, **Add a level**, **Add someone**, **Add a limit**, **Add a vCenter**, **Add a workflow** |

  Never Make, Register or Generate. French says **Nouveau** or **Nouvelle**,
  **Créer** and **Ajouter**: **Nouveau projet**, **Créer le projet**,
  **Ajouter un champ**. A button that does something other than store a
  form says what it does: **Import**, **Adopt 12 VMs**, **Request**.
- **Environments by name.** A page says **Production**, not `prd`: in its
  badges, lists, sheet titles, options and sentences. The key shows only
  where an administrator types or reads it: the environment's own form and
  list, and CEL conditions (`environment == "prd"`).
- **Room to grow.** French is up to a third longer: labels wrap rather than
  truncate, and every page still reflows at 320 px.
- **Each language in its own words:** the language menu lists English and
  Français, whatever the language shown.
