# Example: decommission an application

A custom Temporal workflow for rvav ([ADR-0022](../../docs/adr/0022-custom-workflows-and-composites.md)),
written in Python with the [`rvav-workflows`](../../sdk/python) helpers. It runs
over several days with a human step:

1. **Freeze** the application and tell its owners.
2. **Ask** for approval to delete its data, in rvav's inbox (the Infrastructure
   leads by default); nobody deciding within two days fails it.
3. **Keep** the data for the retention period (3 days by default): the workflow
   sleeps, durably, in Temporal.
4. **Archive** the data and **retire** the CMDB entry.

Each step shows in the request's timeline in rvav, with its log. The
activities in `decommission/activities.py` only log: a real worker calls the
CMDB and the backup system there, with credentials that stay in the worker.

## Tests

```sh
uv run pytest
```

They run the workflow on Temporal's time-skipping test server, with rvav's
activities stubbed: three days pass in a second. Approved, rejected, and
nobody deciding.

## Run the worker

```sh
TEMPORAL_ADDRESS=localhost:7233 TEMPORAL_NAMESPACE=default \
TASK_QUEUE=platform-workflows uv run python -m decommission.worker
```

In a container, from the repository's root (the helpers are a path dependency):

```sh
docker build -f examples/decommission/Dockerfile -t decommission-worker .
docker run --network rvav_default -e TEMPORAL_ADDRESS=temporal:7233 decommission-worker
```

In production the worker authenticates to Temporal with mTLS
(`TEMPORAL_TLS_CERT`, `TEMPORAL_TLS_KEY`) and runs in rvav's namespace.

## Register it in rvav

As an administrator (Administration → Custom workflows, or the API):

```sh
curl -X POST "$RVAV/api/v1/admin/workflows" -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d @- <<'JSON'
{
  "key": "decommission",
  "title": "Decommission an application",
  "workflow_type": "decommission.v1",
  "task_queue": "platform-workflows",
  "input_schema": {"type": "object", "required": ["application"],
    "properties": {"application": {"type": "string", "minLength": 1},
      "retention_days": {"type": "integer", "minimum": 0, "default": 3}}},
  "usages": ["catalog_item"]
}
JSON
```

Then make a catalog item of type **Custom workflow** whose blueprint is
`{kind: workflow, workflow: decommission}`, with a form for the inputs. To
offer it on deployments instead, add `"day2_action"` to its usages with a
`day2` section (name, what it runs on, a condition): it becomes the action
`wf.decommission`.

## Changing it

The type ends with its major version. A change that would break workflows
already running (Temporal replays their history against the new code) or
that changes the inputs is **`decommission.v2`**: register it beside v1, keep
v1's worker running until rvav shows no v1 run left, then disable v1. Smaller
changes stay in v1 with `workflow.patched("…")`.
