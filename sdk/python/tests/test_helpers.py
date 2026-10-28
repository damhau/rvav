"""The helpers against stub rvav activities, on Temporal's time-skipping
test server."""

from __future__ import annotations

import uuid
from datetime import timedelta
from typing import Any

import pytest
from temporalio import activity, workflow
from temporalio.testing import WorkflowEnvironment
from temporalio.worker import Worker

with workflow.unsafe.imports_passed_through():
    from rvav_workflows import Approval, Context, RvavWorkflow

calls: list[tuple[str, Any]] = []


@activity.defn(name="rvav.ReportStep")
async def report_step(arg: dict[str, Any]) -> None:
    calls.append(("step", (arg["key"], arg["status"])))


@activity.defn(name="rvav.AppendLog")
async def append_log(arg: dict[str, Any]) -> None:
    calls.append(("log", (arg["step_key"], tuple(arg["lines"]))))


@activity.defn(name="rvav.RequestApproval")
async def request_approval(arg: dict[str, Any]) -> dict[str, str]:
    calls.append(("approval", arg["group"]))
    return {"approval_id": "apr_1"}


@workflow.defn(name="sample.v1")
class Sample(RvavWorkflow):
    @workflow.run
    async def run(self, context: dict[str, Any]) -> dict[str, Any]:
        rc = self.start(context)
        async with self.step("one", "Step one"):
            await self.append_log("one", "a", "b")
        d = await self.request_approval(Approval(title="Go on?", group="leads", wait=timedelta(days=2)))
        return {"approved": d.approved, "timed_out": d.timed_out, "comment": d.comment, "app": rc.inputs.get("app")}


def test_context_keeps_unknown_fields() -> None:
    rc = Context.from_dict({"contract": "rvav.v1", "environment": "dev", "inputs": {"a": 1}, "later": True})
    assert rc.environment == "dev" and rc.inputs == {"a": 1} and rc.extra == {"later": True}


async def run_sample(env: WorkflowEnvironment, signal: dict[str, Any] | None) -> dict[str, Any]:
    async with (
        Worker(env.client, task_queue="rvav-test", activities=[report_step, append_log, request_approval]),
        Worker(env.client, task_queue="custom", workflows=[Sample]),
    ):
        handle = await env.client.start_workflow(
            "sample.v1",
            {"contract": "rvav.v1", "rvav_task_queue": "rvav-test", "inputs": {"app": "billing"}},
            id=f"sample-{uuid.uuid4()}",
            task_queue="custom",
        )
        if signal:
            await handle.signal("rvav.approval", signal)
        return await handle.result()


@pytest.mark.asyncio
async def test_steps_logs_and_approval() -> None:
    calls.clear()
    async with await WorkflowEnvironment.start_time_skipping() as env:
        result = await run_sample(env, {"approval_id": "apr_1", "outcome": "rejected", "comment": "No"})
    assert result == {"approved": False, "timed_out": False, "comment": "No", "app": "billing"}
    assert calls == [
        ("step", ("one", "running")),
        ("log", ("one", ("a", "b"))),
        ("step", ("one", "done")),
        ("approval", "leads"),
    ]


@pytest.mark.asyncio
async def test_approval_wait_times_out() -> None:
    async with await WorkflowEnvironment.start_time_skipping() as env:
        result = await run_sample(env, None)
    assert result["timed_out"] is True and result["approved"] is False
