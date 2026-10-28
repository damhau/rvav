# rvav-workflows

Helpers for custom Temporal workflows that rvav runs as catalog items, day-2
actions, subscription targets or composite steps
([ADR-0022](../../docs/adr/0022-custom-workflows-and-composites.md)). They speak
rvav's side of the contract, `rvav.v1`:

- `RvavWorkflow.start(context)` reads the context rvav starts the workflow with
  (the request, project, environment, requester, deployment, resources, inputs,
  earlier steps' outputs, a subscription's event);
- `report_step`, `step` and `append_log` show the workflow's own steps and logs
  in the request's timeline;
- `request_approval` opens an approval in rvav's inbox and waits for the
  `rvav.approval` signal.

The Go equivalent is `github.com/damhau/rvav/pkg/rvavflow`. An example with its
tests and worker is in [examples/decommission](../../examples/decommission).

Use it from this repository (a path dependency, as the example does); publishing
it to a package index is the organisation's choice.
