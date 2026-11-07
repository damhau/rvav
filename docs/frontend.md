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
| Tables | TanStack Table plus TanStack Virtual |
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
| i18n | Ready from day one (`i18next`), English first |
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
│  │     │  └─ $itemId.tsx     # an item: versions, designer, YAML, details, entitlements
│  │     └─ ...
│  ├─ features/                # domain modules (UI + hooks + queries)
│  │  ├─ catalog/
│  │  ├─ deployments/
│  │  ├─ requests/             # timeline components, SSE subscription
│  │  ├─ approvals/
│  │  ├─ day2/                 # action registry, dialogs
│  │  └─ admin/catalog/        # catalog administration; designer/ is the form designer
│  ├─ components/
│  │  ├─ ui/                   # shadcn/ui primitives (generated, then owned)
│  │  ├─ common/               # app-level building blocks: DataTable, PageHeader,
│  │  │                        # EmptyState, StatusBadge, CopyButton, Timeline…
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
- `components/ui` stays close to shadcn upstream; app-specific variants go in
  `components/common`.

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
- **Loaders call `queryClient.ensureQueryData`**, so data is there when the route
  renders, and stays cached for navigation back.
- **Intent preloading** (`defaultPreload: "intent"`): hovering a link prefetches
  its route code and data.
- Route guards check auth and role in `beforeLoad`; admin routes are hidden and
  protected.
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

### Tokens
- Colours, radius, spacing, shadows and motion as CSS variables in `globals.css`
  (shadcn convention: `--background`, `--foreground`, `--primary`, `--muted`, …),
  plus semantic status tokens: `--status-success`, `--status-running`,
  `--status-warning`, `--status-danger`, `--status-neutral`.
- Light and dark themes, plus "system". Theme switching has no flash: the class
  is set before React hydrates.
- One typeface (Inter or Geist) with tabular numbers in tables; a monospaced font
  for IDs, IPs and logs.
- Density setting: comfortable or compact, for power users with big tables.

### Building blocks (`components/common`)
`AppShell`, `PageHeader` (title, breadcrumbs, primary actions), `DataTable`
(sorting, filtering, column visibility, row selection, bulk action bar, saved
views, virtualisation), `EmptyState` (illustration, explanation, call to action),
`StatusBadge` (one mapping from domain status to colour and icon), `Timeline`
(nested steps, durations, expandable logs), `ResourceCard`, `PowerStateDot`,
`CopyButton`, `RelativeTime` (with a full date in the tooltip), `ConfirmDialog`
(typed confirmation for destructive actions), `KeyValueList`, `CodeBlock`,
`LogViewer` (virtualised, ANSI colours, search, follow mode).

### Interaction rules
- Every list has: search, filters in the URL, an empty state, a loading
  skeleton, and an error state.
- Destructive actions need a confirmation; "delete prod" needs the name typed.
- Every async button shows a pending state and is disabled to prevent double
  submits.
- Toasts for results, inline messages for validation, never `alert()`.
- Motion is short (150–200 ms), purposeful, and disabled with `prefers-reduced-motion`.
- Responsive down to tablet width; the approval flows also work on a phone.

## Command palette (⌘K)

- Groups: Navigate, Catalog items (request directly), Deployments and VMs (search
  by name, IP or FQDN), Actions on the current page, Admin.
- Server-backed search, debounced, with results cached in TanStack Query.
- Context-aware: on a deployment page, the day-2 actions for it come first.
- Shortcuts are shown next to commands; `?` opens the shortcut sheet.

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
- **Rules** (visible, required, read-only, computed value) are CEL expressions
  evaluated by the server: the request preview returns each field's results
  (`fields`) and the renderer shows them (`fieldStates`). The browser doesn't
  evaluate CEL; the mock backend has a small evaluator of its own
  (`mocks/cel.ts`) so the UI runs without a server.
- Validation: ajv on the client for instant feedback; the server is the final
  authority, and its errors map back onto the fields.
- Layout: sections, columns, tabs and wizard steps; a review step shows the
  summary panel (name preview, quota, approval path).
- Draft auto-save (debounced) for long forms.

## Catalog administration and the form designer

Catalog admins and administrators build items under **Catalog items**
([ADR-0019](adr/0019-catalog-administration.md)):

- The list page: items with filters, **New item** (it starts as a draft
  1.0) and **Import YAML**; categories; stored secrets for REST value
  sources and calls (values are write-only); the templates imported from AWX.
- An item's page, in tabs: **Versions** (publish a draft, roll back to a
  deprecated version, withdraw the published one, discard, export as YAML,
  compare two versions), **Form designer**, **Definition** (the version as
  YAML), **Details** (icon from rvav's own set) and **Entitlements**.
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
  Schema, UI schema and initial values in step.
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
are in `features/admin/governance/`. The sidebar shows each person the
ones they may use; auditors see them read only.

- **Setup** (`features/admin/SetupPage.tsx`, ADR-0030): the setup
  checklist, from `GET /admin/setup`, read again each time it opens. Each
  step links to its page (an integration's tab through `?tab=`). While it
  is open, an administrator lands on it from the Overview once per page
  load (`SetupLanding` in `AppShell.tsx`), and the Overview offers the way
  back. Administration pages work without a project, so an administrator
  in none can set rvav up.
- **System** (`features/admin/SystemPage.tsx`): `GET /admin/system`,
  read every 10 seconds; read only, each saved setting linking to its
  tab.
- **Projects**: the projects (search, archived ones on request, **New
  project** with its environments and first project admins), the
  environments (production or not, snapshots kept, removable while unused)
  and the OIDC group mappings (a global role, or a role in one project).
  Project admins see their own projects only.
- A **project's page**, in tabs: **Members** (add someone found by name or
  email, change a direct role, remove; roles from a group show the group),
  **Placement** (the rules for an environment, by cluster and datastore
  name patterns or vCenter tags, with their storage policy and vCenter
  ([ADR-0024](adr/0024-multi-vm-blueprints.md)), and a preview of the clusters,
  datastores, networks with their subnets, and folder a request would get,
  with what would stop it), **Quotas** (limits for the project or one
  environment, refused or sent to approval when passed, saved as a whole)
  and **Details** (archive, or put back in use).
- **Policies**, in tabs: approval and day-2 policies (moved up and down
  to order them; a condition that isn't valid CEL is refused when saved,
  under its field), lease policies (an environment's, and projects'
  overrides), naming policies (template, counter, where offered, and a
  preview of the next names with the ones skipped because they are taken)
  and **What applies here**, which says which policy decides the approval,
  day-2 action, lease and naming for a project and environment, with the
  quota lines.
- **Templates and networks**: VM templates (a vCenter template, or an
  item of a content library) and sizes, with where each is offered
  (projects and environments), and the network mappings, from EfficientIP
  or by hand.
- **Integrations**: the identity provider, the connections, vRA, the
  notification channels, the allowed hosts and the CA bundle (Trusted
  certificates), one tab each
  (`features/admin/IntegrationSettings.tsx`), the console's panel in
  vCenter's tab and the inventory token's in AWX's
  (`features/admin/InventoryToken.tsx`: made, put in AWX with rvav's
  address as AWX reaches it, the one before revoked); the allowed hosts
  have no secret. A field an environment
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
  and sync intervals apply on next use. The vCenter tab
  lists every vCenter rvav manages below its connection
  (`features/admin/VCenters.tsx`): the others are added, tested, changed
  and removed there, with what the sync last read from each. The
  EfficientIP reconcile report stays below.

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
for what those roles see). `rvav.mock.fresh` set to `1` makes it a new
install: no environments, projects, items or integration settings, and
the setup checklist open (`e2e/setup.spec.ts` follows it).

## Extensibility

Subscriptions and custom workflows ([ADR-0021](adr/0021-event-subscriptions.md),
[ADR-0022](adr/0022-custom-workflows-and-composites.md)) are in
`features/admin/extensibility/`. Administrators change them; auditors read
them.

- **Subscriptions**: each one's topic, condition, target and last 30 days.
  The dialog shows the topic's description and sample event (what the
  condition sees). Blocking is only offered on `.pre` topics, and then
  what happens when the target fails. **Test** sends the sample and opens
  the run. A subscription's page (`/admin/subscriptions/$subscriptionId`)
  lists its runs by status. A run (`runs.tsx`) shows its answer, the
  properties it set, each attempt with what came back, the event sent,
  and **Try again** when it failed.
- **Custom workflows**: registered and changed in one dialog (type, task
  queue, timeout in days, input schema as JSON, usages, projects, and the
  day-2 action when that usage is ticked). The list says what uses each
  one and how many runs haven't ended; removing one in use shows the
  server's refusal.
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
  see Multi-VM items), and a composite request's page (below).
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
  the mocks' run (`simulator.ts`), a branch chooses when planned from the form, as its steps have no outputs, so a
  spec's case reads the inputs; a message reaches the mocks' me only if it tells the requester.
- **Browser tests**, on the mock API: `e2e/topology.spec.ts` (a topology
  beside its YAML and on the request form), `composite-designer.spec.ts`
  (the designer, to a request), `composite-rest.spec.ts` (a REST call and
  its undo from the keyboard, masked on the form and the run), `composite-ask.spec.ts`
  (a wait from the keyboard, masked on the form, answered, rejected, retried, cancelled,
  and someone else's), `composite-wait.spec.ts` (a wait for a time and a poll from the keyboard,
  masked on the form, waiting, cancelled, run to their end, at a deadline, retried), `composite-branch.spec.ts` (a branch
  and a message from the keyboard, masked on the form, its side followed, otherwise, nothing held, a failed message and a
  Retry, what's refused on a side), `request-run.spec.ts` (a failed run
  retried and followed), and D1's acceptance, `one-canvas.spec.ts`, the
  same with the keyboard only; `accessibility.spec.ts` scans the
  designer and a run. The mocks can't approve as someone else, so a
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
[ADR-0026](adr/0026-onboarding.md)) lists the VMs rvav doesn't manage:
- **Filters:** search, vCenter and power, with how many match; pages of 100
  with **Show more**.
- **Selection:** a checkbox per VM, and one to select every VM shown. A
  selection outlives the filters: the count says how many selected VMs
  aren't shown, **Clear** empties it, and the dialog lists them. At most
  200 at once.
- **Adopting:** **Adopt N VMs…** asks for the project, environment, owner
  (members who aren't viewers), catalog item (the project's VM items),
  lease, and optionally one deployment for all with its name. The server's
  errors show by field, a VM's naming it.
- **Result:** a panel lists the deployments made, with links, and the
  addresses and records found.

The mock offers 60 VMs (`mocks/onboarding.ts`); EfficientIP knows half of
them.

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
  - the canvas: a composite draft with a step selected, and a composite
    request's run, failed and running (`e2e/one-canvas.spec.ts` designs,
    requests and follows one with the keyboard only).
- Full keyboard support, visible focus rings, a skip-to-content link.
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
- The user's roles and project memberships are loaded once and used for route
  guards and for hiding actions (the server still enforces everything).

## Error handling

- The API returns RFC 9457 problem details; the client maps them to typed errors.
- Field errors go into the form; other errors go into a toast or the route's
  error boundary, always with the `request_id` for support.

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
