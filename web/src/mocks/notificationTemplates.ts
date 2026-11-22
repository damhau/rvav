// Notification templates in the mock backend (ADR-0059): each kind's
// built-in templates, variables and sample come from the server's fixture
// (internal/notify/testdata/templates.json, kept the same by
// notificationTemplates.test.ts); edits override them, checked and previewed
// with a small Go template engine.
import { http, HttpResponse } from "msw";
import type { components } from "@/lib/api/schema";
import { can } from "@/lib/can";
import { brandingOf, brandingState } from "./branding";
import fixture from "./notificationTemplates.json";
import { audited } from "./governance";
import { TemplateError, checkFields, execute, html, parse, type Values } from "./gotemplate";
import type { Db } from "./seed";

type S = components["schemas"];
type Kind = S["NotificationKind"];
type Channel = S["TemplateChannel"];
type Locale = S["TemplateLocale"];
type Parts = S["TemplateParts"];

interface FixtureKind {
  kind: Kind;
  name: string;
  when: string;
  variables: S["TemplateVariable"][];
  sample: Record<Locale, Values>;
  defaults: Record<Channel, Record<Locale, Parts>>;
}
export const templateFixture = fixture as unknown as { functions: S["TemplateVariable"][]; kinds: FixtureKind[] };

const channels: Channel[] = ["email", "teams", "slack"];
const locales: Locale[] = ["en", "fr"];
// In bytes of UTF-8, as the server counts: what a part is, and what it writes.
const limits = { subject: 1 << 10, text: 20 << 10, html: 200 << 10 };
const writes = { subject: 64 << 10, text: 64 << 10, html: 512 << 10 };
const bytes = (s: string) => new TextEncoder().encode(s).length;

interface Edit extends Parts {
  updated_at: string;
  updated_by: string;
}

function edits(db: Db): Map<string, Edit> {
  const d = db as Db & { notificationTemplates?: Map<string, Edit> };
  d.notificationTemplates ??= new Map();
  return d.notificationTemplates;
}

const keyOf = (kind: string, channel: string, locale: string) => `${kind}/${channel}/${locale}`;
const kindOf = (kind: unknown) => templateFixture.kinds.find((k) => k.kind === kind);

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

const sentence = (s: string) => {
  const t = s.charAt(0).toUpperCase() + s.slice(1);
  return t.endsWith(".") ? t : t + ".";
};

type Part = "subject" | "text" | "html";
interface Problem {
  part: Part;
  message: string;
}

type Compiled = Partial<Record<Part, ReturnType<typeof parse>>>;

function compile(k: FixtureKind, channel: Channel, p: Parts): [Compiled, Problem[]] {
  const vars = k.variables.map((v) => v.name);
  const lists = k.variables.filter((v) => v.type === "list of text").map((v) => v.name);
  const out: Compiled = {};
  const problems: Problem[] = [];
  const one = (part: Part, src: string | undefined) => {
    if (!src?.trim()) return problems.push({ part, message: "it is empty" });
    if (bytes(src) > limits[part])
      return problems.push({ part, message: `it is longer than ${limits[part] >> 10} KB` });
    try {
      const nodes = parse(src);
      checkFields(nodes, vars, lists);
      out[part] = nodes;
    } catch (e) {
      if (!(e instanceof TemplateError)) throw e;
      problems.push({ part, message: e.message });
    }
  };
  one("subject", p.subject);
  one("text", p.text);
  if (channel !== "email" && p.html) problems.push({ part: "html", message: "only emails have an HTML part" });
  else if (channel === "email") one("html", p.html);
  return [out, problems];
}

const markdown = (s: string) => s.replace(/[\\[\]()*_`#>~]/g, (c) => "\\" + c);
const slackEscape = (s: string) => s.replaceAll("&", "&amp;").replaceAll("<", "&lt;").replaceAll(">", "&gt;");

function escaped(values: Values, esc: (s: string) => string): Values {
  return Object.fromEntries(
    Object.entries(values).map(([k, v]) => [
      k,
      typeof v === "string"
        ? esc(v)
        : Array.isArray(v)
          ? v.map((x: unknown) => (typeof x === "string" ? esc(x) : x))
          : v,
    ]),
  );
}

interface Written {
  subject: string;
  text: string;
  html?: string;
}

function write(c: Compiled, channel: Channel, values: Values): [Written, Problem[]] {
  const problems: Problem[] = [];
  const run = (part: Part, v: Values, esc?: (s: string) => string) => {
    try {
      const out = execute(c[part]!, v, esc);
      if (bytes(out) > writes[part]) throw new TemplateError(`it writes more than ${writes[part] >> 10} KB`);
      return out;
    } catch (e) {
      if (!(e instanceof TemplateError)) throw e;
      problems.push({ part, message: e.message.replace(/^line \d+: /, "") });
      return "";
    }
  };
  const textValues =
    channel === "teams" ? escaped(values, markdown) : channel === "slack" ? escaped(values, slackEscape) : values;
  const out: Written = {
    subject: run("subject", values).split(/\s+/).filter(Boolean).join(" "),
    text: run("text", textValues).trim(),
    ...(c.html ? { html: run("html", values, html) } : {}),
  };
  if (!problems.length && !out.subject) problems.push({ part: "subject", message: "it writes nothing" });
  return [out, problems];
}

function sample(db: Db, k: FixtureKind, locale: Locale): Values {
  return { ...k.sample[locale], ".Portal": brandingOf(db).name };
}

/** What is wrong with a template, part by part, as the server says it. */
function check(k: FixtureKind, channel: Channel, locale: Locale, p: Parts): [Compiled, Problem[]] {
  const [c, problems] = compile(k, channel, p);
  if (problems.length) return [c, problems];
  const [, failed] = write(c, channel, k.sample[locale]);
  return [c, failed.map((x) => ({ ...x, message: "with the sample data, " + x.message }))];
}

const refusal = (problems: Problem[]) =>
  problem(
    422,
    "Validation failed",
    problems.map((p) => ({ field: `/${p.part}`, code: "template", message: sentence(p.message) })),
  );

const labels = {
  en: {
    request: "Request",
    item: "Item",
    deployment: "Deployment",
    project: "Project",
    open: "Open in %s",
    hello: "Hello",
  },
  fr: {
    request: "Demande",
    item: "Élément",
    deployment: "Déploiement",
    project: "Projet",
    open: "Ouvrir dans %s",
    hello: "Bonjour",
  },
};
const buttons: Record<Locale, Partial<Record<Kind, string>>> = {
  en: { approval_needed: "Decide in %s", lease_expiring: "Extend the lease" },
  fr: { approval_needed: "Décider dans %s", lease_expiring: "Prolonger le bail" },
};

function facts(v: Values, locale: Locale): { label: string; value: string }[] {
  const l = labels[locale];
  const s = (name: string) => {
    const x = v[name];
    return typeof x === "string" ? x : "";
  };
  const out: { label: string; value: string }[] = [];
  const add = (label: string, value: string) => value && out.push({ label, value });
  if (s(".Request.Number")) add(l.request, `${s(".Request.Number")}, ${s(".Request.Title")}`);
  add(l.item, s(".Request.Item"));
  add(l.deployment, s(".Deployment.Name"));
  if (s(".Project.Name")) add(l.project, `${s(".Project.Name")} (${s(".Project.Environment")})`);
  return out;
}

function preview(
  db: Db,
  k: FixtureKind,
  channel: Channel,
  locale: Locale,
  w: Written,
): S["NotificationTemplatePreview"] {
  const v = sample(db, k, locale);
  const portal = String(v[".Portal"]);
  const link = String(v[".Link"]);
  const l = labels[locale];
  const button = (buttons[locale][k.kind] ?? l.open).replace("%s", portal);
  const open = l.open.replace("%s", portal);
  const fs = facts(v, locale);
  if (channel === "email") {
    const b = brandingOf(db);
    const logo = brandingState(db).logos.light;
    const logoSrc =
      logo && logo.type !== "image/svg+xml"
        ? `data:${logo.type};base64,${btoa(String.fromCharCode(...logo.content))}`
        : "";
    const user = html(String(v[".User.Name"]));
    const text = [
      `${l.hello} ${String(v[".User.Name"])},`,
      "",
      w.text,
      ...fs.map((f) => `${(f.label + ":").padEnd(12)} ${f.value}`),
      "",
      `${button}: ${link}`,
      "",
      "--",
      `${portal}. You get this because of your notification preferences: ${String(v[".Preferences"])}`,
    ].join("\n");
    const page = `<!doctype html>
<html lang="${locale}"><head><meta charset="utf-8"><title>${html(w.subject)}</title></head>
<body style="margin:0;padding:24px;background:#f6f7f9;font-family:-apple-system,'Segoe UI',Helvetica,Arial,sans-serif;color:#1f2328;line-height:1.5">
<div style="max-width:560px;margin:0 auto 12px">${logoSrc ? `<img src="${logoSrc}" alt="${html(portal)}" height="32" style="display:block;height:32px;width:auto;border:0">` : `<span style="font-size:16px;font-weight:600">${html(portal)}</span>`}</div>
<div style="max-width:560px;margin:0 auto;background:#ffffff;border:1px solid #d8dee4;border-radius:8px;padding:24px">
<p style="margin:0 0 12px">${l.hello} ${user},</p>
${w.html ?? ""}
${fs.length ? `<table style="border-collapse:collapse;margin:16px 0;font-size:14px">${fs.map((f) => `<tr><td style="padding:2px 16px 2px 0;color:#59636e">${html(f.label)}</td><td>${html(f.value)}</td></tr>`).join("")}</table>` : ""}
<p style="margin:20px 0 0"><a href="${html(link)}" style="display:inline-block;background:${b.light.accent};color:${b.light.accent_fg};text-decoration:none;padding:8px 14px;border-radius:6px">${html(button)}</a></p>
</div>
<p style="max-width:560px;margin:12px auto 0;font-size:12px;color:#59636e">${html(portal)}. You get this because of your <a href="${html(String(v[".Preferences"]))}" style="color:#59636e">notification preferences</a>.</p>
</body></html>`;
    return { subject: w.subject, text, html: page };
  }
  if (channel === "teams") {
    const body: unknown[] = [
      { type: "TextBlock", text: markdown(w.subject), wrap: true, weight: "Bolder", size: "Medium" },
      { type: "TextBlock", text: w.text, wrap: true },
    ];
    if (fs.length)
      body.push({ type: "FactSet", facts: fs.map((f) => ({ title: markdown(f.label), value: markdown(f.value) })) });
    return {
      subject: w.subject,
      text: w.text,
      card: {
        type: "AdaptiveCard",
        $schema: "http://adaptivecards.io/schemas/adaptive-card.json",
        version: "1.4",
        body,
        actions: [{ type: "Action.OpenUrl", title: open, url: link }],
      },
    };
  }
  const blocks: Record<string, unknown>[] = [
    { type: "header", text: { type: "plain_text", text: w.subject.slice(0, 150) } },
    { type: "section", text: { type: "mrkdwn", text: w.text } },
  ];
  if (fs.length)
    blocks.push({
      type: "section",
      fields: fs.map((f) => ({ type: "mrkdwn", text: `*${slackEscape(f.label)}*\n${slackEscape(f.value)}` })),
    });
  blocks.push({
    type: "actions",
    block_id: "rvav",
    elements: [
      { type: "button", action_id: "rvav_open", value: "open", text: { type: "plain_text", text: open }, url: link },
    ],
  });
  return { subject: w.subject, text: w.text, blocks };
}

function served(db: Db, k: FixtureKind, channel: Channel, locale: Locale): S["NotificationTemplate"] {
  const d = k.defaults[channel][locale];
  const e = edits(db).get(keyOf(k.kind, channel, locale));
  const parts = e ?? d;
  return {
    kind: k.kind,
    channel,
    locale,
    subject: parts.subject,
    text: parts.text,
    ...(channel === "email" ? { html: parts.html ?? "" } : {}),
    edited: Boolean(e),
    default: d,
    ...(e ? { updated_at: e.updated_at, updated_by: e.updated_by } : {}),
  };
}

const isChannel = (c: unknown): c is Channel => channels.includes(c as Channel);
const isLocale = (l: unknown): l is Locale => locales.includes(l as Locale);

/** As the server's templateTarget: a channel or locale that isn't one is a 400 in the query, a 422 in the body. */
function badTarget(channel: unknown, locale: unknown, inQuery: boolean) {
  const errors: S["FieldError"][] = [];
  if (!isChannel(channel)) errors.push({ field: "/channel", code: "invalid", message: "The channel is email, teams or slack." }); // prettier-ignore
  if (!isLocale(locale)) errors.push({ field: "/locale", code: "invalid", message: "The locale is en or fr." });
  if (!errors.length) return undefined;
  if (inQuery) return problem(400, "The request is not valid", undefined, errors.map((e) => e.message).join(" "));
  return problem(422, "Some inputs need attention", errors);
}

export function notificationTemplateHandlers(db: Db, base: string, wait: () => Promise<void>) {
  const manage = () => can(db.me, "system.manage");
  const query = (request: Request) => {
    const u = new URL(request.url);
    return { channel: u.searchParams.get("channel"), locale: u.searchParams.get("locale") };
  };
  return [
    http.get(`${base}/admin/notification-templates`, async () => {
      await wait();
      if (!manage()) return problem(403, "Forbidden");
      return HttpResponse.json({
        functions: templateFixture.functions,
        kinds: templateFixture.kinds.map((k) => ({
          kind: k.kind,
          name: k.name,
          when: k.when,
          variables: k.variables,
          templates: channels.flatMap((channel) =>
            locales.map((locale) => {
              const e = edits(db).get(keyOf(k.kind, channel, locale));
              return {
                channel,
                locale,
                edited: Boolean(e),
                ...(e ? { updated_at: e.updated_at, updated_by: e.updated_by } : {}),
              };
            }),
          ),
        })),
      } satisfies S["NotificationTemplateCatalog"]);
    }),
    http.get(`${base}/admin/notification-templates/:kind`, async ({ request, params }) => {
      await wait();
      if (!manage()) return problem(403, "Forbidden");
      const k = kindOf(params.kind);
      if (!k) return problem(404, "Not found");
      const { channel, locale } = query(request);
      const bad = badTarget(channel, locale, true);
      if (bad || !isChannel(channel) || !isLocale(locale)) return bad;
      return HttpResponse.json(served(db, k, channel, locale));
    }),
    http.put(`${base}/admin/notification-templates/:kind`, async ({ request, params }) => {
      await wait();
      if (!manage()) return problem(403, "Forbidden");
      const k = kindOf(params.kind);
      if (!k) return problem(404, "Not found");
      const w = (await request.json()) as S["NotificationTemplateWrite"];
      const bad = badTarget(w.channel, w.locale, false);
      if (bad) return bad;
      const [, problems] = check(k, w.channel, w.locale, w);
      if (problems.length) return refusal(problems);
      const key = keyOf(k.kind, w.channel, w.locale);
      const was = served(db, k, w.channel, w.locale);
      edits(db).set(key, {
        subject: w.subject,
        text: w.text,
        ...(w.channel === "email" ? { html: w.html ?? "" } : {}),
        updated_at: new Date().toISOString(),
        updated_by: db.me.user.name,
      });
      audited(db, "settings.notification_template.update", "notification_template", key, {
        subject: w.subject,
        was: { subject: was.subject, edited: was.edited },
      });
      return HttpResponse.json(served(db, k, w.channel, w.locale));
    }),
    http.delete(`${base}/admin/notification-templates/:kind`, async ({ request, params }) => {
      await wait();
      if (!manage()) return problem(403, "Forbidden");
      const k = kindOf(params.kind);
      if (!k) return problem(404, "Not found");
      const { channel, locale } = query(request);
      const bad = badTarget(channel, locale, true);
      if (bad || !isChannel(channel) || !isLocale(locale)) return bad;
      const key = keyOf(k.kind, channel, locale);
      if (edits(db).delete(key)) audited(db, "settings.notification_template.reset", "notification_template", key);
      return HttpResponse.json(served(db, k, channel, locale));
    }),
    http.post(`${base}/admin/notification-templates/:kind/preview`, async ({ request, params }) => {
      await wait();
      if (!manage()) return problem(403, "Forbidden");
      const k = kindOf(params.kind);
      if (!k) return problem(404, "Not found");
      const w = (await request.json()) as S["NotificationTemplateWrite"];
      const bad = badTarget(w.channel, w.locale, false);
      if (bad) return bad;
      const [c, problems] = check(k, w.channel, w.locale, w);
      if (problems.length) return refusal(problems);
      // Written with the portal's branding, the first problem only, as the server says it.
      const [written, failed] = write(c, w.channel, sample(db, k, w.locale));
      if (failed.length) return refusal(failed.slice(0, 1));
      return HttpResponse.json(preview(db, k, w.channel, w.locale, written));
    }),
  ];
}
