# R0 progress

Working file for building release R0 (slices S2 to S5 of the
[implementation plan](implementation-plan.md)). Each item ends with a commit
on `main`. A slice is done when every item is checked, the slice's
definition of done holds, and an independent review of the slice diff has
been addressed.

Status: `[ ]` to do, `[~]` in progress, `[x]` done, `[-]` needs real
infrastructure (see the last section).

## Gate for every item

- `make check` passes: spec lint, generated code current, `go vet`, Go tests,
  end-to-end tests, web typecheck, lint, unit tests, build and Playwright.
- CI is green on the pushed commit.

## How tests run here

Every sandboxed command has its own network, so services started by one
command can't be reached from the next. Tests therefore start what they need
inside their own process: vcsim and the Temporal test environment in Go,
`embedded-postgres` and the Temporal dev server for end-to-end tests, Vite
with MSW for Playwright. Docker Compose is used to prove the real stack
(images, nginx, Keycloak), and in CI.

## S2 Make it real

- [x] S2.1 Compose stack runs for real: project name `rvav`, configurable host ports, start order and health checks (Temporal namespace, `rvav healthcheck`, nginx); `deploy/smoke.sh` submits a request and it succeeds through real Temporal in about 10 s
- [x] S2.2 End-to-end tests start their own PostgreSQL (`internal/pgtest`, embedded-postgres, TCP only) when `RVAV_TEST_DATABASE_URL` is not set; `make test` runs `-short` and skips them. The Temporal dev server belongs to S2.5
- [x] S2.3 `make check` runs the whole gate locally (about 1 minute); `PNPM` can be overridden; Playwright no longer needs a global pnpm; screenshots go to `test-results/` unless `UPDATE_SCREENSHOTS=1`
- [x] S2.4 CI (GitHub Actions, `.github/workflows/ci.yml`): Go vet, tests and end-to-end; web lint, typecheck, tests, build and Playwright; spec lint; generated-code check; the compose stack built from the Dockerfiles with `deploy/smoke.sh`. Green on the first run. It runs on every push to `main`; "required on pull requests" does not apply while commits go straight to `main`
- [x] S2.5 Real Temporal: the worker connects once Temporal is healthy (S2.1); retries of transient errors and giving up after four attempts are tested; `e2e-stack/request.spec.ts` submits in the browser and reaches Ready through the compose stack (27 s, also in CI); workflows and their history are in the Temporal UI; `TestThroughTemporal` runs start, approval signal and cancel on a real Temporal dev server
- [ ] S2.6 Refuse to start in dev auth mode unless an explicit development flag is set
- [ ] S2.7 OIDC sign-in end to end with Keycloak: PKCE in the web app, token checks in the API, group-to-role mapping; Playwright test against the stack
- [ ] S2.8 ADRs for the decisions S2 needs: where Temporal runs, hosting, identity provider
- [ ] S2.9 Slice review, docs and roadmap status
- [-] S2.x First run against the lab vCenter with a real template, customisation spec and network

## S3 Approvals

- [ ] S3.1 Spec: approvals waiting for me, detail, approve or reject with a comment, approve with edited inputs (within field limits), delegate, approval history on a request
- [ ] S3.2 Policies: several levels, any or all approvers per level, CEL conditions, auto-approve rules, timeout with auto-approve, auto-reject or escalation
- [ ] S3.3 Workflow waits for the decision signal; timeouts use Temporal timers
- [ ] S3.4 API, audit entries and live events
- [ ] S3.5 UI: approvals inbox and detail, approval step in the timeline, sidebar badge; mocks and Playwright
- [ ] S3.6 Replace the `rvav approve` command; end-to-end test of the acceptance scenario
- [ ] S3.7 Slice review, docs and roadmap status

## S4 Day-2 actions

- [ ] S4.1 Console spike and ADR: WebMKS ticket, proxy in rvav, or VMRC link
- [ ] S4.2 Spec: actions available for a resource (filtered by policy), run an action with its form, action runs with their own timeline, deployment history, retry and roll back
- [ ] S4.3 Power: on, off, shut down guest, restart guest, reset
- [ ] S4.4 Resize CPU and RAM (hot-add aware), add or extend disks, add, remove or move NICs
- [ ] S4.5 Snapshots: create, revert, delete, retention policy
- [ ] S4.6 Rename (vCenter and DNS), mount ISO, change owner or project (quota check), delete deployment (VM, IP and DNS released)
- [ ] S4.7 Day-2 policies: roles, resources, conditions, approval for some actions
- [ ] S4.8 Retry a failed request from its failed step; roll back
- [ ] S4.9 Web console
- [ ] S4.10 UI: action menus and dialogs, bulk actions, History tab; mocks and Playwright
- [ ] S4.11 Slice review, docs and roadmap status
- [-] S4.x Console through the firewalls of a real vCenter and ESXi hosts

## S5 Leases and quotas

- [ ] S5.1 Lease policies: default and maximum per project and environment, extensions, extension through approval, expiry action and grace period
- [ ] S5.2 Temporal schedule every 15 minutes: reminders 7 days and 1 day before (in-app), expiry workflow per deployment
- [ ] S5.3 Quotas per project and environment, hard or soft, checked on submit, resize and change of project
- [ ] S5.4 UI: lease section, extend dialog, quota on the overview and in the form summary
- [ ] S5.5 End-to-end test of the acceptance scenario
- [ ] S5.6 Slice review, docs and roadmap status

## R0 close

- [ ] Demo script for the lab, roadmap status, report to the product owner

## Decisions taken

| Decision | Choice | Where |
|---|---|---|
| Git workflow | Commits go straight to `main`; CI runs on every push | – |

## Needs real infrastructure

Items marked `[-]` above, plus anything found while building:

| Item | Needs |
|---|---|
| S2 acceptance on the lab vCenter | Lab vCenter, RHEL 9 template, customisation spec, network |
| Corporate sign-in | OIDC client in the corporate identity provider and its group names |
| Console through firewalls | Network path from browsers to the ESXi hosts, or to rvav |
