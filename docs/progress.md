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
- [x] S2.4 CI (GitHub Actions, `.github/workflows/ci.yml`): Go vet, tests and end-to-end; web lint, typecheck, tests, build and Playwright; spec lint; generated-code check. Green on the first run. It runs on every push to `main`; "required on pull requests" does not apply while commits go straight to `main`. The compose stack job (images, `deploy/smoke.sh`, `pnpm e2e:stack`) was taken out of CI at the user's request; those checks run locally
- [x] S2.5 Real Temporal: the worker connects once Temporal is healthy (S2.1); retries of transient errors and giving up after four attempts are tested; `e2e-stack/request.spec.ts` submits in the browser and reaches Ready through the compose stack (27 s); workflows and their history are in the Temporal UI; `TestThroughTemporal` runs start, approval signal and cancel on a real Temporal dev server
- [x] S2.6 Dev auth mode refuses to start without `RVAV_ALLOW_DEV_AUTH=1` (checked where the authenticator is built, so `api` and `dev` only); the compose stack and `.env.example` set it
- [x] S2.7 OIDC sign-in end to end with Keycloak: PKCE sign-in, return to the page asked for, sign-out (`e2e-stack/sign-in.spec.ts` with `RVAV_STACK_AUTH=oidc`); the request test passes with real tokens too. Fixed on the way: the `offline_access` scope Keycloak refused (now `VITE_OIDC_SCOPE`, default without it), the API exiting when Keycloak isn't up yet (provider discovered on first use, `503` with `Retry-After` until then), nginx keeping a stale API address. `TestOIDCTokens` covers signature, issuer, audience, expiry and groups to roles. Keycloak has a health check
- [x] S2.8 ADRs for the decisions S2 needs: [ADR-0008](adr/0008-self-hosted-temporal.md) self-hosted Temporal, [ADR-0009](adr/0009-hosting.md) Kubernetes with Helm and Compose for development, [ADR-0010](adr/0010-identity-provider.md) any OIDC provider with Keycloak for development
- [x] S2.9 Slice review, docs and roadmap status. An independent review of `488788a..1d58379` found no high-severity issues; fixed from it: `/readyz` fails until the OIDC provider is discovered (a wrong issuer stops a rollout instead of answering 503 everywhere), discovery retried at most every 5 s, key-fetch failures answer 503 rather than 401, nginx takes its resolver from `/etc/resolv.conf` (needs the service's full name on Kubernetes), compose ports published on 127.0.0.1 with `RVAV_ALLOW_DEV_AUTH` overridable, CI no longer cancels runs on `main`, a portable UUID in `smoke.sh`, ADR wording, more OIDC test cases and a Temporal test race. Found during the stack check: dev authentication lost seeded users once they had signed in through Keycloak; fixed. Stack check at the end of the slice: dev mode (smoke and browser) and Keycloak mode from a cold start (browser), all passing
- [-] S2.x First run against the lab vCenter with a real template, customisation spec and network

## S3 Approvals

- [x] S3.1 Spec: approvals waiting for me, detail, approve or reject with a comment, approve with edited inputs (within field limits), delegate, approval history on a request. Design in [ADR-0011](adr/0011-approvals.md); handlers answer 501 until S3.4
- [x] S3.2 Policies: several levels, any or all approvers per level, CEL conditions, auto-approve rules (`auto_approve`, for example an infrastructure lead's own production VM), timeout with auto-approve, auto-reject or escalation; levels with the same approvers are merged across policies; migration `00002_approvals.sql`
- [x] S3.3 Workflow opens each level, waits for its `approval` signal or a Temporal timer, re-reads the level every 5 minutes in case a signal is lost, and loads the plan again after approval
- [x] S3.4 API, audit entries (`audit_events`, read in S9) and live events (`approval.updated` on the `approvals` topic, to each approver); group membership stored from tokens; approver edits re-evaluate the request as its requester
- [x] S3.5 UI: approvals inbox and detail (from the mockup: reasons and rules, facts, requester's reason, editable size, comment required to reject, due and escalation), approval panel on the request page, sidebar badge kept live by `approval.updated`; mock API with seeded levels; Vitest and Playwright. Delegating from the UI waits for a user-search API (S9); the API supports it
- [x] S3.6 `rvav approve` removed; `TestApprovals` runs the acceptance scenario on a real Temporal dev server (48 GB VM lowered to 32 GB and approved, two levels, mode all, delegation, escalation then rejection, auto-approval, rejection, cancel)
- [x] S3.7 Slice review, docs and roadmap status. An independent review of `c848276..fa1aeac` found one high-severity issue, fixed: a requester could delegate the approval of their own request. Also fixed from it: a recheck that grew workflow history (hourly now, with continue-as-new), merged levels that could weaken a policy, API tokens taking roles from stored groups, mode `all` with delegation and escalation, approver edits (under the lock, older versions, steps refreshed), a guarded finish after a cancel, permission checks before state, approval events to the requester, "decided" meaning voted on. Found by the stack check and fixed: admins and approvers with no project membership stuck on "Loading", approvers unable to read the request, no live updates for requests outside the current project. Stack check: all four browser tests pass with Keycloak, including Thomas asking for 48 GB and Marc lowering it to 32 GB

## S4 Day-2 actions

- [x] S4.1 Console spike and [ADR-0012](adr/0012-vm-console.md): a VMRC link (clone ticket) always, and a WebMKS session proxied by rvav when the organisation provides VMware's WMKS client; vcsim has no WebMKS tickets, so a real ESXi host must confirm it
- [x] S4.2 Spec: actions available on a deployment (filtered by policy and state), run an action (a request of kind `day2`), deployment history, console session and its WebSocket, retry and roll back. Design in [ADR-0013](adr/0013-day2-actions.md); handlers answer 501 until built
- [x] S4.3 Power: on, off, shut down guest, restart guest, reset. Day-2 plumbing: `internal/day2` catalogue, requests of kind `day2` run by the request workflow (one `RunOp` activity per step, VM refreshed after each), one request at a time per deployment, history, `action.run` audit entries. Until S4.7, members, project admins and the owner may run actions. vcsim VMs have no VMware Tools; the test simulates it
- [x] S4.4 Resize CPU and RAM (hot-add aware: a running VM without hot add is shut down, resized and powered on when the user allows it), add or extend disks (not with snapshots), add, remove or move NICs on networks the placement rules allow; the first adapter, which carries the managed address and DNS, stays. Quota on resize comes with S5. A retry of add disk or NIC after a lost vCenter response adds a second one
- [x] S4.5 Snapshots: create (with memory when running), revert, delete; retention per environment (seed: dev and tst 3 snapshots for 7 days, prd 2 for 3 days): the count at create, the age by the `Housekeeping` workflow on a 15-minute Temporal schedule, which starts `snapshot_delete` requests as the system user `rvav`. vcsim cannot remove the current snapshot at the root of the tree (a simulator panic); the test avoids it
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
| Go version | 1.27.3, pinned with `toolchain` in `go.mod` (CI and the image follow it) | – |
| Where Temporal runs | Self-hosted on PostgreSQL | ADR-0008 |
| Hosting | Kubernetes with Helm; Compose for development and the lab | ADR-0009 |
| Identity provider | Any OIDC provider; Keycloak for development and tests | ADR-0010 |
| Approvals | Levels merged across policies, votes in the database, the workflow signalled per level; group membership from tokens, stored per user | ADR-0011 |
| Workflow versioning | Not before the R1 pilot: development stacks are drained or reset when workflow code changes; from R1 on, `GetVersion` or worker versioning | ADR-0011 |
| VM console | VMRC link always; web console through an rvav WebSocket proxy, with the WMKS client provided by the organisation | ADR-0012 |
| Day-2 actions | Requests of kind `day2`, run by a `Day2` workflow; `day2_policies` allow, deny or require approval | ADR-0013 |
| Stack checks | `make check` per commit; the compose stack once per slice, not in CI | – |

## Needs real infrastructure

Items marked `[-]` above, plus anything found while building:

| Item | Needs |
|---|---|
| S2 acceptance on the lab vCenter | Lab vCenter, RHEL 9 template, customisation spec, network |
| Corporate sign-in | OIDC client in the corporate identity provider and its group names; `VITE_OIDC_SCOPE` with `offline_access` if it is Entra ID |
| Console through firewalls | Network path from browsers to the ESXi hosts, or to rvav |
