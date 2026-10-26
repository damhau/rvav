# rvav web

The rvav UI: React 19, TypeScript, Vite, TanStack Router and Query, Tailwind v4 and shadcn-style
components on Radix. See [docs/frontend.md](../docs/frontend.md) for the architecture.

## Run it

```sh
pnpm install
pnpm dev          # mock API in the browser (MSW), no backend needed
pnpm dev:real     # real API: proxies /api to the Go server on :8080, no sign-in (API in RVAV_AUTH_MODE=dev)
```

Mock mode signs you in as a development user (Léa Martin, Web Platform) and seeds example
projects, catalog items, deployments and requests. Submitting a request runs a simulated
provisioning workflow with live updates. Mock data lives in the browser tab and resets on reload.

## Scripts

| Script              | What it does                                                                   |
| ------------------- | ------------------------------------------------------------------------------ |
| `pnpm gen:api`      | Regenerate `src/lib/api/schema.d.ts` from `../api/openapi.yaml`                |
| `pnpm typecheck`    | Strict TypeScript check                                                        |
| `pnpm lint`         | ESLint (typescript-eslint, react-hooks, jsx-a11y, tanstack-query) and Prettier |
| `pnpm test`         | Vitest and Testing Library, against the mock API through `msw/node`            |
| `pnpm e2e`          | Playwright smoke tests and screenshots, against `vite` in mock mode            |
| `pnpm build`        | Production build (real API)                                                    |
| `pnpm build:mockup` | One self-contained HTML page with the mock API running in-process              |

## Configuration

Copy `.env.example` to `.env.local`. `VITE_API_MODE` picks `mock` or `real`. Real mode signs in with
`VITE_OIDC_AUTHORITY` and `VITE_OIDC_CLIENT_ID`, unless `VITE_AUTH_MODE=none` (or `dev`), which sends
no token and is meant for a local API started with `RVAV_AUTH_MODE=dev`. In mock mode, `VITE_MOCK_TICK_MS` sets how fast
provisioning steps advance.

## Docker image

`Dockerfile` builds the app for the real API and serves it with nginx on port 8080. nginx proxies `/api` to `RVAV_API_UPSTREAM` (default `http://api:8080`),
with buffering off for `/api/v1/events`, so the browser uses a single origin
and needs no CORS. Sign-in is fixed at build time with the `VITE_AUTH_MODE`,
`VITE_OIDC_AUTHORITY` and `VITE_OIDC_CLIENT_ID` build arguments. Docker
Compose builds it as the `web` service (see `deploy/README.md`).

## Layout

```
src/
  lib/api/        generated schema types, typed client (openapi-fetch), ApiError
  lib/live/       event stream over fetch (SSE), cache bridge, LiveProvider
  lib/query.ts    query key factories and query options
  lib/cel.ts      CEL subset for form rules
  forms/          request form renderer (JSON Schema + UI schema) and widgets
  mocks/          MSW handlers, seed data, provisioning simulator
  components/     ui/ primitives, common/ building blocks, shell/ (sidebar, top bar, ⌘K)
  features/       screens
  routes/         TanStack Router file routes (thin: params, search validation)
```
