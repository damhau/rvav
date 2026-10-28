"""Decommission an application over several days.

1. Freeze it and tell its owners.
2. Ask for approval to delete its data (rvav's inbox).
3. Keep it for the retention period (days: the workflow sleeps, durably).
4. Archive the data and retire the CMDB entry.

Registered in rvav as ``decommission.v1``: a change that breaks running
workflows is ``decommission.v2``, registered beside it.
"""

from __future__ import annotations

from datetime import timedelta
from typing import Any

from temporalio import workflow
from temporalio.exceptions import ApplicationError

with workflow.unsafe.imports_passed_through():
    from rvav_workflows import Approval, RvavWorkflow

    from decommission import activities

TYPE = "decommission.v1"
#: Who approves deleting the data: set here, not on the form, so the
#: requester can't choose.
APPROVER_GROUP = "rvav-infra-leads"
ACT = {"start_to_close_timeout": timedelta(minutes=5)}

#: Its form in rvav: register this as the workflow's input schema.
INPUT_SCHEMA: dict[str, Any] = {
    "type": "object",
    "required": ["application"],
    "properties": {
        "application": {"type": "string", "minLength": 1},
        "retention_days": {"type": "integer", "minimum": 0, "default": 3},
        "decision_days": {"type": "integer", "minimum": 1, "default": 2},
    },
}


@workflow.defn(name=TYPE)
class Decommission(RvavWorkflow):
    @workflow.run
    async def run(self, context: dict[str, Any]) -> dict[str, Any]:
        rc = self.start(context)
        app = rc.inputs["application"]
        retention = int(rc.inputs.get("retention_days", 3))
        owners = [u["email"] for u in (rc.requester, rc.owner) if u and u.get("email")]

        async with self.step("freeze", f"Freeze {app}"):
            await self.append_log("freeze", await workflow.execute_activity(activities.freeze_application, app, **ACT))
            await self.append_log(
                "freeze", await workflow.execute_activity(activities.notify_owners, args=[app, owners], **ACT)
            )

        decision = await self.request_approval(
            Approval(
                title=f"Delete {app}'s data",
                reason=f"{app} is frozen; its data is deleted after {retention} days.",
                group=APPROVER_GROUP,
                wait=timedelta(days=int(rc.inputs.get("decision_days", 2))),
            )
        )
        if decision.timed_out:
            await self.report_step("retention", "Keep the data", "skipped", "No decision in time.")
            raise ApplicationError(f"No decision on deleting {app}'s data in time.", non_retryable=True)
        if not decision.approved:
            await self.report_step("retention", "Keep the data", "skipped", "Rejected.")
            raise ApplicationError(f"Deleting {app}'s data was rejected: {decision.comment}", non_retryable=True)

        async with self.step("retention", f"Keep the data {retention} days"):
            await workflow.sleep(timedelta(days=retention))

        async with self.step("archive", "Archive the data"):
            await self.append_log("archive", await workflow.execute_activity(activities.archive_data, app, **ACT))
        async with self.step("cmdb", "Retire the CMDB entry"):
            await self.append_log("cmdb", await workflow.execute_activity(activities.retire_cmdb_entry, app, **ACT))

        return {
            "application": app,
            "approved_by_comment": decision.comment,
            "retention_days": retention,
            "finished_at": workflow.now().isoformat(),
        }
