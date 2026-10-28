"""The custom worker: runs the decommission workflow and its activities.

    TEMPORAL_ADDRESS=temporal:7233 TEMPORAL_NAMESPACE=default \\
    TASK_QUEUE=platform-workflows uv run python -m decommission.worker

In production, connect with mTLS (TEMPORAL_TLS_CERT and TEMPORAL_TLS_KEY).
"""

from __future__ import annotations

import asyncio
import logging
import os

from temporalio.client import Client, TLSConfig
from temporalio.worker import Worker

from decommission import activities
from decommission.workflow import Decommission


async def main() -> None:
    logging.basicConfig(level=logging.INFO)
    tls: TLSConfig | bool = False
    if os.environ.get("TEMPORAL_TLS_CERT"):
        with open(os.environ["TEMPORAL_TLS_CERT"], "rb") as c, open(os.environ["TEMPORAL_TLS_KEY"], "rb") as k:
            tls = TLSConfig(client_cert=c.read(), client_private_key=k.read())
    client = await Client.connect(
        os.environ.get("TEMPORAL_ADDRESS", "localhost:7233"),
        namespace=os.environ.get("TEMPORAL_NAMESPACE", "default"),
        tls=tls,
    )
    worker = Worker(
        client,
        task_queue=os.environ.get("TASK_QUEUE", "platform-workflows"),
        workflows=[Decommission],
        activities=activities.ALL,
    )
    logging.info("decommission worker on %s", worker.task_queue)
    await worker.run()


if __name__ == "__main__":
    asyncio.run(main())
