"""The decommission workflow over several days, time skipped, with rvav's
activities stubbed: what it reports, asks and returns."""

from __future__ import annotations

import uuid
from typing import Any

import pytest
from temporalio import activity
from temporalio.client import WorkflowFailureError
from temporalio.testing import WorkflowEnvironment
from temporalio.worker import Worker

from decommission import activities
from decommission.workflow import TYPE, Decommission

steps: list[tuple[str, str]] = []
approvals: list[dict[str, Any]] = []


@activity.defn(name="rvav.ReportStep")
async def report_step(arg: dict[str, Any]) -> None:
    steps.append((arg["key"], arg["status"]))


@activity.defn(name="rvav.AppendLog")
async def append_log(arg: dict[str, Any]) -> None:
    pass


@activity.defn(name="rvav.RequestApproval")
async def request_approval(arg: dict[str, Any]) -> dict[str, str]:
    approvals.append(arg)
    return {"approval_id": "apr_billing"}


CONTEXT = {
    "contract": "rvav.v1",
    "rvav_task_queue": "rvav-test",
    "requester": {"id": "usr_tnguyen", "name": "Thomas Nguyen", "email": "thomas.nguyen@corp.example"},
    "inputs": {"application": "billing", "retention_days": 3},
}


async def run(env: WorkflowEnvironment, decision: dict[str, Any] | None) -> dict[str, Any]:
    steps.clear()
    approvals.clear()
    async with (
        Worker(env.client, task_queue="rvav-test", activities=[report_step, append_log, request_approval]),
        Worker(env.client, task_queue="platform-workflows", workflows=[Decommission], activities=activities.ALL),
    ):
        handle = await env.client.start_workflow(
            TYPE, CONTEXT, id=f"decommission-{uuid.uuid4()}", task_queue="platform-workflows"
        )
        if decision:
            await handle.signal("rvav.approval", decision)
        start = await env.get_current_time()
        result = await handle.result()
        result["took"] = (await env.get_current_time()) - start
        return result


@pytest.mark.asyncio
async def test_approved_it_keeps_the_data_three_days_then_archives() -> None:
    async with await WorkflowEnvironment.start_time_skipping() as env:
        result = await run(env, {"approval_id": "apr_billing", "outcome": "approved", "comment": "Go"})
    assert result["application"] == "billing" and result["approved_by_comment"] == "Go"
    assert result["took"].days >= 3
    assert approvals[0]["group"] == "rvav-infra-leads" and "3 days" in approvals[0]["reason"]
    assert steps == [
        ("freeze", "running"), ("freeze", "done"),
        ("retention", "running"), ("retention", "done"),
        ("archive", "running"), ("archive", "done"),
        ("cmdb", "running"), ("cmdb", "done"),
    ]  # fmt: skip


@pytest.mark.asyncio
async def test_rejected_it_fails_with_why() -> None:
    async with await WorkflowEnvironment.start_time_skipping() as env:
        with pytest.raises(WorkflowFailureError) as e:
            await run(env, {"approval_id": "apr_billing", "outcome": "rejected", "comment": "Payroll uses it"})
    assert "Payroll uses it" in str(e.value.cause)
    assert ("retention", "skipped") in steps and ("archive", "running") not in steps


@pytest.mark.asyncio
async def test_nobody_decides_it_gives_up_after_two_days() -> None:
    async with await WorkflowEnvironment.start_time_skipping() as env:
        with pytest.raises(WorkflowFailureError) as e:
            await run(env, None)
    assert "No decision" in str(e.value.cause)
