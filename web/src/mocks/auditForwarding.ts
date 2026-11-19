// Audit forwarding in the mock backend (ADR-0058): syslog servers and
// webhooks that get audit events, with their delivery state. Checks follow
// the server's; a test reaches a host whose name starts with "down" never.
import { http, HttpResponse } from "msw";
import type { components } from "@/lib/api/schema";
import { can } from "@/lib/can";
import { audited } from "./governance";
import type { Db } from "./seed";

type S = components["schemas"];
type Destination = S["AuditDestination"];

const now = () => new Date().toISOString();
const ago = (minutes: number) => new Date(Date.now() - minutes * 60_000).toISOString();
const later = (seconds: number) => new Date(Date.now() + seconds * 1000).toISOString();

const webhookHosts = ["cmdb.corp.example", "hooks.corp.example", "servicenow.corp.example"];
const categories: S["AuditCategory"][] = ["sign_in", "token", "access", "catalog", "policy", "infrastructure", "extensibility", "settings", "request", "other"]; // prettier-ignore
const facilities = new Set<string>(["kern", "user", "mail", "daemon", "auth", "syslog", "lpr", "news", "uucp", "cron", "authpriv", "ftp", "ntp", "audit", "alert", "clock", "local0", "local1", "local2", "local3", "local4", "local5", "local6", "local7"]); // prettier-ignore

function state(db: Db): Destination[] {
  const d = db as Db & { auditDestinations?: Destination[] };
  d.auditDestinations ??= [
    {
      id: "adt_siem",
      name: "Corporate SIEM",
      enabled: true,
      kind: "syslog",
      categories: [],
      syslog: { host: "siem.corp.example", port: 6514, transport: "tls", facility: "audit", app_name: "rvav" },
      state: { backlog: 0, delivered: 18_342, failures: 0, last_success_at: ago(1) },
      created_at: ago(60 * 24 * 40),
      updated_at: ago(60 * 24 * 40),
    },
    {
      id: "adt_soc",
      name: "SOC sign-ins",
      enabled: true,
      kind: "webhook",
      categories: ["sign_in", "token"],
      webhook: { url: "https://hooks.corp.example/soc/rvav", secret: "webhook-signing" },
      state: {
        backlog: 37,
        delivered: 912,
        failures: 4,
        last_success_at: ago(26),
        last_error: "503 Service Unavailable from hooks.corp.example",
        last_error_at: ago(1),
        next_attempt_at: later(240),
      },
      created_at: ago(60 * 24 * 9),
      updated_at: ago(60 * 24 * 9),
    },
  ];
  return d.auditDestinations;
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

const storedSecrets = (db: Db) =>
  ((db as Db & { catalogAdmin?: { secrets: S["Secret"][] } }).catalogAdmin?.secrets ?? []).filter((s) => !s.project_id);

/** The destination a write makes, or what is wrong with it. */
function check(
  db: Db,
  w: S["AuditDestinationWrite"],
): { errors: S["FieldError"][]; saved: Omit<Destination, "id" | "state" | "created_at" | "updated_at"> } {
  const errors: S["FieldError"][] = [];
  const err = (field: string, code: string, message: string) => errors.push({ field, code, message });
  const name = (w.name ?? "").trim();
  if (!name || name.length > 80) err("/name", "required", "Give the destination a name of at most 80 characters.");
  const cats: S["AuditCategory"][] = [];
  (w.categories ?? []).forEach((c, i) => {
    if (!categories.includes(c)) err(`/categories/${i}`, "enum", `Not a category: ${c}.`);
    else if (!cats.includes(c)) cats.push(c);
  });
  cats.sort();
  const secrets = storedSecrets(db);
  const known = (secret: string) => secrets.length === 0 || secrets.some((s) => s.name === secret);
  const base = { name, enabled: w.enabled ?? true, categories: cats };
  if (Boolean(w.syslog) === Boolean(w.webhook)) {
    err("/syslog", "one_of", "Give a syslog server or a webhook, one of them.");
    return { errors, saved: { ...base, kind: "syslog" } };
  }
  if (w.syslog) {
    const s = w.syslog;
    const host = s.host.trim();
    const transport = s.transport ?? "tls";
    if (!/^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$/i.test(host) || host.length > 253)
      err("/syslog/host", "invalid", "A host name or an IP address, such as siem.corp.example.");
    if (s.port !== undefined && (!Number.isInteger(s.port) || s.port < 1 || s.port > 65535))
      err("/syslog/port", "range", "A port from 1 to 65535.");
    if (s.facility && !facilities.has(s.facility))
      err("/syslog/facility", "enum", "A syslog facility, such as local0 or audit.");
    if (s.app_name !== undefined && !/^[!-~]{1,48}$/.test(s.app_name))
      err("/syslog/app_name", "invalid", "At most 48 printable ASCII characters, no space.");
    const ca = s.ca_certificate?.trim() || undefined;
    const cert = s.client_certificate?.trim() || undefined;
    const key = s.client_key_secret?.trim() || undefined;
    if (ca && transport !== "tls") err("/syslog/ca_certificate", "tls_only", "A CA certificate is for TLS only.");
    else if (ca && !ca.includes("-----BEGIN CERTIFICATE-----"))
      err("/syslog/ca_certificate", "invalid", "Not CA certificates in PEM: no certificate.");
    if ((cert || key) && transport !== "tls")
      err("/syslog/client_certificate", "tls_only", "A client certificate is for TLS only.");
    else if (Boolean(cert) !== Boolean(key))
      err(
        "/syslog/client_certificate",
        "pair",
        "Give the client certificate with the stored secret that holds its key.",
      );
    else if (key && !known(key)) err("/syslog/client_key_secret", "unknown", `There is no stored secret ${key}.`);
    return {
      errors,
      saved: {
        ...base,
        kind: "syslog",
        syslog: {
          host,
          port: s.port ?? (transport === "tls" ? 6514 : 514),
          transport,
          facility: s.facility ?? "local0",
          app_name: s.app_name ?? "rvav",
          ...(ca ? { ca_certificate: ca } : {}),
          ...(cert ? { client_certificate: cert } : {}),
          ...(key ? { client_key_secret: key } : {}),
        },
      },
    };
  }
  const url = w.webhook!.url.trim();
  const secret = w.webhook!.secret.trim();
  let host = "";
  try {
    const u = new URL(url);
    host = u.protocol === "https:" || u.protocol === "http:" ? u.hostname : "";
  } catch {
    host = "";
  }
  if (!host) err("/webhook/url", "invalid", "A URL such as https://siem.corp.example/rvav.");
  else if (!webhookHosts.includes(host))
    err("/webhook/url", "host", `${host} is not one of the hosts webhooks may call (Administration › Integrations › Allowed hosts).`); // prettier-ignore
  if (!secret) err("/webhook/secret", "required", "Its calls are signed: give the stored secret to sign them with.");
  else if (!known(secret)) err("/webhook/secret", "unknown", `There is no stored secret ${secret}.`);
  return { errors, saved: { ...base, kind: "webhook", webhook: { url, secret } } };
}

const hostOf = (d: Destination) => {
  if (d.syslog) return d.syslog.host;
  try {
    return new URL(d.webhook?.url ?? "").hostname;
  } catch {
    return "";
  }
};

export function auditForwardingHandlers(db: Db, base: string, wait: () => Promise<void>) {
  const manage = () => can(db.me, "integrations.manage");
  const read = () => manage() || can(db.me, "audit.read");
  return [
    http.get(`${base}/admin/audit-destinations`, async () => {
      await wait();
      if (!read()) return problem(403, "Forbidden");
      return HttpResponse.json({ items: [...state(db)].sort((a, b) => a.name.localeCompare(b.name)) });
    }),
    http.get(`${base}/admin/audit-destinations/:id`, async ({ params }) => {
      await wait();
      const d = state(db).find((x) => x.id === params.id);
      return d ? HttpResponse.json(d) : problem(404, "Not found");
    }),
    ...(["post", "put"] as const).map((method) =>
      http[method](`${base}/admin/audit-destinations${method === "put" ? "/:id" : ""}`, async ({ params, request }) => {
        await wait();
        if (!manage()) return problem(403, "Forbidden");
        const list = state(db);
        const current = method === "put" ? list.find((x) => x.id === params.id) : undefined;
        if (method === "put" && !current) return problem(404, "Not found");
        const { errors, saved } = check(db, (await request.json()) as S["AuditDestinationWrite"]);
        if (!errors.length && list.some((x) => x.name === saved.name && x.id !== current?.id))
          errors.push({ field: "/name", code: "taken", message: `Another destination is called ${saved.name}.` });
        if (errors.length) return problem(422, "Some inputs need attention", errors);
        const d: Destination = {
          ...saved,
          id: current?.id ?? `adt_${Math.random().toString(36).slice(2, 10)}`,
          // Saved, the events waiting are tried again at once.
          state: current
            ? { backlog: current.state.backlog, delivered: current.state.delivered, failures: current.state.failures, ...(current.state.last_success_at ? { last_success_at: current.state.last_success_at } : {}) } // prettier-ignore
            : { backlog: 0, delivered: 0, failures: 0 },
          created_at: current?.created_at ?? now(),
          updated_at: now(),
        };
        if (current && d.enabled && d.state.failures > 0 && !hostOf(d).startsWith("down")) {
          d.state = { backlog: 0, delivered: d.state.delivered + d.state.backlog, failures: 0, last_success_at: now() };
        }
        if (current) list[list.indexOf(current)] = d;
        else list.push(d);
        audited(db, `settings.audit_destination.${current ? "update" : "create"}`, "audit_destination", d.id, {
          name: d.name,
          kind: d.kind,
          enabled: d.enabled,
          categories: d.categories,
        });
        return HttpResponse.json(d, { status: current ? 200 : 201 });
      }),
    ),
    http.delete(`${base}/admin/audit-destinations/:id`, async ({ params }) => {
      await wait();
      if (!manage()) return problem(403, "Forbidden");
      const list = state(db);
      const i = list.findIndex((x) => x.id === params.id);
      if (i < 0) return problem(404, "Not found");
      const [d] = list.splice(i, 1);
      audited(db, "settings.audit_destination.delete", "audit_destination", d!.id, { name: d!.name });
      return new HttpResponse(null, { status: 204 });
    }),
    http.post(`${base}/admin/audit-destinations/:id/test`, async ({ params }) => {
      await wait();
      if (!manage()) return problem(403, "Forbidden");
      const d = state(db).find((x) => x.id === params.id);
      if (!d) return problem(404, "Not found");
      const host = hostOf(d);
      const result: S["AuditDestinationTest"] =
        host.startsWith("down") || d.state.failures > 0
          ? { ok: false, message: d.state.last_error ?? `dial tcp: lookup ${host}: no such host`, duration_ms: 3012 }
          : {
              ok: true,
              message:
                d.kind === "syslog"
                  ? `Sent to ${host}:${d.syslog?.port ?? 6514} over ${(d.syslog?.transport ?? "tls").toUpperCase()}.`
                  : `${host} answered 200 OK.`,
              duration_ms: 84,
            };
      audited(db, "settings.audit_destination.test", "audit_destination", d.id, { name: d.name, ok: result.ok });
      return HttpResponse.json(result);
    }),
  ];
}
