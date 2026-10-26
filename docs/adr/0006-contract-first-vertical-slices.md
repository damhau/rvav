# ADR-0006: Contract first, delivered in vertical slices

- Status: accepted
- Date: 2026-10-26

## Context
The UI direction was validated with a clickable mockup. Building every screen
before the backend exists would mean guessing data shapes (pagination, errors,
live events, form definitions) and reworking them later. Waiting for a
finished backend would delay real user feedback on the UI, which is the main
reason to replace vRA.

## Decision
- The OpenAPI spec (`api/openapi.yaml`) is written first for each slice and is
  the single source of truth. Go server code and TypeScript client types are
  generated from it.
- Work is delivered in vertical slices. Each slice adds the spec, the backend
  and the screens for one end-to-end flow. Slice 1: catalog, request form,
  submitting and following a VM request, deployments.
- The frontend develops against mock handlers that follow the same spec (MSW),
  so switching to the real backend is a configuration change.

## Consequences
- Backend and frontend can be built in parallel without drifting apart.
- Changing an API shape means changing the spec first, which is visible in review.
- The hand-written types and mock API in `web/src/lib/types.ts` and
  `web/src/mock/` are replaced by generated types and spec-based mocks.
