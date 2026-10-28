// Event subscriptions and custom workflows in the mock backend (S10,
// ADR-0021 and ADR-0022). Checks follow the server's where the UI shows
// them; deliveries are made up.
import { http, HttpResponse } from "msw";
import type { components } from "@/lib/api/schema";
import { celCheck } from "./cel";
import { audited } from "./governance";
import type { Db } from "./seed";

type S = components["schemas"];
type Run = S["SubscriptionRunDetail"];

interface ExtState {
  subs: S["Subscription"][];
  runs: Run[];
  workflows: S["WorkflowRegistration"][];
  seq: number;
}

const now = () => new Date().toISOString();
const ago = (minutes: number) => new Date(Date.now() - minutes * 60_000).toISOString();
const webhookHosts = ["cmdb.corp.example", "hooks.corp.example", "servicenow.corp.example"];

const topics: { key: S["EventTopicKey"]; description: string }[] = [
  { key: "request.submitted", description: "A request was submitted, before any approval." },
  { key: "provisioning.pre", description: "A catalog request is about to build: a blocking subscription may stop it." },
  { key: "provisioning.post", description: "A catalog request ended: built, failed or cancelled." },
  { key: "day2.pre", description: "A day-2 action is about to run: a blocking subscription may stop it." },
  { key: "day2.post", description: "A day-2 action ended." },
  { key: "delete.pre", description: "A deployment is about to be deleted: a blocking subscription may stop it." },
  { key: "delete.post", description: "A deployment was deleted, or its deletion failed." },
  { key: "lease.expiring", description: "A deployment's lease ends in a few days." },
  { key: "lease.expired", description: "A deployment's lease ended." },
];

function sample(topic: S["EventTopicKey"]): S["IntegrationEvent"] {
  return {
    id: "evt_sample",
    topic,
    contract: "rvav.event.v1",
    occurred_at: now(),
    data: {
      request: { id: "req_sample", number: "REQ-2026-000123", kind: "provision", status: "running" },
      item: { slug: "rhel9-server", name: "RHEL 9 server", version: "3.1" },
      project: { code: "WEB", name: "Web Platform", cost_center: "CC-4410" },
      environment: "dev",
      production: false,
      requester: { name: "Tom Nguyen", email: "tnguyen@corp.example" },
      inputs: { role: "web", size: "M" },
      deployment: null,
      lease: null,
    },
  };
}

function problem(status: number, title: string, errors?: S["FieldError"][], detail?: string) {
  return HttpResponse.json(
    {
      type: "https://rvav.dev/problems/x",
      title,
      status,
      request_id: "req_mock",
      ...(errors ? { errors } : {}),
      ...(detail ? { detail } : {}),
    },
    { status, headers: { "Content-Type": "application/problem+json" } },
  );
}

function state(db: Db): ExtState {
  const d = db as Db & { extensibility?: ExtState };
  if (d.extensibility) return d.extensibility;
  const first = db.requests[0]?.request;
  const request = first ? { id: first.id, number: first.number } : undefined;
  const subs: S["Subscription"][] = [
    {
      id: "sub_cmdb_check",
      name: "CMDB pre-check",
      description: "The CMDB refuses VMs of applications it doesn't know.",
      topic: "provisioning.pre",
      condition: 'environment == "prd"',
      blocking: true,
      on_failure: "stop_request",
      priority: 10,
      timeout_seconds: 20,
      enabled: true,
      target: { webhook: { url: "https://cmdb.corp.example/rvav/check", secret: "webhook-signing" } },
      created_at: ago(60 * 24 * 20),
      runs: { delivered: 41, failed: 0, pending: 0 },
      last_run_at: ago(30),
    },
    {
      id: "sub_servicenow",
      name: "ServiceNow change record",
      description: "Closes the change record when a VM is built.",
      topic: "provisioning.post",
      condition: "",
      blocking: false,
      priority: 100,
      timeout_seconds: 30,
      enabled: true,
      target: { webhook: { url: "https://servicenow.corp.example/api/rvav", secret: "webhook-signing" } },
      created_at: ago(60 * 24 * 12),
      runs: { delivered: 37, failed: 1, pending: 0 },
      last_run_at: ago(12),
    },
  ];
  const event = (topic: S["EventTopicKey"], minutes: number): S["IntegrationEvent"] => ({
    ...sample(topic),
    id: `evt_${minutes}`,
    occurred_at: ago(minutes),
  });
  const runs: Run[] = [
    {
      id: "run_sn_failed",
      subscription_id: "sub_servicenow",
      event_id: "evt_12",
      topic: "provisioning.post",
      status: "failed",
      blocking: false,
      test: false,
      request,
      attempts: 6,
      reason: "HTTP 503 from servicenow.corp.example",
      created_at: ago(60 * 25),
      finished_at: ago(12),
      payload: event("provisioning.post", 60 * 25),
      history: [
        { at: ago(60 * 25), ok: false, status_code: 503, response: "Service Unavailable", duration_ms: 812 },
        { at: ago(12), ok: false, status_code: 503, response: "Service Unavailable", duration_ms: 790 },
      ],
    },
    {
      id: "run_sn_ok",
      subscription_id: "sub_servicenow",
      event_id: "evt_90",
      topic: "provisioning.post",
      status: "delivered",
      blocking: false,
      test: false,
      request,
      attempts: 1,
      created_at: ago(90),
      finished_at: ago(90),
      payload: event("provisioning.post", 90),
      history: [{ at: ago(90), ok: true, status_code: 200, response: '{"change":"CHG0041207"}', duration_ms: 240 }],
    },
    {
      id: "run_cmdb_ok",
      subscription_id: "sub_cmdb_check",
      event_id: "evt_30",
      topic: "provisioning.pre",
      status: "delivered",
      blocking: true,
      test: false,
      request,
      attempts: 1,
      outcome: "continue",
      created_at: ago(30),
      finished_at: ago(30),
      payload: event("provisioning.pre", 30),
      properties: { cmdb_ci: "CI0098812" },
      history: [
        {
          at: ago(30),
          ok: true,
          status_code: 200,
          response: '{"decision":"continue","properties":{"cmdb_ci":"CI0098812"}}',
          duration_ms: 180,
        },
      ],
    },
  ];
  const workflows: S["WorkflowRegistration"][] = [
    {
      key: "decommission",
      title: "Decommission an application",
      description: "Freezes it, asks for approval, keeps the data for the retention period, then archives it.",
      workflow_type: "decommission.v1",
      task_queue: "platform-workflows",
      input_schema: {
        type: "object",
        required: ["application"],
        properties: {
          application: { type: "string", title: "Application" },
          retention_days: { type: "integer", title: "Retention (days)", default: 30 },
        },
      },
      timeout_seconds: 60 * 86400,
      project_ids: [],
      usages: ["catalog_item", "day2_action"],
      day2: { name: "Decommission", types: ["deployment"], condition: "", offered: true },
      enabled: true,
      running: 1,
      used_by: [],
      created_at: ago(60 * 24 * 5),
    },
  ];
  d.extensibility = { subs, runs, workflows, seq: 0 };
  return d.extensibility;
}

function checkSubscription(db: Db, w: S["SubscriptionWrite"]): S["FieldError"][] {
  const st = state(db);
  const errors: S["FieldError"][] = [];
  const err = (field: string, code: string, message: string) => errors.push({ field, code, message });
  if (!w.name?.trim()) err("/name", "required", "Name it.");
  const topic = topics.find((t) => t.key === w.topic);
  if (!topic) err("/topic", "unknown", `"${w.topic}" is not a topic.`);
  if (w.condition) {
    const why = celCheck(w.condition);
    if (why) err("/condition", "invalid", `Not valid CEL: ${why}`);
  }
  if (w.blocking && !topic?.key.endsWith(".pre")) err("/blocking", "invalid", "Only .pre topics may block.");
  if (w.timeout_seconds < 1 || w.timeout_seconds > 3600)
    err("/timeout_seconds", "range", "Between 1 and 3600 seconds.");
  const t = w.target;
  const kinds = [t.webhook, t.awx, t.workflow].filter(Boolean).length;
  if (kinds !== 1) err("/target", "one_of", "Choose one target: a webhook, an AWX template or a custom workflow.");
  if (t.webhook) {
    let host = "";
    try {
      const u = new URL(t.webhook.url);
      host = u.protocol === "https:" ? u.hostname : "";
    } catch {
      host = "";
    }
    if (!host) err("/target/webhook/url", "invalid", "An https URL.");
    else if (!webhookHosts.includes(host))
      err("/target/webhook/url", "not_allowed", `${host} is not an allowed webhook host (RVAV_WEBHOOK_HOSTS).`);
    const secrets = (db as Db & { catalogAdmin?: { secrets: S["Secret"][] } }).catalogAdmin?.secrets ?? [];
    if (!t.webhook.secret) err("/target/webhook/secret", "required", "Choose the secret it is signed with.");
    else if (secrets.length > 0 && !secrets.some((s) => s.name === t.webhook?.secret))
      err("/target/webhook/secret", "unknown", `No secret is named ${t.webhook.secret}.`);
  }
  if (t.awx && !t.awx.job_template && !t.awx.template_id) err("/target/awx", "required", "Choose the AWX template.");
  if (t.workflow) {
    const wf = st.workflows.find((x) => x.key === t.workflow?.key);
    if (!wf) err("/target/workflow/key", "unknown", "Choose a registered workflow.");
    else if (!wf.usages.includes("subscription_target"))
      err("/target/workflow/key", "not_allowed", `${wf.title} isn't registered as a subscription target.`);
  }
  return errors;
}

function checkWorkflow(w: S["WorkflowRegistrationWrite"], create: boolean, taken: boolean): S["FieldError"][] {
  const errors: S["FieldError"][] = [];
  const err = (field: string, code: string, message: string) => errors.push({ field, code, message });
  if (create && !/^[a-z][a-z0-9_-]{1,40}$/.test(w.key ?? ""))
    err("/key", "pattern", "Lower-case letters, digits, dashes and underscores.");
  else if (create && taken) err("/key", "duplicate", `${w.key} is registered already.`);
  if (!w.title?.trim()) err("/title", "required", "Give it a title.");
  if (!/\.v[0-9]+$/.test(w.workflow_type ?? ""))
    err("/workflow_type", "pattern", "The type ends with its major version: decommission.v1.");
  if (!w.task_queue?.trim()) err("/task_queue", "required", "The task queue its workers poll.");
  if (w.input_schema && w.input_schema.type !== "object")
    err("/input_schema", "invalid", 'The input schema is an object schema: "type": "object".');
  if (w.usages.length === 0) err("/usages", "required", "Say how it is used.");
  if (w.usages.includes("day2_action")) {
    if (!w.day2?.name?.trim()) err("/day2/name", "required", "Name the action.");
    if (!w.day2 || w.day2.types.length === 0) err("/day2/types", "required", "Say what it runs on.");
    const why = w.day2?.condition ? celCheck(w.day2.condition) : null;
    if (why) err("/day2/condition", "invalid", `Not valid CEL: ${why}`);
  }
  return errors;
}

export function extensibilityHandlers(db: Db, base: string, wait: () => Promise<void>) {
  const findSub = (id: string) => state(db).subs.find((s) => s.id === id);
  return [
    http.get(`${base}/admin/event-topics`, async () => {
      await wait();
      return HttpResponse.json({
        items: topics.map((t) => ({ ...t, may_block: t.key.endsWith(".pre"), sample: sample(t.key) })),
      });
    }),
    http.get(`${base}/admin/subscriptions`, async () => {
      await wait();
      return HttpResponse.json({ items: state(db).subs });
    }),
    http.post(`${base}/admin/subscriptions`, async ({ request }) => {
      await wait();
      const w = (await request.json()) as S["SubscriptionWrite"];
      const errors = checkSubscription(db, w);
      if (errors.length > 0) return problem(422, "Some inputs need attention", errors);
      const st = state(db);
      const sub: S["Subscription"] = {
        ...w,
        id: `sub_${++st.seq}`,
        created_at: now(),
        runs: { delivered: 0, failed: 0, pending: 0 },
      };
      st.subs.push(sub);
      audited(db, "subscription.created", "subscription", sub.id, { name: sub.name });
      return HttpResponse.json(sub, { status: 201 });
    }),
    http.get(`${base}/admin/subscriptions/:id`, async ({ params }) => {
      await wait();
      const sub = findSub(String(params.id));
      return sub ? HttpResponse.json(sub) : problem(404, "Not found");
    }),
    http.put(`${base}/admin/subscriptions/:id`, async ({ params, request }) => {
      await wait();
      const sub = findSub(String(params.id));
      if (!sub) return problem(404, "Not found");
      const w = (await request.json()) as S["SubscriptionWrite"];
      const errors = checkSubscription(db, w);
      if (errors.length > 0) return problem(422, "Some inputs need attention", errors);
      Object.assign(sub, w, { updated_at: now() });
      audited(db, "subscription.updated", "subscription", sub.id, { name: sub.name });
      return HttpResponse.json(sub);
    }),
    http.delete(`${base}/admin/subscriptions/:id`, async ({ params }) => {
      await wait();
      const st = state(db);
      const i = st.subs.findIndex((s) => s.id === params.id);
      if (i < 0) return problem(404, "Not found");
      const gone = st.subs.splice(i, 1)[0]!;
      st.runs = st.runs.filter((r) => r.subscription_id !== gone.id);
      audited(db, "subscription.deleted", "subscription", gone.id, { name: gone.name });
      return new HttpResponse(null, { status: 204 });
    }),
    http.post(`${base}/admin/subscriptions/:id/test`, async ({ params }) => {
      await wait();
      const st = state(db);
      const sub = findSub(String(params.id));
      if (!sub) return problem(404, "Not found");
      const ok = !sub.target.webhook?.url.includes("fail");
      const at = now();
      const run: Run = {
        id: `run_${++st.seq}`,
        subscription_id: sub.id,
        event_id: `evt_test_${st.seq}`,
        topic: sub.topic,
        status: ok ? "delivered" : "failed",
        blocking: sub.blocking,
        test: true,
        attempts: 1,
        ...(sub.blocking && ok ? { outcome: "continue" } : {}),
        ...(ok ? {} : { reason: "HTTP 500 from the target" }),
        created_at: at,
        finished_at: at,
        payload: { ...sample(sub.topic), id: `evt_test_${st.seq}` },
        history: [
          ok
            ? {
                at,
                ok: true,
                ...(sub.target.webhook ? { status_code: 200, response: '{"decision":"continue"}' } : {}),
                ...(sub.target.awx ? { job_id: 4120 + st.seq } : {}),
                ...(sub.target.workflow ? { workflow_id: `rvav-subscription/run_${st.seq}` } : {}),
                duration_ms: 150,
              }
            : { at, ok: false, status_code: 500, error: "HTTP 500 from the target", duration_ms: 95 },
        ],
      };
      st.runs.unshift(run);
      sub.runs[ok ? "delivered" : "failed"]++;
      sub.last_run_at = at;
      return HttpResponse.json(run);
    }),
    http.get(`${base}/admin/subscriptions/:id/runs`, async ({ params, request }) => {
      await wait();
      if (!findSub(String(params.id))) return problem(404, "Not found");
      const status = new URL(request.url).searchParams.get("status");
      const items = state(db)
        .runs.filter((r) => r.subscription_id === params.id && (!status || r.status === status))
        .map(({ payload: _p, history: _h, properties: _x, ...run }) => run);
      return HttpResponse.json({ items, next_cursor: null });
    }),
    http.get(`${base}/admin/subscription-runs/:id`, async ({ params }) => {
      await wait();
      const run = state(db).runs.find((r) => r.id === params.id);
      return run ? HttpResponse.json(run) : problem(404, "Not found");
    }),
    http.post(`${base}/admin/subscription-runs/:id/retry`, async ({ params }) => {
      await wait();
      const st = state(db);
      const run = st.runs.find((r) => r.id === params.id);
      if (!run) return problem(404, "Not found");
      if (run.status !== "failed") return problem(409, "Conflict", undefined, "Only a failed run is tried again.");
      const at = now();
      run.status = "delivered";
      run.attempts++;
      run.reason = undefined;
      run.finished_at = at;
      run.history.push({ at, ok: true, status_code: 200, response: '{"change":"CHG0041301"}', duration_ms: 210 });
      const sub = findSub(run.subscription_id);
      if (sub) {
        sub.runs.failed = Math.max(0, sub.runs.failed - 1);
        sub.runs.delivered++;
      }
      const { payload: _p, history: _h, properties: _x, ...out } = run;
      return HttpResponse.json(out);
    }),

    http.get(`${base}/admin/workflows`, async () => {
      await wait();
      const st = state(db);
      return HttpResponse.json({
        items: st.workflows.map((w) => ({
          ...w,
          used_by: [
            ...w.used_by,
            ...st.subs.filter((s) => s.target.workflow?.key === w.key).map((s) => `subscription ${s.name}`),
          ],
        })),
      });
    }),
    http.post(`${base}/admin/workflows`, async ({ request }) => {
      await wait();
      const st = state(db);
      const w = (await request.json()) as S["WorkflowRegistrationWrite"];
      const errors = checkWorkflow(
        w,
        true,
        st.workflows.some((x) => x.key === w.key),
      );
      if (errors.length > 0) return problem(422, "Some inputs need attention", errors);
      const reg: S["WorkflowRegistration"] = {
        ...w,
        input_schema: w.input_schema ?? {},
        project_ids: w.project_ids ?? [],
        running: 0,
        used_by: [],
        created_at: now(),
      };
      st.workflows.push(reg);
      audited(db, "workflow.registered", "workflow", reg.key ?? "", { type: reg.workflow_type });
      return HttpResponse.json(reg, { status: 201 });
    }),
    http.get(`${base}/admin/workflows/:key`, async ({ params }) => {
      await wait();
      const w = state(db).workflows.find((x) => x.key === params.key);
      return w ? HttpResponse.json(w) : problem(404, "Not found");
    }),
    http.put(`${base}/admin/workflows/:key`, async ({ params, request }) => {
      await wait();
      const w = state(db).workflows.find((x) => x.key === params.key);
      if (!w) return problem(404, "Not found");
      const body = (await request.json()) as S["WorkflowRegistrationWrite"];
      const errors = checkWorkflow(body, false, false);
      if (errors.length > 0) return problem(422, "Some inputs need attention", errors);
      Object.assign(w, body, { key: w.key, updated_at: now() });
      audited(db, "workflow.updated", "workflow", w.key ?? "", { type: w.workflow_type });
      return HttpResponse.json(w);
    }),
    http.delete(`${base}/admin/workflows/:key`, async ({ params }) => {
      await wait();
      const st = state(db);
      const i = st.workflows.findIndex((x) => x.key === params.key);
      if (i < 0) return problem(404, "Not found");
      const w = st.workflows[i]!;
      if (w.running > 0)
        return problem(
          409,
          "Conflict",
          undefined,
          "Runs of it haven't ended: disable it instead, and remove it once they have.",
        );
      const users = st.subs.filter((s) => s.target.workflow?.key === w.key).map((s) => `subscription ${s.name}`);
      if (users.length > 0)
        return problem(409, "Conflict", undefined, `It is used by ${users.join(", ")}: disable it instead.`);
      st.workflows.splice(i, 1);
      audited(db, "workflow.deleted", "workflow", w.key ?? "");
      return new HttpResponse(null, { status: 204 });
    }),
  ];
}
