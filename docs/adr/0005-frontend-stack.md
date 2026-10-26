# ADR-0005: Frontend stack

- Status: accepted
- Date: 2026-10-26

## Context
A modern UX is the main reason to replace vRA. The UI needs complex dynamic
forms, large live-updating tables, realtime timelines, and a consistent design
system.

## Decision
React + TypeScript + Vite, TanStack Router and TanStack Query, Tailwind CSS with
shadcn/ui. The API client is generated from the OpenAPI spec. See
[frontend architecture](../frontend.md).

## Consequences
- End-to-end type safety: typed routes, search params and API calls.
- TanStack Query is the single source of server state; SSE events update its
  cache, so the UI stays live without polling.
- shadcn/ui components live in our repo, which gives full control over look and
  behaviour, but upgrades from upstream are manual.
- The catalog form renderer is built in-house, which is the largest frontend
  investment but central to the product.
