// ServiceNow in the mock backend (ADR-0065): the token ServiceNow calls rvav
// with, and the CMDB report. Checks follow the server's where the UI shows
// them.
import { http, HttpResponse } from "msw";
import type { components } from "@/lib/api/schema";
import { adminState } from "./administration";
import { audited } from "./governance";
import type { Db } from "./seed";

type S = components["schemas"];

interface SnowState {
  token?: S["ApiToken"];
  reconciledAt?: string;
  seq: number;
}

const ago = (min: number) => new Date(Date.now() - min * 60_000).toISOString();

function snowState(db: Db): SnowState {
  const d = db as Db & { servicenow?: SnowState };
  d.servicenow ??= db.fresh
    ? { seq: 0 }
    : {
        seq: 0,
        reconciledAt: ago(12),
        token: {
          id: "tok_snow1",
          name: "ServiceNow",
          scope: "servicenow",
          user: { id: "usr_rvav-servicenow", name: "ServiceNow (rvav)" },
          created_at: ago(60 * 24 * 20),
          expires_at: new Date(Date.now() + 345 * 86_400_000).toISOString(),
          last_used_at: ago(3),
        },
      };
  return d.servicenow;
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

function addressOK(s: string): boolean {
  const [addr = "", bits, more] = s.split("/");
  if (more !== undefined || (bits !== undefined && !/^\d{1,3}$/.test(bits))) return false;
  if (addr.includes(":")) return /^[0-9a-f:]+$/i.test(addr) && (bits === undefined || Number(bits) <= 128);
  const parts = addr.split(".");
  return (
    parts.length === 4 &&
    parts.every((p) => /^\d{1,3}$/.test(p) && Number(p) <= 255) &&
    (bits === undefined || Number(bits) <= 32)
  );
}

/** What the server refuses in the integration's request settings. */
export function serviceNowSettingsErrors(settings: Record<string, unknown>): S["FieldError"][] {
  const out: S["FieldError"][] = [];
  const sources = settings.allowed_sources;
  const bad = Array.isArray(sources)
    ? (sources as unknown[]).map((x) => String(x).trim()).find((x) => x && !addressOK(x))
    : undefined;
  if (bad !== undefined)
    out.push({
      field: "/settings/allowed_sources",
      code: "invalid",
      message: `"${bad}" is neither an address nor a network such as 10.0.0.0/8.`,
    });
  const rpm = settings.requests_per_minute;
  if (typeof rpm === "number" && (rpm < 1 || rpm > 6000 || !Number.isInteger(rpm)))
    out.push({ field: "/settings/requests_per_minute", code: "invalid", message: "From 1 to 6000." });
  return out;
}

/** A Go duration such as 30m or 1h30m, in milliseconds; 30 minutes when unset. */
function durationMs(d: unknown): number {
  const unit: Record<string, number> = { h: 3_600_000, m: 60_000, s: 1000, ms: 1 };
  const parts = typeof d === "string" ? [...d.matchAll(/(\d+(?:\.\d+)?)(ms|h|m|s)/g)] : [];
  return parts.length ? parts.reduce((n, [, v, u]) => n + Number(v) * unit[u!]!, 0) : 30 * 60_000;
}

function report(db: Db): S["ServiceNowCmdb"] {
  const it = adminState(db).integrations.servicenow!;
  const s = it.settings;
  const enabled = Boolean(s.url) && s.cmdb === true;
  const chosen = Array.isArray(s.projects) ? (s.projects as unknown[]).map(String) : [];
  const projects = db.projects
    .filter((p) => chosen.includes(p.id) || chosen.includes(p.code))
    .map((p) => ({ id: p.id, code: p.code, name: p.name }));
  const base: S["ServiceNowCmdb"] = {
    enabled,
    class: typeof s.cmdb_class === "string" && s.cmdb_class ? s.cmdb_class : "cmdb_ci_vmware_instance",
    data_source: typeof s.data_source === "string" && s.data_source ? s.data_source : "rvav",
    projects,
    kept: 0,
    retired: 0,
    queued: 0,
    failing: 0,
    failures: [],
    orphans: [],
    orphans_total: 0,
  };
  const st = snowState(db);
  if (!enabled) return base;
  const inScope = db.deployments.filter(
    (d) => chosen.length === 0 || chosen.includes(d.project.id) || chosen.includes(d.project.code ?? ""),
  );
  const kept = inScope.reduce((n, d) => n + d.vm_count, 0);
  const refused = inScope.find((d) => d.status === "failed");
  const url = String(s.url).replace(/\/+$/, "");
  const orphans: S["ServiceNowOrphan"][] = [
    {
      sys_id: "b7e2c4a91b3d6010a8f1c0d5e04bcb21",
      name: "web-dev-old001",
      instance_uuid: "4215a3c1-7d2e-9b4f-6c1a-0e8d3f5b2a71",
      updated_at: "2026-10-28 09:14:52",
    },
    { sys_id: "c19f0e6d1b3d6010a8f1c0d5e04bcb9e", name: "fin-tst-batch003", updated_at: "2026-09-30 17:02:11" },
  ].map((o) => ({ ...o, url: `${url}/${base.class}.do?sys_id=${o.sys_id}` }));
  return {
    ...base,
    kept,
    retired: 3,
    failing: refused ? 1 : 0,
    failures: refused
      ? [
          {
            deployment_id: refused.id,
            deployment_name: refused.name,
            attempts: 4,
            error: "ServiceNow answered 403: Insufficient rights to update cmdb_ci_vmware_instance.",
            next_attempt_at: new Date(Date.now() + 8 * 60_000).toISOString(),
          },
        ]
      : [],
    ...(st.reconciledAt
      ? {
          reconciled_at: st.reconciledAt,
          next_reconcile_at: new Date(Date.parse(st.reconciledAt) + durationMs(s.sync_interval)).toISOString(),
          servicenow: kept + orphans.length,
          orphans,
          orphans_total: orphans.length,
        }
      : {}),
  };
}

export function serviceNowHandlers(db: Db, base: string, wait: () => Promise<void>) {
  const settings = () => adminState(db).integrations.servicenow!.settings;
  const forbidden = () =>
    db.me.permissions.includes("integrations.manage")
      ? undefined
      : problem(403, "Not allowed", undefined, "You do not have permission to do this.");
  return [
    http.get(`${base}/admin/integrations/servicenow/cmdb`, async () => {
      await wait();
      return forbidden() ?? HttpResponse.json(report(db));
    }),
    http.post(`${base}/admin/integrations/servicenow/cmdb/reconcile`, async () => {
      await wait();
      const refused = forbidden();
      if (refused) return refused;
      if (!report(db).enabled)
        return problem(409, "Conflict", undefined, "Set ServiceNow's connection and turn its CMDB on first.");
      snowState(db).reconciledAt = new Date().toISOString();
      audited(db, "servicenow.reconcile", "integration", "servicenow");
      return HttpResponse.json(report(db), { status: 202 });
    }),
    http.get(`${base}/admin/integrations/servicenow/token`, async () => {
      await wait();
      const refused = forbidden();
      if (refused) return refused;
      const st = snowState(db);
      const s = settings();
      return HttpResponse.json({
        ...(st.token ? { token: st.token } : {}),
        allowed_sources: Array.isArray(s.allowed_sources) ? (s.allowed_sources as string[]) : [],
        requests_per_minute: typeof s.requests_per_minute === "number" ? s.requests_per_minute : 30,
      } satisfies S["ServiceNowToken"]);
    }),
    http.post(`${base}/admin/integrations/servicenow/token`, async () => {
      await wait();
      const refused = forbidden();
      if (refused) return refused;
      const st = snowState(db);
      const revoked = st.token ? 1 : 0;
      const id = `tok_snow${++st.seq + 1}`;
      st.token = {
        id,
        name: "ServiceNow",
        scope: "servicenow",
        user: { id: "usr_rvav-servicenow", name: "ServiceNow (rvav)" },
        created_at: new Date().toISOString(),
        expires_at: new Date(Date.now() + 365 * 86_400_000).toISOString(),
      };
      audited(db, "servicenow.token", "api_token", id, { revoked, expires_at: st.token.expires_at });
      return HttpResponse.json(
        {
          token: st.token,
          secret: `rvav_pat_mock${st.seq}Xq7vR2nL9sK4wT1yB6hD3fJ8`,
          revoked,
        } satisfies S["ServiceNowTokenMade"],
        { status: 201 },
      );
    }),
    http.delete(`${base}/admin/integrations/servicenow/token`, async () => {
      await wait();
      const refused = forbidden();
      if (refused) return refused;
      const st = snowState(db);
      if (st.token) audited(db, "servicenow.token_revoked", "integration", "servicenow", { revoked: 1 });
      st.token = undefined;
      return new HttpResponse(null, { status: 204 });
    }),
  ];
}
