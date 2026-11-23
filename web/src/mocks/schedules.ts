// Recurring schedules in the mock backend (ADR-0057): a request of an item
// that makes no deployment, made again and again as its owner. Checks follow
// the server's; nothing fires here, the runs are seeded.
import { http, HttpResponse } from "msw";
import type { components } from "@/lib/api/schema";
import { can } from "@/lib/can";
import { audited } from "./governance";
import type { Db } from "./seed";

type S = components["schemas"];
type Stored = Omit<S["Schedule"], "next_runs" | "last_run" | "runs"> & { runs: S["ScheduleRun"][] };

/** What the mock's request checks say of a schedule's draft. */
export interface DraftCheck {
  errors: S["FieldError"][];
  warnings: S["PolicyWarning"][];
  makesDeployments: boolean;
  secrets: string[];
}

const ago = (min: number) => new Date(Date.now() - min * 60_000).toISOString();

function state(db: Db): Stored[] {
  const d = db as Db & { schedules?: Stored[] };
  if (d.schedules) return d.schedules;
  const tls = db.items.find((i) => i.slug === "tls-cert")!;
  const dns = db.items.find((i) => i.slug === "dns-record")!;
  const web = db.projects.find((p) => p.id === "prj_web")!;
  const project = { id: web.id, code: web.code, name: web.name };
  const ref = (i: typeof tls) => ({ id: i.id, slug: i.slug, name: i.name, type: i.type });
  const req = db.requests.find((r) => r.request.number === "REQ-10458")?.request;
  d.schedules = [
    {
      id: "sch_tlsweekly",
      name: "Weekly TLS renewal",
      catalog_item: ref(tls),
      version: tls.version,
      project,
      environment: "prd",
      owner: db.me.user,
      owner_seen_at: ago(5),
      inputs: { target: "shop.corp.example" },
      justification: "The shop's certificate, renewed before it nears expiry.",
      accept_warnings: false,
      cron: "0 6 * * 1",
      timezone: "Europe/Zurich",
      paused: false,
      created_at: ago(60 * 24 * 40),
      runs: [
        ...(req
          ? [
              {
                at: req.submitted_at,
                outcome: "started" as const,
                request: { id: req.id, number: req.number, status: req.status },
              },
            ]
          : []),
        {
          at: ago(60 * 24 * 13),
          outcome: "refused" as const,
          reason: "Target: the job's inventory has no host shop.corp.example.",
        },
      ],
    },
    {
      id: "sch_dnsaudit",
      name: "DNS zone audit",
      catalog_item: ref(dns),
      version: dns.version,
      project,
      environment: "dev",
      owner: db.users.find((u) => u.id === "usr_mkeller") ?? db.me.user,
      owner_seen_at: ago(60 * 24 * 12),
      inputs: { target: "dev.corp.example" },
      accept_warnings: false,
      every_minutes: 240,
      timezone: "UTC",
      paused: true,
      paused_note: "Until the zone has moved to the new DDI.",
      created_at: ago(60 * 24 * 200),
      // Its owner was away for months before signing in again, 12 days ago.
      runs: [
        { at: ago(60 * 24 * 3), outcome: "skipped", reason: "It was paused." },
        {
          at: ago(60 * 24 * 30),
          outcome: "skipped",
          reason:
            "Its owner hasn't signed in for 90 days: a run would have the groups of their last sign-in. It runs again once they sign in.",
        },
      ],
    },
  ];
  return d.schedules;
}

const monthNames = ["JAN", "FEB", "MAR", "APR", "MAY", "JUN", "JUL", "AUG", "SEP", "OCT", "NOV", "DEC"];
const dayNames = ["SUN", "MON", "TUE", "WED", "THU", "FRI", "SAT"];

/** A field's names as their numbers, as Temporal reads them: MON-FRI is 1-5, JAN is 1. */
function numbered(field: string, names: string[], first: number): string {
  return field.replace(/[A-Za-z]{3}/g, (n) => {
    const i = names.indexOf(n.toUpperCase());
    return i < 0 ? n : String(i + first);
  });
}

function fieldSet(field: string, min: number, max: number): Set<number> | null {
  const out = new Set<number>();
  if (max === 12) field = numbered(field, monthNames, 1);
  if (max === 7) field = numbered(field, dayNames, 0);
  for (const part of field.split(",")) {
    const m = /^(\*|(\d+)(?:-(\d+))?)(?:\/(\d+))?$/.exec(part);
    if (!m) return null;
    const lo = m[1] === "*" ? min : Number(m[2]);
    const hi = m[1] === "*" ? max : m[3] ? Number(m[3]) : m[4] ? max : lo;
    const step = Number(m[4] ?? 1);
    if (lo < min || hi > max || lo > hi || step < 1) return null;
    for (let v = lo; v <= hi; v += step) out.add(v === 7 && max === 7 ? 0 : v);
  }
  return out;
}

/** The offset of a time zone from UTC now, in minutes. */
function offsetOf(tz: string, at: Date): number {
  const parts = new Intl.DateTimeFormat("en-US", {
    timeZone: tz,
    hourCycle: "h23",
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  }).formatToParts(at);
  const v = (t: string) => Number(parts.find((p) => p.type === t)?.value);
  return Math.round((Date.UTC(v("year"), v("month") - 1, v("day"), v("hour"), v("minute")) - at.getTime()) / 60_000);
}

/** A cron expression's next times, read in its time zone (one offset: the mock's DST is approximate). */
function cronNext(cron: string, tz: string, n: number): string[] {
  const f = cron.split(/\s+/);
  const sets = [
    fieldSet(f[0] ?? "", 0, 59),
    fieldSet(f[1] ?? "", 0, 23),
    fieldSet(f[2] ?? "", 1, 31),
    fieldSet(f[3] ?? "", 1, 12),
    fieldSet(f[4] ?? "", 0, 7),
  ];
  if (f.length !== 5 || sets.some((s) => !s)) return [];
  const [mi, h, dom, mo, dow] = sets as Set<number>[];
  const now = new Date();
  const off = offsetOf(tz, new Date(Math.floor(now.getTime() / 60_000) * 60_000));
  let t = Math.floor(now.getTime() / 60_000) + 1 + off;
  const out: string[] = [];
  const day = 60 * 24;
  // Five years, as Temporal finds a 29 February: a day that can't match is skipped whole.
  const end = t + day * 366 * 5;
  while (t < end && out.length < n) {
    const d = new Date(t * 60_000);
    if (!dom!.has(d.getUTCDate()) || !mo!.has(d.getUTCMonth() + 1) || !dow!.has(d.getUTCDay())) {
      t = (Math.floor(t / day) + 1) * day;
      continue;
    }
    if (mi!.has(d.getUTCMinutes()) && h!.has(d.getUTCHours())) out.push(new Date((t - off) * 60_000).toISOString());
    t++;
  }
  return out;
}

function nextRuns(s: Stored): string[] {
  if (s.paused) return [];
  if (s.cron) return cronNext(s.cron, s.timezone, 5);
  const every = (s.every_minutes ?? 60) * 60_000;
  // As Temporal's intervals: counted from the epoch, not from when it was made.
  const first = Math.ceil((Date.now() + 1) / every) * every;
  return Array.from({ length: 5 }, (_, k) => new Date(first + k * every).toISOString());
}

function served(s: Stored, withRuns: boolean): S["Schedule"] {
  const { runs, ...rest } = s;
  return {
    ...rest,
    next_runs: nextRuns(s),
    ...(runs[0] ? { last_run: runs[0] } : {}),
    ...(withRuns ? { runs } : {}),
  };
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

const cronField = /^[0-9A-Za-z*,/\-?#]+$/;

function knownZone(tz: string): boolean {
  try {
    new Intl.DateTimeFormat("en-US", { timeZone: tz });
    return true;
  } catch {
    return false;
  }
}

function specErrors(w: S["ScheduleWrite"]): S["FieldError"][] {
  const cron = (w.cron ?? "").trim().split(/\s+/).filter(Boolean);
  const errors: S["FieldError"][] = [];
  if (cron.length > 0 && w.every_minutes != null)
    errors.push({
      field: "/cron",
      code: "one_of",
      message: "Give a cron expression or a number of minutes, not both.",
    });
  else if (cron.length === 0 && w.every_minutes == null)
    errors.push({
      field: "/cron",
      code: "required",
      message: "Say when it runs: a cron expression, or every so many minutes.",
    });
  else if (w.every_minutes != null) {
    if (!Number.isInteger(w.every_minutes) || w.every_minutes < 5 || w.every_minutes > 44640)
      errors.push({
        field: "/every_minutes",
        code: "out_of_range",
        message: "From every 5 minutes to every 31 days (44640 minutes).",
      });
  } else if (cron.length !== 5 || cron.some((f) => !cronField.test(f)))
    errors.push({
      field: "/cron",
      code: "pattern",
      message:
        "Five fields: minute, hour, day of month, month and day of week, such as 0 2 * * 1-5 for 2:00 on weekdays.",
    });
  else if (cronNext(cron.join(" "), "UTC", 1).length === 0)
    errors.push({ field: "/cron", code: "invalid", message: "Temporal refuses it: a field is out of its range." });
  if (!knownZone((w.timezone ?? "UTC").trim() || "UTC"))
    errors.push({
      field: "/timezone",
      code: "unknown",
      message: "Not a time zone rvav knows: an IANA name, such as Europe/Zurich.",
    });
  return errors;
}

export function scheduleHandlers(
  db: Db,
  base: string,
  wait: () => Promise<void>,
  check: (draft: S["RequestDraft"]) => DraftCheck | null,
) {
  const manages = (s: Stored) => s.owner.id === db.me.user.id || can(db.me, "request.manage", s.project.id);
  const sees = (s: Stored) => manages(s) || can(db.me, "deployments.read_all", s.project.id);
  const find = (id: unknown) => state(db).find((s) => s.id === id);
  // As the server: one who sees the project learns that it exists, and may not change it.
  const changeable = (id: unknown) => {
    const s = find(id);
    if (!s || (!sees(s) && !can(db.me, "project.see", s.project.id))) return problem(404, "Not found");
    if (!manages(s))
      return problem(403, "Not allowed", undefined, "Only its owner or a project admin can change this schedule.");
    return s;
  };
  return [
    http.get(`${base}/schedules`, async ({ request }) => {
      await wait();
      const project = new URL(request.url).searchParams.get("project_id");
      const items = state(db)
        .filter((s) => sees(s) && (!project || s.project.id === project))
        .map((s) => served(s, false));
      return HttpResponse.json<S["ScheduleList"]>({ items });
    }),
    http.post(`${base}/schedules`, async ({ request }) => {
      await wait();
      const w = (await request.json()) as S["ScheduleWrite"];
      const item = db.items.find((i) => i.id === w.catalog_item_id);
      const project = db.projects.find((p) => p.id === w.project_id);
      if (!item || !project) return problem(404, "Not found");
      if (!can(db.me, "request.submit", project.id)) return problem(403, "Not allowed");
      const errors = specErrors(w);
      const name = (w.name ?? "").trim();
      if (!name) errors.push({ field: "/name", code: "required", message: "Name it, in 120 characters at most." });
      else if ([...name].length > 120)
        errors.push({ field: "/name", code: "too_long", message: "Name it in 120 characters at most." });
      const inputs = w.inputs ?? {};
      const checked = check({
        catalog_item_id: item.id,
        version_id: w.version_id,
        project_id: project.id,
        environment: w.environment,
        inputs,
        count: 1,
        ...(w.justification ? { justification: w.justification } : {}),
      });
      if (!checked) return problem(404, "Not found");
      errors.push(...checked.errors);
      if (checked.errors.length === 0) {
        if (checked.makesDeployments)
          errors.push({
            field: "/catalog_item_id",
            code: "makes_deployments",
            message: `${item.name} makes a deployment, each run would make another: only an item that makes none, such as a custom workflow, runs on a schedule.`,
          });
        for (const f of checked.secrets)
          if (inputs[f] != null && inputs[f] !== "")
            errors.push({
              field: `/inputs/${f}`,
              code: "secret_input",
              message: "A schedule doesn't keep secret inputs: leave it empty, or have the item read a stored secret.",
            });
      }
      if (errors.length > 0) return problem(422, "Some inputs need attention", errors);
      if (checked.warnings.length > 0 && !w.accept_warnings)
        return problem(
          422,
          "Some inputs need attention",
          checked.warnings.map((x) => ({ field: "/accept_warnings", code: "warning", message: x.message, warning: x })),
          "A policy warns: submit again to go on.",
        );
      const cron = (w.cron ?? "").trim().split(/\s+/).filter(Boolean).join(" ");
      const s: Stored = {
        id: `sch_${Math.random().toString(36).slice(2, 10)}`,
        name,
        catalog_item: { id: item.id, slug: item.slug, name: item.name, type: item.type },
        version: db.forms[item.id]?.version_id === w.version_id ? item.version : { id: w.version_id, number: "" },
        project: { id: project.id, code: project.code, name: project.name },
        environment: w.environment,
        owner: db.me.user,
        owner_seen_at: new Date().toISOString(),
        inputs,
        ...(w.justification?.trim() ? { justification: w.justification.trim() } : {}),
        accept_warnings: Boolean(w.accept_warnings),
        ...(cron ? { cron } : { every_minutes: w.every_minutes! }),
        timezone: (w.timezone ?? "").trim() || "UTC",
        paused: false,
        created_at: new Date().toISOString(),
        runs: [],
      };
      state(db).unshift(s);
      audited(db, "schedule.create", "schedule", s.id, { name: s.name, item: item.slug });
      return HttpResponse.json(served(s, true), { status: 201 });
    }),
    http.get(`${base}/schedules/:id`, async ({ params }) => {
      await wait();
      const s = find(params.id);
      return s && sees(s) ? HttpResponse.json(served(s, true)) : problem(404, "Not found");
    }),
    http.post(`${base}/schedules/:id/pause`, async ({ params, request }) => {
      await wait();
      const s = changeable(params.id);
      if (s instanceof Response) return s;
      const raw = await request.text();
      const body = (raw ? JSON.parse(raw) : {}) as S["SchedulePause"];
      const note = (body.note ?? "").trim();
      if ([...note].length > 500)
        return problem(422, "Some inputs need attention", [
          { field: "/note", code: "too_long", message: "500 characters at most." },
        ]);
      s.paused = true;
      if (note) s.paused_note = note;
      else delete s.paused_note;
      audited(db, "schedule.pause", "schedule", s.id, { name: s.name, note });
      return HttpResponse.json(served(s, true));
    }),
    http.post(`${base}/schedules/:id/resume`, async ({ params }) => {
      await wait();
      const s = changeable(params.id);
      if (s instanceof Response) return s;
      s.paused = false;
      delete s.paused_note;
      audited(db, "schedule.resume", "schedule", s.id, { name: s.name });
      return HttpResponse.json(served(s, true));
    }),
    http.delete(`${base}/schedules/:id`, async ({ params }) => {
      await wait();
      const s = changeable(params.id);
      if (s instanceof Response) return s;
      const list = state(db);
      list.splice(list.indexOf(s), 1);
      audited(db, "schedule.delete", "schedule", s.id, { name: s.name }, s.name);
      return new HttpResponse(null, { status: 204 });
    }),
  ];
}
