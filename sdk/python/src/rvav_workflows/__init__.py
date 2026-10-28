"""Helpers for custom Temporal workflows run by rvav.

rvav starts a custom workflow as a child of a request's workflow with a
context (contract ``rvav.v1``, docs/adr/0022-custom-workflows-and-composites.md)
and serves three activities on its own task queue: ``rvav.ReportStep``,
``rvav.AppendLog`` and ``rvav.RequestApproval``. A decided approval comes
back as the ``rvav.approval`` signal.

Write the workflow as a subclass of :class:`RvavWorkflow`::

    @workflow.defn(name="decommission.v1")
    class Decommission(RvavWorkflow):
        @workflow.run
        async def run(self, context: dict) -> dict:
            rc = self.start(context)
            async with self.step("inventory", "List what it uses"):
                ...
            decision = await self.request_approval(Approval(title="Delete the data", group="rvav-infra-leads"))
            if not decision.approved:
                raise ApplicationError(decision.comment, non_retryable=True)
            ...

and register its type, which ends with its major version, in rvav.
"""

from __future__ import annotations

import contextlib
from collections.abc import AsyncIterator
from dataclasses import dataclass, field
from datetime import timedelta
from typing import Any

from temporalio import workflow
from temporalio.common import RetryPolicy

__all__ = [
    "CONTRACT",
    "SIGNAL",
    "Approval",
    "Context",
    "Decision",
    "RvavWorkflow",
]

#: The version of rvav's side these helpers speak.
CONTRACT = "rvav.v1"
#: The signal rvav sends when an approval is decided.
SIGNAL = "rvav.approval"

RUNNING, DONE, FAILED, SKIPPED = "running", "done", "failed", "skipped"


@dataclass
class Context:
    """What rvav starts a workflow with.

    Fields are only added within ``rvav.v1``; unknown ones are kept in
    :attr:`extra`, so a newer rvav doesn't break an older workflow.
    """

    contract: str = ""
    rvav_task_queue: str = ""
    request: dict[str, Any] | None = None
    item: dict[str, Any] | None = None
    project: dict[str, Any] | None = None
    environment: str = ""
    production: bool = False
    requester: dict[str, Any] | None = None
    owner: dict[str, Any] | None = None
    deployment: dict[str, Any] | None = None
    deployments: list[dict[str, Any]] = field(default_factory=list)
    resources: list[dict[str, Any]] = field(default_factory=list)
    inputs: dict[str, Any] = field(default_factory=dict)
    #: The outputs of a composite item's earlier steps.
    steps: dict[str, Any] = field(default_factory=dict)
    #: The event of a subscription (``rvav.event.v1``), else None.
    event: dict[str, Any] | None = None
    extra: dict[str, Any] = field(default_factory=dict)

    @classmethod
    def from_dict(cls, data: dict[str, Any]) -> Context:
        known = {f for f in cls.__dataclass_fields__ if f != "extra"}
        kwargs = {k: v for k, v in data.items() if k in known and v is not None}
        return cls(**kwargs, extra={k: v for k, v in data.items() if k not in known})


@dataclass
class Approval:
    """An approval to ask for, in rvav's inbox."""

    title: str
    reason: str = ""
    group: str = ""
    approvers: list[str] = field(default_factory=list)
    mode: str = "any"
    #: How long to wait for a decision: after it the decision has
    #: ``timed_out``, and the approval stays open in rvav. None: no limit.
    wait: timedelta | None = None


@dataclass
class Decision:
    """How an approval ended."""

    approval_id: str
    approved: bool = False
    timed_out: bool = False
    comment: str = ""


class RvavWorkflow:
    """Base class of a custom workflow: call :meth:`start` first in ``run``."""

    def __init__(self) -> None:
        self._rc: Context | None = None
        self._decisions: dict[str, dict[str, Any]] = {}

    @workflow.signal(name=SIGNAL)
    def _rvav_approval(self, decision: dict[str, Any]) -> None:
        self._decisions[decision.get("approval_id", "")] = decision

    def start(self, context: dict[str, Any]) -> Context:
        """Read the context rvav gave the workflow."""
        self._rc = Context.from_dict(context)
        return self._rc

    @property
    def context(self) -> Context:
        if self._rc is None:
            raise RuntimeError("call start(context) first")
        return self._rc

    async def _call(self, name: str, arg: dict[str, Any]) -> Any:
        return await workflow.execute_activity(
            name,
            arg,
            task_queue=self.context.rvav_task_queue,
            start_to_close_timeout=timedelta(seconds=30),
            retry_policy=RetryPolicy(maximum_interval=timedelta(minutes=1)),
        )

    async def report_step(self, key: str, title: str, status: str, detail: str = "") -> None:
        """Add or update a step of the workflow, shown under its step in the
        request's timeline. ``detail`` is why, when it failed."""
        await self._call("rvav.ReportStep", {"key": key, "title": title, "status": status, "detail": detail})

    async def append_log(self, step_key: str, *lines: str) -> None:
        """Add lines to a step's log: the workflow's own step when empty."""
        await self._call("rvav.AppendLog", {"step_key": step_key, "lines": list(lines)})

    @contextlib.asynccontextmanager
    async def step(self, key: str, title: str) -> AsyncIterator[None]:
        """Run a block as a step: running, then done, or failed with its error."""
        await self.report_step(key, title, RUNNING)
        try:
            yield
        except Exception as e:
            await self.report_step(key, title, FAILED, str(e))
            raise
        await self.report_step(key, title, DONE)

    async def request_approval(self, a: Approval) -> Decision:
        """Open an approval on the request and wait for its decision."""
        out = await self._call(
            "rvav.RequestApproval",
            {"title": a.title, "reason": a.reason, "group": a.group, "approvers": a.approvers, "mode": a.mode},
        )
        approval_id = out["approval_id"]
        decision = Decision(approval_id=approval_id)
        try:
            await workflow.wait_condition(
                lambda: approval_id in self._decisions,
                timeout=a.wait,
            )
        except TimeoutError:
            decision.timed_out = True
            return decision
        got = self._decisions[approval_id]
        decision.approved = got.get("outcome") == "approved"
        decision.comment = got.get("comment", "")
        return decision
