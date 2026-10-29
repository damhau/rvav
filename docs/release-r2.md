# R2 1.0: report for the sign-off

This report is for the product owner's decision on R2, slices S10 to S14.
R2 is rvav's 1.0, as the [implementation plan](implementation-plan.md)
defines it. It holds the 1.0 gate checklist with its evidence. The demo
script is [demo-r2.md](demo-r2.md).

## In short

R2 is built and accepted on the lab stack:
- extensibility (S10);
- notifications and request options (S11);
- advanced provisioning (S12);
- inventory and reporting (S13);
- production readiness (S14).

With R1, rvav covers 83% of what the organisation uses vRA for
(implementation plan, section 3).

Of the 1.0 gate, everything that can be shown without the organisation's
infrastructure and people is shown, with the evidence below. What remains
is of three kinds:
- **The lab:** rvav against the real vCenter, SOLIDserver, AWX, identity
  provider, mail server, Teams and Slack (the
  [Lab acceptance milestone](https://github.com/damhau/rvav/milestone/9),
  eight issues).
- **The organisation:**
  - the external penetration test;
  - an accessibility check by a person with assistive technology;
  - the pilot project's feedback.
- **Service levels on real systems:** they were measured on the lab
  stack, with simulated vCenter, AWX and SOLIDserver.

None of these needs code that is known today.

**Recommendation:** go for installing 1.0 in the pilot environment, with
the Helm chart, and running the Lab acceptance and the penetration test
there. No-go for production use until those, the accessibility check and
the pilot's feedback are done: these items are marked *open* in the
checklist below.

## What R2 delivers

| Slice | Built | Accepted |
|---|---|---|
| S10 Extensibility | Events from an outbox, subscriptions to signed webhooks, AWX or custom workflows, blocking checks that can stop a request or set properties ([ADR-0021](adr/0021-event-subscriptions.md)); custom Temporal workflows as items, day-2 actions, subscription targets or composite steps, with Go and Python helpers; composite items ([ADR-0022](adr/0022-custom-workflows-and-composites.md)) | On a real Temporal server and the compose stack ([milestone S10](https://github.com/damhau/rvav/milestone/3)); its review's follow-ups fixed or decided in S14 (#81) |
| S11 Notifications | The notification centre; email, Teams and Slack per person and kind, in English or French; approving from a Teams card or a Slack message; several VMs at once, scheduled requests, on behalf, drafts, request again ([ADR-0023](adr/0023-notifications.md)) | Against fake Teams and SMTP endpoints and Mailpit ([milestone S11](https://github.com/damhau/rvav/milestone/4)); the real services are #82 |
| S12 Advanced provisioning | Multi-VM blueprints with DRS anti-affinity, extra disks and adapters, tags; Windows domain join and cloud-init; content libraries and storage policies; several vCenters; the PostgreSQL cluster composite ([ADR-0024](adr/0024-multi-vm-blueprints.md)) | On vcsim and the compose stack ([milestone S12](https://github.com/damhau/rvav/milestone/5)); real vCenters and guests are #83 |
| S13 Inventory and reporting | The inventory from vCenter's change notifications; drift ([ADR-0025](adr/0025-drift.md)); onboarding existing VMs ([ADR-0026](adr/0026-onboarding.md)); reports with CSV export | On vcsim and the stack: a power-off seen in 0.2 s, 50 VMs adopted in one operation ([milestone S13](https://github.com/damhau/rvav/milestone/6)); at scale is #84 |
| S14 Production readiness | High availability, the Helm chart and a VM installation, backups and restores, upgrades with requests in flight, metrics, traces, dashboards and alerts, a security review with scanning and SBOMs, a load test, an accessibility audit, AWX contract tests in CI, user, admin and operator guides, the API reference, runbooks | On the compose stack, kind and CI ([milestone S14](https://github.com/damhau/rvav/milestone/7)); the evidence is in the checklist below |

## The 1.0 gate (implementation plan, section 8)

**Met** is shown here. **Open** waits for the infrastructure, the
organisation or the pilot.

### Service levels

| Criterion | Status | Evidence |
|---|---|---|
| API p95 under 300 ms | Met on the lab stack | Load test: 200 people and 500 requests an hour for an hour. Reads p95 13.4 ms over 220,542 calls, with none failed; every read route under 5 ms p95 at the server ([load-test.md](operations/load-test.md)). |
| Pages usable in under 1.5 s | Met on the lab stack | Each page loaded cold in a new browser, until its content shows: 160 to 230 ms; 540 to 760 ms with the CPU slowed 4× (catalog, form, requests, deployments, approvals, reports, admin). |
| Provisioning succeeds 98% of the time with healthy systems | Met on the lab stack | Load test: all 497 accepted requests succeeded (186 VMs, 156 deletes, 76 snapshots, 79 power actions). An alert fires under 98% ([observability.md](operations/observability.md)). |
| The same on the organisation's systems | Open: lab | [Lab acceptance](https://github.com/damhau/rvav/milestone/9) #76, #78, #80, #83. |

### Resilience

| Criterion | Status | Evidence |
|---|---|---|
| No single point of failure | Met | Two or more of each process; the sync elects a leader; console tickets in the database; Temporal in HA or Temporal Cloud; nginx retries another API replica ([high-availability.md](operations/high-availability.md)). On kind, two replicas of each, with disruption budgets. |
| Losing one API or worker node interrupts nothing | Met on the lab stack | `deploy/ha-test.sh`: replicas killed while requests run. 24 of 24 requests succeeded, 821 reads had no error, and the sync took over. On kind, 50 of 50 reads succeeded while an API pod was deleted (#58, #59). |
| Restore tested, RPO 15 minutes, RTO 1 hour | Met on the lab stack | WAL archived at least every minute, so RPO is at most a minute. `deploy/backup/restore-test.sh` restored the whole server to a moment, with requests in flight, in 14 s ([backups.md](operations/backups.md), #60). |

### Security

| Criterion | Status | Evidence |
|---|---|---|
| SSO only; dev sign-in impossible in production | Met | `dev` sign-in starts only with `RVAV_ALLOW_DEV_AUTH`. The Helm chart sets OIDC on every container, which neither its values nor its Secret can change (`make lint-chart` checks it). Only public-key token signatures are accepted ([security.md](operations/security.md)). |
| Penetration test with no open high findings | Open: organisation | The threat model and the review are in [security.md](operations/security.md). Scanning runs on every push and daily (govulncheck, `pnpm audit`, Trivy); each release has SBOMs. |
| Secrets never in logs or responses | Met | Stored secrets are sealed and write-only. Secret form inputs are sealed, kept out of plans and Temporal's history, and deleted when done (`TestSecretInputs`, `TestGuests`). A secret widget is secret everywhere (#63). |
| Full audit trail | Met | Every administrative change and approver edit is in the audit log, searchable and exported as CSV; requests keep who asked, decided and what each step did. |

### Operations

| Criterion | Status | Evidence |
|---|---|---|
| Dashboards and alerts | Met | Prometheus metrics from every process, a Grafana dashboard, and twelve alerts, also in the Helm chart; traces from the API through Temporal into each activity ([observability.md](operations/observability.md), #62). |
| Runbooks for the top 10 failure cases | Met | Eleven, one per alert ([runbooks.md](operations/runbooks.md)). |
| An upgrade with requests in flight | Met on the lab stack | `deploy/upgrade-test.sh`: from S13.7 to S14, with a request waiting for approval and a composite running an AWX job. It finished in 22 s, and both completed afterwards. Recorded histories replay on every build (`TestReplayHistories`, [upgrades.md](operations/upgrades.md), #61). |

### Quality

| Criterion | Status | Evidence |
|---|---|---|
| CI green | Met | CI, Security and AWX workflows green on `main`. |
| End-to-end tests for every slice | Met | An acceptance test per slice (`internal/e2e/acceptance_s*`). The end-to-end suite runs on a four-connection pool, which caught a deadlock (#64). The browser tests run in mock mode and against the stack. The AWX contract tests run on a fresh AWX 24.6.1 in CI (#66). |
| Accessibility audit passed | Met by the tools; open: a person | axe (WCAG 2.2 AA) on every page and tab in both themes, landmarks and headings, reflow at 400%, a request made with the keyboard only, dialogs; nine kinds of problem found and fixed ([accessibility.md](accessibility.md), #65). A person with screen readers, speech input and forced colours is still to check it. |

### People

| Criterion | Status | Evidence |
|---|---|---|
| User guide and admin guide published | Met | [User guide](guides/user.md), [administrator guide](guides/admin.md), [operations](operations/README.md); the API reference with each release, and `/api/v1/openapi.json` on every rvav (#67). |
| The pilot project's feedback addressed | Open: pilot | After the pilot. |

## How it was verified

- **Every commit:**
  - `make check`: spec lint, generated code, Go vet and unit tests, the
    end-to-end tests on PostgreSQL with vcsim and Temporal, the Ansible
    collection, the Python helpers, the web app's checks and 136 browser
    tests;
  - CI on every push, the Security workflow daily, and the AWX workflow
    on changes to the AWX client.
- **On the compose stack:**
  - the stack's browser tests, with Keycloak sign-in and with dev
    sign-in;
  - the smoke test;
  - the HA, restore, upgrade and load tests;
  - Prometheus, Grafana and Jaeger with the observability override.
- **On kind:** the Helm chart installed with two replicas each, rolled,
  and an API pod deleted under load. AWX 24.6.1 for the contract tests.
- **Independent reviews** at the end of each slice. For S14, two
  reviewers read the slice's changes:
  - **The Go backend:** the reviewer found an approver's secret edit that
    could be kept although the decision failed, a held event that could
    stay held, two sync processes active after a lost database
    connection, metric labels a caller could multiply, an unbounded wait
    for AWX's events, and composite steps keeping secret inputs in their
    deployments.
  - **Web, chart, scripts and docs:** the reviewer found a restore script
    that could pick the wrong base backup after deleting the data, a
    chart guard against dev sign-in that the Secret could get round, pods
    not restarted when the Secret changes, an API-down alert that could
    stay silent, and statements in the docs that went further than the
    configuration.

  All of these are fixed, with tests where they apply. Three minor ones
  are noted, not changed: a 404 from AWX that isn't the job's own would
  launch it again; prefetched names count toward the hundred DNS checks;
  and images and actions from GitHub are pinned by version (third-party
  actions by commit).

## What remains

- **[Lab acceptance](https://github.com/damhau/rvav/milestone/9):**
  - #76 the lab vCenter;
  - #77 the web console through a real vCenter and ESXi;
  - #78 a real SOLIDserver (with the EfficientIP contract test,
    `docs/integrations.md`);
  - #79 the corporate identity provider;
  - #80 the organisation's AWX;
  - #82 mail, Teams and Slack;
  - #83 advanced provisioning;
  - #84 inventory at scale.
- **The penetration test** and an accessibility check by a person.
- **The pilot:** its feedback.
- **After 1.0:** [P2](https://github.com/damhau/rvav/milestone/8), the vRA
  importer and cutover.

## Sign-off

| | Name | Date | Decision |
|---|---|---|---|
| Product owner | | | |
| Operations | | | |
| Security | | | |
