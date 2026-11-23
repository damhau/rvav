// Audit forwarding in the mock backend (ADR-0058): syslog servers and
// webhooks that get audit events, with their delivery state. Checks follow
// the server's; a test reaches a host whose name starts with "down" never.
import { http, HttpResponse } from "msw";
import type { components } from "@/lib/api/schema";
import { can } from "@/lib/can";
import { audited } from "./governance";
import type { Db } from "./seed";
import { unknownField } from "./strict";

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
      state: { backlog: 0, set_aside: 0, delivered: 18_342, failures: 0, last_success_at: ago(1) },
      created_at: ago(60 * 24 * 40),
      updated_at: ago(60 * 24 * 40),
    },
    {
      id: "adt_soc",
      name: "SOC sign-ins",
      enabled: true,
      kind: "webhook",
      categories: ["sign_in", "token"],
      webhook: {
        url: "https://hooks.corp.example/soc/rvav?index=rvav-audit",
        secret: "webhook-signing",
        auth_header: "Authorization",
      },
      state: {
        backlog: 37,
        set_aside: 0,
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

const hostName = /^[A-Za-z0-9]([A-Za-z0-9-]{0,62}[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]{0,62}[A-Za-z0-9])?)*$/;
const isIPv6 = (host: string) => {
  if (!host.includes(":") || host.includes("%")) return false;
  try {
    return new URL(`http://[${host}]/`).hostname !== "";
  } catch {
    return false;
  }
};
const transports = ["tls", "tcp", "udp"];
const headerName = /^[!#$%&'*+.^_`|~0-9A-Za-z-]{1,64}$/;
// The headers rvav sets on a webhook's calls itself, or that HTTP owns.
const ownHeaders = ["content-type", "content-length", "host", "user-agent", "transfer-encoding", "connection"];
const REDACTED = "REDACTED";

/** A URL as auditors see it: its password and each query value REDACTED, without a fragment (auditlog.RedactURL). */
export function redactURL(raw: string): string {
  let u: URL;
  try {
    u = new URL(raw);
  } catch {
    return raw;
  }
  if (!u.password && !u.search && !u.hash) return raw;
  if (u.password) u.password = REDACTED;
  u.hash = "";
  const q = u.search.slice(1);
  if (q)
    u.search = q
      .split("&")
      .map((pair) => {
        const i = pair.indexOf("=");
        return i >= 0 && i < pair.length - 1 ? pair.slice(0, i + 1) + REDACTED : pair;
      })
      .join("&");
  return u.toString();
}

/** The value each webhook's authentication header is sent with, sealed on the server and never shown. */
function sealed(db: Db): Map<string, string> {
  const d = db as Db & { auditAuthValues?: Map<string, string> };
  d.auditAuthValues ??= new Map([["adt_soc", "Splunk 6f1c-mock-token"]]);
  return d.auditAuthValues;
}

/** A destination as the API shows it. */
function present(d: Destination): Destination {
  if (!d.webhook) return d;
  const { auth_value: _, ...webhook } = d.webhook;
  return { ...d, webhook: { ...webhook, url: redactURL(webhook.url) } };
}

const writeFields = ["name", "enabled", "categories", "syslog", "webhook"];
const syslogFields = ["host", "port", "transport", "facility", "app_name", "ca_certificate", "client_certificate", "client_key_secret"]; // prettier-ignore
const webhookFields = ["url", "secret", "auth_header", "auth_value"];

/** A field the server's strict decoding refuses, at any depth. */
function strictly(body: unknown) {
  const w = body as S["AuditDestinationWrite"] | null;
  return (
    unknownField(body, writeFields) ?? unknownField(w?.syslog, syslogFields) ?? unknownField(w?.webhook, webhookFields)
  );
}

const storedSecrets = (db: Db) =>
  ((db as Db & { catalogAdmin?: { secrets: S["Secret"][] } }).catalogAdmin?.secrets ?? []).filter((s) => !s.project_id);

/** The destination a write makes, or what is wrong with it. */
function check(
  db: Db,
  w: S["AuditDestinationWrite"],
  before?: Destination,
): {
  errors: S["FieldError"][];
  saved: Omit<Destination, "id" | "state" | "created_at" | "updated_at">;
  value?: string;
} {
  const errors: S["FieldError"][] = [];
  const err = (field: string, code: string, message: string) => errors.push({ field, code, message });
  const name = (w.name ?? "").trim();
  if (!name || [...name].length > 80) err("/name", "required", "Give the destination a name of at most 80 characters.");
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
    const host = (s.host ?? "").trim();
    if (!host || host.length > 253 || !(hostName.test(host) || isIPv6(host)))
      err("/syslog/host", "invalid", "A host name or an IP address, such as siem.corp.example.");
    let transport = s.transport ?? "tls";
    if (!transports.includes(transport)) {
      err("/syslog/transport", "enum", "tls, tcp or udp.");
      transport = "tls";
    }
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
  const stored = before?.webhook;
  let url = (w.webhook!.url ?? "").trim();
  if (stored && url === redactURL(stored.url)) url = stored.url;
  const secret = (w.webhook!.secret ?? "").trim();
  let u: URL | undefined;
  try {
    u = new URL(url);
    if ((u.protocol !== "https:" && u.protocol !== "http:") || !u.host) u = undefined;
  } catch {
    u = undefined;
  }
  if (!u) err("/webhook/url", "invalid", "A URL such as https://siem.corp.example/rvav.");
  else if (u.username || u.password)
    err("/webhook/url", "credentials", "Leave the user name and password out of the URL: auditors would see them. Send the credential in an authentication header."); // prettier-ignore
  else if (u.search.includes("=" + REDACTED))
    err("/webhook/url", "redacted", "Type the query's values again: they are kept only when the URL is sent back as it is shown."); // prettier-ignore
  else if (!webhookHosts.includes(u.hostname))
    err("/webhook/url", "host", `${u.host} is not one of the hosts webhooks may call (Administration › Integrations › Allowed hosts).`); // prettier-ignore
  const header = (w.webhook!.auth_header ?? "").trim();
  const given = w.webhook!.auth_value ?? "";
  let value: string | undefined;
  if (!header) {
    if (given) err("/webhook/auth_header", "required", "Name the header the value goes in, such as Authorization.");
  } else if (!headerName.test(header)) {
    err("/webhook/auth_header", "invalid", "A header's name: letters, digits and - at most 64.");
  } else if (ownHeaders.includes(header.toLowerCase()) || header.toLowerCase().startsWith("x-rvav-")) {
    err("/webhook/auth_header", "reserved", `rvav sets ${header} itself: name another header.`);
  } else if (!given) {
    const was = before && sealed(db).get(before.id);
    const sameOrigin = (() => {
      try {
        return Boolean(stored && u && new URL(stored.url).origin === u.origin);
      } catch {
        return false;
      }
    })();
    if (was && sameOrigin) value = was;
    else
      err("/webhook/auth_value", "required", "Give the header's value: a new URL's server doesn't get the one kept.");
  } else if (given.length > 4096 || [...given].some((c) => (c < " " && c !== "\t") || c === "\u007f")) {
    err("/webhook/auth_value", "invalid", "At most 4,096 characters, on one line.");
  } else value = given;
  if (!secret) err("/webhook/secret", "required", "Its calls are signed: give the stored secret to sign them with.");
  else if (!known(secret)) err("/webhook/secret", "unknown", `There is no stored secret ${secret}.`);
  return {
    errors,
    saved: { ...base, kind: "webhook", webhook: { url, secret, ...(header ? { auth_header: header } : {}) } },
    value,
  };
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
      return HttpResponse.json({ items: [...state(db)].sort((a, b) => a.name.localeCompare(b.name)).map(present) });
    }),
    http.get(`${base}/admin/audit-destinations/:id`, async ({ params }) => {
      await wait();
      if (!read()) return problem(403, "Forbidden");
      const d = state(db).find((x) => x.id === params.id);
      return d ? HttpResponse.json(present(d)) : problem(404, "Not found");
    }),
    ...(["post", "put"] as const).map((method) =>
      http[method](`${base}/admin/audit-destinations${method === "put" ? "/:id" : ""}`, async ({ params, request }) => {
        await wait();
        if (!manage()) return problem(403, "Forbidden");
        const list = state(db);
        const current = method === "put" ? list.find((x) => x.id === params.id) : undefined;
        if (method === "put" && !current) return problem(404, "Not found");
        const body: unknown = await request.json();
        const strict = strictly(body);
        if (strict) return strict;
        const { errors, saved, value } = check(db, body as S["AuditDestinationWrite"], current);
        if (!errors.length && list.some((x) => x.name === saved.name && x.id !== current?.id))
          errors.push({ field: "/name", code: "taken", message: `Another destination is called ${saved.name}.` });
        if (errors.length) return problem(422, "Some inputs need attention", errors);
        const d: Destination = {
          ...saved,
          id: current?.id ?? `adt_${Math.random().toString(36).slice(2, 10)}`,
          // Saved, the events waiting are tried again at once.
          state: current
            ? { backlog: current.state.backlog + current.state.set_aside, set_aside: 0, delivered: current.state.delivered, failures: current.state.failures, ...(current.state.last_success_at ? { last_success_at: current.state.last_success_at } : {}) } // prettier-ignore
            : { backlog: 0, set_aside: 0, delivered: 0, failures: 0 },
          created_at: current?.created_at ?? now(),
          updated_at: now(),
        };
        if (current && d.enabled && d.state.failures > 0 && !hostOf(d).startsWith("down")) {
          d.state = {
            backlog: 0,
            set_aside: 0,
            delivered: d.state.delivered + d.state.backlog,
            failures: 0,
            last_success_at: now(),
          };
        }
        if (current) list[list.indexOf(current)] = d;
        else list.push(d);
        if (value) sealed(db).set(d.id, value);
        else sealed(db).delete(d.id);
        const shown = present(d);
        audited(db, `settings.audit_destination.${current ? "update" : "create"}`, "audit_destination", d.id, {
          name: d.name,
          kind: d.kind,
          enabled: d.enabled,
          categories: d.categories,
          settings: shown.webhook ?? shown.syslog,
        });
        return HttpResponse.json(shown, { status: current ? 200 : 201 });
      }),
    ),
    http.delete(`${base}/admin/audit-destinations/:id`, async ({ params }) => {
      await wait();
      if (!manage()) return problem(403, "Forbidden");
      const list = state(db);
      const i = list.findIndex((x) => x.id === params.id);
      if (i < 0) return problem(404, "Not found");
      const [d] = list.splice(i, 1);
      sealed(db).delete(d!.id);
      audited(db, "settings.audit_destination.delete", "audit_destination", d!.id, { name: d!.name }, d!.name);
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
