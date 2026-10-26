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
│  │  └─ admin/...
│  ├─ features/                # domain modules (UI + hooks + queries)
│  │  ├─ catalog/
│  │  ├─ deployments/
│  │  ├─ requests/             # timeline components, SSE subscription
│  │  ├─ approvals/
│  │  ├─ day2/                 # action registry, dialogs
│  │  └─ admin/form-designer/
│  ├─ components/
│  │  ├─ ui/                   # shadcn/ui primitives (generated, then owned)
│  │  └─ common/               # app-level building blocks: DataTable, PageHeader,
│  │                           # EmptyState, StatusBadge, CopyButton, Timeline…
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
is shown subtly in the top bar.

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
  evaluated by the server and returned with the form state. Optionally a WASM
  CEL evaluator runs in the browser for instant feedback.
- Validation: ajv on the client for instant feedback; the server is the final
  authority, and its errors map back onto the fields.
- Layout: sections, columns, tabs and wizard steps; a review step shows the
  summary panel (name preview, quota, cost, approval path).
- Draft auto-save (debounced) for long forms.

## Performance budget

- Initial JS under 200 kB gzipped for the app shell; routes and heavy widgets
  (Monaco, charts, the form designer) are split and lazy-loaded.
- Largest contentful paint under 1.5 s on a typical corporate network; route
  transitions under 100 ms when prefetched.
- Lists of 10,000+ rows are virtualised.
- Bundle size is checked in CI (`size-limit` or `rollup-plugin-visualizer` report).

## Accessibility

- WCAG 2.2 AA. Radix primitives provide focus management and ARIA.
- `eslint-plugin-jsx-a11y` in CI, axe checks in Playwright tests.
- Full keyboard support, visible focus rings, a skip-to-content link.
- Colour is never the only signal: status always has an icon and text.

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
| Accessibility | axe in Playwright and Storybook | Key screens |

## Developer experience

- `pnpm dev` runs Vite with a proxy to the Go API; MSW mode lets the UI run with
  no backend at all.
- `pnpm gen:api` regenerates the typed client from `api/openapi.yaml`. CI fails
  if the generated code is out of date.
- TanStack Router and Query devtools in development builds.
- Conventional commits; checks before commit (lint-staged: ESLint, Prettier,
  typecheck on changed files).
