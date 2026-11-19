import { describe, expect, it } from "vitest";
import type { ApprovalPage, ServiceRequest, Step, StepData } from "@/lib/api/types";
import { bus } from "./events";
import { db } from "./seed";

// The mocks' run of a composite (ADR-0033): a branch chooses when it runs, from
// a person's answer as the server's does, and a message reads it when sent. A
// case it can't read fails it, masked, as the server's does.

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

type Item = { id: string; draft_version: { id: string } };

async function published(slug = "missing-detail", bp: Record<string, unknown> = blueprint): Promise<Item> {
  const item = await api<Item>("POST", "/admin/catalog/items", {
    slug,
    name: slug,
    summary: "Asks.",
    category: "compute",
    type: "composite",
  });
  const version = `/admin/catalog/items/${slug}/versions/${item.draft_version.id}`;
  const draft = await api<{ definition: Record<string, unknown>; revision: number }>("GET", version);
  await api("PUT", version, { definition: { ...draft.definition, blueprint: bp }, base_revision: draft.revision });
  await api("POST", `${version}/publish`);
  await api("PUT", `/admin/catalog/items/${slug}/entitlements`, { all_projects: true, project_ids: [] });
  return item;
}

const request = (item: Item) =>
  api<ServiceRequest>("POST", "/requests", {
    catalog_item_id: item.id,
    version_id: item.draft_version.id,
    project_id: "prj_web",
    environment: "dev",
    inputs: { name: "web-01" },
  });

/** The request once it ended, and its steps by key. */
async function ended(id: string) {
  const r = await until("it didn't end", async () => {
    const got = await api<ServiceRequest>("GET", `/requests/${id}`);
    return got.status === "succeeded" || got.status === "failed" ? got : undefined;
  });
  const { items } = await api<{ items: Step[] }>("GET", `/requests/${id}/steps`);
  return { r, steps: Object.fromEntries(items.map((s) => [s.key, s])) };
}

/** Requests it, answers the requester's question, and gives its steps once it ended. */
async function run(item: Item, backup: string) {
  const req = await request(item);
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

  const secret = "lit-br4nch-secret";
  const side = (key: string) => ({ key, rest: { method: "GET", url: `https://cmdb.corp.example/api/ci/${key}` } });
  const reading = (when: string, ...before: Record<string, unknown>[]) => ({
    kind: "composite",
    steps: [
      ...before,
      { key: "choose", branch: { cases: [{ title: "Reads", when, steps: [side("no")] }], else: [side("other")] } },
    ],
  });
  const skipped = { ...side("earlier"), when: "inputs.name == 'other'" };

  it.each([
    ["absent", reading(`inputs.absent == '${secret}'`), "what it reads isn't there"],
    ["text", reading(`'${secret}'`), "it isn't true or false"],
    // A step its own `when` skipped isn't there: it is read with has().
    ["skipped", reading(`steps.earlier.status == 200 || inputs.name == '${secret}'`, skipped), "isn't there"],
  ])("a case it can't read (%s) fails the branch, masked, its sides left pending", async (slug, bp, why) => {
    const req = await request(await published(`branch-${slug}`, bp));
    const { r, steps } = await ended(req.id);
    expect(r.status).toBe("failed");
    expect(r.error?.message).toContain(`Its side couldn't be chosen: case 1's condition`);
    expect(r.error?.message).toContain(why);
    expect(steps["step:choose"]).toMatchObject({ status: "failed", branch_case: null, error: r.error?.message });
    expect(JSON.stringify([r, steps])).not.toContain(secret);
    expect(steps["step:no"]!.status).toBe("pending");
    expect(steps["step:other"]!.status).toBe("pending");
  });

  it("a Retry reads a failed branch's condition again", async () => {
    const req = await request(await published("branch-again", reading(`inputs.absent == '${secret}'`)));
    expect((await ended(req.id)).r.status).toBe("failed");
    // Retried as it was, it fails again; once what it reads is there, it takes its side.
    await api("POST", `/requests/${req.id}/retry`);
    const again = await ended(req.id);
    expect(again.r.status).toBe("failed");
    expect(again.steps["step:choose"]!.error).toContain("what it reads isn't there");
    db.requests.find((x) => x.request.id === req.id)!.request.inputs!.absent = secret;
    await api("POST", `/requests/${req.id}/retry`);
    const { r, steps } = await ended(req.id);
    expect(r.status).toBe("succeeded");
    expect(steps["step:choose"]).toMatchObject({ status: "done", branch_case: 1, detail: "Took: Reads" });
    expect(steps["step:no"]!.status).toBe("done");
    expect(steps["step:other"]).toMatchObject({ status: "skipped", detail: "Its side of the branch wasn't taken." });
  });

  it("keeps what each step was given and gave (ADR-0057)", async () => {
    const bp = {
      kind: "composite",
      steps: [
        {
          key: "register",
          rest: {
            method: "POST",
            url: "https://cmdb.corp.example/api/ci?name=${inputs.name}",
            secret: "cmdb-token",
            body: '{"name": inputs.name}',
          },
        },
        {
          key: "agent",
          awx: { job_template: "monitoring-agent" },
          with: { target: "inputs.name", answered: "steps.register.status" },
        },
      ],
    };
    const req = await request(await published("run-data", bp));
    const { r, steps } = await ended(req.id);
    expect(r.status).toBe("succeeded");
    expect(steps["step:register"]!.has_data).toBe(true);
    const data = (s: Step) => api<StepData>("GET", `/requests/${req.id}/steps/${s.id}/data`);
    expect(await data(steps["step:register"]!)).toMatchObject({
      inputs: {
        method: "POST",
        url: "https://cmdb.corp.example/api/ci?name=web-01",
        secret: "cmdb-token",
        body: { name: "web-01" },
      },
      outputs: { status: 201 },
      outputs_hidden: false,
    });
    const agent = await data(steps["step:agent"]!);
    expect(agent.inputs).toEqual({ target: "web-01", answered: 201 });
    expect(typeof agent.outputs?.job_id).toBe("number");
    expect(steps["request.submit"]!.has_data).toBeUndefined();
  });

  it("reads the shared variables as they are when it runs (ADR-0057)", async () => {
    const bp = {
      kind: "composite",
      steps: [
        {
          key: "agent",
          awx: { job_template: "monitoring-agent" },
          when: 'vars.cmdb.site == "gva"',
          with: { target: "inputs.name", dns: "vars.network.dns_servers" },
        },
      ],
    };
    const item = await published("shared-vars", bp);
    const first = await ended((await request(item)).id);
    const data = (id: string, s: Step) => api<StepData>("GET", `/requests/${id}/steps/${s.id}/data`);
    expect((await data(first.r.id, first.steps["step:agent"]!)).inputs).toEqual({
      target: "web-01",
      dns: ["10.20.0.53", "10.20.0.54"],
    });
    await api("PUT", "/admin/variables/cmdb.site", { value: "zrh" });
    const second = await ended((await request(item)).id);
    expect(second.steps["step:agent"]!.status).toBe("skipped");
  });
});

describe("loops, catch paths and signals in the mocks (ADR-0057)", () => {
  const call = (key: string, extra: Record<string, unknown> = {}) => ({
    key,
    rest: { method: "POST", url: "https://itsm.corp.example/api/x" },
    ...extra,
  });

  it("waits for a signal until its timeout, and skips what catches a failure that didn't come", async () => {
    const bp = {
      kind: "composite",
      steps: [
        call("agents", { for_each: { items: "['a', 'b']" }, catch: [call("fix")] }),
        { key: "cab", signal: { name: "cab-approved", timeout: "1m", on_timeout: "continue" } },
        { parallel: [call("p1"), call("p2")], catch: [call("tell")] },
      ],
      on_error: [call("incident")],
    };
    const { r, steps } = await ended((await request(await published("flow-continue", bp))).id);
    expect(r.status).toBe("succeeded");
    expect(steps["step:cab"]).toMatchObject({
      status: "done",
      system: "wait",
      detail: "No signal cab-approved came in 1m, so it went on.",
    });
    expect(steps["step:fix"]).toMatchObject({ status: "skipped", detail: "Nothing failed before it." });
    expect(steps["step:tell"]).toMatchObject({ status: "skipped", detail: "Nothing failed before it." });
    expect(steps["step:incident"]).toMatchObject({ status: "skipped", detail: "Nothing failed." });
  });

  it("fails a step whose signal didn't come, as the server does", async () => {
    const bp = { kind: "composite", steps: [{ key: "ev", signal: { event: "provisioning.post", timeout: "1m" } }] };
    const { r, steps } = await ended((await request(await published("flow-fail", bp))).id);
    expect(r.status).toBe("failed");
    expect(steps["step:ev"]!.status).toBe("failed");
    expect(r.error?.message).toContain("No event provisioning.post came in 1m.");
  });
});
