// MSW handlers for every slice 1 operation in api/openapi.yaml.
// Response bodies are typed with the generated schema types at each call site.
import { delay, http, HttpResponse } from "msw";
import type { components, paths } from "@/lib/api/schema";
import type {
  CatalogItem,
  DeploymentDetail,
  DeploymentStatus,
  EnvironmentKey,
  FieldError,
  LiveEvent,
  Page,
  Problem,
  RequestDraft,
  RequestStatus,
  ServiceRequest,
} from "@/lib/api/types";
import {
  canDecide,
  factsFor,
  isApproverOf,
  openLevels,
  summaryOf,
  viewOf,
  vote,
  type StoredApproval,
} from "./approvals";
import { actionsFor, runAction } from "./day2";
import { bus, matches } from "./events";
import { catalogAdminHandlers } from "./catalogAdmin";
import { demand, preview, resolveField, summarize, validateInputs, vmCount } from "./logic";
import { db, type StoredItem, type StoredRequest } from "./seed";
import { cancel, newRequestNumber, planSteps, publishRequest, rememberHost, start } from "./simulator";

type S = components["schemas"];
type Ok<P extends keyof paths, M extends "get" | "post"> = paths[P][M] extends {
  responses: infer R;
}
  ? R extends Record<string | number, unknown>
    ? Extract<R[keyof R], { content: { "application/json": unknown } }>["content"]["application/json"]
    : never
  : never;

export const latency = { ms: 180 };

const base = "*/api/v1";
const rid = () => `rq-${Math.random().toString(36).slice(2, 10)}`;

function json<T>(body: T, init: ResponseInit = {}) {
  const requestId = rid();
  return new HttpResponse(JSON.stringify(body), {
    ...init,
    headers: { "Content-Type": "application/json", "X-Request-Id": requestId, ...(init.headers ?? {}) },
  });
}

function problem(status: number, slug: string, title: string, detail?: string, errors?: FieldError[]) {
  const requestId = rid();
  const body: Problem = {
    type: `https://rvav.dev/problems/${slug}`,
    title,
    status,
    request_id: requestId,
    ...(detail ? { detail } : {}),
    ...(errors ? { errors } : {}),
  };
  return new HttpResponse(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/problem+json", "X-Request-Id": requestId },
  });
}

const notFound = (what: string) =>
  problem(404, "not-found", `${what} not found`, `It doesn't exist, or you can't see it.`);

async function wait() {
  if (latency.ms > 0) await delay(latency.ms);
}

function authorized(request: Request) {
  return (request.headers.get("Authorization") ?? "").startsWith("Bearer ");
}

function page<T>(items: T[], url: URL): Page<T> {
  const limit = Math.min(200, Math.max(1, Number(url.searchParams.get("limit") ?? 50)));
  const offset = Number(atob(url.searchParams.get("cursor") ?? "") || 0);
  const slice = items.slice(offset, offset + limit);
  return { items: slice, next_cursor: offset + limit < items.length ? btoa(String(offset + limit)) : null };
}

const list = (url: URL, name: string) =>
  (url.searchParams.get(name) ?? "")
    .split(",")
    .map((s) => s.trim())
    .filter(Boolean);

const myProjectIds = () => new Set(db.me.projects.map((p) => p.project.id));
const projectById = (id: string | null) =>
  id && myProjectIds().has(id) ? db.projects.find((p) => p.id === id) : undefined;
const itemByKey = (key: string) => db.items.find((i) => i.id === key || i.slug === key);
const requestByKey = (key: string) => db.requests.find((r) => r.request.id === key || r.request.number === key);

function publicItem(i: StoredItem): CatalogItem {
  const { projectIds: _p, ...rest } = i;
  return { ...rest, favourite: db.favourites.has(i.id) };
}

function visibleRequest(r: ServiceRequest) {
  return myProjectIds().has(r.project.id);
}

function title(item: StoredItem, host: string | null, inputs: Record<string, unknown>) {
  if (host) return `${item.name} · ${host}`;
  const first = Object.values(inputs).find((v) => typeof v === "string" && v.length > 0);
  return typeof first === "string" ? `${item.name} · ${first}` : item.name;
}

/** A deployment for a request that starts provisioning now. */
function createDeployment(
  item: StoredItem,
  project: (typeof db.projects)[number],
  environment: EnvironmentKey,
  inputs: Record<string, unknown>,
  count: number,
  host: string | null,
  number: string,
  leaseDays?: number | null,
): DeploymentDetail | null {
  if (vmCount(item, count) === 0 || !host) return null;
  const production = db.environments.find((e) => e.key === environment)?.production ?? false;
  const days = leaseDays ?? (production ? null : 30);
  const dep: DeploymentDetail = {
    id: `dep_${(db.seq.id++).toString(36)}`,
    name: host.replace(/\d+$/, ""),
    status: "provisioning",
    project: { id: project.id, code: project.code, name: project.name },
    environment,
    catalog_item: { id: item.id, slug: item.slug, name: item.name },
    version: item.version,
    owner: db.me.user,
    created_at: new Date().toISOString(),
    created_by_request_id: number,
    lease: {
      expires_at: days ? new Date(Date.now() + days * 86_400_000).toISOString() : null,
      extensions_used: 0,
      extensions_max: days ? 2 : null,
      max_days: days ? 90 : null,
      grace_days: 7,
      expiry_action: "power_off_then_delete",
    },
    vm_count: 0,
    vms_powered_on: 0,
    primary_address: null,
    sync_state: "in_sync",
    inputs,
    resources: [],
  };
  db.deployments.unshift(dep);
  const q = db.quota[project.id];
  if (q) {
    const need = demand(item, inputs, count);
    for (const k of Object.keys(need) as (keyof typeof need)[]) q.used[k] += need[k];
  }
  return dep;
}

function publishApproval(a: StoredApproval) {
  bus.publish({
    event: {
      id: bus.nextId(),
      type: "approval.updated",
      at: new Date().toISOString(),
      data: { approval_id: a.id, request_id: a.request.id, status: a.status },
    },
    projectId: a.project.id,
    requestId: a.request.id,
    userIds: [...a.approvers, ...a.delegates].map((u) => u.id),
  });
}

/** Every level approved: the request is planned again with its inputs and starts. */
function provisionApproved(stored: StoredRequest, outcome: string, detail: string) {
  const r = stored.request;
  if (r.kind === "day2") {
    const policy = stored.steps.find((s) => s.key === "policy.approval");
    if (policy)
      Object.assign(policy, { title: outcome, detail, status: "done", finished_at: new Date().toISOString() });
    const d = db.deployments.find((x) => x.id === r.deployment_id);
    if (d) d.status = r.action?.key === "delete" ? "deleting" : "updating";
    r.status = "running";
    r.started_at = new Date().toISOString();
    publishRequest(stored);
    start(r.id);
    return;
  }
  const item = itemByKey(r.catalog_item.id);
  const project = db.projects.find((p) => p.id === r.project.id);
  if (!item || !project) return;
  const { steps, host } = planSteps(item, project, r.environment, r.inputs ?? {}, null);
  const [, policy, ...rest] = steps;
  if (policy) Object.assign(policy, { title: outcome, detail, status: "done", finished_at: new Date().toISOString() });
  stored.steps = [stored.steps[0]!, ...(policy ? [policy] : []), ...rest];
  const dep = createDeployment(item, project, r.environment, r.inputs ?? {}, r.count ?? 1, host, r.number);
  if (dep) r.deployment_ids = [dep.id];
  if (host) {
    rememberHost(r.id, host);
    r.title = `${item.name} · ${host}`;
  }
  r.status = "running";
  r.started_at = new Date().toISOString();
  publishRequest(stored);
  start(r.id);
}

function rejectRequest(stored: StoredRequest, outcome: string, comment: string) {
  const now = new Date().toISOString();
  for (const s of stored.steps) {
    if (s.key === "policy.approval")
      Object.assign(s, { title: outcome, detail: `“${comment}”`, status: "failed", finished_at: now });
    else if (s.status === "pending") s.status = "cancelled";
  }
  Object.assign(stored.request, { status: "rejected", finished_at: now });
  publishRequest(stored);
}

const levelsOf = (requestId: string) =>
  db.approvals.filter((a) => a.request.id === requestId).sort((a, b) => a.level.index - b.level.index);

const visibleApproval = (a: StoredApproval) => isApproverOf(db, a) || myProjectIds().has(a.project.id);

export const handlers = [
  // Every call needs a bearer token, as with the real API.
  http.all(`${base}/*`, ({ request }) => {
    if (!authorized(request)) return problem(401, "unauthorized", "Sign in again", "Your session has expired.");
    return undefined;
  }),

  http.get(`${base}/me`, async () => {
    await wait();
    return json<S["Me"]>(db.me);
  }),

  http.get(`${base}/environments`, async () => {
    await wait();
    return json<Ok<"/environments", "get">>({ items: db.environments });
  }),

  // AWX templates imported into the catalog: one whose survey changed.
  ...(() => {
    const templates = (): S["AwxTemplate"][] => [
      {
        template_id: 12,
        name: "install-monitoring-agent",
        workflow: false,
        item_id: "itm_monitoring",
        item_name: "Install monitoring agent",
        survey_changed: true,
        survey_diff: ["listen_port: maximum 65535 → 32767", "scrape_interval added (integer)"],
        warnings: [],
        gone: false,
        synced_at: new Date().toISOString(),
      },
      {
        template_id: 9,
        name: "configure-vm",
        workflow: true,
        item_id: "itm_configure",
        item_name: "Configure vm",
        survey_changed: false,
        survey_diff: [],
        warnings: ["Variables are not prompted on launch: AWX ignores rvav's context and anything outside the survey."],
        gone: false,
        synced_at: new Date().toISOString(),
      },
    ];
    return [
      http.get(`${base}/admin/awx/templates`, async () => {
        await wait();
        return json<S["AwxTemplateList"]>({ items: templates() });
      }),
      http.post(`${base}/admin/awx/templates/sync`, async () => {
        await wait();
        return json<S["AwxTemplateList"]>({ items: templates() });
      }),
    ];
  })(),

  ...catalogAdminHandlers(db, base, wait),

  // AWX templates with the day2 label, offered as day-2 actions (ADR-0018).
  ...(() => {
    const actions: S["AwxAction"][] = [
      {
        key: "awx.install-monitoring-agent",
        name: "Install monitoring agent",
        description: "Install a monitoring agent on a VM.",
        template_id: 12,
        template: "install-monitoring-agent",
        workflow: false,
        types: ["vsphere.vm"],
        condition: "resource.os_family == 'linux'",
        enabled: true,
        fields: ["monitoring_agent", "agent_version", "listen_port"],
        warnings: [],
        gone: false,
        synced_at: new Date().toISOString(),
      },
      {
        key: "awx.patch-now",
        name: "Patch now",
        description: "Apply the pending updates.",
        template_id: 14,
        template: "patch-now",
        workflow: false,
        types: ["vsphere.vm", "deployment"],
        condition: "",
        enabled: false,
        fields: [],
        warnings: ["Inventory is not prompted on launch: rvav won't run it, as it can't run on rvav's inventory."],
        gone: false,
        synced_at: new Date().toISOString(),
      },
    ];
    return [
      http.get(`${base}/admin/awx/actions`, async () => {
        await wait();
        return json<S["AwxActionList"]>({ items: actions });
      }),
      http.patch(`${base}/admin/awx/actions/:key`, async ({ params, request }) => {
        await wait();
        const a = actions.find((x) => x.key === params.key);
        if (!a) return notFound("AWX action");
        const body = (await request.json()) as S["AwxActionUpdate"];
        // The server compiles the condition; here, unbalanced parentheses are enough.
        const c = body.condition ?? "";
        if ((c.match(/\(/g) ?? []).length !== (c.match(/\)/g) ?? []).length)
          return problem(422, "validation-failed", "Some fields need attention", undefined, [
            { field: "/condition", code: "invalid", message: "This condition is not valid: Syntax error: missing ')'" },
          ]);
        Object.assign(a, body);
        return json<S["AwxAction"]>(a);
      }),
    ];
  })(),

  // The mock user is an administrator for this check; one address and one
  // record left behind, and a PTR record someone deleted.
  http.get(`${base}/admin/ipam/reconcile`, async () => {
    await wait();
    return json<S["IpamReconcile"]>({
      checked_at: new Date().toISOString(),
      managed_addresses: 13,
      managed_records: 26,
      orphan_addresses: [
        { efficientip_id: "48213", ip: "10.31.10.61", name: "web-dev-app014.corp.example", subnet: "10.31.10.0/24" },
      ],
      orphan_records: [
        { efficientip_id: "90311", fqdn: "web-dev-app014.corp.example", type: "A", value: "10.31.10.61" },
      ],
      missing: [
        {
          resource_id: "res_01",
          deployment_id: db.deployments[0]?.id ?? "dep_01",
          deployment_name: db.deployments[0]?.name ?? "web-dev-app001",
          type: "dns.record",
          name: "PTR 57.10.31.10.in-addr.arpa",
          efficientip_id: "90207",
        },
      ],
    });
  }),

  http.get(`${base}/projects`, async ({ request }) => {
    await wait();
    const mine = db.projects.filter((p) => myProjectIds().has(p.id));
    return json<S["ProjectPage"]>(page(mine, new URL(request.url)));
  }),

  http.get<{ projectId: string }>(`${base}/projects/:projectId`, async ({ params }) => {
    await wait();
    const p = projectById(params.projectId);
    return p ? json<S["Project"]>(p) : notFound("Project");
  }),

  http.get<{ projectId: string }>(`${base}/projects/:projectId/quota`, async ({ params, request }) => {
    await wait();
    const p = projectById(params.projectId);
    const q = p && db.quota[p.id];
    if (!p || !q) return notFound("Project");
    const env = new URL(request.url).searchParams.get("environment");
    return json<S["QuotaUsage"]>({
      project_id: p.id,
      environment: env,
      lines: (["vcpu", "memory_gb", "storage_gb", "vm_count"] as const).map((resource) => ({
        resource,
        limit: q.limits[resource],
        used: q.used[resource],
        // Running requests already count in `used` here, so nothing is held separately.
        reserved: 0,
        enforcement: "hard",
      })),
    });
  }),

  http.get(`${base}/catalog/categories`, async ({ request }) => {
    await wait();
    const projectId = new URL(request.url).searchParams.get("project_id");
    const items = db.items.filter((i) => projectId && i.projectIds.includes(projectId));
    return json<Ok<"/catalog/categories", "get">>({
      items: db.categories.map((c) => ({ ...c, item_count: items.filter((i) => i.category === c.key).length })),
    });
  }),

  http.get(`${base}/catalog/items`, async ({ request }) => {
    await wait();
    const url = new URL(request.url);
    const projectId = url.searchParams.get("project_id");
    const category = url.searchParams.get("category");
    const type = url.searchParams.get("type");
    const favourite = url.searchParams.get("favourite") === "true";
    const q = (url.searchParams.get("q") ?? "").trim().toLowerCase();
    const items = db.items
      .filter((i) => projectId && i.projectIds.includes(projectId))
      .filter((i) => !category || i.category === category)
      .filter((i) => !type || i.type === type)
      .filter((i) => !favourite || db.favourites.has(i.id))
      .filter((i) => !q || `${i.name} ${i.summary} ${i.source.awx_template_name ?? ""}`.toLowerCase().includes(q))
      .map(publicItem);
    return json<S["CatalogItemPage"]>(page(items, url));
  }),

  http.get<{ itemId: string }>(`${base}/catalog/items/:itemId`, async ({ params }) => {
    await wait();
    const item = itemByKey(params.itemId);
    return item ? json<S["CatalogItem"]>(publicItem(item)) : notFound("Catalog item");
  }),

  http.put<{ itemId: string }>(`${base}/catalog/items/:itemId/favourite`, async ({ params }) => {
    await wait();
    const item = itemByKey(params.itemId);
    if (!item) return notFound("Catalog item");
    db.favourites.add(item.id);
    return new HttpResponse(null, { status: 204 });
  }),

  http.delete<{ itemId: string }>(`${base}/catalog/items/:itemId/favourite`, async ({ params }) => {
    await wait();
    const item = itemByKey(params.itemId);
    if (!item) return notFound("Catalog item");
    db.favourites.delete(item.id);
    return new HttpResponse(null, { status: 204 });
  }),

  http.get<{ itemId: string }>(`${base}/catalog/items/:itemId/form`, async ({ params, request }) => {
    await wait();
    const item = itemByKey(params.itemId);
    const projectId = new URL(request.url).searchParams.get("project_id");
    if (!item) return notFound("Catalog item");
    const role = db.me.projects.find((p) => p.project.id === projectId)?.role;
    if (role === "viewer")
      return problem(403, "forbidden", "You can't request items in this project", "Viewers can only look.");
    return json<S["RequestForm"]>(db.forms[item.id]!);
  }),

  http.post(`${base}/forms/values`, async ({ request }) => {
    await wait();
    // `environment` is not in FieldValuesQuery yet (spec gap); the web client sends it anyway.
    const body = (await request.json()) as S["FieldValuesQuery"] & { environment?: EnvironmentKey };
    const item = db.items.find((i) => i.version.id === body.version_id);
    const project = projectById(body.project_id);
    if (!item || !project) return notFound("Form");
    const env = body.environment ?? project.environments[0] ?? "dev";
    const result = resolveField(db, item, project, env, body.field, body.inputs ?? {});
    return result
      ? json<S["FieldValues"]>(result)
      : problem(400, "bad-request", `Field “${body.field}” has no value source`);
  }),

  http.post(`${base}/requests/preview`, async ({ request }) => {
    await wait();
    const draft = (await request.json()) as RequestDraft;
    const item = itemByKey(draft.catalog_item_id);
    const project = projectById(draft.project_id);
    if (!item || !project) return notFound("Catalog item or project");
    return json<S["RequestPreview"]>(preview(db, item, project, draft));
  }),

  http.get(`${base}/requests`, async ({ request }) => {
    await wait();
    const url = new URL(request.url);
    const projectId = url.searchParams.get("project_id");
    const statuses = list(url, "status") as RequestStatus[];
    const requester = url.searchParams.get("requester");
    const q = (url.searchParams.get("q") ?? "").trim().toLowerCase();
    const items = db.requests
      .map((r) => r.request)
      .filter(visibleRequest)
      .filter((r) => !projectId || r.project.id === projectId)
      .filter((r) => statuses.length === 0 || statuses.includes(r.status))
      .filter((r) => !requester || r.requester.id === (requester === "me" ? db.me.user.id : requester))
      .filter((r) => !q || `${r.number} ${r.title} ${r.requester.name}`.toLowerCase().includes(q))
      .sort((a, b) => b.submitted_at.localeCompare(a.submitted_at));
    return json<S["RequestPage"]>(page(items, url));
  }),

  http.post(`${base}/requests`, async ({ request }) => {
    await wait();
    const key = request.headers.get("Idempotency-Key");
    if (!key) return problem(400, "bad-request", "Idempotency-Key header is required");
    const raw = await request.text();
    const seen = db.idempotency.get(key);
    if (seen) {
      if (seen.body !== raw)
        return problem(409, "idempotency-conflict", "This request was already sent with different values");
      return json(seen.response, { status: seen.status });
    }
    const draft = JSON.parse(raw) as RequestDraft;
    const item = itemByKey(draft.catalog_item_id);
    const project = projectById(draft.project_id);
    if (!item || !project) return notFound("Catalog item or project");
    if (db.me.projects.find((p) => p.project.id === project.id)?.role === "viewer") {
      return problem(403, "forbidden", "You can't request items in this project");
    }
    const check = preview(db, item, project, draft);
    const errors = [
      ...validateInputs(db, item, draft.inputs),
      ...check.errors.filter((e) => e.field === "" || e.field === "/environment"),
    ];
    if (errors.length) {
      return problem(
        422,
        "validation",
        "Some values need fixing",
        "Fix the highlighted fields and submit again.",
        errors,
      );
    }

    const approval = check.approval.policies[0]?.reason ?? null;
    const number = newRequestNumber();
    const id = `req_${number.slice(4)}`;
    const { steps, host } = planSteps(item, project, draft.environment, draft.inputs, approval);
    const now = new Date().toISOString();
    const deploymentIds: string[] = [];
    const dep = approval
      ? null
      : createDeployment(
          item,
          project,
          draft.environment,
          draft.inputs,
          draft.count ?? 1,
          host,
          number,
          draft.lease_days,
        );
    if (dep) deploymentIds.push(dep.id);
    const req: ServiceRequest = {
      id,
      number,
      kind: "provision",
      title: title(item, host, draft.inputs),
      status: approval ? "pending_approval" : "running",
      catalog_item: { id: item.id, slug: item.slug, name: item.name, type: item.type },
      version: item.version,
      project: { id: project.id, code: project.code, name: project.name },
      environment: draft.environment,
      requester: db.me.user,
      inputs: draft.inputs,
      count: draft.count ?? 1,
      justification: draft.justification,
      submitted_at: now,
      scheduled_at: draft.scheduled_at ?? null,
      started_at: approval ? null : now,
      finished_at: null,
      deployment_ids: deploymentIds,
      current_step: null,
      progress: { done: 0, total: steps.length },
      error: null,
    };
    const stored = { request: req, steps, logs: {} };
    db.requests.unshift(stored);
    if (host) rememberHost(id, host);
    if (approval) {
      const levels = openLevels(db, req, check.approval, host, () => `apr_${(db.seq.id++).toString(36)}`);
      db.approvals.push(...levels);
      levels.forEach(publishApproval);
    }
    publishRequest(stored);
    if (!approval) start(id);
    db.idempotency.set(key, { body: raw, status: 202, response: structuredClone(req) });
    return json<S["Request"]>(req, { status: 202, headers: { Location: `/api/v1/requests/${number}` } });
  }),

  http.get<{ requestId: string }>(`${base}/requests/:requestId`, async ({ params }) => {
    await wait();
    const r = requestByKey(params.requestId);
    return r && visibleRequest(r.request) ? json<S["Request"]>(r.request) : notFound("Request");
  }),

  http.get<{ requestId: string }>(`${base}/requests/:requestId/steps`, async ({ params }) => {
    await wait();
    const r = requestByKey(params.requestId);
    return r && visibleRequest(r.request)
      ? json<Ok<"/requests/{requestId}/steps", "get">>({ items: r.steps })
      : notFound("Request");
  }),

  http.get<{ requestId: string; stepId: string }>(
    `${base}/requests/:requestId/steps/:stepId/log`,
    async ({ params, request }) => {
      await wait();
      const r = requestByKey(params.requestId);
      if (!r || !visibleRequest(r.request)) return notFound("Request");
      const s = r.steps.find((x) => x.id === params.stepId);
      if (!s) return notFound("Step");
      const url = new URL(request.url);
      const offset = Math.max(0, Number(url.searchParams.get("offset") ?? 0));
      const limit = Math.min(5000, Math.max(1, Number(url.searchParams.get("limit") ?? 1000)));
      const lines = (r.logs[s.id] ?? []).slice(offset, offset + limit);
      const finished = s.status !== "pending" && s.status !== "running";
      return json<S["LogChunk"]>({
        step_id: s.id,
        offset,
        next_offset: offset + lines.length,
        complete: finished && offset + lines.length >= (r.logs[s.id]?.length ?? 0),
        lines,
      });
    },
  ),

  http.post<{ requestId: string }>(`${base}/requests/:requestId/retry`, async ({ params }) => {
    await wait();
    const r = requestByKey(params.requestId);
    if (!r || !visibleRequest(r.request)) return notFound("Request");
    if (r.request.status !== "failed")
      return problem(
        409,
        "conflict",
        `${r.request.number} is ${r.request.status}; only failed requests can be retried.`,
      );
    const d = db.deployments.find((x) => x.id === r.request.deployment_ids?.[0]);
    const rolledBack = r.steps.some((s) => s.key === "rollback" && s.status === "done");
    const busy = db.requests.find(
      (x) =>
        x !== r &&
        x.request.deployment_ids?.some((id) => id === d?.id) &&
        ["pending_approval", "queued", "running", "cancelling"].includes(x.request.status),
    );
    if (busy)
      return problem(
        409,
        "conflict",
        `${busy.request.number} is running on this deployment; try again when it is done.`,
      );
    if (d?.status === "deleted" && !rolledBack)
      return problem(409, "conflict", "The deployment was deleted since. Submit a new request instead.");
    // The failed step and the ones after it run again; after a rollback,
    // everything after the approval.
    r.steps = r.steps.filter((s) => s.key !== "rollback");
    for (const step of r.steps) {
      const reset = rolledBack
        ? !["request.submit", "policy.approval"].includes(step.key)
        : step.status === "failed" || step.status === "cancelled";
      if (reset) {
        Object.assign(step, { status: "pending", started_at: null, finished_at: null, error: undefined });
        step.children.forEach((c) => Object.assign(c, { status: "pending", started_at: null, finished_at: null }));
      }
    }
    // The mock's simulator only advances running requests (the server answers queued).
    Object.assign(r.request, { status: "running", error: null, finished_at: null });
    if (d && r.request.kind !== "day2") d.status = "provisioning";
    publishRequest(r);
    start(r.request.id);
    return json<S["Request"]>(r.request, { status: 202 });
  }),

  http.post<{ requestId: string }>(`${base}/requests/:requestId/rollback`, async ({ params }) => {
    await wait();
    const r = requestByKey(params.requestId);
    if (!r || !visibleRequest(r.request)) return notFound("Request");
    if (r.request.status !== "failed" || r.request.kind === "day2")
      return problem(409, "conflict", `${r.request.number} has nothing to roll back.`);
    const d = db.deployments.find((x) => x.id === r.request.deployment_ids?.[0]);
    if (!d || d.status === "deleted") return problem(409, "conflict", `${r.request.number} has nothing to roll back.`);
    const at = new Date().toISOString();
    const done = d.resources.map((x) => (x.type === "vsphere.vm" ? `deleted VM ${x.name}` : `released ${x.name}`));
    d.resources = [];
    d.status = "deleted";
    r.steps.push({
      id: `stp_rb_${r.request.id}`,
      key: "rollback",
      title: "Rolled back",
      detail: done.join(", "),
      system: "rvav",
      status: "done",
      children: [],
      has_log: false,
      started_at: at,
      finished_at: at,
    });
    publishRequest(r);
    return json<S["Request"]>(r.request, { status: 202 });
  }),

  http.post<{ requestId: string }>(`${base}/requests/:requestId/cancel`, async ({ params }) => {
    await wait();
    const r = requestByKey(params.requestId);
    if (!r || !visibleRequest(r.request)) return notFound("Request");
    const cancellable: RequestStatus[] = ["pending_approval", "scheduled", "queued", "running"];
    if (!cancellable.includes(r.request.status)) {
      return problem(
        409,
        "conflict",
        "This request can't be cancelled",
        `It is already ${r.request.status.replace("_", " ")}.`,
      );
    }
    cancel(r);
    for (const a of db.approvals.filter(
      (x) => x.request.id === r.request.id && ["waiting", "pending"].includes(x.status),
    )) {
      a.status = "cancelled";
      a.due_at = null;
      publishApproval(a);
    }
    return json<S["Request"]>(r.request, { status: 202 });
  }),

  http.get(`${base}/approvals`, async ({ request }) => {
    await wait();
    const url = new URL(request.url);
    if (url.searchParams.get("view") === "all") {
      return problem(403, "forbidden", "Not allowed", "Only administrators and auditors can see every approval.");
    }
    const statuses = list(url, "status");
    const wanted = statuses.length ? statuses : ["pending"];
    const open = (a: StoredApproval) => a.status === "pending" || a.status === "waiting";
    const voted = (a: StoredApproval) => a.votes.some((v) => v.user.id === db.me.user.id);
    const mine = db.approvals.filter(
      (a) =>
        wanted.includes(a.status) &&
        a.requester.id !== db.me.user.id &&
        (open(a) ? isApproverOf(db, a) && !voted(a) && (a.status !== "pending" || canDecide(db, a)) : voted(a)),
    );
    const pendingOnly = wanted.length === 1 && wanted[0] === "pending";
    mine.sort((a, b) =>
      pendingOnly
        ? a.submitted_at.localeCompare(b.submitted_at)
        : (b.decided_at ?? b.submitted_at).localeCompare(a.decided_at ?? a.submitted_at),
    );
    const items = mine.map((a) => summaryOf(db, a));
    return json<S["ApprovalPage"]>({ ...page(items, url), total: items.length });
  }),

  http.get<{ approvalId: string }>(`${base}/approvals/:approvalId`, async ({ params }) => {
    await wait();
    const a = db.approvals.find((x) => x.id === params.approvalId);
    if (!a || !visibleApproval(a)) return notFound("Approval");
    return json<S["Approval"]>(viewOf(db, a, requestByKey(a.request.id)?.request));
  }),

  http.post<{ approvalId: string }>(`${base}/approvals/:approvalId/decision`, async ({ params, request }) => {
    await wait();
    const a = db.approvals.find((x) => x.id === params.approvalId);
    if (!a || !visibleApproval(a)) return notFound("Approval");
    const stored = requestByKey(a.request.id);
    if (!stored) return notFound("Request");
    if (a.status !== "pending") return problem(409, "conflict", "Not possible right now", `This level is ${a.status}.`);
    if (!isApproverOf(db, a)) return problem(403, "forbidden", "Not allowed", "You are not an approver of this level.");
    if (a.requester.id === db.me.user.id)
      return problem(403, "forbidden", "Not allowed", "You can't approve your own request.");
    if (a.votes.some((v) => v.user.id === db.me.user.id))
      return problem(409, "conflict", "Not possible right now", "You have already decided on this level.");
    const body = (await request.json()) as S["ApprovalDecision"];
    const comment = (body.comment ?? "").trim();
    if (body.decision === "reject" && !comment) {
      return problem(422, "validation", "Some inputs need attention", "Say why you reject it.", [
        { field: "/comment", code: "required", message: "Say why you reject it: the requester sees this comment." },
      ]);
    }
    if (body.decision === "reject" && Object.keys(body.inputs ?? {}).length > 0) {
      return problem(422, "validation", "Some inputs need attention", "Inputs can only be changed when approving.", [
        { field: "/inputs", code: "not_allowed", message: "Inputs can only be changed when approving." },
      ]);
    }
    const changes: S["ApprovalVote"]["changes"] = [];
    const editable = viewOf(db, a, stored.request).editable_fields;
    for (const [field, to] of Object.entries(body.inputs ?? {})) {
      const f = editable.find((x) => x.field === field);
      if (!f || (f.options && !f.options.some((o) => o.value === to))) {
        return problem(422, "validation", "Some inputs need attention", "Approvers can't set this value.", [
          {
            field: `/inputs/${field}`,
            code: "not_editable",
            message: "Approvers can't change this input to that value.",
          },
        ]);
      }
      if (to !== f.value) changes.push({ field, label: f.label, from: f.value, to });
    }
    a.votes.push(vote(db.me.user, body.decision, comment, changes));
    if (changes.length) {
      stored.request.inputs = { ...stored.request.inputs, ...Object.fromEntries(changes.map((c) => [c.field, c.to])) };
      const host = stored.request.title.split(" · ")[1] ?? null;
      for (const l of levelsOf(a.request.id)) l.facts = factsFor(stored.request.inputs ?? {}, host);
    }
    const now = new Date().toISOString();
    if (body.decision === "reject") {
      Object.assign(a, {
        status: "rejected",
        outcome: `Rejected by ${db.me.user.name}`,
        decided_at: now,
        due_at: null,
      });
      for (const l of levelsOf(a.request.id)) if (l.status === "waiting") l.status = "cancelled";
      rejectRequest(stored, a.outcome!, comment);
    } else if (
      a.mode === "any" ||
      a.approvers.every((u) => u.id === a.requester.id || a.votes.some((v) => v.user.id === u.id))
    ) {
      Object.assign(a, {
        status: "approved",
        outcome: `Approved by ${db.me.user.name}`,
        decided_at: now,
        due_at: null,
      });
      const next = levelsOf(a.request.id).find((l) => l.status === "waiting");
      if (next) {
        Object.assign(next, { status: "pending", due_at: new Date(Date.now() + 48 * 3_600_000).toISOString() });
        publishApproval(next);
      } else {
        const levels = levelsOf(a.request.id);
        const notes = levels.flatMap((l) =>
          l.votes.flatMap((v) => [
            ...(v.comment ? [`“${v.comment}”`] : []),
            ...v.changes.map((c) => `${c.label} changed from ${String(c.from)} to ${String(c.to)} by ${v.user.name}`),
          ]),
        );
        provisionApproved(stored, levels.map((l) => l.outcome).join("; "), notes.join(" · "));
      }
    }
    a.request.status = stored.request.status;
    publishApproval(a);
    return json<S["Approval"]>(viewOf(db, a, stored.request));
  }),

  http.post<{ approvalId: string }>(`${base}/approvals/:approvalId/delegate`, async ({ params, request }) => {
    await wait();
    const a = db.approvals.find((x) => x.id === params.approvalId);
    if (!a || !visibleApproval(a)) return notFound("Approval");
    if (a.status !== "pending") return problem(409, "conflict", "Not possible right now", `This level is ${a.status}.`);
    if (!isApproverOf(db, a)) return problem(403, "forbidden", "Not allowed", "Only an approver can delegate.");
    const body = (await request.json()) as S["ApprovalDelegation"];
    const to = db.users.find((u) => u.id === body.user_id);
    if (!to || to.id === a.requester.id) {
      return problem(422, "validation", "Some inputs need attention", "Pick another user.", [
        {
          field: "/user_id",
          code: "invalid",
          message: to ? "The requester can't approve their own request." : "There is no user with this ID.",
        },
      ]);
    }
    if (!a.delegates.some((u) => u.id === to.id)) a.delegates.push(to);
    publishApproval(a);
    return json<S["Approval"]>(viewOf(db, a, requestByKey(a.request.id)?.request));
  }),

  http.get<{ requestId: string }>(`${base}/requests/:requestId/approvals`, async ({ params }) => {
    await wait();
    const r = requestByKey(params.requestId);
    if (!r || !visibleRequest(r.request)) return notFound("Request");
    return json({ items: levelsOf(r.request.id).map((a) => viewOf(db, a, r.request)) });
  }),

  http.get(`${base}/deployments`, async ({ request }) => {
    await wait();
    const url = new URL(request.url);
    const projectId = url.searchParams.get("project_id");
    const statuses = list(url, "status") as DeploymentStatus[];
    const envs = list(url, "environment");
    const owner = url.searchParams.get("owner");
    const before = url.searchParams.get("lease_expires_before");
    const q = (url.searchParams.get("q") ?? "").trim().toLowerCase();
    const sort = url.searchParams.get("sort") ?? "name";
    const desc = sort.startsWith("-");
    const field = sort.replace(/^-/, "");
    const items = db.deployments
      .filter((d) => myProjectIds().has(d.project.id) && d.status !== "deleted")
      .filter((d) => !projectId || d.project.id === projectId)
      .filter((d) => statuses.length === 0 || statuses.includes(d.status))
      .filter((d) => envs.length === 0 || envs.includes(d.environment))
      .filter((d) => !owner || d.owner.id === (owner === "me" ? db.me.user.id : owner))
      .filter((d) => !before || (d.lease.expires_at !== null && d.lease.expires_at < before))
      .filter(
        (d) =>
          !q ||
          [
            d.name,
            d.owner.name,
            ...d.resources.map((r) => r.name),
            ...d.resources.flatMap((r) =>
              r.type === "vsphere.vm" ? r.vm.nics.flatMap((n) => n.ip_addresses ?? []) : [],
            ),
          ]
            .join(" ")
            .toLowerCase()
            .includes(q),
      )
      .sort((a, b) => {
        const av =
          field === "created_at"
            ? a.created_at
            : field === "lease_expires_at"
              ? (a.lease.expires_at ?? "9999")
              : a.name;
        const bv =
          field === "created_at"
            ? b.created_at
            : field === "lease_expires_at"
              ? (b.lease.expires_at ?? "9999")
              : b.name;
        return desc ? bv.localeCompare(av) : av.localeCompare(bv);
      })
      .map(summarize);
    return json<S["DeploymentPage"]>(page(items, url));
  }),

  http.get<{ deploymentId: string }>(`${base}/deployments/:deploymentId/actions`, async ({ params }) => {
    await wait();
    const dep = db.deployments.find((d) => d.id === params.deploymentId);
    if (!dep) return notFound("Deployment");
    return json<Ok<"/deployments/{deploymentId}/actions", "get">>({ items: actionsFor(db, dep) });
  }),

  http.post<{ deploymentId: string }>(`${base}/deployments/:deploymentId/actions`, async ({ params, request }) => {
    await wait();
    const key = request.headers.get("Idempotency-Key");
    if (!key) return problem(400, "bad-request", "Idempotency-Key header is required");
    const raw = await request.text();
    const seen = db.idempotency.get(key);
    if (seen) return json(seen.response, { status: seen.status });
    const dep = db.deployments.find((d) => d.id === params.deploymentId);
    if (!dep) return notFound("Deployment");
    const res = runAction(db, dep, JSON.parse(raw) as S["Day2Draft"], newRequestNumber());
    if (res.error) {
      const e = res.error;
      return problem(
        e.status,
        e.status === 422 ? "validation" : e.status === 403 ? "forbidden" : "conflict",
        e.title,
        e.detail,
        e.field ? [{ field: e.field, message: e.title }] : undefined,
      );
    }
    const stored = res.stored!;
    db.requests.unshift(stored);
    if (res.approval) {
      const levels = openLevels(db, stored.request, res.approval, null, () => `apr_${(db.seq.id++).toString(36)}`);
      db.approvals.push(...levels);
      levels.forEach(publishApproval);
    }
    publishRequest(stored);
    if (!res.approval) start(stored.request.id);
    db.idempotency.set(key, { body: raw, status: 202, response: structuredClone(stored.request) });
    return json<S["Request"]>(stored.request, { status: 202 });
  }),

  http.get<{ deploymentId: string }>(`${base}/deployments/:deploymentId/history`, async ({ params, request }) => {
    await wait();
    const dep = db.deployments.find((d) => d.id === params.deploymentId);
    if (!dep) return notFound("Deployment");
    const items = db.requests
      .map((r) => r.request)
      .filter(
        (r) =>
          r.deployment_ids?.includes(dep.id) ||
          r.id === dep.created_by_request_id ||
          r.number === dep.created_by_request_id,
      )
      .sort((a, b) => b.submitted_at.localeCompare(a.submitted_at));
    return json<S["RequestPage"]>(page(items, new URL(request.url)));
  }),

  http.post<{ deploymentId: string; resourceId: string }>(
    `${base}/deployments/:deploymentId/resources/:resourceId/console`,
    async ({ params }) => {
      await wait();
      const dep = db.deployments.find((d) => d.id === params.deploymentId);
      const vm = dep?.resources.find((r) => r.id === params.resourceId && r.type === "vsphere.vm");
      if (!dep || !vm || vm.type !== "vsphere.vm") return notFound("VM");
      if (vm.vm.power_state !== "on") return problem(409, "conflict", "The VM is powered off.", "Power it on first.");
      // No WMKS client in the mock: VMRC only, as without RVAV_WMKS_SDK_URLS.
      return json<S["ConsoleSession"]>({ vmrc_url: `vmrc://clone:mock-ticket@${vm.vm.vcenter}/?moid=${vm.vm.moref}` });
    },
  ),

  http.get<{ deploymentId: string }>(`${base}/deployments/:deploymentId`, async ({ params }) => {
    await wait();
    const d = db.deployments.find((x) => x.id === params.deploymentId && myProjectIds().has(x.project.id));
    return d ? json<S["DeploymentDetail"]>({ ...d, ...summarize(d) }) : notFound("Deployment");
  }),

  http.get(`${base}/events`, ({ request }) => {
    const url = new URL(request.url);
    const topics = list(url, "topics");
    const projectId = url.searchParams.get("project_id");
    const lastId = request.headers.get("Last-Event-ID");
    const encoder = new TextEncoder();
    let unsubscribe = () => {};
    let heartbeat: ReturnType<typeof setInterval> | undefined;
    const cleanup = () => {
      unsubscribe();
      clearInterval(heartbeat);
    };
    const stream = new ReadableStream<Uint8Array>({
      start(controller) {
        const send = (e: LiveEvent) => {
          try {
            controller.enqueue(encoder.encode(`id: ${e.id}\nevent: ${e.type}\ndata: ${JSON.stringify(e)}\n\n`));
          } catch {
            cleanup();
          }
        };
        const control = (type: "heartbeat" | "resync") =>
          send({ id: bus.nextId(), type, at: new Date().toISOString() });
        controller.enqueue(encoder.encode(": connected\n\n"));
        if (lastId) {
          const missed = bus.since(lastId);
          if (missed === null) control("resync");
          else missed.filter((env) => matches(env, topics, projectId, db.me.user.id)).forEach((env) => send(env.event));
        }
        unsubscribe = bus.subscribe((env) => {
          if (matches(env, topics, projectId, db.me.user.id)) send(env.event);
        });
        heartbeat = setInterval(() => control("heartbeat"), 15_000);
        request.signal.addEventListener("abort", () => {
          cleanup();
          try {
            controller.close();
          } catch {
            // already closed
          }
        });
      },
      cancel() {
        cleanup();
      },
    });
    return new HttpResponse(stream, {
      headers: { "Content-Type": "text/event-stream", "Cache-Control": "no-cache", "X-Request-Id": rid() },
    });
  }),
];
