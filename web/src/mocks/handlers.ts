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
import { bus, matches } from "./events";
import { demand, preview, resolveField, summarize, validateInputs, vmCount } from "./logic";
import { db, type StoredItem } from "./seed";
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
    const vms = vmCount(item, draft.count ?? 1);
    if (!approval && vms > 0 && host) {
      const production = db.environments.find((e) => e.key === draft.environment)?.production ?? false;
      const days = draft.lease_days ?? (production ? null : 30);
      const dep: DeploymentDetail = {
        id: `dep_${(db.seq.id++).toString(36)}`,
        name: host.replace(/\d+$/, ""),
        status: "provisioning",
        project: { id: project.id, code: project.code, name: project.name },
        environment: draft.environment,
        catalog_item: { id: item.id, slug: item.slug, name: item.name },
        version: item.version,
        owner: db.me.user,
        created_at: now,
        created_by_request_id: number,
        lease: {
          expires_at: days ? new Date(Date.now() + days * 86_400_000).toISOString() : null,
          extensions_used: 0,
          extensions_max: days ? 2 : null,
          expiry_action: "power_off_then_delete",
        },
        vm_count: 0,
        vms_powered_on: 0,
        primary_address: null,
        sync_state: "in_sync",
        inputs: draft.inputs,
        resources: [],
      };
      db.deployments.unshift(dep);
      deploymentIds.push(dep.id);
      const q = db.quota[project.id];
      if (q) {
        const need = demand(item, draft.inputs, draft.count ?? 1);
        for (const k of Object.keys(need) as (keyof typeof need)[]) q.used[k] += need[k];
      }
    }
    const req: ServiceRequest = {
      id,
      number,
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
    return json<S["Request"]>(r.request, { status: 202 });
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
          else missed.filter((env) => matches(env, topics, projectId)).forEach((env) => send(env.event));
        }
        unsubscribe = bus.subscribe((env) => {
          if (matches(env, topics, projectId)) send(env.event);
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
