import { describe, expect, it } from "vitest";
import type { ApprovalPage, ServiceRequest, Step } from "@/lib/api/types";
import { bus } from "./events";

// The mocks' run of a composite (ADR-0033): a branch chooses when it runs, from
// a person's answer as the server's does, and a message reads it when sent.

const api = async <T>(method: string, path: string, body?: unknown): Promise<T> => {
  const res = await fetch(`http://localhost/api/v1${path}`, {
    method,
    headers: {
      Authorization: "Bearer test",
      "Content-Type": "application/json",
      "Idempotency-Key": crypto.randomUUID(),
    },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  if (!res.ok) throw new Error(`${method} ${path}: ${res.status} ${await res.text()}`);
  return (res.status === 204 ? undefined : await res.json()) as T;
};

const until = async <T>(what: string, read: () => Promise<T | undefined>): Promise<T> => {
  for (let i = 0; i < 300; i++) {
    const got = await read();
    if (got !== undefined) return got;
    await new Promise((r) => setTimeout(r, 10));
  }
  throw new Error(what);
};

const blueprint = {
  kind: "composite",
  steps: [
    {
      key: "detail",
      title: "Ask",
      ask: {
        requester: true,
        form: [{ key: "backup", label: "Backup policy", type: "choice", options: ["daily", "none"], required: true }],
      },
    },
    { key: "record", rest: { method: "PATCH", url: "https://cmdb.corp.example/api/ci/${inputs.name}" } },
    {
      key: "way",
      branch: {
        cases: [
          {
            title: "No backup",
            when: "steps.detail.answer.backup == 'none' && steps.record.status == 200",
            steps: [
              { key: "warn", notify: { requester: true, message: "${inputs.name}: ${steps.detail.answer.backup}" } },
            ],
          },
        ],
      },
    },
  ],
};

async function published() {
  const item = await api<{ id: string; draft_version: { id: string } }>("POST", "/admin/catalog/items", {
    slug: "missing-detail",
    name: "Missing detail",
    summary: "Asks.",
    category: "compute",
    type: "composite",
  });
  const draft = await api<{ definition: Record<string, unknown>; revision: number }>(
    "GET",
    `/admin/catalog/items/missing-detail/versions/${item.draft_version.id}`,
  );
  await api("PUT", `/admin/catalog/items/missing-detail/versions/${item.draft_version.id}`, {
    definition: { ...draft.definition, blueprint },
    base_revision: draft.revision,
  });
  await api("POST", `/admin/catalog/items/missing-detail/versions/${item.draft_version.id}/publish`);
  await api("PUT", "/admin/catalog/items/missing-detail/entitlements", { all_projects: true, project_ids: [] });
  return item;
}

/** Requests it, answers the requester's question, and gives its steps once it ended. */
async function run(item: { id: string; draft_version: { id: string } }, backup: string) {
  const req = await api<ServiceRequest>("POST", "/requests", {
    catalog_item_id: item.id,
    version_id: item.draft_version.id,
    project_id: "prj_web",
    environment: "dev",
    inputs: { name: "web-01" },
  });
  const asked = await until("nobody was asked", async () => {
    const inbox = await api<ApprovalPage>("GET", "/approvals");
    return inbox.items.find((a) => a.request.id === req.id);
  });
  await api("POST", `/approvals/${asked.id}/decision`, { decision: "approve", answer: { backup } });
  await until("it didn't end", async () => {
    const r = await api<ServiceRequest>("GET", `/requests/${req.id}`);
    return r.status === "succeeded" ? r : undefined;
  });
  const { items } = await api<{ items: Step[] }>("GET", `/requests/${req.id}/steps`);
  return Object.fromEntries(items.map((s) => [s.key, s]));
}

describe("a composite's run in the mocks", () => {
  it("a branch follows a person's answer, and a message reads it when sent", async () => {
    const said: string[] = [];
    bus.subscribe(({ event }) => {
      if (event.type === "notification" && event.data.title.startsWith("REQ-")) said.push(event.data.body ?? "");
    });
    const item = await published();
    let steps = await run(item, "none");
    expect(steps["step:way"]).toMatchObject({ status: "done", branch_case: 1, detail: "Took: No backup" });
    expect(steps["step:warn"]).toMatchObject({ status: "done", detail: "Message sent." });
    expect(said).toEqual(["web-01: none"]);

    steps = await run(item, "daily");
    expect(steps["step:way"]).toMatchObject({
      status: "done",
      branch_case: 0,
      detail: "No case held: nothing ran here.",
    });
    expect(steps["step:warn"]).toMatchObject({ status: "skipped", detail: "Its side of the branch wasn't taken." });
  });
});
