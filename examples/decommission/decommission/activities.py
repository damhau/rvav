"""What the decommission workflow does in other systems. In this example
they only log; a real worker calls the CMDB, the backup system and so on,
with credentials that stay in the worker, never in rvav."""

from __future__ import annotations

import logging

from temporalio import activity

log = logging.getLogger(__name__)


@activity.defn
async def freeze_application(app: str) -> str:
    log.info("freezing %s", app)
    return f"{app}: no more changes accepted"


@activity.defn
async def notify_owners(app: str, owners: list[str]) -> str:
    log.info("telling %s about %s", owners, app)
    return f"told {', '.join(owners) or 'nobody'}"


@activity.defn
async def archive_data(app: str) -> str:
    log.info("archiving %s", app)
    return f"{app}: archived to cold storage"


@activity.defn
async def retire_cmdb_entry(app: str) -> str:
    log.info("retiring %s in the CMDB", app)
    return f"{app}: retired in the CMDB"


ALL = [freeze_application, notify_owners, archive_data, retire_cmdb_entry]
