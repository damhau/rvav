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
  checkAnswer,
  isApproverOf,
  openLevels,
  summaryOf,
  viewOf,
  vote,
  type StoredApproval,
} from "./approvals";
import { can } from "@/lib/can";
import { actionsFor, runAction, type RunResult } from "./day2";
import { previewUpdate, runUpdate } from "./update";
import { bus, matches } from "./events";
import {
  adminItemOf,
  catalogAdminHandlers,
  designerForm,
  draftFromRepository,
  itemFile,
  notMember,
  requestTopology,
} from "./catalogAdmin";
import { catalogGitHandlers } from "./catalogGit";
import { administrationHandlers, leasePolicyOf } from "./administration";
import { onboardingHandlers } from "./onboarding";
import { vraHandlers } from "./vra";
import { fileHandlers } from "./files";
import { reportHandlers } from "./reports";
import { governanceHandlers } from "./governance";
import { extensibilityHandlers } from "./extensibility";
import { notificationHandlers } from "./notifications";
import { signInHandlers } from "./signIn";
import { draftHandlers } from "./drafts";
import { sharedWith, sharingHandlers } from "./sharing";
import { listingHandlers, seesDeployment, visibleDeployments } from "./listing";
import { faults } from "./faults";
import { demand, preview, resolveField, ruled, summarize, vmCount } from "./logic";
import { db, type StoredItem, type StoredRequest } from "./seed";
import {
  cancel,
  newRequestNumber,
  planSteps,
  publishApproval,
  publishRequest,
  rememberHost,
  start,
  stepAnswered,
  stepRejected,
} from "./simulator";

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
  const me = db.me.user.id;
  return seesDeployment(db, {
    project: r.project,
    owner: { id: r.requester.id === me || r.on_behalf_of?.id === me ? me : r.requester.id },
  });
}

function title(item: StoredItem, host: string | null, inputs: Record<string, unknown>) {
  if (host) return `${item.name} · ${host}`;
  const first = Object.values(inputs).find((v) => typeof v === "string" && v.length > 0);
  return typeof first === "string" ? `${item.name} · ${first}` : item.name;
}

/** Inputs as the server shows them once kept: a secret input's value is ••••. */
function sealed(itemId: string, inputs: Record<string, unknown>): Record<string, unknown> {
  const secret = Object.entries(db.forms[itemId]?.ui_schema.fields ?? {}).filter(
    ([k, f]) => (f.widget === "secret" || f.secret) && inputs[k] !== undefined && inputs[k] !== null,
  );
  return secret.length ? { ...inputs, ...Object.fromEntries(secret.map(([k]) => [k, "••••"])) } : inputs;
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
  if (vmCount(item, count, inputs) === 0 || !host) return null;
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

/** Every level approved: the request is planned again with its inputs and starts. */
function provisionApproved(stored: StoredRequest, outcome: string, detail: string) {
  const r = stored.request;
  if (r.kind !== "provision") {
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
  db.approvals.filter((a) => a.request.id === requestId && !a.step_key).sort((a, b) => a.level.index - b.level.index);

/** The answer to a composite step's wait, as core.DecideApproval takes it: it goes on the step, not a level. */
function answerStep(a: StoredApproval, stored: StoredRequest, body: S["ApprovalDecision"]) {
  const form = a.question?.form ?? [];
  const refuse = (field: string, code: string, message: string) =>
    problem(422, "validation", "Some inputs need attention", message, [{ field, code, message }]);
  const comment = (body.comment ?? "").trim();
  if (body.answer && form.length === 0)
    return refuse("/answer", "not_allowed", "This asks to approve or reject: it has no form.");
  if (form.length > 0 && body.decision === "reject")
    return refuse("/decision", "not_allowed", "This asks a form: answer it.");
  if (body.decision === "reject" && !comment)
    return refuse("/comment", "required", "Say why you reject it: the requester sees this comment.");
  const { clean, errors } = checkAnswer(form, body.answer ?? {});
  if (errors.length > 0) return problem(422, "validation", "Some inputs need attention", errors[0]!.message, errors);
  const name = db.me.user.name;
  const rejected = body.decision === "reject";
  const outcome = rejected ? `Rejected by ${name}` : form.length > 0 ? `Answered by ${name}` : `Approved by ${name}`;
  a.votes.push(vote(db.me.user, body.decision, comment, []));
  Object.assign(a, {
    status: rejected ? "rejected" : "approved",
    outcome,
    decided_at: new Date().toISOString(),
    due_at: null,
  });
  if (a.question && form.length > 0) a.question.answer = clean;
  if (rejected) stepRejected(stored, a.step_key!, `${outcome}: ${comment}`);
  else
    // What later steps read of it (ADR-0033): a field left blank is its default, or null.
    stepAnswered(stored, a.step_key!, outcome, {
      answered: true,
      by: db.me.user.email ?? "",
      comment,
      answer: Object.fromEntries(form.map((f) => [f.key, clean[f.key] ?? f.default ?? null])),
    });
  publishApproval(a);
  return json<S["Approval"]>(viewOf(db, a, stored.request));
}

const visibleApproval = (a: StoredApproval) => isApproverOf(db, a) || myProjectIds().has(a.project.id);

/** A project as its GET answers: with what the caller holds there. */
function withMine(p: S["Project"]): S["Project"] {
  return { ...p, my_permissions: db.me.projects.find((x) => x.project.id === p.id)?.permissions ?? db.me.permissions };
}

/** The answer to a day-2 action or an update: its request, started unless it waits for approval. */
function submitted(key: string, raw: string, res: RunResult) {
  if (res.error) {
    const e = res.error;
    return problem(
      e.status,
      e.status === 422 ? "validation" : e.status === 403 ? "forbidden" : "conflict",
      e.title,
      e.detail,
      e.errors ?? (e.field ? [{ field: e.field, message: e.title }] : undefined),
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
  ...catalogGitHandlers(db, base, wait, {
    fileOf: (item, versionId) => itemFile(db, item, versionId),
    draftFromRepository: (item) => draftFromRepository(db, item),
    adminItem: (item) => adminItemOf(db, item),
  }),
  ...governanceHandlers(db, base, wait),
  ...administrationHandlers(db, base, wait),
  ...onboardingHandlers(db, base, wait),
  ...vraHandlers(db, base, wait),
  ...fileHandlers(db, base, wait),
  ...reportHandlers(db, base, wait),
  ...extensibilityHandlers(db, base, wait),
  ...notificationHandlers(db, base, wait),
  ...signInHandlers(base, wait),
  ...draftHandlers(db, base, wait),
  ...sharingHandlers(db, base, wait),
  ...listingHandlers(db, base, wait),

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
    const mine = db.projects.filter((p) => myProjectIds().has(p.id)).map(withMine);
    return json<S["ProjectPage"]>(page(mine, new URL(request.url)));
  }),

  http.get<{ projectId: string }>(`${base}/projects/:projectId`, async ({ params }) => {
    await wait();
    const p = projectById(params.projectId);
    return p ? json<S["Project"]>(withMine(p)) : notFound("Project");
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
    const items = db.items.filter((i) => projectId && sharedWith(db, i, projectId));
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
      .filter((i) => projectId && sharedWith(db, i, projectId))
      .filter((i) => !category || i.category === category)
      .filter((i) => !type || i.type === type)
      .filter((i) => !favourite || db.favourites.has(i.id))
      .filter((i) => !q || `${i.name} ${i.summary} ${i.source.awx_template_name ?? ""}`.toLowerCase().includes(q))
      .map(publicItem);
    return json<S["CatalogItemPage"]>(page(items, url));
  }),

  http.get<{ itemId: string }>(`${base}/catalog/items/:itemId`, async ({ params, request }) => {
    await wait();
    const item = itemByKey(params.itemId);
    const projectId = new URL(request.url).searchParams.get("project_id");
    if (item && projectId && item.projectIds.includes(projectId) && !sharedWith(db, item, projectId))
      return notFound("Catalog item");
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
    if (!can(db.me, "request.submit", projectId ?? ""))
      return problem(
        403,
        "forbidden",
        "You can't request items in this project",
        "Your roles there don't let you request.",
      );
    if (projectId && item.projectIds.includes(projectId) && !sharedWith(db, item, projectId))
      return notFound("Catalog item");
    return json<S["RequestForm"]>(db.forms[item.id]!);
  }),

  http.post(`${base}/forms/values`, async ({ request }) => {
    await wait();
    // `environment` is not in FieldValuesQuery yet (spec gap); the web client sends it anyway.
    const body = (await request.json()) as S["FieldValuesQuery"] & { environment?: EnvironmentKey };
    if (faults.fieldValuesFail(body.field))
      return problem(
        500,
        "internal",
        "Something went wrong on our side",
        "The error was logged. Quote the request ID if you contact support.",
      );
    const project = projectById(body.project_id);
    // The form designer resolves any version, as the user it previews as.
    const designer =
      body.as_user_id && project ? designerForm(db, body.version_id, project.code, body.as_user_id) : undefined;
    if (designer && !designer.member) return notMember(db, designer.user.id);
    const item = designer?.item ?? db.items.find((i) => i.version.id === body.version_id);
    if (!item || !project) return notFound("Form");
    const env = body.environment ?? project.environments[0] ?? "dev";
    let result: S["FieldValues"] | null;
    try {
      result = resolveField(db, item, project, env, body.field, body.inputs ?? {}, designer, body.search);
    } catch (e) {
      return problem(400, "bad-request", `The expression failed: ${String(e)}`);
    }
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
    const validate = new URL(request.url).searchParams.get("validate") === "true";
    return json<S["RequestPreview"]>(preview(db, item, project, draft, validate));
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
    if (!can(db.me, "request.submit", project.id)) {
      return problem(403, "forbidden", "You can't request items in this project");
    }
    if (item.projectIds.includes(project.id) && !sharedWith(db, item, project.id)) {
      return problem(
        403,
        "forbidden",
        "You can't request this item here",
        `${item.name} is shared in ${project.name} with chosen people, and you aren't one of them.`,
      );
    }
    const check = preview(db, item, project, draft, true);
    // As the server: the inputs after the rules, so a field they hide isn't checked.
    const errors = check.errors.filter(
      (e) =>
        e.field.startsWith("/inputs/") ||
        e.field.startsWith("/validations/") ||
        ["", "/environment", "/count", "/scheduled_at"].includes(e.field),
    );
    if (errors.length) {
      return problem(
        422,
        "validation",
        "Some values need fixing",
        "Fix the highlighted fields and submit again.",
        errors,
      );
    }
    const warnings = check.warnings ?? [];
    if (warnings.length && !draft.accept_warnings)
      return problem(
        422,
        "validation",
        "Some values need fixing",
        "A policy warns: submit again to go on.",
        warnings.map((w) => ({ field: "/accept_warnings", code: "warning", message: w.message })),
      );

    // Kept as the server keeps them: without the inputs of fields the rules hide, the secret ones sealed.
    const inputs = sealed(item.id, ruled(db, item, project, draft).inputs);
    const approval = check.approval.policies[0]?.reason ?? null;
    const later = !approval && draft.scheduled_at && new Date(draft.scheduled_at) > new Date();
    const behalf = draft.on_behalf_of ? db.users.find((u) => u.id === draft.on_behalf_of) : undefined;
    const number = newRequestNumber();
    const id = `req_${number.slice(4)}`;
    const { steps, host } = planSteps(item, project, draft.environment, inputs, approval);
    const now = new Date().toISOString();
    const deploymentIds: string[] = [];
    const dep =
      approval || later
        ? null
        : createDeployment(item, project, draft.environment, inputs, draft.count ?? 1, host, number, draft.lease_days);
    if (dep) deploymentIds.push(dep.id);
    const req: ServiceRequest = {
      id,
      number,
      kind: "provision",
      title: title(item, host, inputs),
      status: approval ? "pending_approval" : later ? "scheduled" : "running",
      catalog_item: { id: item.id, slug: item.slug, name: item.name, type: item.type },
      version: item.version,
      project: { id: project.id, code: project.code, name: project.name },
      environment: draft.environment,
      requester: db.me.user,
      ...(behalf ? { on_behalf_of: behalf } : {}),
      inputs,
      count: draft.count ?? 1,
      justification: draft.justification,
      submitted_at: now,
      scheduled_at: draft.scheduled_at ?? null,
      started_at: approval || later ? null : now,
      finished_at: null,
      deployment_ids: deploymentIds,
      current_step: null,
      progress: { done: 0, total: steps.length },
      error: null,
      ...(warnings.length ? { warnings } : {}),
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
    if (!approval && !later) start(id);
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

  http.get<{ requestId: string }>(`${base}/requests/:requestId/topology`, async ({ params }) => {
    await wait();
    const r = requestByKey(params.requestId);
    return r && visibleRequest(r.request)
      ? json<S["RequestTopology"]>(requestTopology(db, r.request))
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
        // A composite step undone runs again: its undo goes (ADR-0022).
        step.children = step.children.filter((c) => !c.key.endsWith("/undo"));
        if (step.detail === "Undone." || step.detail === "Its side of the branch wasn't taken.")
          step.detail = undefined;
        Object.assign(step, { status: "pending", started_at: null, finished_at: null, error: undefined, due_at: null });
        // Rolled back, a branch chooses again and a message is sent again (ADR-0033).
        if (step.system === "branch") Object.assign(step, { branch_case: null, detail: "Chooses which side runs" });
        if (step.detail === "Message sent.") step.detail = "Sends a message";
        step.children.forEach((c) => Object.assign(c, { status: "pending", started_at: null, finished_at: null }));
      }
    }
    // The mock's simulator only advances running requests (the server answers queued).
    Object.assign(r.request, { status: "running", error: null, finished_at: null });
    if (d && r.request.kind !== "day2") d.status = r.request.kind === "update" ? "updating" : "provisioning";
    publishRequest(r);
    start(r.request.id);
    return json<S["Request"]>(r.request, { status: 202 });
  }),

  http.post<{ requestId: string }>(`${base}/requests/:requestId/rollback`, async ({ params }) => {
    await wait();
    const r = requestByKey(params.requestId);
    if (!r || !visibleRequest(r.request)) return notFound("Request");
    if (r.request.status !== "failed" || r.request.kind !== "provision")
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
    const statuses = list(url, "status");
    const wanted = statuses.length ? statuses : ["pending"];
    if (url.searchParams.get("view") === "all") {
      if (!can(db.me, "approval.read_all"))
        return problem(
          403,
          "forbidden",
          "Not allowed",
          "Seeing every approval needs approval.read_all for all of rvav.",
        );
      const all = db.approvals.filter((a) => wanted.includes(a.status)).map((a) => summaryOf(db, a));
      return json<S["ApprovalPage"]>({ ...page(all, url), total: all.length });
    }
    const open = (a: StoredApproval) => a.status === "pending" || a.status === "waiting";
    const voted = (a: StoredApproval) => a.votes.some((v) => v.user.id === db.me.user.id);
    const mine = db.approvals.filter(
      (a) =>
        wanted.includes(a.status) &&
        (a.requester.id !== db.me.user.id || Boolean(a.step_key)) &&
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
    if (a.requester.id === db.me.user.id && !a.step_key)
      return problem(403, "forbidden", "Not allowed", "You can't approve your own request.");
    if (a.votes.some((v) => v.user.id === db.me.user.id))
      return problem(409, "conflict", "Not possible right now", "You have already decided on this level.");
    const body = (await request.json()) as S["ApprovalDecision"];
    if (a.step_key) return answerStep(a, stored, body);
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
    if (a.step_key)
      return problem(
        403,
        "forbidden",
        "Not allowed",
        "Only those a step names may answer it, and it can't be handed on.",
      );
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
    // Its levels, then what its composite's steps asked (ADR-0033).
    const asked = db.approvals.filter((a) => a.request.id === r.request.id && a.step_key);
    return json({ items: [...levelsOf(r.request.id), ...asked].map((a) => viewOf(db, a, r.request)) });
  }),

  http.get(`${base}/deployments`, async ({ request }) => {
    await wait();
    const url = new URL(request.url);
    const projectId = url.searchParams.get("project_id");
    const wanted = list(url, "status");
    const expired = wanted.includes("expired");
    const statuses = wanted.filter((s) => s !== "expired") as DeploymentStatus[];
    const envs = list(url, "environment");
    const itemKeys = list(url, "item");
    const templates = list(url, "template");
    const owner = url.searchParams.get("owner");
    const before = url.searchParams.get("lease_expires_before");
    const q = (url.searchParams.get("q") ?? "").trim().toLowerCase();
    const sort = url.searchParams.get("sort") ?? "name";
    const desc = sort.startsWith("-");
    const field = sort.replace(/^-/, "");
    const now = new Date().toISOString();
    const key = (d: DeploymentDetail): string => {
      switch (field) {
        case "created_at":
          return d.created_at;
        case "lease_expires_at":
          return d.lease.expires_at ?? "9999";
        case "status":
          return d.status;
        case "environment":
          return d.environment;
        case "owner":
          return d.owner.name;
        case "item":
          return d.catalog_item.name;
        case "project":
          return d.project.name;
        case "vm_count":
          return String(d.resources.filter((r) => r.type === "vsphere.vm").length).padStart(6, "0");
        default:
          return d.name;
      }
    };
    const items = visibleDeployments(db, projectId)
      .filter(
        (d) =>
          (statuses.length === 0 && !expired) ||
          statuses.includes(d.status) ||
          (expired && d.lease.expires_at !== null && d.lease.expires_at <= now),
      )
      .filter((d) => envs.length === 0 || envs.includes(d.environment))
      .filter(
        (d) => itemKeys.length === 0 || itemKeys.includes(d.catalog_item.slug) || itemKeys.includes(d.catalog_item.id),
      )
      .filter(
        (d) =>
          templates.length === 0 ||
          d.resources.some((r) => r.type === "vsphere.vm" && templates.includes(r.vm.template ?? "")),
      )
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
        const byKey = key(a).localeCompare(key(b));
        return (field === "name" || byKey !== 0 ? byKey : a.name.localeCompare(b.name)) * (desc ? -1 : 1);
      })
      .map(summarize);
    return json<S["DeploymentPage"]>(page(items, url));
  }),

  http.get<{ deploymentId: string }>(`${base}/deployments/:deploymentId/actions`, async ({ params }) => {
    await wait();
    const dep = db.deployments.find((d) => d.id === params.deploymentId && seesDeployment(db, d));
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
    const dep = db.deployments.find((d) => d.id === params.deploymentId && seesDeployment(db, d));
    if (!dep) return notFound("Deployment");
    return submitted(key, raw, runAction(db, dep, JSON.parse(raw) as S["Day2Draft"], newRequestNumber()));
  }),

  http.post<{ deploymentId: string }>(
    `${base}/deployments/:deploymentId/update/preview`,
    async ({ params, request }) => {
      await wait();
      const dep = db.deployments.find((d) => d.id === params.deploymentId && seesDeployment(db, d));
      if (!dep) return notFound("Deployment");
      const res = previewUpdate(db, dep, (await request.json()) as S["UpdateDraft"]);
      if (res.error) return problem(res.error.status, "conflict", res.error.title);
      return json<S["UpdatePreview"]>(res.preview!);
    },
  ),

  http.post<{ deploymentId: string }>(`${base}/deployments/:deploymentId/update`, async ({ params, request }) => {
    await wait();
    const key = request.headers.get("Idempotency-Key");
    if (!key) return problem(400, "bad-request", "Idempotency-Key header is required");
    const raw = await request.text();
    const seen = db.idempotency.get(key);
    if (seen) return json(seen.response, { status: seen.status });
    const dep = db.deployments.find((d) => d.id === params.deploymentId && seesDeployment(db, d));
    if (!dep) return notFound("Deployment");
    return submitted(key, raw, runUpdate(db, dep, JSON.parse(raw) as S["UpdateDraft"], newRequestNumber()));
  }),

  http.get<{ deploymentId: string }>(`${base}/deployments/:deploymentId/history`, async ({ params, request }) => {
    await wait();
    const dep = db.deployments.find((d) => d.id === params.deploymentId && seesDeployment(db, d));
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
      const dep = db.deployments.find((d) => d.id === params.deploymentId && seesDeployment(db, d));
      const vm = dep?.resources.find((r) => r.id === params.resourceId && r.type === "vsphere.vm");
      if (!dep || !vm || vm.type !== "vsphere.vm") return notFound("VM");
      if (vm.vm.power_state !== "on") return problem(409, "conflict", "The VM is powered off.", "Power it on first.");
      // No WMKS client in the mock: VMRC only, as without RVAV_WMKS_SDK_URLS.
      return json<S["ConsoleSession"]>({ vmrc_url: `vmrc://clone:mock-ticket@${vm.vm.vcenter}/?moid=${vm.vm.moref}` });
    },
  ),

  http.post<{ deploymentId: string }>(`${base}/deployments/:deploymentId/private-key`, async ({ params }) => {
    await wait();
    const d = db.deployments.find((x) => x.id === params.deploymentId && seesDeployment(db, x));
    const k = d?.generated_key;
    if (!d || !k) return notFound("Generated key");
    if (d.owner.id !== db.me.user.id)
      return problem(
        403,
        "forbidden",
        "Not allowed",
        `Only the deployment's owner, ${d.owner.name}, can download its private key.`,
      );
    if (!k.available)
      return problem(
        409,
        "conflict",
        "Not possible right now",
        "The private key was downloaded, and rvav no longer has it.",
      );
    Object.assign(k, { available: false, downloaded_at: new Date().toISOString(), downloaded_by: db.me.user });
    // Not a key: the mock's stand-in, its markers put together so no scanner takes it for one.
    const marker = (edge: string) => `-----${edge} OPENSSH ${"PRIVATE"} KEY-----`;
    const body =
      "b3BlbnNzaC1rZXktdjEAAAAABG5vbmUAAAAEbm9uZQAAAAAAAAABAAAAMwAAAAtzc2gtZWQyNTUxOQ\nbW9jay1vbmx5LW5vdC1hLXJlYWwta2V5LWZvci10aGUtcnZhdi1tb2NrLXNlcnZlcg==";
    return json<S["PrivateKey"]>({
      private_key: `${marker("BEGIN")}\n${body}\n${marker("END")}\n`,
      username: k.username,
      fingerprint: k.fingerprint,
    });
  }),

  http.get<{ deploymentId: string }>(`${base}/deployments/:deploymentId`, async ({ params }) => {
    await wait();
    const d = db.deployments.find((x) => x.id === params.deploymentId && seesDeployment(db, x));
    if (!d) return notFound("Deployment");
    const policy = d.lease.expires_at ? leasePolicyOf(db, d.project.id, d.environment) : undefined;
    const lease = policy ? { ...d.lease, enforcement: policy.enforcement ?? "hard" } : d.lease;
    return json<S["DeploymentDetail"]>({ ...d, ...summarize(d), lease });
  }),

  http.get(`${base}/events`, ({ request }) => {
    const url = new URL(request.url);
    const topics = list(url, "topics");
    const projectId = url.searchParams.get("project_id");
    const lastId = request.headers.get("Last-Event-ID");
    const seesAll = (id: string) => seesDeployment(db, { project: { id }, owner: { id: "" } });
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
          else
            missed
              .filter((env) => matches(env, topics, projectId, db.me.user.id, seesAll))
              .forEach((env) => send(env.event));
        }
        unsubscribe = bus.subscribe((env) => {
          if (matches(env, topics, projectId, db.me.user.id, seesAll)) send(env.event);
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
