// Deployment limit policies (ADR-0047), as the server's deployment_limits.go:
// each limit decided on its own by priority, every hard global policy holding
// on top; and what they add to previews and What applies here.
import { http, HttpResponse } from "msw";
import type { components } from "@/lib/api/schema";
import { audited, auditTargetNames, policyNameTooLong } from "./governance";
import type { Db } from "./seed";
import { warning } from "./warnings";

type S = components["schemas"];
type Policy = S["DeploymentLimitPolicy"];
type Kind = S["DeploymentLimitKind"];
type Choice = { pol: Policy; limit: number };

export const limitKinds: Kind[] = [
  "deployments_per_user",
  "deployments_per_project",
  "vcpu_per_deployment",
  "memory_gb_per_deployment",
];

const kindNames: Record<Kind, string> = {
  deployments_per_user: "deployments per user",
  deployments_per_project: "deployments per project",
  vcpu_per_deployment: "vCPU per deployment",
  memory_gb_per_deployment: "memory per deployment",
};

const isCount = (k: Kind) => k === "deployments_per_user" || k === "deployments_per_project";

auditTargetNames("deployment_limit_policy", (db, id) => limitState(db).policies.find((p) => p.id === id)?.name);

export function limitState(db: Db): { policies: Policy[]; seq: number } {
  const d = db as Db & { limits?: { policies: Policy[]; seq: number } };
  return (d.limits ??= { policies: [], seq: 0 });
}

/** In the order they are tried: by priority, global ones first on a tie, then by name. */
const ordered = (list: Policy[]) =>
  [...list].sort(
    (a, b) =>
      (a.priority ?? 100) - (b.priority ?? 100) ||
      Number(Boolean(a.project_id)) - Number(Boolean(b.project_id)) ||
      a.name.localeCompare(b.name) ||
      a.id.localeCompare(b.id),
  );

const appliesHere = (p: Policy, projectId: string, env: string) =>
  p.enabled && (!p.project_id || p.project_id === projectId) && (!p.environment || p.environment === env);

const joinNames = (n: string[]) => (n.length < 2 ? (n[0] ?? "") : `${n.slice(0, -1).join(", ")} and ${n.at(-1)}`);

/** As the server's limitsHere: per limit, the deciding policy first, then every other hard global one that sets it. */
export function limitsHere(db: Db, projectId: string, env: string) {
  const all = ordered(limitState(db).policies);
  const held: Partial<Record<Kind, Choice[]>> = {};
  for (const k of limitKinds) {
    for (const pol of all) {
      const v = pol[k];
      if (v == null || !appliesHere(pol, projectId, env)) continue;
      const list = (held[k] ??= []);
      if (list.length === 0 || (!pol.project_id && pol.enforcement !== "soft")) list.push({ pol, limit: v });
    }
  }
  return { all, held };
}

/** As the server's limitReasons: what each policy holding a limit decides, given the one shown per limit. */
function limitReasons(held: Partial<Record<Kind, Choice[]>>, shown: Partial<Record<Kind, Choice>>) {
  const decided: Record<string, string[]> = {};
  const gave: Record<string, string[]> = {};
  const also: Record<string, string[]> = {};
  const order: string[] = [];
  for (const k of limitKinds) {
    (held[k] ?? []).forEach((c, i) => {
      const id = c.pol.id;
      if (!order.includes(id)) order.push(id);
      if (shown[k]?.pol.id === id) (decided[id] ??= []).push(kindNames[k]);
      else if (i === 0)
        (gave[id] ??= []).push(`Its ${kindNames[k]} gives way to the global ${shown[k]!.pol.name}, hard and stricter.`);
      else (also[id] ??= []).push(kindNames[k]);
    });
  }
  const why: Record<string, string> = {};
  for (const id of order) {
    const a = also[id] ?? [];
    why[id] = [
      ...(decided[id] ? [`Decides ${joinNames(decided[id])}.`] : []),
      ...(gave[id] ?? []),
      ...(a.length
        ? [
            `Its ${joinNames(a)} ${a.length === 1 ? "holds" : "hold"} too: a hard global limit applies on top of the policy that decides.`,
          ]
        : []),
    ].join(" ");
  }
  return why;
}

/** A project's deployments not deleted, in env when given, and those ownerId owns. */
function counted(db: Db, projectId: string, ownerId: string, env?: string | null) {
  const here = db.deployments.filter(
    (d) => d.project.id === projectId && d.status !== "deleted" && (!env || d.environment === env),
  );
  return { all: here.length, owned: here.filter((d) => d.owner.id === ownerId).length };
}

export type LimitScope = {
  project: { id: string; name: string };
  env: { key: string; name: string };
  owner: { id: string; name: string };
  you: boolean;
};

export type LimitAsk = {
  toOwner: number;
  toProject: number;
  vcpu: number;
  memory: number;
  addVcpu: number;
  addMemory: number;
  many?: boolean;
};

const deployments = (n: number) => (n === 1 ? "1 deployment" : `${n} deployments`);

/** As the server's checkLimits: one line per limit, the hard limits passed in words, and the soft ones' warnings. */
export function limitCheck(db: Db, sc: LimitScope, ask: LimitAsk) {
  const { held } = limitsHere(db, sc.project.id, sc.env.key);
  const lines: S["DeploymentLimitLine"][] = [];
  const hard: string[] = [];
  const soft: S["PolicyWarning"][] = [];
  for (const k of limitKinds) {
    const here: S["DeploymentLimitLine"][] = [];
    let refused = false;
    for (const c of held[k] ?? []) {
      const n = counted(db, sc.project.id, sc.owner.id, c.pol.environment);
      const [value, add] = {
        deployments_per_user: [n.owned + ask.toOwner, ask.toOwner],
        deployments_per_project: [n.all + ask.toProject, ask.toProject],
        vcpu_per_deployment: [ask.vcpu, ask.addVcpu],
        memory_gb_per_deployment: [ask.memory, ask.addMemory],
      }[k] as [number, number];
      if (isCount(k) ? add === 0 : value === 0) continue;
      const exceeds = add > 0 && value > c.limit;
      here.push({
        kind: k,
        limit: c.limit,
        value,
        exceeds,
        enforcement: c.pol.enforcement ?? "hard",
        policy: c.pol.name,
        ...(c.pol.environment ? { environment: c.pol.environment } : {}),
      });
      if (!exceeds) continue;
      const isSoft = c.pol.enforcement === "soft";
      const where = c.pol.environment ? `${sc.project.name} (${sc.env.name})` : sc.project.name;
      const may = isSoft ? "should" : "may";
      const which = ask.many ? "the largest" : "this one";
      const msg = {
        deployments_per_user: `Each user ${may} have at most ${deployments(c.limit)} in ${where} under ${c.pol.name}, and ${sc.you ? "you" : sc.owner.name} would have ${value}.`,
        deployments_per_project: `${where} ${may} have at most ${deployments(c.limit)} under ${c.pol.name}, and it would have ${value}.`,
        vcpu_per_deployment: `Deployments in ${where} ${may} have at most ${c.limit} vCPU under ${c.pol.name}, and ${which} would have ${value}.`,
        memory_gb_per_deployment: `Deployments in ${where} ${may} have at most ${c.limit} GB of memory under ${c.pol.name}, and ${which} would have ${value}.`,
      }[k];
      if (isSoft) {
        soft.push(warning(c.pol.name, msg));
        continue;
      }
      if (refused) continue;
      refused = true;
      const whom = !c.pol.project_id || c.pol.set_by_admin ? "an administrator" : "a project admin";
      hard.push(`${msg} ${isCount(k) ? "Delete one" : "Choose a smaller size"}, or ask ${whom} to raise the limit.`);
    }
    if (here.length) lines.push(bindingLine(here));
  }
  return { lines, hard, soft };
}

/** As the server's bindingLine: the first hard limit gone over, else the first soft one, else the least room left. */
function bindingLine(lines: S["DeploymentLimitLine"][]) {
  const rank = (l: S["DeploymentLimitLine"]) => (l.exceeds ? (l.enforcement === "soft" ? 1 : 0) : 2);
  return lines.reduce((best, l) => {
    const r = rank(l);
    const b = rank(best);
    return r < b || (r === b && r === 2 && l.limit - l.value < best.limit - best.value) ? l : best;
  });
}

/** What applies here's limits, counting what userId owns: of those held, the one with the least room left. */
export function explainLimits(db: Db, projectId: string, env: string, userId: string): S["PolicyLimits"] {
  const { all, held } = limitsHere(db, projectId, env);
  const used = (k: Kind, c: Choice) => {
    const n = counted(db, projectId, userId, c.pol.environment);
    return k === "deployments_per_user" ? n.owned : k === "deployments_per_project" ? n.all : undefined;
  };
  const lines: S["PolicyLimitLine"][] = [];
  const shown: Partial<Record<Kind, Choice>> = {};
  for (const k of limitKinds) {
    const list = held[k];
    if (!list?.length) continue;
    const room = (c: Choice) => c.limit - (used(k, c) ?? 0);
    const c = list.reduce((best, c) => (room(c) < room(best) ? c : best));
    shown[k] = c;
    const u = used(k, c);
    lines.push({
      kind: k,
      limit: c.limit,
      enforcement: c.pol.enforcement ?? "hard",
      policy: c.pol.name,
      ...(c.pol.environment ? { environment: c.pol.environment } : {}),
      ...(u === undefined ? {} : { used: u }),
    });
  }
  const why = limitReasons(held, shown);
  const considered = all
    .filter((p) => db.me.permissions.includes("policies.read") || !p.project_id || p.project_id === projectId)
    .map((p) => {
      const base = { id: p.id, name: p.name };
      if (!p.enabled) return { ...base, applies: false, why: "Disabled." };
      if (p.project_id && p.project_id !== projectId)
        return { ...base, applies: false, why: "Only for another project." };
      if (p.environment && p.environment !== env) return { ...base, applies: false, why: `Only for ${p.environment}.` };
      if (why[p.id] !== undefined) return { ...base, applies: true, why: why[p.id]! };
      return { ...base, applies: false, why: "A policy tried before it decides each of its limits." };
    });
  return { lines, considered };
}

function holdsIn(db: Db, projectId: string, permission: string): boolean {
  return (
    db.me.permissions.includes(permission) ||
    Boolean(db.me.projects.find((p) => p.project.id === projectId)?.permissions.includes(permission))
  );
}

function problem(status: number, title: string, errors?: S["FieldError"][], detail?: string) {
  return HttpResponse.json(
    { type: "https://rvav.dev/problems/x", title, status, request_id: "req_mock", ...(errors ? { errors } : {}), ...(detail ? { detail } : {}) }, // prettier-ignore
    { status, headers: { "Content-Type": "application/problem+json" } },
  );
}

export function limitHandlers(db: Db, base: string, wait: () => Promise<void>) {
  const st = () => limitState(db);
  const global = () => db.me.permissions.includes("policies.manage");
  const anywhere = () => global() || db.me.projects.some((p) => p.permissions.includes("project.policies.manage"));
  const shows = (projectId?: string | null) =>
    db.me.permissions.includes("policies.read") || !projectId || holdsIn(db, projectId, "project.policies.manage");
  const writes = (projectId?: string | null) =>
    global() || (Boolean(projectId) && holdsIn(db, projectId!, "project.policies.manage"));
  const changes = (p: Policy) => global() || (!p.set_by_admin && writes(p.project_id));
  const forbidden = () => problem(403, "Not allowed");
  const save = (id: string | null, w: S["DeploymentLimitPolicyWrite"]) => {
    if (!anywhere()) return forbidden();
    const unknown = Boolean(w.project_id) && !db.projects.some((p) => p.id === w.project_id);
    if (!global() && (unknown || !writes(w.project_id))) return forbidden();
    const was = id ? st().policies.find((p) => p.id === id && shows(p.project_id)) : undefined;
    if (id && !was) return problem(404, "Not found");
    if (was && !changes(was)) return forbidden();
    const errors: S["FieldError"][] = [];
    if (unknown) errors.push({ field: "/project_id", code: "unknown", message: "There is no such project." });
    const long = policyNameTooLong(w.name);
    if (!w.name.trim()) errors.push({ field: "/name", code: "required", message: "Give the policy a name." });
    else if (long) errors.push(long);
    if (w.environment && !db.environments.some((e) => e.key === w.environment))
      errors.push({ field: "/environment", code: "unknown", message: `"${w.environment}" is not an environment.` });
    let set = false;
    for (const k of limitKinds) {
      const v = w[k];
      if (v == null) continue;
      set = true;
      const least = isCount(k) ? 0 : 1;
      if (v < least) errors.push({ field: `/${k}`, code: "minimum", message: `${least} or more.` });
    }
    if (!set)
      errors.push({
        field: "",
        code: "required",
        message: "Set at least one limit: deployments per user or per project, or vCPU or memory per deployment.",
      });
    if (errors.length) return problem(422, "Some inputs need attention", errors);
    const pol: Policy = {
      id: id ?? `dlp_${++st().seq}`,
      name: w.name.trim(),
      project_id: w.project_id ?? null,
      environment: w.environment || null,
      priority: w.priority ?? 100,
      enabled: w.enabled ?? true,
      enforcement: w.enforcement ?? "hard",
      deployments_per_user: w.deployments_per_user ?? null,
      deployments_per_project: w.deployments_per_project ?? null,
      vcpu_per_deployment: w.vcpu_per_deployment ?? null,
      memory_gb_per_deployment: w.memory_gb_per_deployment ?? null,
      set_by_admin: global(),
    };
    st().policies = [...st().policies.filter((p) => p.id !== pol.id), pol];
    audited(db, `policy.limit.${id ? "update" : "create"}`, "deployment_limit_policy", pol.id);
    return HttpResponse.json(pol, { status: id ? 200 : 201 });
  };
  return [
    http.get(`${base}/admin/deployment-limit-policies`, async () => {
      await wait();
      if (!db.me.permissions.includes("policies.read") && !anywhere()) return forbidden();
      return HttpResponse.json({ items: ordered(st().policies).filter((p) => shows(p.project_id)) });
    }),
    http.post(`${base}/admin/deployment-limit-policies`, async ({ request }) => {
      await wait();
      return save(null, (await request.json()) as S["DeploymentLimitPolicyWrite"]);
    }),
    http.put(`${base}/admin/deployment-limit-policies/:id`, async ({ params, request }) => {
      await wait();
      return save(String(params.id), (await request.json()) as S["DeploymentLimitPolicyWrite"]);
    }),
    http.delete(`${base}/admin/deployment-limit-policies/:id`, async ({ params }) => {
      await wait();
      if (!anywhere()) return forbidden();
      const was = st().policies.find((p) => p.id === params.id && shows(p.project_id));
      if (!was) return problem(404, "Not found");
      if (!changes(was)) return forbidden();
      st().policies = st().policies.filter((p) => p.id !== was.id);
      audited(db, "policy.limit.delete", "deployment_limit_policy", was.id, {}, was.name);
      return new HttpResponse(null, { status: 204 });
    }),
  ];
}
