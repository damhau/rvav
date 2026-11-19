// Each member's quota in a project and rvav's own (ADR-0047), as the
// server's quotas_admin.go, and the lines its quota check adds for them.
import { http, HttpResponse } from "msw";
import type { components } from "@/lib/api/schema";
import { audited } from "./governance";
import type { Db } from "./seed";

type S = components["schemas"];
type Resource = S["QuotaResource"];
type Usage = Record<Resource, number>;

const resources: Resource[] = ["vcpu", "memory_gb", "storage_gb", "vm_count"];

type QuotaState = { perUser: Record<string, S["UserQuota"][]>; global: S["GlobalQuota"][] };

export function quotaState(db: Db): QuotaState {
  const d = db as Db & { userQuotas?: QuotaState };
  return (d.userQuotas ??= { perUser: {}, global: [] });
}

const zero = (): Usage => ({ vcpu: 0, memory_gb: 0, storage_gb: 0, vm_count: 0 });

/** What a user's live deployments hold, in a project or across rvav. */
function owned(db: Db, ownerId: string, projectId?: string): Usage {
  const out = zero();
  for (const d of db.deployments) {
    if (d.owner.id !== ownerId || d.status === "deleted" || (projectId && d.project.id !== projectId)) continue;
    for (const r of d.resources) {
      if (r.type !== "vsphere.vm" || !("vm" in r)) continue;
      out.vcpu += r.vm.cpu_count;
      out.memory_gb += Math.round(r.vm.memory_mb / 1024);
      out.storage_gb += r.vm.disks.reduce((n, k) => n + k.capacity_gb, 0);
      out.vm_count += 1;
    }
  }
  return out;
}

function allOfRvav(db: Db): Usage {
  const out = zero();
  for (const q of Object.values(db.quota)) for (const r of resources) out[r] += q.used[r];
  return out;
}

const label: Record<Resource, string> = { vcpu: "vCPU", memory_gb: "GB of memory", storage_gb: "GB of storage", vm_count: "VMs" }; // prettier-ignore
const amount = (r: Resource, n: number) => (r === "vm_count" && n === 1 ? "1 VM" : `${n} ${label[r]}`);

export type QuotaOwner = { project: { id: string; name: string }; owner: { id: string; name: string }; you: boolean };

/**
 * The lines the user and rvav levels add to a quota check, with the hard
 * refusals, in the server's words; what names the change ("This request").
 */
export function userQuotaCheck(db: Db, o: QuotaOwner, what: string, adding: Partial<Usage>) {
  const st = quotaState(db);
  const whose = o.you ? "your" : `${o.owner.name}'s`;
  const levels: {
    level: S["QuotaLevel"];
    limits: { resource: Resource; limit?: number | null; enforcement: "hard" | "soft" }[];
    used: Usage;
    where: string;
    whom: string;
  }[] = [
    {
      level: "project_user",
      limits: st.perUser[o.project.id] ?? [],
      used: owned(db, o.owner.id, o.project.id),
      where: `${whose} quota in ${o.project.name}`,
      whom: "a project admin",
    },
    {
      level: "global",
      limits: st.global.filter((g) => !g.per_user),
      used: allOfRvav(db),
      where: "rvav",
      whom: "an administrator",
    },
    {
      level: "global_user",
      limits: st.global.filter((g) => g.per_user),
      used: owned(db, o.owner.id),
      where: `${whose} quota across rvav`,
      whom: "an administrator",
    },
  ];
  const lines: S["QuotaImpact"][] = [];
  const hard: string[] = [];
  for (const lv of levels)
    for (const r of resources) {
      const l = lv.limits.find((x) => x.resource === r);
      if (!l) continue;
      const add = adding[r] ?? 0;
      const used = lv.used[r];
      const exceeds = l.limit != null && add > 0 && used + add > l.limit;
      if (exceeds && l.enforcement === "hard")
        hard.push(
          `${what} needs ${amount(r, add)} but ${lv.where} has ${Math.max(l.limit! - used, 0)} left. Choose a smaller size or ask ${lv.whom} to raise the quota.`,
        );
      lines.push({ resource: r, level: lv.level, limit: l.limit ?? null, used, adding: add, exceeds, enforcement: l.enforcement }); // prettier-ignore
    }
  return { lines, hard };
}

/** What applies here's lines for the user and rvav levels, counting what userId holds. */
export function explainQuotaLines(db: Db, projectId: string, userId: string): S["PolicyQuotaLine"][] {
  const st = quotaState(db);
  const mine = owned(db, userId, projectId);
  const all = allOfRvav(db);
  const across = owned(db, userId);
  const line = (l: S["UserQuota"] | S["GlobalQuota"], level: S["QuotaLevel"], used: Usage): S["PolicyQuotaLine"] => ({
    resource: l.resource,
    level,
    scope: "",
    limit: l.limit ?? null,
    enforcement: l.enforcement,
    used: used[l.resource],
    reserved: 0,
  });
  return [
    ...(st.perUser[projectId] ?? []).map((l) => line(l, "project_user", mine)),
    ...st.global.map((l) => (l.per_user ? line(l, "global_user", across) : line(l, "global", all))),
  ];
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

const lineErrors = (ptr: string, l: { resource: string; limit?: number | null; enforcement: string }) => {
  const out: S["FieldError"][] = [];
  if (!resources.includes(l.resource as Resource))
    out.push({ field: `${ptr}/resource`, code: "enum", message: "vcpu, memory_gb, storage_gb or vm_count." });
  if (l.limit != null && l.limit < 0)
    out.push({ field: `${ptr}/limit`, code: "minimum", message: "Zero or more, or no limit." });
  if (l.enforcement !== "hard" && l.enforcement !== "soft")
    out.push({ field: `${ptr}/enforcement`, code: "enum", message: "hard or soft." });
  return out;
};

const same = (a: S["UserQuotaWrite"], b: S["UserQuotaWrite"]) =>
  a.resource === b.resource && a.enforcement === b.enforcement && (a.limit ?? null) === (b.limit ?? null);

export function quotaHandlers(db: Db, base: string, wait: () => Promise<void>) {
  const st = () => quotaState(db);
  const project = (id: string) => db.projects.find((p) => p.id === id);
  return [
    http.get(`${base}/admin/projects/:id/user-quotas`, async ({ params }) => {
      await wait();
      const p = project(String(params.id));
      if (!p) return problem(404, "Not found");
      if (!holdsIn(db, p.id, "project.settings.read")) return problem(403, "Not allowed");
      return HttpResponse.json({ items: st().perUser[p.id] ?? [] });
    }),
    http.put(`${base}/admin/projects/:id/user-quotas`, async ({ params, request }) => {
      await wait();
      const { items } = (await request.json()) as S["UserQuotaWriteList"];
      const p = project(String(params.id));
      if (!p) return problem(404, "Not found");
      const admin = holdsIn(db, p.id, "project.quotas.manage");
      if (!admin && !holdsIn(db, p.id, "project.policies.manage")) return problem(403, "Not allowed");
      const old = st().perUser[p.id] ?? [];
      const own = db.quota[p.id]?.limits;
      const errors: S["FieldError"][] = [];
      const rows: S["UserQuota"][] = [];
      items.forEach((q, i) => {
        const ptr = `/items/${i}`;
        errors.push(...lineErrors(ptr, q));
        if (rows.some((r) => r.resource === q.resource))
          errors.push({ field: ptr, code: "duplicate", message: "This resource already has a limit for each member." });
        const kept = old.find((o) => same(o, q));
        const cap = own?.[q.resource];
        if (!kept && !admin && cap != null && (q.limit == null || q.limit > cap))
          errors.push({
            field: `${ptr}/limit`,
            code: "looser",
            message: `${p.name} has ${amount(q.resource, cap)} in all: each member's limit can't be more.`,
          });
        rows.push({ ...q, limit: q.limit ?? null, set_by_admin: kept ? kept.set_by_admin : admin });
      });
      if (!admin) {
        const lost = old.find((o) => o.set_by_admin && !rows.some((r) => same(o, r)));
        if (lost)
          return problem(403, "Not allowed", undefined, `An administrator set each member's ${label[lost.resource]} limit: only they can change it.`); // prettier-ignore
      }
      if (errors.length) return problem(422, "Some inputs need attention", errors);
      st().perUser[p.id] = rows.sort((a, b) => a.resource.localeCompare(b.resource));
      audited(db, "policy.quota.user.set", "project", p.id);
      return HttpResponse.json({ items: st().perUser[p.id] });
    }),
    http.get(`${base}/admin/quotas`, async () => {
      await wait();
      if (!db.me.permissions.includes("project.settings.read")) return problem(403, "Not allowed");
      return HttpResponse.json({ items: st().global });
    }),
    http.put(`${base}/admin/quotas`, async ({ request }) => {
      await wait();
      if (!db.me.permissions.includes("project.quotas.manage")) return problem(403, "Not allowed");
      const { items } = (await request.json()) as S["GlobalQuotaList"];
      const errors: S["FieldError"][] = [];
      const seen = new Set<string>();
      items.forEach((q, i) => {
        errors.push(...lineErrors(`/items/${i}`, q));
        const key = `${q.resource}/${q.per_user}`;
        if (seen.has(key))
          errors.push({ field: `/items/${i}`, code: "duplicate", message: "This resource already has a limit here." });
        seen.add(key);
      });
      if (errors.length) return problem(422, "Some inputs need attention", errors);
      const byResource = (a: S["GlobalQuota"], b: S["GlobalQuota"]) => a.resource.localeCompare(b.resource);
      st().global = [...items.filter((q) => !q.per_user).sort(byResource), ...items.filter((q) => q.per_user).sort(byResource)]; // prettier-ignore
      audited(db, "policy.quota.global.set", "quota", "rvav");
      return HttpResponse.json({ items: st().global });
    }),
  ];
}
