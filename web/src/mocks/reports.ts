// Reports in the mock backend (S13.4): a year of requests made up from a
// fixed seed, counted as the server counts them, with the quotas and
// leases of the mock data.
import { http, HttpResponse } from "msw";
import type { components } from "@/lib/api/schema";
import type { Db } from "./seed";

type S = components["schemas"];

interface Fake {
  number: string;
  item: { id: string; slug: string; name: string };
  project: string;
  environment: string;
  status: "succeeded" | "failed" | "rejected" | "cancelled" | "running";
  submitted: number;
  run?: number;
  approval?: { opened: number; decided: number; status: "approved" | "rejected" };
}

const DAY = 86_400_000;

function mulberry32(seed: number) {
  return () => {
    seed = (seed + 0x6d2b79f5) | 0;
    let t = seed;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function fakes(db: Db): Fake[] {
  const d = db as Db & { reportFakes?: Fake[] };
  if (d.reportFakes) return d.reportFakes;
  const rand = mulberry32(13);
  const items = db.items.filter((i) => i.type === "vm_blueprint");
  const today = Math.floor(Date.now() / DAY) * DAY;
  const out: Fake[] = [];
  for (let day = 400; day >= 0; day--) {
    const weekend = [0, 6].includes(new Date(today - day * DAY).getUTCDay());
    const n = Math.round((weekend ? 1 : 4) + rand() * (weekend ? 2 : 7));
    for (let i = 0; i < n; i++) {
      const project = db.projects[rand() < 0.65 ? 0 : 1 % db.projects.length]!;
      const environment = project.environments[Math.floor(rand() * project.environments.length)]!;
      const item = items[Math.floor(rand() * items.length)]!;
      const x = rand();
      const status =
        day === 0 && i === 0
          ? "running"
          : x < 0.86
            ? "succeeded"
            : x < 0.93
              ? "failed"
              : x < 0.96
                ? "rejected"
                : "cancelled";
      const submitted = today - day * DAY + Math.floor((7 + rand() * 10) * 3_600_000);
      const f: Fake = {
        number: `REQ-${8000 + out.length}`, item: { id: item.id, slug: item.slug, name: item.name },
        project: project.id, environment, status, submitted,
      }; // prettier-ignore
      if (status === "succeeded") f.run = Math.round((item.slug.startsWith("windows") ? 900 : 300) + rand() * 500);
      if (environment === "prd" || status === "rejected") {
        const opened = submitted + 5_000;
        f.approval = {
          opened,
          decided: opened + Math.round((0.3 + rand() * 18) * 3_600_000),
          status: status === "rejected" ? "rejected" : "approved",
        };
      }
      out.push(f);
    }
  }
  d.reportFakes = out;
  return out;
}

function scopeOf(db: Db, project?: string | null) {
  const all = db.me.global_roles.includes("admin") || db.me.global_roles.includes("auditor");
  const mine = all ? db.projects.map((p) => p.id) : db.me.projects.map((p) => p.project.id);
  return project ? mine.filter((p) => p === project) : mine;
}

const startOf = (t: number, interval: string) => {
  const d = new Date(Math.floor(t / DAY) * DAY);
  if (interval === "week") return d.getTime() - ((d.getUTCDay() + 6) % 7) * DAY;
  if (interval === "month") return Date.UTC(d.getUTCFullYear(), d.getUTCMonth(), 1);
  return d.getTime();
};
const next = (t: number, interval: string) => {
  const d = new Date(t);
  if (interval === "month") return Date.UTC(d.getUTCFullYear(), d.getUTCMonth() + 1, 1);
  return t + (interval === "week" ? 7 : 1) * DAY;
};
const iso = (t: number) => new Date(t).toISOString().slice(0, 10);

function counts(list: Fake[]): S["ReportCounts"] {
  const n = (s: Fake["status"]) => list.filter((f) => f.status === s).length;
  const c: S["ReportCounts"] = { submitted: list.length, succeeded: n("succeeded"), failed: n("failed"), rejected: n("rejected"), cancelled: n("cancelled") }; // prettier-ignore
  if (c.succeeded + c.failed > 0) c.success_rate = c.succeeded / (c.succeeded + c.failed);
  return c;
}

function durations(values: number[]): S["ReportDurations"] {
  if (values.length === 0) return { count: 0 };
  const v = [...values].sort((a, b) => a - b);
  const at = (p: number) => {
    const i = p * (v.length - 1);
    return v[Math.floor(i)]! + (v[Math.ceil(i)]! - v[Math.floor(i)]!) * (i - Math.floor(i));
  };
  return {
    count: v.length,
    p50_seconds: at(0.5),
    p90_seconds: at(0.9),
    average_seconds: v.reduce((a, b) => a + b, 0) / v.length,
  };
}

export function reportHandlers(db: Db, base: string, wait: () => Promise<void>) {
  const select = (u: URL) => {
    const today = Math.floor(Date.now() / DAY) * DAY;
    const to = u.searchParams.get("to") ? Date.parse(u.searchParams.get("to")!) : today;
    const from = u.searchParams.get("from") ? Date.parse(u.searchParams.get("from")!) : to - 29 * DAY;
    const projects = scopeOf(db, u.searchParams.get("project_id"));
    const env = u.searchParams.get("environment");
    const scoped = (p: string, e: string) => projects.includes(p) && (!env || e === env);
    const list = fakes(db).filter(
      (f) => scoped(f.project, f.environment) && f.submitted >= from && f.submitted < to + DAY,
    );
    return { from, to, projects, env, scoped, list };
  };
  const quota = (projects: string[], env: string | null): S["ReportQuotaLine"][] =>
    db.projects
      .filter((p) => projects.includes(p.id) && !env)
      .flatMap((p) => {
        const q = db.quota[p.id];
        if (!q) return [];
        return (["vcpu", "memory_gb", "storage_gb", "vm_count"] as const).map((r) => ({
          project: { id: p.id, code: p.code, name: p.name },
          resource: r,
          used: q.used[r],
          ...(q.limits[r] !== null ? { limit: q.limits[r] } : {}),
        }));
      });
  const leases = (scoped: (p: string, e: string) => boolean): S["ReportLease"][] =>
    db.deployments
      .filter(
        (d) =>
          scoped(d.project.id, d.environment) && ["ready", "updating", "failed"].includes(d.status) &&
          d.lease.expires_at && Date.parse(d.lease.expires_at) >= Date.now() && Date.parse(d.lease.expires_at) < Date.now() + 30 * DAY,
      ) // prettier-ignore
      .sort((a, b) => Date.parse(a.lease.expires_at!) - Date.parse(b.lease.expires_at!))
      .map((d) => ({ deployment: { id: d.id, name: d.name }, project: d.project, environment: d.environment, owner: d.owner, expires_at: d.lease.expires_at! })); // prettier-ignore
  const approvalsOf = (u: URL) => {
    const { from, to, projects, scoped } = select(u);
    const all = fakes(db).filter((f) => scoped(f.project, f.environment) && f.approval);
    const decided = all.filter(
      (f) => f.approval!.decided >= from && f.approval!.decided < to + DAY && f.approval!.decided <= Date.now(),
    );
    const pending = all.filter((f) => f.approval!.decided > Date.now());
    return { projects, decided, pending };
  };
  return [
    http.get(`${base}/reports`, async ({ request }) => {
      await wait();
      const u = new URL(request.url);
      const interval = u.searchParams.get("interval") ?? "day";
      const { from, to, projects, env, scoped, list } = select(u);
      const over_time: S["ReportPeriod"][] = [];
      for (let t = startOf(from, interval); t < to + DAY; t = next(t, interval)) {
        const end = next(t, interval);
        over_time.push({ period: iso(t), ...counts(list.filter((f) => f.submitted >= t && f.submitted < end)) });
      }
      const byItem = new Map<string, Fake[]>();
      for (const f of list) byItem.set(f.item.id, [...(byItem.get(f.item.id) ?? []), f]);
      const by_item = [...byItem.values()]
        .sort((a, b) => b.length - a.length)
        .slice(0, 10)
        .map((fs) => ({
          item: fs[0]!.item,
          ...counts(fs),
          provisioning: durations(fs.flatMap((f) => (f.run ? [f.run] : []))),
        }));
      const { decided, pending } = approvalsOf(u);
      const report: S["Report"] = {
        from: iso(from),
        to: iso(to),
        interval: interval as S["Report"]["interval"],
        requests: { totals: counts(list), over_time, by_item },
        provisioning: durations(list.flatMap((f) => (f.run ? [f.run] : []))),
        approvals: {
          decided: decided.length,
          approved: decided.filter((f) => f.approval!.status === "approved").length,
          rejected: decided.filter((f) => f.approval!.status === "rejected").length,
          lead_time: durations(decided.map((f) => (f.approval!.decided - f.approval!.opened) / 1000)),
          pending: pending.length,
          ...(pending.length
            ? { oldest_pending_since: new Date(Math.min(...pending.map((f) => f.approval!.opened))).toISOString() }
            : {}),
        },
        quota: quota(projects, env),
        leases: leases(scoped),
      };
      return HttpResponse.json(report);
    }),
    http.get(`${base}/reports/export`, async ({ request }) => {
      await wait();
      const u = new URL(request.url);
      const { projects, env, scoped, list } = select(u);
      const t = (ms?: number) => (ms === undefined ? "" : new Date(ms).toISOString());
      let rows: (string | number)[][];
      switch (u.searchParams.get("section")) {
        case "requests":
          rows = [["number", "item", "project", "environment", "status", "submitted_at", "run_seconds"],
            ...list.map((f) => [f.number, f.item.slug, f.project, f.environment, f.status, t(f.submitted), f.run ?? ""])]; // prettier-ignore
          break;
        case "approvals": {
          const { decided, pending } = approvalsOf(u);
          rows = [["request", "status", "opened_at", "decided_at", "lead_seconds"],
            ...[...decided, ...pending].map((f) => [f.number, f.approval!.decided > Date.now() ? "pending" : f.approval!.status, t(f.approval!.opened), f.approval!.decided > Date.now() ? "" : t(f.approval!.decided), f.approval!.decided > Date.now() ? "" : Math.round((f.approval!.decided - f.approval!.opened) / 1000)])]; // prettier-ignore
          break;
        }
        case "quota":
          rows = [["project", "environment", "resource", "limit", "used"],
            ...quota(projects, env).map((l) => [l.project.code, l.environment ?? "", l.resource, l.limit ?? "", l.used])]; // prettier-ignore
          break;
        default:
          rows = [["deployment", "project", "environment", "owner", "expires_at"],
            ...leases(scoped).map((l) => [l.deployment.name, l.project.code, l.environment, l.owner.name, l.expires_at])]; // prettier-ignore
      }
      const text = rows
        .map((r) => r.map((c) => (/[",\n]/.test(String(c)) ? `"${String(c).replace(/"/g, '""')}"` : c)).join(","))
        .join("\n");
      return new HttpResponse(text + "\n", { headers: { "Content-Type": "text/csv; charset=utf-8" } });
    }),
  ];
}
