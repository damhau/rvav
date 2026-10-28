// Administration in the mock backend (S9, ADR-0020): API tokens and the
// audit log so far.
import { http, HttpResponse } from "msw";
import type { components } from "@/lib/api/schema";
import type { Db } from "./seed";

type S = components["schemas"];

interface GovernanceState {
  tokens: S["ApiToken"][];
  audit: S["AuditEvent"][];
  seq: number;
}

const now = () => new Date().toISOString();

function state(db: Db): GovernanceState {
  const d = db as Db & { governance?: GovernanceState };
  if (!d.governance) {
    const ago = (min: number) => new Date(Date.now() - min * 60_000).toISOString();
    const lea = { id: db.me.user.id, name: db.me.user.name };
    const web = db.projects[0]!;
    const project = { id: web.id, code: web.code, name: web.name };
    d.governance = {
      seq: 100,
      tokens: [
        {
          id: "tok_ci", name: "GitLab pipeline", scope: "all", user: lea, created_at: ago(60 * 24 * 20),
          expires_at: new Date(Date.now() + 70 * 86_400_000).toISOString(), last_used_at: ago(42),
        },
      ], // prettier-ignore
      audit: [
        { id: "5", at: ago(12), actor: lea, action: "catalog.version.publish", target_type: "catalog_item", target_id: "itm_rhel9", project: null, detail: { version: "3.2" } },
        { id: "4", at: ago(55), actor: lea, action: "member.set", target_type: "project", target_id: web.id, project, detail: { user_id: "usr_tnguyen", role: "member" } },
        { id: "3", at: ago(180), actor: null, action: "approval.timeout", target_type: "approval", target_id: "apr_17", project, detail: {} },
        { id: "2", at: ago(60 * 26), actor: lea, action: "policy.lease.set", target_type: "lease_policy", target_id: "dev", project: null, detail: { default_days: 30 } },
        { id: "1", at: ago(60 * 50), actor: lea, action: "token.create", target_type: "api_token", target_id: "tok_ci", project: null, detail: { name: "GitLab pipeline" } },
      ], // prettier-ignore
    };
  }
  return d.governance;
}

/** Records a change in the mock audit log, as the server does. */
export function audited(
  db: Db,
  action: string,
  targetType: string,
  targetId: string,
  detail: Record<string, unknown> = {},
) {
  const st = state(db);
  st.audit.unshift({
    id: String(++st.seq),
    at: now(),
    actor: { id: db.me.user.id, name: db.me.user.name },
    action,
    target_type: targetType,
    target_id: targetId,
    project: null,
    detail,
  });
}

function problem(status: number, title: string, errors?: S["FieldError"][]) {
  return HttpResponse.json(
    { type: "https://rvav.dev/problems/x", title, status, request_id: "req_mock", ...(errors ? { errors } : {}) },
    { status, headers: { "Content-Type": "application/problem+json" } },
  );
}

export function governanceHandlers(db: Db, base: string, wait: () => Promise<void>) {
  return [
    http.get(`${base}/tokens`, async ({ request }) => {
      await wait();
      const all = new URL(request.url).searchParams.get("all") === "true";
      const st = state(db);
      return HttpResponse.json({ items: all ? st.tokens : st.tokens.filter((t) => t.user.id === db.me.user.id) });
    }),
    http.post(`${base}/tokens`, async ({ request }) => {
      await wait();
      const c = (await request.json()) as S["ApiTokenCreate"];
      if (!c.name.trim())
        return problem(422, "Some inputs need attention", [
          { field: "/name", code: "required", message: "Say what the token is for." },
        ]);
      const st = state(db);
      const t: S["ApiToken"] = {
        id: `tok_${++st.seq}`,
        name: c.name.trim(),
        scope: c.scope ?? "all",
        user: { id: db.me.user.id, name: db.me.user.name },
        created_at: now(),
        expires_at: c.expires_at,
      };
      st.tokens.unshift(t);
      audited(db, "token.create", "api_token", t.id, { name: t.name });
      return HttpResponse.json(
        { ...t, token: `rvav_pat_mock${Math.random().toString(36).slice(2, 14)}` },
        { status: 201 },
      );
    }),
    http.delete(`${base}/tokens/:id`, async ({ params }) => {
      await wait();
      const st = state(db);
      const i = st.tokens.findIndex((t) => t.id === params.id);
      if (i < 0) return problem(404, "Not found");
      const [t] = st.tokens.splice(i, 1);
      audited(db, "token.revoke", "api_token", t!.id, { name: t!.name });
      return new HttpResponse(null, { status: 204 });
    }),
    http.get(`${base}/audit`, async ({ request }) => {
      await wait();
      const p = new URL(request.url).searchParams;
      const search = (p.get("q") ?? "").toLowerCase();
      const action = p.get("action") ?? "";
      const project = p.get("project_id");
      const from = p.get("from");
      const to = p.get("to");
      const items = state(db).audit.filter(
        (e) =>
          (!search || JSON.stringify(e).toLowerCase().includes(search)) &&
          (!action || (action.endsWith(".") ? e.action.startsWith(action) : e.action === action)) &&
          (!project || e.project?.id === project) &&
          (!from || e.at >= from) &&
          (!to || e.at < to),
      );
      if (p.get("format") === "csv") {
        const rows = items.map((e) =>
          [
            e.at,
            e.actor?.name ?? "rvav",
            e.action,
            e.target_type,
            e.target_id,
            e.project?.code ?? "",
            JSON.stringify(e.detail),
          ]
            .map((v) => (/[",\n]/.test(v) ? `"${v.replaceAll('"', '""')}"` : v))
            .join(","),
        );
        return new HttpResponse(["at,actor,action,target_type,target_id,project,detail", ...rows].join("\n") + "\n", {
          headers: { "Content-Type": "text/csv" },
        });
      }
      return HttpResponse.json({ items, next_cursor: null });
    }),
  ];
}
