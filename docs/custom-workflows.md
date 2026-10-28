# Writing a custom workflow for rvav

Custom workflows are level 3 of rvav's extensibility
([ADR-0007](adr/0007-extensibility-model.md)): Temporal workflows a team
writes, runs on its own worker, and registers in rvav. rvav starts them
inside a request. Their steps, logs and approvals show in the request's
timeline and inbox. The contract is
[ADR-0022](adr/0022-custom-workflows-and-composites.md). This guide
covers how to build on it.

Reach for one when a process runs long, crosses systems, waits for
people or needs compensation. Most glue is simpler:

| Need | Use |
|---|---|
| Chain a VM, AWX jobs and other items | A composite item ([service-catalog.md](service-catalog.md)) |
| Call AD, a CMDB, DNS or backup tools | An AWX job template |
| React to requests, provisioning, day-2 actions, deletions, leases | A subscription: webhook or AWX template ([ADR-0021](adr/0021-event-subscriptions.md)) |
| Days of waiting, approvals, compensation, many systems | A custom workflow |

## 1. Write it

Write it in Go with `github.com/damhau/rvav/pkg/rvavflow`, or in Python
with `rvav-workflows` (`sdk/python`). Both decode the context and wrap
rvav's activities. Another language works too: the contract is plain
Temporal (one argument, three activities, one signal).

**Python** (from [examples/decommission](../examples/decommission)):

```python
from datetime import timedelta
from temporalio import workflow
from temporalio.exceptions import ApplicationError

with workflow.unsafe.imports_passed_through():
    from rvav_workflows import Approval, RvavWorkflow

@workflow.defn(name="decommission.v1")
class Decommission(RvavWorkflow):
    @workflow.run
    async def run(self, context: dict) -> dict:
        rc = self.start(context)                      # the rvav.v1 context
        app = rc.inputs["application"]
        async with self.step("freeze", f"Freeze {app}"):
            await self.append_log("freeze", "frozen")
        d = await self.request_approval(Approval(
            title=f"Delete {app}'s data", group="rvav-infra-leads", wait=timedelta(days=2)))
        if not d.approved:
            raise ApplicationError(f"Not approved: {d.comment}", non_retryable=True)
        async with self.step("retention", "Keep the data 30 days"):
            await workflow.sleep(timedelta(days=30))
        return {"application": app}
```

**Go:**

```go
func Decommission(ctx workflow.Context, rc rvavflow.Context) (map[string]any, error) {
	app, _ := rc.Inputs["application"].(string)
	if err := rvavflow.Step(ctx, rc, "freeze", "Freeze "+app, func() error {
		return workflow.ExecuteActivity(ctx, Freeze, app).Get(ctx, nil)
	}); err != nil {
		return nil, err
	}
	d, err := rvavflow.RequestApproval(ctx, rc, rvavflow.Approval{Title: "Delete " + app + "'s data",
		Group: "rvav-infra-leads", Wait: 48 * time.Hour})
	if err != nil || !d.Approved {
		return nil, temporal.NewNonRetryableApplicationError("not approved: "+d.Comment, "rejected", err)
	}
	return map[string]any{"application": app}, nil
}
// worker: w.RegisterWorkflowWithOptions(Decommission, workflow.RegisterOptions{Name: "decommission.v1"})
```

### The context

The workflow's only argument. Fields are only added within `rvav.v1`, so
decode it leniently. The helpers do: Python keeps unknown fields in
`Context.extra`.

| Field | What |
|---|---|
| `contract` | `rvav.v1` |
| `rvav_task_queue` | rvav's task queue: where the contract's activities run |
| `request` | `id`, `number`, `kind`, `action`, plus `resource_id` and `resource_name` for a day-2 action |
| `item`, `project`, `environment`, `production` | What was requested, where |
| `requester`, `owner` | `id`, `name`, `email` |
| `deployment`, `resources` | For day-2 actions, and for composite steps after a VM: name, FQDN, address, moref |
| `inputs` | The form, validated against the registered input schema |
| `steps` | A composite's earlier steps' outputs |
| `event` | A subscription's event (`rvav.event.v1`); null otherwise |

### What rvav does for it

| Helper (Go / Python) | Activity on `rvav_task_queue` | Shows |
|---|---|---|
| `ReportStep` / `report_step` | `rvav.ReportStep {key, title, status, detail}` | A step under the workflow's step: `running`, `done`, `failed` (detail says why), `skipped` |
| `Step` / `async with step` | the same, twice | Running, then done, or failed with the error |
| `AppendLog` / `append_log` | `rvav.AppendLog {step_key, lines}` | Lines in a step's log; no `step_key` means the workflow's own step |
| `RequestApproval` / `request_approval` | `rvav.RequestApproval {title, reason, group, approvers, mode}`, then waits for the signal `rvav.approval {approval_id, outcome, comment}` | An approval in rvav's inbox for the group or the people named (`mode` `any` or `all`) |

- rvav serves these only to workflows it started: it finds the request
  from the caller's workflow ID (`<request workflow>/<step>`).
- An approval has no deadline in rvav. Give the helper a `Wait` and decide
  what a timeout means: the example fails after two days without a
  decision. The approval stays open in the inbox until decided or until
  the run ends; rvav then cancels it.
- Approvers can't change the request's inputs in these approvals: the
  workflow already has them.
- Who approves is the workflow's choice, not the requester's: don't take
  the group from the form (the example sets it in code).
- The Go helper waits for one approval at a time. To wait for several at
  once, receive `rvav.approval` yourself; the Python helper keeps every
  decision.
- These approvals are not policy levels, so the request's approval chain
  ignores them.

### Result and errors

- The workflow's result, any JSON object, is kept on its step. It also
  becomes `steps.<key>` for later steps of a composite.
- As a blocking subscription's target, the result is read as
  `{outcome: continue|stop, reason, properties}`.
- An error fails the step and the request, with the error's message. Use
  non-retryable application errors for decisions ("rejected") and let
  Temporal retry the rest.
- Cancelling the request cancels the workflow. Clean up in a disconnected
  context if your systems need it.

## 2. Test it

Test on Temporal's test environments. Mock the contract's activities by
name, signal the decision, and skip time:

- **Python:** `WorkflowEnvironment.start_time_skipping()`, a worker for
  your workflow, and activities named `rvav.ReportStep`, `rvav.AppendLog`
  and `rvav.RequestApproval` on the context's `rvav_task_queue`.
  `examples/decommission/tests` covers approved after three days,
  rejected, and nobody deciding.
- **Go:** `testsuite.WorkflowTestSuite` with `RegisterActivityWithOptions`
  for the three names and `RegisterDelayedCallback` to signal
  `rvav.approval`. See `pkg/rvavflow/rvavflow_test.go`.

`make test-python` runs the SDK's and the example's tests. CI runs them
too.

## 3. Run its worker

- Connect to rvav's Temporal namespace and poll the task queue you will
  register (the example: `TASK_QUEUE=platform-workflows`).
- In production, authenticate with mTLS (`TEMPORAL_TLS_CERT`,
  `TEMPORAL_TLS_KEY` in the example).
- Credentials for your own systems live in your worker, never in rvav.
- `examples/decommission/Dockerfile` builds a worker image from the
  repository's root.

## 4. Register it

Administrators register it under Administration → Custom workflows, or
with `POST /api/v1/admin/workflows`:

- key, title, workflow type ending with its major version
  (`decommission.v1`), task queue;
- input schema (JSON Schema), timeout (default 30 days), and the projects
  that may use it (none: all);
- usages:
  - **Catalog item:** an item of type *Custom workflow* whose blueprint is
    `{kind: workflow, workflow: <key>}`.
  - **Day-2 action:** `wf.<key>` on VMs or deployments, with a name, a CEL
    condition and *offered*. Its form is generated from the input schema
    (`title`, `description`, `default`, `enum`). The day-2 policies decide
    who runs it.
  - **Subscription target:** started with the event in `event`, found again
    on retries.
  - **Composite step:** `workflow: <key>` in a composite's steps, with
    `with` bindings and an optional `undo`.

Auditors read registrations, with their runs not ended and what uses
them. A registration in use, or with runs, can only be disabled.

## 5. Change it

- **Breaking changes** (anything replay would trip on, or new inputs): a
  new type, `decommission.v2`, registered beside v1. Point new uses at v2,
  keep v1's worker until the registration shows no run left, then disable
  v1.
- **Small changes** stay in the same type behind Temporal's versioning
  (`workflow.GetVersion`, `workflow.patched`).
- rvav's side changes only as ADR-0022 allows: optional fields within
  `rvav.v1`, new activity names for `rvav.v2`.

## Checklist

- [ ] Type ends with `.vN`; the worker registers it under that name.
- [ ] Decodes the context leniently and reads `inputs` it registered.
- [ ] Reports steps people can follow; logs what operators need.
- [ ] Approvals have a `Wait`, and a timeout has a meaning.
- [ ] Decisions fail with non-retryable errors; transient failures retry.
- [ ] Tests with time skipped for every branch.
- [ ] Worker on mTLS, in rvav's namespace, with its own credentials.
