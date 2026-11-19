// Deployment limit policies (ADR-0047), as the server's deployment_limits.go:
// each limit decided on its own by priority, a hard stricter global policy
// winning over a project's; and what they add to previews and What applies here.
import { http, HttpResponse } from "msw";
import type { components } from "@/lib/api/schema";
import { audited } from "./governance";
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

/** As the server's limitsHere: the policy deciding each limit, and why each policy decides what it does. */
export function limitsHere(db: Db, projectId: string, env: string) {
  const all = ordered(limitState(db).policies);
  const chosen: Partial<Record<Kind, Choice>> = {};
  const gave: Record<string, string[]> = {};
  for (const k of limitKinds) {
    let first: Choice | undefined;
    for (const pol of all) {
      const v = pol[k];
      if (v == null || !appliesHere(pol, projectId, env)) continue;
      if (!first) {
        first = { pol, limit: v };
        if (!pol.project_id) break;
        continue;
      }
      if (pol.project_id) continue;
      const stricter = v < first.limit || (v === first.limit && first.pol.enforcement === "soft");
      if (pol.enforcement !== "soft" && stricter) {
        (gave[first.pol.id] ??= []).push(`Its ${kindNames[k]} gives way to the global ${pol.name}, hard and stricter.`);
        first = { pol, limit: v };
      }
      break;
    }
    if (first) chosen[k] = first;
  }
  const decided: Record<string, string[]> = {};
  for (const k of limitKinds) {
    const c = chosen[k];
    if (c) (decided[c.pol.id] ??= []).push(kindNames[k]);
  }
  const why: Record<string, string> = {};
  for (const pol of all) {
    const parts = [...(decided[pol.id] ? [`Decides ${joinNames(decided[pol.id]!)}.`] : []), ...(gave[pol.id] ?? [])];
    if (parts.length) why[pol.id] = parts.join(" ");
  }
  return { all, chosen, why, decides: (id: string) => Boolean(decided[id]) };
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

/** As the server's checkLimits: the lines, the hard limits passed in words, and the soft ones' warnings. */
export function limitCheck(db: Db, sc: LimitScope, ask: LimitAsk) {
  const { chosen } = limitsHere(db, sc.project.id, sc.env.key);
  const lines: S["DeploymentLimitLine"][] = [];
  const hard: string[] = [];
  const soft: S["PolicyWarning"][] = [];
  for (const k of limitKinds) {
    const c = chosen[k];
    if (!c) continue;
    const n = counted(db, sc.project.id, sc.owner.id, c.pol.environment);
    const [value, add] = {
      deployments_per_user: [n.owned + ask.toOwner, ask.toOwner],
      deployments_per_project: [n.all + ask.toProject, ask.toProject],
      vcpu_per_deployment: [ask.vcpu, ask.addVcpu],
      memory_gb_per_deployment: [ask.memory, ask.addMemory],
    }[k] as [number, number];
    if (isCount(k) ? add === 0 : value === 0) continue;
    const exceeds = add > 0 && value > c.limit;
    lines.push({
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
    const whom = !c.pol.project_id || c.pol.set_by_admin ? "an administrator" : "a project admin";
    hard.push(`${msg} ${isCount(k) ? "Delete one" : "Choose a smaller size"}, or ask ${whom} to raise the limit.`);
  }
  return { lines, hard, soft };
}

/** What applies here's limits, counting what userId owns. */
export function explainLimits(db: Db, projectId: string, env: string, userId: string): S["PolicyLimits"] {
  const { all, chosen, why, decides } = limitsHere(db, projectId, env);
  const lines: S["PolicyLimitLine"][] = [];
  for (const k of limitKinds) {
    const c = chosen[k];
    if (!c) continue;
    const n = counted(db, projectId, userId, c.pol.environment);
    lines.push({
      kind: k,
      limit: c.limit,
      enforcement: c.pol.enforcement ?? "hard",
      policy: c.pol.name,
      ...(c.pol.environment ? { environment: c.pol.environment } : {}),
      ...(k === "deployments_per_user" ? { used: n.owned } : k === "deployments_per_project" ? { used: n.all } : {}),
    });
  }
  const considered = all
    .filter((p) => db.me.permissions.includes("policies.read") || !p.project_id || p.project_id === projectId)
    .map((p) => {
      const base = { id: p.id, name: p.name };
      if (!p.enabled) return { ...base, applies: false, why: "Disabled." };
      if (p.project_id && p.project_id !== projectId)
        return { ...base, applies: false, why: "Only for another project." };
      if (p.environment && p.environment !== env) return { ...base, applies: false, why: `Only for ${p.environment}.` };
      if (decides(p.id)) return { ...base, applies: true, why: why[p.id]! };
      return { ...base, applies: false, why: why[p.id] ?? "A policy tried before it decides each of its limits." };
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
    if (!w.name.trim()) errors.push({ field: "/name", code: "required", message: "Give the policy a name." });
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
      audited(db, "policy.limit.delete", "deployment_limit_policy", was.id);
      return new HttpResponse(null, { status: 204 });
    }),
  ];
}
