# Frontend architecture

The UI is the product. It gets the same engineering care as the backend:
typed end to end, tested, fast, accessible, and consistent.

## Stack

| Concern | Choice |
|---|---|
| Language | TypeScript (strict: `strict`, `noUncheckedIndexedAccess`, `exactOptionalPropertyTypes`) |
| Build and dev server | Vite |
| UI library | React 19 |
| Routing | TanStack Router (file-based routes, typed params and search params, loaders) |
| Server state | TanStack Query (caching, prefetching, optimistic updates, invalidation) |
| Tables | Our own `DataTable` on a plain `<table>` (see [Building blocks](#building-blocks-componentsui)) |
| Styling | Tailwind CSS v4 with design tokens as CSS variables |
| Components | shadcn/ui (Radix primitives); code lives in the repo and we own it |
| Icons | lucide-react |
| Forms (app screens) | react-hook-form + zod |
| Forms (catalog requests) | Our own JSON Schema + UI schema renderer on top of react-hook-form, with ajv for validation |
| Command palette | `cmdk` (through the shadcn `Command` component) |
| Toasts | sonner |
| Charts | Recharts (through shadcn charts) |
| Graphs (topology, composite designer, runs) | React Flow (`@xyflow/react`), laid out by dagre ([ADR-0032](adr/0032-one-canvas.md)) |
| Code editor (YAML views) | Monaco or CodeMirror 6, loaded lazily |
| API client | Generated from `api/openapi.yaml` with `openapi-typescript` + `openapi-fetch` |
| Realtime | SSE (`EventSource`), bridged into the TanStack Query cache |
| Dates | date-fns |
| i18n | None yet: the UI is in English, and its strings are in the components. Translation, starting with French, comes in [O1.3](https://github.com/damhau/rvav/issues/169) |
| Lint and format | ESLint (typescript-eslint, react-hooks, jsx-a11y, tanstack-query plugin), Prettier with the Tailwind plugin |
| Testing | Vitest + Testing Library, MSW for API mocks, Playwright for end-to-end and visual tests, Storybook for components |

## Project layout

```
web/
├─ src/
│  ├─ main.tsx                 # QueryClient, RouterProvider, ThemeProvider
│  ├─ routes/                  # TanStack Router file-based routes
│  │  ├─ __root.tsx            # app shell: sidebar, top bar, ⌘K, toaster
│  │  ├─ index.tsx             # home
│  │  ├─ catalog/
│  │  │  ├─ index.tsx          # grid + filters (search params typed)
│  │  │  └─ $itemSlug.request.tsx
│  │  ├─ deployments/
│  │  │  ├─ index.tsx
│  │  │  └─ $deploymentId/
│  │  │     ├─ route.tsx       # layout + tabs
│  │  │     ├─ index.tsx       # overview
│  │  │     ├─ history.tsx
│  │  │     └─ audit.tsx
│  │  ├─ requests/$requestId.tsx
│  │  ├─ approvals/index.tsx
│  │  └─ admin/
│  │     ├─ catalog-items/
│  │     │  ├─ index.tsx       # items, categories, secrets, AWX templates
│  │     │  └─ $itemId.tsx     # an item: versions, designer, YAML, details, sharing
│  │     └─ ...
│  ├─ features/                # domain modules (UI + hooks + queries)
│  │  ├─ catalog/
│  │  ├─ deployments/
│  │  ├─ requests/             # timeline components, SSE subscription
│  │  ├─ approvals/
│  │  ├─ day2/                 # action registry, dialogs
│  │  └─ admin/catalog/        # catalog administration; designer/ is the form designer
│  ├─ components/
│  │  ├─ ui/                   # primitives and the building blocks: PageHeader,
│  │  │                        # Panel, DataTable, Sheet, ConfirmDialog, Button…
│  │  ├─ common/               # app parts: Page, status badges, Timeline, Meter…
│  │  └─ canvas/               # a blueprint as a graph (React Flow, dagre; ADR-0032)
│  ├─ forms/                   # JSON Schema form renderer + widgets
│  ├─ lib/
│  │  ├─ api/                  # generated types, fetch client, error handling
│  │  ├─ query/                # queryClient, key factories, SSE bridge
│  │  ├─ auth/                 # OIDC (oidc-client-ts), route guards
│  │  └─ utils/
│  ├─ hooks/
│  ├─ styles/                  # globals.css, tokens
│  └─ i18n/
├─ e2e/                        # Playwright
└─ .storybook/
```

Rules:
- `routes/` contains only thin route files: loaders, search param schemas, and
  composition of `features/` components.
- `features/*` doesn't import from other features' internals; anything shared
  goes into `components/common` or `lib`.
- `components/ui` holds the primitives and the building blocks every page is
  made of; `components/common` holds the parts that know rvav's domain, such as
  status badges and the request timeline.

## Routing (TanStack Router)

- **File-based routes** with code-splitting per route (`autoCodeSplitting`).
- **Typed search params** validated with zod: filters, sorting, pagination and
  the selected tab live in the URL, so every view can be shared and bookmarked.
  ```ts
  const deploymentsSearch = z.object({
    q: z.string().optional(),
    project: z.string().optional(),
    status: z.array(z.enum(["running", "ready", "failed"])).optional(),
    sort: z.enum(["created", "-created", "name"]).default("-created"),
    page: z.number().int().min(1).default(1),
  });
  ```
- **A list's views** (`features/deployments/listing.tsx`) build on the
  address. `SavedViewsMenu` saves the list's search params, as
  `toQuery(search)` gives them, under `/me/views`, and applying a view
  navigates to its query, which the route's `validateSearch` checks like any
  address. `ScopeToggle` sets `?project=all` for every project the person
  is in. The layout, table or cards, is `?layout=`; without it, the list
  shows the layout the person last picked, kept in localStorage per person
  (`rememberedLayout`).
- **Loaders call `queryClient.ensureQueryData`**, so data is there when the route
  renders, and stays cached for navigation back.
- **Intent preloading** (`defaultPreload: "intent"`): hovering a link prefetches
  its route code and data.
- Route guards check auth and role in `beforeLoad`. Under `/admin`, the layout
  route (`routes/admin/route.tsx`) finds the page an address is on, or the
  page above it, with `adminPageAt` from `nav.ts`. When that page's menu
  entry is hidden from the person (`show(me)` is false), it shows
  `PageNotAllowed` (**You can't open this page**) instead of the page, so a
  typed or shared address doesn't open a page whose actions the server
  refuses. An address that matches no page, `/admin` included, shows
  `PageNotFound`, the root route's `notFoundComponent`.
- `pendingComponent` shows skeletons that match the final layout (no layout
  shift); `errorComponent` shows a friendly error with retry and a request ID.

## Server state (TanStack Query)

- **Query key factories** per feature, so invalidation is precise:
  ```ts
  export const deploymentKeys = {
    all: ["deployments"] as const,
    lists: () => [...deploymentKeys.all, "list"] as const,
    list: (f: DeploymentFilters) => [...deploymentKeys.lists(), f] as const,
    detail: (id: string) => [...deploymentKeys.all, "detail", id] as const,
  };
  ```
- `queryOptions()` helpers are shared between loaders, components and prefetching.
- Defaults: `staleTime` of 30 s for most data, longer for reference data such as
  inventory pickers; `refetchOnWindowFocus` stays on.
- **Optimistic updates** for fast actions (power on/off, favourites, approve),
  with rollback on error and a toast.
- Mutations invalidate by key factory; long-running operations return a request
  ID and the UI follows it over SSE rather than polling.
- `placeholderData: keepPreviousData` for paginated tables (no flicker).
- **A search box** uses `useSearchBox(search, onSearch)` (`lib/hooks.ts`): it
  shows what's typed at once, sends it after 250 ms, and takes the search
  from outside (Back, a link, **Clear filters**) without putting the old
  text back. A list the server searches also keeps its rows while the next
  ones load (`keepPreviousData`), unless its key changes with the project,
  whose old rows would show for a moment under the new one.
- **Mutations that must not overlap** share a `mutationKey`, and the buttons
  that start them wait on `useIsMutating` for that key and say why: a
  catalog item's Publish and Roll back, the vRA import's read, map, import
  and adopt.
- **Deleting what a page shows:** leave the page first, then remove its query
  (`removeQueries`), and leave it out of the invalidation. Otherwise the page
  reads itself again before it leaves, and flashes "not found".

### Realtime bridge (SSE → Query cache)

One `EventSource` per tab (`/api/v1/events/stream`), with topic subscriptions
managed by a small client. Incoming events update the cache directly:

| Event | Cache effect |
|---|---|
| `request.event` | Append to `requestKeys.timeline(id)` with `setQueryData` |
| `request.status` | Patch `requestKeys.detail(id)`, invalidate related deployment |
| `resource.updated` (power, IP) | Patch the resource in the deployment detail and lists |
| `approval.created` | Invalidate the approvals list, bump the sidebar badge |
| `notification` | Toast plus the notification centre |

Reconnection uses `Last-Event-ID`, so no events are lost. The connection state
is shown subtly in the top bar. An event that arrives during a read would be
overwritten by that read's older answer, so the query is read again (`readAgain`).
An invalidate restarts only a read with data to fall back on, so a first read is
read again after its answer.

## Design system

The look is an operations console: a fixed left rail, one work column, IBM
Plex Sans and a petrol accent. It aims to be calm and readable, in the manner
of Linear, Vercel or GitHub's settings: few borders, one primary action per
page, and secondary text that still reads.

### Tokens
- Colours are CSS variables in `src/styles.css`, with a light and a dark set:
  surfaces (`--bg`, `--surface`, `--sunken`, `--rail`), lines (`--line`,
  `--line-strong`), text (`--fg`, `--fg-muted`, `--fg-faint`), the accent, and
  status pairs (`--ok`, `--warn`, `--bad` and `--run`, each with a `-soft`
  background). In Tailwind they're `bg-surface`, `text-muted`, `border-line`,
  and so on.
- Text on a solid accent or bad background is `text-accent-fg` or
  `text-bad-fg`, never `text-white`: both backgrounds are light in the dark
  theme, so their text is dark there.
- Muted and faint text keep at least 4.5:1 contrast on every surface in both
  themes, so you can use them for any secondary text.
- The theme follows the system until the user picks one in the top bar
  (`src/lib/theme.ts`).
- IBM Plex Sans for text, with tabular numbers (`tnum`) where numbers line up.
  IBM Plex Mono only for identifiers, addresses and code: a VM's name under its
  label, an IP, a key, a path, YAML. Counts, times and step numbers stay in the
  text face.

### Type scale
The body is 14 px, and nothing is under 12 px but badges. Tailwind's default
sizes are cleared, so pages use these names; `typography.test.ts` fails on
`text-xs`, `text-sm` or an arbitrary size such as `text-[13px]`.

| Class | Size and line height | Use it for |
|---|---|---|
| `text-title` | 20 / 28 px, semibold | The page's `h1` |
| `text-section` | 15 / 22 px, semibold | Panel, section, sheet and dialog titles |
| `text-body` | 14 / 20 px | Text, table cells and controls: the default |
| `text-secondary` | 13 / 18 px | Descriptions, help under a field, a row's second line |
| `text-caption` | 12 / 16 px | Table headers and labels (`.label`), canvas cards |
| `text-badge` | 11 / 16 px | Environment badges, avatars and key caps only |

Labels and table headers are sentence case, not capitals.

### Building blocks (`components/ui`)
Make a page from these before writing markup of your own. Each has tests beside
it and works in both themes.

| Block | What it is | When to use it |
|---|---|---|
| `PageHeader` | The page's `h1`, a sentence on what the page is for (`description`), facts about what it shows (`meta`), `breadcrumbs` and `actions` | Once, at the top of every page. One of its actions is primary. The actions stay beside the title while there is room, and its `meta` wraps under it. |
| `Breadcrumbs` | A `Breadcrumb` navigation landmark: links to the pages above this one, top-level first. The page's own title follows it, so it isn't repeated. | In `PageHeader`'s `breadcrumbs` on every page below a top-level one: a deployment, a request, a request form, a catalog item, a project, a role. Top-level pages have none. |
| `Panel` | A bordered region named by its title: a header with a title, a description and actions, a body, and an optional footer | One thing on a page: a list, a settings form, a summary. Pass `flush` when the body is a table. A settings form's Save goes in `footer`. A section of a long form is `as="fieldset"`: a group of fields its title names. |
| `Section` | A titled region without a box | Content that has boxes of its own (cards, panels), or needs none. |
| `DataTable` | A table of records: sortable columns, a ⋯ menu on each row, rows that open their link, a toolbar, loading, empty and failed states, and optional selection | Every list of records. Search (`TableSearch`), filter selects and chips go in `toolbar`; a row's actions in `rowMenu`, named by `rowMenuLabel`; an `EmptyState` in `empty`. Selecting all selects the rows shown; unticking it leaves selected rows the filters hide as they are (name it **Select all shown** with `selection.allLabel` when filters or paging hide rows). Pass `rowLinks` when a row has a page: a click anywhere on the row, but not on its buttons, checkbox or menu, focuses its first link and follows it, so what opens gives the focus back there. Only such rows light up under the pointer. A list loaded page by page sorts only where the server sorts too (its sort in the query, through `sort` and `onSortChange`, as Deployments does); otherwise leave `sortValue` off, as sorting the loaded rows would mislead. Pass `presorted` when the rows come in the server's order: a header then only changes the sort. |
| `Segmented` | A row of toggle buttons for one choice, a `group` named by its `label`, the chosen one `aria-pressed`; an option can show only its icon (`iconOnly`), named by its label | A choice of two to four views of the same thing: a list's layout, its scope, the canvas's **Show as**. Not for a setting saved with a form: use `Select` or radios there. |
| `EmptyState` | An icon, a title, one line, and the next step | A list or panel with nothing in it yet: say what goes there, and offer the step that adds it. |
| `Sheet` | A side panel on the Radix dialog. It opens on its first field, keeps the focus inside, closes with Escape, and gives the focus back to what opened it, a row menu's button included. | Creating and editing a record, with the list still in view. Pass `onSubmit` and `submitLabel` for a Cancel and Save footer, or a `footer` of its own. Pass `pending` while its change runs: Cancel, Close and the submit button wait, and Escape or a click outside don't close it. When its action removes what opened it (a row decided, say), `returnFocus` names where the focus goes instead. |
| `ConfirmDialog` | An alert dialog that says what will be removed (`description`) and what depends on it (`consequences`) | Before removing or breaking something; never `window.confirm`. Pass `pending` while the change runs, and `cancelLabel` when "Cancel" would read as the action (**Keep it running** beside **Cancel request**). |
| `DialogContent` | A centred modal | Short questions and small forms that aren't a record's: a reason, a choice, a result. |
| `Button` | Variants `primary`, `secondary`, `quiet`, `danger` and `danger-solid`; sizes `sm`, `md` and `icon` | One `primary` per page or panel; `secondary` for the others; `quiet` in rows and toolbars; `danger` for an action that asks before it removes, and `danger-solid` for the button that confirms. |
| `IconButton` | A button that shows only an icon, named by its `label` | Close, more actions, copy: actions whose icon is clear. |
| `Menu` | A dropdown menu on Radix: `MenuItem`, `MenuLabel` for a group's name, `MenuSeparator`, and `MenuSub` with `MenuSubTrigger` and `MenuSubContent` for a submenu. It isn't modal, so the page behind keeps its accessibility tree, and it scrolls when it's taller than the window. | A ⋯ menu, or an **Actions** menu that holds all of a record's actions, such as a deployment's, with a submenu per VM. A disabled item says why in its text. An item that opens a dialog uses `onSelect`; the dialog gives the focus back to the menu's button, from a submenu too. |
| `StepMark` | A step's number, a tick once done, the accent for the step to do now (`stepNow` marks its row) | A list of steps in order: the setup checklist, the vRA import's steps. The step's own text says its state to screen readers. |
| `Field` | A control's label, a line of help, and the problem with its value, tied to the control (`aria-describedby`, `aria-invalid`) | Every form control. `Field` adds its note's id to the ids already describing its first child, so a control of your own passes `aria-describedby` and `aria-invalid` through to its input. A problem with the value goes in `error`, not `hint`, so it's announced as invalid. A control that has no `Field` of its own (a radio group, a form widget) puts `FieldNote` under it, for the same error and help. |

`components/common` has the rest: `Page` (a page's width and padding),
`KeyValues`, the status badges, `Timeline`, `Meter`, the skeletons and
`ErrorState`. `Markdown` renders what an administrator writes in Markdown, such
as a catalog item's description: CommonMark with its HTML and images left out,
links opening in a new tab, and its headings moved below the section it's in
(`headingLevel`). It loads `react-markdown`, so use it only on code-split
routes.

### Interaction rules
- One primary action per page, and at most one per panel. An empty list
  offers the action that adds one in its empty state only; the header shows
  it once the list has rows.
- Create and edit records in a `Sheet`, so the list stays in view. Keep dialogs
  for questions.
- A record's actions go in its row's ⋯ menu; the panel header keeps the action
  that adds one. A page about one record has its primary action in
  `PageHeader`, and the rest in a ⋯ menu beside it.
- A page's tabs are in its address (`?tab=`, checked by the route's
  `validateSearch`), so a link or a reload opens the same tab.
- Every list has an empty state with a next step, a loading state, and an
  error state with a retry.
- Ask before removing anything, and say what depends on it. Actions that need
  it ask for the deployment's name to be typed.
- An async button shows that it's pending and is disabled, so it can't submit
  twice.
- A disabled control says why, in its text or beside it. Rather than a
  button disabled until something is selected, show the action once there
  is a selection, as Deployments' and Onboarding's selection bars do.
- An overlay (a sheet, a dialog, the command palette, the phone drawer)
  opens on what it asks first and gives the focus back to what opened it:
  `useOpener` in its `onCloseAutoFocus`. A dialog that asks for a value
  focuses it on open (`onOpenAutoFocus`) rather than **Close**.
- A failed load says so and offers **Try again**, in a menu too: never an
  empty list or a skeleton that stays.
- Toasts for results, inline messages for validation, never `alert()`.
- Don't show what doesn't work yet: no disabled buttons for features that come
  later.
- Motion is short (150–200 ms), answers the user's action, and is off with
  `prefers-reduced-motion`.
- Responsive down to tablet width; the approval flows also work on a phone.

### Navigation
- The sidebar (`components/shell/Sidebar.tsx`) starts with the main menu:
  Overview, Catalog, Deployments, Resources, Requests, Approvals and
  Reports. Below a line, **Administration** lists its pages in four groups,
  each under a small heading: Catalog, Governance, Infrastructure and
  Operations.
- `components/shell/nav.ts` lists the pages, their icons, their groups and
  who may open each. The sidebar shows only the pages the person may open,
  and leaves out a group with none. The command palette lists the same
  pages.
- A menu entry's label is its page's title. `Shell.test.tsx` opens every
  page and checks its `h1` and the entry marked `aria-current="page"`.
- From 1024 px wide, the sidebar shows labels. From 768 px, it's a rail of
  icons, each with its label in a tooltip on hover and focus. Narrower, it's
  a drawer. On the rail and the drawer, the top bar's **Open navigation**
  button opens the drawer, with labels.

## Command palette (⌘K)

- Groups: Deployments and VMs (by name, IP or FQDN) and Requests, searched on
  the server from two characters; Request (catalog items, to request
  directly); Go to (the main menu's pages); Actions (the theme).
- Once something is typed, the administration pages the person may open
  come after Go to, in the sidebar's groups. They're found by their name, their
  group's name, or a keyword from `keywords` in `components/shell/nav.ts`,
  such as "sizes" for **Templates and networks**.
- Once two characters are typed, **Find … in Resources** ends the
  Deployments and VMs group: it opens `/resources?q=…`, which lists every VM
  the search finds, not the palette's first few deployments.
- Results are cached in TanStack Query.

## Catalog form renderer

The most important custom component, used for catalog requests, day-2 forms and
the designer preview.

- Input: JSON Schema + UI schema (from the API) and the request context
  (project, user).
- A widget registry maps `ui:widget` to components such as `text`, `select`,
  `multiselect`, `vsphere-network`, `efficientip-subnet` or `datagrid`. Each
  widget is a shadcn-based component that works with react-hook-form.
- **Dynamic values**: each field with a value source uses `useQuery` with a key
  built from its `dependsOn` values. When those values change, the query key
  changes and the options load again (with a loading state on the field, not on
  the whole form).
- **What a field shows is what the form sends.** A source that fails says so
  under its field, with **Try again** (`forms/failed.tsx`), and the failure
  stays while the retry runs. Until it answers, a select shows the value it
  holds, or **Not loaded** when it holds none, and a checklist lists the picks
  it holds. Once the options load, a held value they don't offer gives way to
  the server's default, or to none. A successful **Try again** gives the focus
  to the field's control.
- **Rules** (visible, required, read-only, computed value) are CEL expressions
  evaluated by the server: the request preview returns each field's results
  (`fields`) and the renderer shows them (`fieldStates`). The browser doesn't
  evaluate CEL; the mock backend has a small evaluator of its own
  (`mocks/cel.ts`) so the UI runs without a server.
- Validation: ajv on the client for instant feedback; the server is the final
  authority, and its errors map back onto the fields.
- **Secrets stay out of the caches.** A query's key, and a mutation's
  variables, stay in TanStack Query's cache after the page is gone. The
  previews of the request page, the update page and the designer key on
  the inputs with each secret one as a stand-in (`forms/secretKeys.ts`),
  and read the real values when they ask the server. Submit's mutation has
  `gcTime: 0`, so its variables go once it's done.
- Layout (`layout.kind`): `FormRenderer` shows the sections on one page,
  as a wizard (`forms/wizard.tsx`) or as tabs (`forms/tabs.tsx`); what
  they share (each field's state, the field grid) is in
  `forms/sections.tsx`. Every page or tab stays mounted and hidden when
  not shown, so values and widget state are kept and a problem's control
  is there to focus. A page or tab whose fields the rules all hide is
  left out. The wizard's Next calls the page's `check`: the request page
  and the designer's preview ask a fresh preview, mark the page's inputs
  touched and show that preview until the preview of a later debounced
  draft arrives, then drop it, so the next page follows the rules as
  they stand and neither an older preview nor the check itself hides
  what a later change showed. Next decides on that fresh preview alone:
  a validation's message from the last check stays shown without
  holding the page. The review step
  (`forms/review.tsx`) shows option labels from the fields' own queries
  and masks secrets; the request page puts its lease, reason, options and
  Submit on it. A `FormHandle` ref (`focus(name)`) opens the page or tab
  of an input before focusing it: the request page's refused submit goes
  through it. When the form has validations (ADR-0043), the wizard calls
  `checkAll` before the review step: the request page asks
  `POST /requests/preview?validate=true`, and a message on an input keeps
  the wizard on that input's page, focused through the handle. Messages
  for no input show above the form.
- Draft auto-save (debounced) for long forms.

## Catalog administration and the form designer

Catalog admins and administrators build items under **Catalog items**
([ADR-0019](adr/0019-catalog-administration.md)):

- The list page, in tabs: **Items** (search, filters by kind, category and
  state, **New item**, which starts as a draft 1.0, and **Import YAML**,
  below the Git repository's banner when the catalog is in Git);
  **Categories**; **Secrets** for REST value sources and calls (values are
  write-only); **AWX templates**, those imported from AWX.
- An item's page: **Publish** the draft or **New draft** in its header, and a
  ⋯ menu for the whole item (request form, export, withdraw, delete). Its
  tabs: **Versions** (a row menu to publish a draft, roll back to a
  deprecated version, withdraw the published one, export as YAML or
  discard; **Compare** two versions), **Form designer**, **Definition**
  (the version as YAML), **Details** (icon from rvav's own set),
  **Sharing** (the projects, then a table of who may request it in each,
  changed in a sheet that previews who can request; `SharingTab.tsx`) and,
  in Git, **Git**.
- The **Definition** tab is a CodeMirror 6 editor, loaded only when opened.
  A YAML parse error, or a 422 from the server with JSON pointers under
  `/definition`, shows on its line (`definitionYaml.ts` walks the YAML
  document to the pointer's node). Published and deprecated versions are
  read only; leaving the tab with unsaved YAML asks first.
- Beside the editor (from `xl`, the graph taking three fifths of the
  width; below it on narrower screens), a VM blueprint's or a
  composite's graph: `DefinitionGraph.tsx`, loaded
  lazily with the tab so React Flow stays out of the other pages, draws
  the editor's text (`useDeferredValue`, so typing stays quick) through
  `drawDefinition` (parse, take `blueprint`, `blueprintGraph`). A text is
  parsed once for all that read it on a keystroke (the graph, the
  designer, the step selected, their problems: `parseText`, which keeps
  the last few, read only), and the graph keeps its object while what it
  draws is the same, so a keystroke outside the blueprint doesn't lay it
  out again. The text
  is the editor's state, which starts from the designer's unsaved working
  copy when there is one, so the graph follows the draft as typed and as
  designed. A newer copy or draft replaces the text only while it is
  unedited: the form designer's save, which goes on after its tab is
  left, answering or refused, doesn't take what is being typed. When the text doesn't parse, or has no blueprint the canvas
  draws, the last graph stays (zoom, selection and focus with it) under a
  line saying why; the graph's own problems are listed with their lines
  (`problemsOnLines`). The Form designer tab has no graph: its edits
  don't change the blueprint.
- A **composite draft's** graph is the **composite designer**
  (`composite/CompositeDesigner.tsx`, loaded lazily with the tab): the
  same graph, with **Add a step**, **Undo** and **Redo** above it and the
  selected node's editor under it (its `renderDetails`). Each edit is a
  pure function from the editor's text to a new one (`composite/edits.ts`,
  on the `yaml` Document API, so comments, quoting and inline maps
  survive until a save, which stores JSON), written into the editor through its handle (`YamlEditor`'s
  `ref`: `apply`, `undo`, `redo`) as one isolated step of CodeMirror's
  history: typed and designed edits are undone in the order they were
  made, from the buttons, Ctrl+Z, or Ctrl+Shift+Z and Ctrl+Y outside the
  text fields (`onHistory` says what can be). What a step can run comes
  from `GET /admin/catalog/composite-steps` (`q.compositeStepTargets`);
  `composite/scope.ts` says what an input of a step can be bound to at its
  place (the form's inputs, the request's environment and project, the
  outputs of the slots before it, none for a VM step) and what the server
  would refuse when the item is requested, in its words (with the item's
  entitlements, a workflow not offered to all its projects). A picker's
  value names an AWX template as a job or workflow template
  (`targetValue`), and a step is found by its `template_id` when it has one. A REST call
  ([ADR-0033](adr/0033-composite-step-kinds.md)) is a target with no ref: its fields
  (`RestCallFields`) replace the inputs, on a step and on an undo, edited by `setRestField`;
  a key renamed follows into a URL's `${...}` only (`renameInUrl`), and `scope.ts` gives
  its outputs (`restOutputs`). A wait for a person is another (`AskFields`, `AskQuestion`, edited by
  `setAskField`, `setAskEscalate` and `setAskQuestion`), with no inputs or undo, and `askOutputs` gives its
  outputs, one per question. Its only refusal on request depends on who requests, so the
  Approvers field's hint says it. A wait for a time and a poll are others (`WaitFields`, `PollFields`,
  a poll's call being `RestCallFields` with `poll`, edited by `setWaitField`, `setPollField` and
  `setRestField`), with no inputs or undo, and `waitOutputs` and `pollOutputs` give their outputs. A branch has
  `BranchFields` (`addBranchCase`, `moveBranchCase`, `setBranchCase`, `removeBranchCase`), and its sides' steps are
  selected and edited as any other: `edits.ts` and `scope.ts` flatten them right after the branch with their own
  pointers, a step's `side` says where it moves, and `earlierSteps` says what it can read. A message has `NotifyFields`
  (`setNotifyField`), its text a textarea that commits on blur, as Enter is a new line. Those problems,
  and the server's last refusal of a save while the text is the one it
  refused, go on their step (`addProblems`). ADR-0032 records why.
- The **form designer** has three panes: the fields by section (add from
  any widget, move fields and sections with buttons, so by keyboard), a
  live preview, and the selected field's or section's properties (label,
  widget, required, limits, options, initial value, value source and what
  it depends on, the four CEL rules, handling flags). Its edits are pure
  functions over the definition (`designer/model.ts`) that keep the JSON
  Schema, UI schema and initial values in step. **Layout**, above the
  list, sets `layout.kind` (`setLayout`); the list and the properties
  then call sections pages or tabs (`partWord`). The preview shows the
  page or tab of the selection (`FormRenderer`'s `showing`).
- The form library (`features/admin/catalog/FormLibraryPage.tsx`,
  ADR-0043), beside **Property groups** under Catalog in the sidebar: a
  table of entries and a Sheet that adds or changes one (a save is a new
  version, and says which items get a draft), tests it with its
  parameters (`POST /admin/form-library/test`) and lists the items'
  versions that use it. The designer's **Value source** offers **Form
  library**: an entry, its version and a CEL expression per parameter
  (`designer/Properties.tsx`, `LibraryRef`), a parameter named as an input
  starting bound to it. **Form validations**, after the sections in
  the field list, selects the form's validations: each written in the form
  (CEL or REST) or taken from the library, with the inputs its messages
  show on.
- The designer edits a **working copy** of the draft
  (`designer/draftStore.ts`), kept outside React so it outlives the tab.
  Every change is saved after a 600 ms pause, one save at a time, each
  sending the revision it started from; leaving the tab saves what is
  pending, and leaving the page with unsaved changes asks first. The
  server checks the whole definition on each save, and its problems show
  under the label, rule or source they are about, with a mark on the field
  in the list; anything else (the blueprint) is listed above the panes. A
  copy the server refused stays, problems included: the Definition tab
  shows it with the problems on their lines, and Publish waits until the
  server has it. A 409 (the draft changed elsewhere, or was published or
  discarded) stops saving until the admin loads the other changes or saves
  theirs over them.
- The preview is the server's: `POST …/versions/{id}/preview` gives the
  form, the rule results and the problems a request with these inputs
  would have, as a chosen project member (`as_user_id`) in a chosen
  project and environment; the fields' value sources resolve through
  `/forms/values` with the same `as_user_id`. **Test the source** calls the
  selected field's source with the preview's inputs. Previews are as
  members of the chosen project.
- A field with a `visible` rule stays hidden in a request form until the
  server's results arrive, rather than show and then vanish.

## Administration

The other administration pages ([ADR-0020](adr/0020-administration.md))
are in `features/admin/governance/`, roles in `features/admin/access/`
([ADR-0034](adr/0034-custom-roles.md)). The sidebar shows each person the
ones they may use, in groups (see [Navigation](#navigation)); auditors see
them read only. A page that picks or
names projects lists them with `adm.projectNames()`
(`GET /admin/project-names`), which its own permission is enough for;
`adm.projects()` takes `project.settings.read`, so only **Projects** uses
it.

- **Setup** (`features/admin/SetupPage.tsx`, ADR-0030): the setup
  checklist, from `GET /admin/setup`, read again each time it opens. Each
  step links to its page (an integration's at `/admin/integrations/<kind>`).
  The first step not done that this administrator can do is current
  (`aria-current="step"`, its link the primary button); the password
  step is skipped when signed in with the provider. While it
  is open, an administrator lands on it from the Overview once per page
  load (`SetupLanding` in `AppShell.tsx`), and the Overview offers the way
  back. Administration pages work without a project, so an administrator
  in none can set rvav up.
- **System** (`features/admin/SystemPage.tsx`): `GET /admin/system`,
  read every 10 seconds; read only, each saved setting linking to its
  integration's page. Its tables have no sortable column, so the page has
  nothing to press.
- **Projects**: the projects (search, archived ones on request, **New
  project** with its environments and first project admins), the
  environments (production or not, snapshots kept, removable while unused)
  and the identity provider groups (any role, for all of rvav or in one
  project, as the role can be given; a role with `access.manage` warns
  that the group's members are administrators in all but name). Project
  admins see their own projects only.
- **Roles** (`access.read`), in tabs, with **New role** as the page's
  primary action: **Roles**, a table of the built-in and custom roles,
  where each can be given and how often it is, whose rows open the role
  and whose row menu makes a role from it or deletes a custom one;
  **For all of rvav**, the roles given to users for all of rvav (given
  from the table's toolbar and taken away from a row menu after a
  confirm, with `access.manage`; the ones rvav sets have no menu);
  **What someone may do**, for a user and a project or all of rvav
  (`?user=&project=` in the URL), each permission held or not with the
  roles granting it and where each comes from
  (`GET /admin/access/explain`,
  `features/admin/access/AccessExplained.tsx`). A role's page (`/admin/roles/<key>`)
  says what it allows in the catalogue's sentences (`GET /permissions`)
  and where it can be given. A built-in role is read only, with **Make a
  role from it** (`/admin/roles/new?from=<key>`) in its header. With
  `access.manage`, a custom role's page is its editor: key fixed once
  made, name, description, permissions by area, day-2 ones by group or
  single action, and a warning when `access.manage` is chosen. Its header
  has **Save role** (**Make role** for a new one) and a menu with
  **Delete role**. Deleting asks in a `ConfirmDialog`; while the role is
  given or named by a day-2 policy, **Delete** is disabled and says why
  (`cantDelete`), in the row menu too. The server's refusals show where
  they apply: a field's under its field (a taken key under **Key**, a
  missing `project.see` or a permission for all of rvav on a role given
  in a project above the permissions), a conflict beside **What it
  allows** (still given, named by a day-2 policy, the last way anyone
  holds `access.manage`).
- **Audit log** (`features/admin/AuditPage.tsx`): a table of events,
  newest first, with search, what, project and date filters in its
  toolbar and **Load older events** in the footer; an event's time opens
  it in a side panel with its detail. **Export CSV** is the header's
  action.
- A **project's page**, in tabs: **Members** (each person's roles, several
  each, given here or through a group; add someone found by name or email
  with a role, change the roles given here in a dialog, remove; a project
  admin is offered only the roles whose permissions they hold there, from
  `GET /projects/{id}/assignable-roles`),
  **Placement** (the rules for an environment, by cluster and datastore
  name patterns or vCenter tags, with their storage policy and vCenter
  ([ADR-0024](adr/0024-multi-vm-blueprints.md)), and a preview of the clusters,
  datastores, networks with their subnets, and folder a request would get,
  with what would stop it), **Quotas** (limits for the project or one
  environment, refused or sent to approval when passed, saved as a whole)
  and **Details** (archive, or put back in use).
- **Policies**, in tabs (`?tab=`, one file each in
  `features/admin/governance/policies/`), each kind a table whose row menu
  changes a policy in a side panel and removes it after saying what
  changes: approval and day-2 policies (moved up and down, given a
  priority or turned off from the row menu; a condition that isn't valid
  CEL is refused when saved, under its field), lease policies (an environment's, and projects'
  overrides), naming policies (template, counter, where offered, and a
  preview of the next names with the ones skipped because they are taken)
  and **What applies here**, which says which policy decides the approval,
  day-2 action, lease and naming for a project and environment, with the
  quota lines. Day-2 and lease policies have an **Enforcement**; soft rows
  say so. A project admin (`project.policies.manage`) gets the page
  without the Naming tab: rows they may not write have no menu, the side
  panel's **Project** offers only their projects, and moving is off, as it
  renumbers every project's policies (`usePolicyRights` in
  `policies/shared.tsx`; ADR-0045).
- **Soft warnings** (`components/common/warnings.tsx`): `PolicyWarnings`
  is a `note` listing what soft policies warn about, in the request form's
  summary, the day-2 dialog, the update's review and a request's page
  (**Policy warnings**). The submit button ends in "anyway" and the draft
  sends `accept_warnings`; a 422 at `/accept_warnings` (`refusedWarnings`)
  shows its warnings until the draft changes. A project's **Deployment
  sharing** panel heads its Members tab.
- **Templates and networks**: VM templates (a vCenter template, or an
  item of a content library) and sizes, with where each is offered
  (projects and environments), and the network mappings, from EfficientIP
  or by hand.
- **Integrations**, in tabs: **Connections** lists the identity provider,
  the connections, vRA, the notification channels, the allowed hosts and
  the CA bundle (Trusted certificates), each with what it is for, whether
  it is configured or applying, and when it was last saved
  (`features/admin/IntegrationsPage.tsx`); **Checks** has the EfficientIP
  reconcile report. A row opens the integration's page,
  `/admin/integrations/<kind>` (`features/admin/IntegrationPage.tsx`; the
  form is in `features/admin/IntegrationSettings.tsx`), whose footer says
  when it was saved and holds the test and **Save settings**, the primary
  action, on the right. The console's form is on vCenter's page and the
  inventory token's on AWX's
  (`features/admin/InventoryToken.tsx`: made, put in AWX with rvav's
  address as AWX reaches it, the one before revoked); the allowed hosts
  have no secret. The addresses of the tabs before V1.4
  (`/admin/integrations?tab=vcenter`) open the integration's page. A field an environment
  variable sets is disabled and names the variable; the password or token
  is write-only (left empty, it stays as it is). **Test connection** runs
  the server's checks with the settings on screen. For a connection over
  HTTPS it shows the server's certificate below them
  (`features/admin/ServerCertificate.tsx`): trusted, pinned, not checked
  or not trusted, and then **Trust this certificate** (its SHA-256 into
  the form's Trusted certificate field, verification on) or **Add it to
  the CA bundle** (confirmed inline, then saved to Trusted certificates). After a save, a
  connection's panel shows it applying: each of rvav's processes
  (`GET /admin/processes`) finishing its running work, then started again
  with the settings, until all have them (`features/admin/Applying.tsx`,
  ADR-0030); the console, vRA, notification channels, the allowed hosts
  and sync intervals apply on next use. vCenter's page
  lists every vCenter rvav manages below its connection
  (`features/admin/VCenters.tsx`): the others are added, tested, changed
  and removed there, with what the sync last read from each.

**What I may do** (`/access`, in the account menu) is the same
explanation about the signed-in person, in their projects or for all of
rvav; it works without a project.

What each person can change follows the server: auditors read every page
without write controls, project admins manage their projects' members and
read the rest. The member picker marks the signed-in person "(you)", as two
people can share a name and email (an identity provider that changed their
subject). Policies are reordered by renumbering the list 10, 20, 30…

Dialogs opened from a page's state give focus back to what had focus when
they opened (`components/ui/dialog.tsx`); Radix only does that for a
`DialogTrigger`. A field's problem from the server goes in `Field`'s
`error`: shown in place of the hint and tied to the control with
`aria-invalid` and `aria-describedby`.

The mock backend follows the placement rules, naming policies and project
quotas set on these pages. `rvav.mock.as` in localStorage set to
`project-admin` or `auditor` makes the mock user one (Playwright uses it
for what those roles see); the auditor holds no role in a project, as on
the server. `vm-operator` and `compliance-auditor` give it a custom role of
A1's acceptance instead (`e2e/a1-acceptance.spec.ts`): the operator powers
VMs on and off in Web Platform, its only project; the compliance auditor
holds the auditor's permissions for all of rvav. `member` is a member of
Web Platform and Data, whose Web Platform shows deployments to their
owners only and has a soft day-2 policy on power off: G1.3's browser spec
(`e2e/g1-sharing.spec.ts`) starts from what the project admin did there.
`rvav.mock.fresh` set to `1` makes it a new
install: no environments, projects, items or integration settings, and
the setup checklist open (`e2e/setup.spec.ts` follows it).

## Extensibility

Subscriptions and custom workflows ([ADR-0021](adr/0021-event-subscriptions.md),
[ADR-0022](adr/0022-custom-workflows-and-composites.md)) are in
`features/admin/extensibility/`. Administrators change them; auditors read
them.

- **Subscriptions**: a table of each one's topic, condition, target and
  last 30 days; a row opens its page, and its row menu sends a test
  event, changes it, turns it off or on, and removes it after saying what
  stops. The side panel shows the topic's description and sample event
  (what the condition sees). Blocking is only offered on `.pre` topics,
  and then what happens when the target fails. **Send a test event**
  sends the sample and opens the run. A subscription's page
  (`/admin/subscriptions/$subscriptionId`) has **Change subscription** in
  its header, the rest in its ⋯ menu, and its runs in a table filtered by
  status. A run (`runs.tsx`) shows its answer, the properties it set, each
  attempt with what came back, the event sent, and **Try again** when it
  failed.
- **Custom workflows**: registered and changed in one side panel (type,
  task queue, timeout in days, input schema as JSON, usages, projects, and
  the day-2 action when that usage is ticked). The table says what uses
  each one and how many runs haven't ended; the row menu turns one off,
  and its Remove says why it can't while runs haven't ended or something
  uses it. The server also refuses one that requests ran.
- **Composite items** are written in the item's Definition tab, in the
  YAML or on the canvas beside it, the composite designer (see Catalog
  administration): groups, parallel steps, conditions, what each runs,
  uses and undoes, with the server's structural checks on their steps
  while typing (`lib/composite.ts`; the mock reuses them, CEL included)
  and what it would refuse when the item is requested. Saving puts the
  server's problems on their lines and on their steps.

The mock backend (`mocks/extensibility.ts`) seeds a blocking CMDB check, a
ServiceNow subscription with a failed run, the `webhook-signing` secret and
the decommission workflow. It checks webhook hosts against
`cmdb.corp.example`, `hooks.corp.example` and `servicenow.corp.example`.

## The canvas

`components/canvas` ([ADR-0032](adr/0032-one-canvas.md)) draws a
blueprint as a graph: an item's topology, the composite designer and a
request's run use the same component. The YAML stays the source of
truth: the graph is built from the blueprint every time it changes, and
nothing about it (positions, layout) is stored.

- **Model** (`graph.ts`, `blueprint.ts`): `blueprintGraph(blueprint)`
  returns a `CanvasGraph`, or null for a kind it doesn't draw (AWX,
  workflow). `compositeGraph` draws a composite top to bottom: `inputs`
  (the request), `step:<key>` per step (`step@<pointer>` when the key is
  missing or used twice), `end`; a parallel group is a `CanvasGroup`
  `parallel:<index>` its steps sit in, and sequence edges join groups, not
  their steps. A `when` makes the edge into its step `conditional`. What
  a step runs, uses (`steps.<key>` in its bindings) and undoes are its
  `lines` (a REST call: badge `REST`, `Calls: POST <url>`, `Undo: DELETE <url>`; a wait: badge `Person`, `Asks`, `Wants`, `Timeout`, from the definition only, so a node's size doesn't follow its run). `vmGraph` draws a VM item left to right: `network:auto`,
  `network:<CEL>` and `segment:<key>`, then `component:<key>` (or
  `component:vm` without components), then `sg:<key>`, `nsx:<ID>`,
  `groups:<CEL>` and `any`; edges are `network` (an adapter, labelled with
  its key), `member` (dashed) and `rule` (labelled with its services,
  `deny` for drop or reject). Every node has `relations` (how it relates
  to the others, in words) and `problems` (the structural checks, at
  their `pointer`); `nodeForPointer` finds the node a JSON pointer falls
  in, for the server's problems.
- **Layout** (`layout.ts`): dagre, in the graph's direction. A node's size
  is fixed by what it shows (`nodeHeight`), so tests and browsers agree.
  A parallel group is laid out as one node holding its steps. Rules
  between a VM item's groups are left out of the layout and drawn as
  brackets on their right, so the groups stay in one column.
- **Where it is used:** an item's Definition tab, beside the YAML
  (`features/admin/catalog/DefinitionGraph.tsx`), where a composite
  draft's is the composite designer
  (`features/admin/catalog/composite/CompositeDesigner.tsx`), and the
  request form, under its fields (`features/requests/RequestTopology.tsx`;
  see Multi-VM items), a composite request's page (below), and a
  deployment's Topology tab (below).
- **`Canvas`**: `graph` and `label` (names the region and its list), and
  optionally `status` (by node ID: a run's step status, or `undone`),
  `selected` and `onSelect` (controlled, or left to the canvas),
  `renderDetails` (what shows under the canvas for the selected node; by
  default `NodeDetails`, all the node shows), `actions` (the caller's
  buttons beside the view switch), `defaultView`, or `view` and
  `onViewChange` (controlled), `list` (the caller's own list view, named
  by its button; no node details under it) and `height`. Read only:
  the designer edits the YAML, and the graph drawn from it again comes
  back. `addProblems(graph, errors)` puts more problems (JSON pointers) on
  the nodes, or the parallel groups, they fall in.
- **Keyboard:** the graph is one Tab stop, on the node last visited. The
  arrow keys move to the nearest node that way, Home and End to the first
  and last, Enter or Space selects, Escape clears. Each node is a button
  whose name is what it shows, word for word and in the card's order
  (badge and title, status, key, lines, problems; WCAG 2.5.3, which axe
  checks), then its relations. The card puts a space between words of
  separate elements even where none is drawn, so its text reads as the
  name does.
- **List view:** the same model as an ordered list of steps (a parallel
  group a list within it) or a VM item's nodes with their relations, for
  screen readers and anyone who would rather read. It shares the
  selection.
- **A request's run** (`features/requests/RequestRun.tsx`, loaded lazily
  by `RequestDetailPage` for a composite's provisioning request): the
  graph of `GET /requests/{id}/topology` (`q.requestTopology`, read once:
  the requested version's topology never changes), with each node's
  status from the steps query, which the event stream keeps current.
  `run.ts` maps steps to nodes (`runSteps`: `step:<key>` on its node,
  submitted and approval on `inputs`, the rest on `end`), their status
  (`runStatus`, with `undone` for a step whose `/undo` ran or a VM step
  of a request rolled back), the node a failed request failed at
  (`failedNode`, selected at first) and the AWX job to show
  (`pickLogStep`, through nested steps; the selected node's, unless one
  was chosen). The selected node's details are its steps as a
  `Timeline`, plus `FailureActions` on the failed node, or under the
  graph when another is selected. The list view is the request's
  `Timeline` and its `FailureActions`. The graph object is memoized from
  the topology, so events never lay it out or fit it again; keep a run's
  status out of what sizes a node.
- **A deployment's Topology tab** (`features/deployments/DeploymentTopology.tsx`,
  loaded lazily by `DeploymentDetailPage` when `?tab=topology`): drawn from the deployment's resources, not its
  blueprint (`deploymentGraph` in `features/deployments/topology.ts`), left to right: `vm:<resource ID>` per VM
  (kind `vm`: power, first IPv4, size, disks; sync drift and NSX tag drift as problems), `network:<name>` and
  `segment:<name>` from each adapter (an edge per adapter, `Adapter <n>`, `· DHCP` when it is), `sg:<NSX ID>` for
  the deployment's groups, `nsx:<NSX ID>` for existing ones, `any`, with `member` edges from the memberships and
  `rule` edges from the policies. A component of up to `ONE_BY_ONE` (4) VMs is drawn VM by VM, in a `frame:<key>`
  group when there are several components; more is one `component:<key>` node with the count, whose details list
  its VMs. With several components, the creating request's topology (`q.requestTopology`, not retried) gives
  titles and the build order (`dependsOn`, `sequence` edges between frames or nodes). Selecting a VM's node, or a
  name in a grouped node's details, opens `VmPropertiesSheet`, the side panel the Overview's VM names open too.
- **Themes:** only React Flow's `base.css` is imported; `canvas.css` points
  its variables at the app's tokens, and the nodes are our own components.
- **A wait on the request's page:** `features/requests/AskPanel.tsx` shows a composite's waits
  for a person from the request's approvals with a `step_key`, apart from its approval levels: the
  form or Approve and Reject for those named (the row has `can_decide`), a problem on the question
  it is about, with the focus there, and `ApprovalHistory` for the rest. The mock run stays on the
  step until it is answered (`simulator.ts`: `openAsk`, `stepAnswered`, `stepRejected`), so a new
  item with a wait must be answered to reach its end. The mocks' me is both the requester and,
  with `requester: true`, the one asked; someone else's question can't be answered in the mocks.
- **A wait for a time or a condition on a run:** `stepStatus` (`features/requests/run.ts`) gives
  `waiting` for a running step of system `wait`, whose node has an hourglass; a person's step stays
  Running, with its panel. The timeline adds `Until <date>` from `due_at`. In the mocks
  (`simulator.ts`: `openWait`, `waitTick`, `checkPoll`) a tick is a minute of a wait, and a poll's
  condition holds at its third check: `for: 72h` and `every: 6h` stay until cancelled, `for: 2m` and
  `every: 1m` run on, and a `timeout: 1m` reaches its deadline first, so a spec picks what it needs.
- **A branch on the graph:** a side's steps are nodes with `onSideOf`, in a lane from the branch to what
  comes next, and two sides with no steps merge into one edge. Its edges are `routed` (`nodes.tsx`), drawn
  on dagre's points, since a smooth step would run a long edge over a side's steps and hide its label. `layout.ts`
  keeps room for `taken` or `not taken` on their labels whether or not there is a run, so nothing moves. A run
  passes `taken` (`branchTaken`, from `Step.branch_case`) and each edge says its word and is drawn as chosen
  (`rvav-edge-taken`, `rvav-edge-not-taken`). The list view nests a side's steps in a dashed box under the branch. In
  the mocks' run (`simulator.ts`), a branch chooses when it runs, as the server's does, from the form and what earlier
  steps gave: a person's answer (`answered`, `by`, `comment`, `answer`) and a REST call's `status`, the only outputs
  the mocks' steps have (another done step's read as null, `unmodelled`). Its cases are read strictly
  (`celEval(…, true)`), as CEL reads them: a field that isn't there, or a condition that isn't true or false, fails
  the branch, its literals masked and its sides not started, and a Retry reads it again (`chooseSide`). A message is
  filled in when sent, and reaches the mocks' me only if it tells the requester.
- **Browser tests**, on the mock API: `e2e/topology.spec.ts` (a topology
  beside its YAML and on the request form), `composite-designer.spec.ts`
  (the designer, to a request), `composite-rest.spec.ts` (a REST call and
  its undo from the keyboard, masked on the form and the run), `composite-ask.spec.ts`
  (a wait from the keyboard, masked on the form, answered, rejected, retried, cancelled,
  and someone else's), `composite-wait.spec.ts` (a wait for a time and a poll from the keyboard,
  masked on the form, waiting, cancelled, run to their end, at a deadline, retried), `composite-branch.spec.ts` (a branch
  and a message from the keyboard, masked on the form, its side followed, otherwise, nothing held, a failed message and a
  Retry, what's refused on a side), `request-run.spec.ts` (a failed run
  retried and followed), D1's acceptance, `one-canvas.spec.ts`, the
  same with the keyboard only, and D2's, `d2-acceptance.spec.ts` (a vRO workflow rebuilt from the keyboard, answered,
  and followed to the side its answer took, with axe in both themes), `deployment-topology.spec.ts` (a
  deployment's Topology tab, a VM's properties opened by click and by keyboard, with axe in both themes);
  `accessibility.spec.ts` scans the designer and a run. The mocks can't approve as someone else, so a
  spec's own request of a seeded composite is followed as far as its
  approval; a new composite item needs none, so `composite-rest.spec.ts`
  follows one to its end, and the seeded REQ-10484 is retried to follow a run with its failure
  (`TestD1Acceptance` follows one through the API). `page.goto` resets
  the mocks: a spec that publishes, then requests, gets there through
  the app.

## Request options

The request form (`features/requests/RequestFormPage.tsx`) has an
**Options** section:

- **How many:** single-VM items only, at most 20, named one after the
  other. A multi-VM item (`one_deployment` on its form) has its counts in
  the form.
- **Start:** as soon as approved, or at a chosen time.
- **For someone else:** project admins only. That person owns what is
  built.

**Save as draft** keeps the request as filled: unchecked, and without
secret fields (`/me/drafts`). Drafts are listed at the top of Requests
(**Resume**, **Discard**). A resumed draft is discarded once submitted.

**Request again** on a request's page opens the form with its inputs
(`?from=REQ-…`): fields since removed, and masked secrets, stay out. A
draft or request from another project switches to that project first.

When the server refuses a submit (422), each error goes under its field
and the focus moves to the first field that needs attention, or to its
**Try again** when the field can't be filled until its options load. A
message at the top of the form says how many inputs need attention, and
lists the errors on inputs the form has no field for, with their names.
The count drops as you change each input. The summary shows only what
the form sends: no placement for a cluster it doesn't hold.

## Updating a deployment

**Update** in a deployment's **Actions** menu (`ActionItems`, which
navigates instead of opening a dialog) opens
`/deployments/$deploymentId/update` (`features/deployments/UpdatePage.tsx`,
[ADR-0042](adr/0042-deployment-updates.md)):

- **The form** is the item's published version's, through `FormRenderer`.
  `startValues` starts it from the deployment's inputs: secret ones are
  left out, and on the same version a defaulted field the deployment
  doesn't have reads `null` (it was emptied), as F1.4's defaults have it;
  on another version, it takes the form's `initial_values`. The body sends
  every key (`sentInputs`).
- **What the update does** previews the draft, debounced, with
  `keepPreviousData`: the changes, those refused (**Can't be changed in
  place**), the quota after the update (a negative change frees some) and
  the approval. **Review the update** is unavailable while nothing
  changes (`unchanged`), something is refused or not allowed.
- **Review** fetches a fresh preview, then asks in a `ConfirmDialog` that
  lists what the update deletes; it is `danger` when it deletes anything.
  When that preview is unchanged, refused or not allowed (`blockedBy`),
  it asks nothing: an alert below the reason says why and takes the focus.
  It is the wizard's only check, as its submit button stays enabled.
  The submit sends an `Idempotency-Key`, and opens the request.
- The mock API plans in `mocks/update.ts`, as the server does: changes,
  in the server's order (each kept VM's in place, then the VMs added,
  those removed, the jobs), refusals, quota on the net change, names from
  the counter on scale out. Its VMs record their first disk as the
  blueprint's (`blueprint_disk`), and only that disk grows; an onboarded
  VM records none, so growing its disk is refused.

## Multi-VM items

A blueprint with components builds one deployment
([ADR-0024](adr/0024-multi-vm-blueprints.md)).

- **Request form:** the summary's **What it builds**
  (`features/requests/ComponentsPreview.tsx`) lists each component from
  the preview: how many VMs and their names, size, disks, placement,
  network and next free address, tags, and **On separate hosts** for
  anti-affinity. It replaces the single VM's name, placement and address
  lines.
- **Request form's topology:** under the fields, a **Topology** section
  (a composite's: **Steps, in the order they run**) draws what the item
  builds on the canvas. It is drawn from the form's `topology`
  (`RequestForm.topology`: the part of the blueprint the canvas draws,
  without guest settings, sizes, naming or a step's binding literals,
  which a requester doesn't see, its CEL's string literals masked, and a
  REST call's URL and body with them, its secret, statuses and `keep` left out, a
  poll's the same, a wait's group, users, message, labels and options too, a message's all but whether the requester or
  owner is told, a branch's cases masked and its steps as any step, and no problems on any step, since a
  mask reads as a mistake;
  `blueprint.Topology` on the server, `topologyOf` in the mocks, checked
  against one fixture, `internal/seed/testdata/topology.json`) and `withPreview` puts the preview's counts
  and names on the components (a composite's step: what the item it
  requests builds), so it follows the values. Only when there is more
  than one VM on a network to draw: a single-VM item's summary says it
  all. `RequestTopology` is loaded lazily, and the form imports only the
  graph's model (`components/canvas/blueprint`), so React Flow stays out
  of the other pages.
- **Deployment page:** the VMs are grouped by component (a row heading
  per component), the placement panel gives each cluster, datastore and
  network once, and the DRS rules keeping VMs apart.
- **Mocks:** the **Web application** item (`mocks/components.ts`) builds
  web servers kept apart and a database server on the request's network;
  the **Three-tier application** item, N1's acceptance item, web,
  application and database tiers on an NSX segment made for the
  deployment, each in a security group of its own, the database in an
  existing group too. Their steps name their VM after `@`, without the
  component steps the server nests them under. `mocks/blueprints.ts`
  holds the seeded items' blueprints, which the Definition tab shows and
  the canvas draws.

## Drift

A VM changed in vCenter outside rvav
([ADR-0025](adr/0025-drift.md)) shows in three places:
- **Header:** the deployment page's header says **Changed outside rvav**.
- **Changed outside rvav panel:** above the VMs, a table per drifted VM:
  what differs, as rvav left it and in vCenter.
- **Accept the changes:** a button that runs that day-2 action at once.
  The VM's actions menu offers it too.

In the mocks, `web-dev-legacy` has more memory and a disk added in vCenter.

## Reports

**Reports** (`features/reports/ReportsPage.tsx`, [reports.md](reports.md))
shows how the platform is used, for the projects the user sees.
- **Filters:** one row above everything: period (7 or 30 days, 13 weeks,
  12 months), by day, week or month, project and environment. The range
  starts at the first day of a period (a Monday, the 1st), so the first
  column counts a whole one. While new figures load, the old ones stay,
  dimmed.
- **Stat tiles:** requests, success rate, provisioning time (median and
  90%), approval lead time, approvals waiting.
- **Requests chart:** an SVG column chart, stacked by how requests ended.
  - It has a legend.
  - Hovering a column, or focusing the chart and using the arrow keys,
    shows a tooltip with every figure of that period; screen readers say
    it (a polite live region).
  - **Show as a table** lists every period.
- **Other sections:** the most requested items, approvals, quota meters
  per project and environment, and the leases ending in 30 days.
- **CSV:** each section exports as CSV.

**Chart colours:** `--chart-succeeded`, `--chart-failed` and
`--chart-other` in `styles.css`, with their own dark steps.
- They were checked as a set against each theme's surface (lightness,
  chroma, colour-blind separation, contrast).
- Succeeded and failed are blue and red, not green and red, because green
  and red can touch in a column and colour-blind readers can't tell them
  apart.
- Text never takes a series colour.

The mock (`mocks/reports.ts`) makes up a year of requests from a fixed seed.

## Onboarding

**Administration → Onboarding** (`features/admin/OnboardingPage.tsx`,
[ADR-0026](adr/0026-onboarding.md), [ADR-0044](adr/0044-onboarding-plans.md))
has two tabs in the address (`?tab=`): **VMs to adopt** and **Plans**.
**VMs to adopt** lists the VMs rvav doesn't manage:
- **Filters:** search, folder, vCenter and power in the table's toolbar,
  with how many match; pages of 100 with **Show more**. A filtered empty
  list offers **Clear filters**.
- **Selection:** a `DataTable` selection: a checkbox per VM, and **Select
  all shown** (`allLabel`). A selection outlives the filters: unticking
  **Select all shown** keeps the selected VMs the filters hide, the count
  says how many are hidden, **Clear selection** empties it, and the side
  panel lists them all. At most 200 at once.
- **Adopting:** once VMs are selected, a bar above the table (**Selected
  VMs**, as Deployments' bulk actions) offers **Adopt N VMs…** and **Clear
  selection**; nothing waits disabled before that. Adopt opens a side
  panel asking for the project and **Where they go**
  (`features/admin/onboarding/target.tsx`, shared with plans): each into
  a deployment of its own, all into one new deployment (its name), or
  into an existing deployment (the project's ready ones). New deployments
  take an environment, owner (members who request there), catalog item
  (the project's VM items), lease and inputs (key and value rows; numbers,
  booleans and JSON lists are read as such). A component is free text: no
  API lists an item's components, and the server's error names them. The
  server's errors show by field, a VM's naming it.
- **Result:** a panel lists the deployments made, with links, and the
  addresses and records found.

**Plans** (`features/admin/onboarding/plans.tsx`) is a `DataTable` of the
saved plans (filter, where they go, last run, schedule), each linking to
its page (`/admin/onboarding/plans/$planId`, `PlanPage.tsx`), with a row
menu (Run now, Change, Delete). **New plan** opens a side panel: name,
**Which VMs** (with how many match now, debounced), **Where they go**,
and **Run every (hours)**. A plan's page has **Run now** as its primary
action, a ⋯ menu (Change plan, Delete plan), its settings, and a table
of its runs; a run opens in a side panel listing each VM's outcome and
why. Lists and pages poll every 1.5 seconds while a run goes on.
**Deployments** links to the plans with **Adopt existing VMs** for those
who hold `import.manage`.

The mock offers 60 VMs (`mocks/onboarding.ts`), with folders and tags;
EfficientIP knows half of them. Its plans run as the server's do, and a
run settles 0.7 seconds after it starts, on the next read.

## vRA import

**Administration → vRA import** (`features/admin/VraImportPage.tsx`,
[ADR-0027](adr/0027-vra-importer.md)) reads vRA, shows the plan and
imports it:
- **Steps:** read vRA, map the projects, import, move each project, each
  with a `StepMark`; the first not done is current (`aria-current="step"`).
- **Header:** the read's source and counts as `meta`; before a plan,
  **Read vRA** (or **Upload file…** when rvav has no vRA set) is the
  primary action, after it **Import…**.
- **The plan,** in tabs kept in the address (`?tab=`). Without one, it opens
  on the tab of the step it's at: projects while some aren't mapped, then
  catalog items or deployments while some are left to import, then
  cutover. That tab is chosen once, when the plan first loads, so mapping
  the last project doesn't move it away. The vRA projects (each mapped with
  a select; the placement strategy is the third column), cloud templates
  (with the templates and property groups they need below them), vRA
  deployments and the vRO triage are `DataTable`s; policies and cutover
  are sections named by their policy or project.
- **Side panels:** a row's notes, the import (catalog, deployments or
  both) and **Mark as moved…**, which a project not mapped to an rvav
  project doesn't offer yet: it says it moves once mapped. **Not moved…** asks in a
  `ConfirmDialog` that warns that deleting an adopted deployment in vRA
  deletes its VMs.

The mock's estate (`mocks/vra.ts`) is read after an earlier import: the
RHEL item exists, published and offered to Web Platform.

## Notifications

The notification centre ([ADR-0023](adr/0023-notifications.md)) is in
`features/notifications/`.

- **The bell in the top bar** shows the unread count, with the latest eight
  notifications. Opening one marks it read and goes to what it is about;
  **Mark all read** is there too.
- **The live stream's `notification` event** still shows a toast, and now
  also refreshes the centre's queries (`["me", "notifications"]`).
- **Approval links:** notifications link to `/approvals/<level>`, which
  redirects to the inbox with that level open (`/approvals?id=`). Links
  in emails and cards keep working.
- **`/notifications`** has two tabs:
  - **Notifications:** everything, or unread only, with load more and
    mark read.
  - **Preferences:** a grid of kinds × channels. In the app is always on.
    A channel that can't reach the person is disabled, with the reason
    shown above the grid. The language of what is sent, and **Send me a
    test** per channel.

  Kinds keep the defaults until changed. Only the changed kinds are sent,
  so the others follow later changes to the defaults.

## Performance budget

- Initial JS under 200 kB gzipped for the app shell; routes and heavy widgets
  (the CodeMirror YAML editor, charts) are split and lazy-loaded.
- Largest contentful paint under 1.5 s on a typical corporate network; route
  transitions under 100 ms when prefetched.
- Lists of 10,000+ rows are virtualised.
- Bundle size is checked in CI (`size-limit` or `rollup-plugin-visualizer` report).

## Accessibility

- WCAG 2.2 AA. Radix primitives provide focus management and ARIA.
- `eslint-plugin-jsx-a11y` in CI. `e2e/accessibility.spec.ts` runs in
  `pnpm e2e` ([accessibility.md](accessibility.md)):
  - axe on every page, in each of its tabs, in both themes;
  - landmarks and headings;
  - reflow at 320 px;
  - a request made with the keyboard only;
  - a dialog's focus handling;
  - a page someone may not open, and an address that matches no page;
  - the canvas: a composite draft with a step selected, and a composite
    request's run, failed and running (`e2e/one-canvas.spec.ts` designs,
    requests and follows one with the keyboard only).
- Full keyboard support, visible focus rings, a skip-to-content link.
  `e2e/focus-return.spec.ts` opens the phone drawer, the command palette, an
  approval's panel, a typed confirmation and a token's revoke with the
  keyboard only, and checks where the focus goes on open and on close.
- Colour is never the only signal: status always has an icon and text.
- **Text colours:** the `faint` text colour keeps 4.5:1 on every background
  it's used on, in both themes. Code is highlighted with the app's colours,
  not CodeMirror's.
- **A button that shows text:** its accessible name contains that text
  (WCAG 2.5.3). Put extra context in a `sr-only` prefix, not an
  `aria-label` that replaces the text.
- **Wide tables** scroll inside their own `overflow-x-auto` container, never
  the page.

## Auth

- OIDC authorization code flow with PKCE through `oidc-client-ts`; silent renew.
- Tokens are kept in memory (not in localStorage); the API client adds the bearer
  token and handles a 401 by renewing once, then redirecting to log in.
- The user's permissions, for all of rvav and in each project, are loaded
  once from `/me` and used for route guards and for hiding actions, through
  `can(me, permission, projectId?)` in `web/src/lib/can.ts`; the web app
  never checks a role name (ADR-0034). The server still enforces
  everything. A change of roles ends the live stream with `resync`, which
  loads `/me` again.

## Error handling

- The API returns RFC 9457 problem details; the client maps them to typed errors.
- Field errors go into the form; other errors go into a toast or the route's
  error boundary, always with the `request_id` for support.
- In mock mode, a test makes a source fail with
  `window.__rvavMocks.faults.failFieldValues(field, times)` (`mocks/faults.ts`).
  The client retries a 5xx twice, so `times: 3` fails one load.

## Testing strategy

| Level | Tool | What |
|---|---|---|
| Unit | Vitest | Utilities, query key factories, form rule evaluation, mappers |
| Component | Vitest + Testing Library + MSW | Widgets, DataTable, Timeline, form renderer behaviour |
| Visual and docs | Storybook (+ Chromatic or Playwright screenshots) | Every `common` component in light and dark themes and all states |
| End to end | Playwright against docker-compose (vcsim, mock AWX, mock EfficientIP) | Login → request VM → approve → timeline → day-2 → delete |
| Accessibility | axe and keyboard checks in Playwright (`e2e/accessibility.spec.ts`) | Every page and tab, both themes, reflow at 320 px |

## Developer experience

- `pnpm dev` runs Vite with a proxy to the Go API; MSW mode lets the UI run with
  no backend at all.
- `pnpm gen:api` regenerates the typed client from `api/openapi.yaml`. CI fails
  if the generated code is out of date.
- TanStack Router and Query devtools in development builds.
- Conventional commits; checks before commit (lint-staged: ESLint, Prettier,
  typecheck on changed files).
