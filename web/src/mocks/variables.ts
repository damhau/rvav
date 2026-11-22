// Shared variables in the mock backend (ADR-0057): named values that aren't
// secret, which composite steps read as vars.<group>.<name>. Checks follow
// the server's.
import { http, HttpResponse } from "msw";
import type { components } from "@/lib/api/schema";
import { can } from "@/lib/can";
import { itemBlueprints } from "./catalogAdmin";
import { audited } from "./governance";
import type { Db } from "./seed";
import { unknownField } from "./strict";

type S = components["schemas"];
type Stored = Omit<S["Variable"], "used_by" | "key">;

const now = () => new Date().toISOString();
const ago = (days: number) => new Date(Date.now() - days * 86_400_000).toISOString();

const part = /^[a-z][a-z0-9_]{0,40}$/;
const secretLike = /password|passwd|passphrase|secret|token|credential|private_key|ssh_key|api_key|apikey/;
const maxValue = 16 << 10;

function state(db: Db): Stored[] {
  const d = db as Db & { variables?: Stored[] };
  d.variables ??= [
    {
      group: "backup",
      name: "s3_bucket",
      value: "pg-backups-gva",
      description: "Where pgBackRest keeps the clusters' backups.",
      created_at: ago(40),
      updated_at: ago(6),
      updated_by: "Léa Martin",
    },
    {
      group: "cmdb",
      name: "site",
      value: "gva",
      description: "",
      created_at: ago(40),
      updated_at: ago(40),
      updated_by: "Léa Martin",
    },
    {
      group: "network",
      name: "dns_servers",
      value: ["10.20.0.53", "10.20.0.54"],
      description: "The resolvers every VM is given.",
      created_at: ago(30),
      updated_at: ago(2),
      updated_by: "Marc Keller",
    },
  ];
  return d.variables;
}

const keyOf = (v: { group: string; name: string }) => `${v.group}.${v.name}`;

const reads = (bp: unknown, key: string) =>
  new RegExp(`\\bvars\\.${key.replace(".", "\\.")}(?![a-z0-9_])`).test(JSON.stringify(bp ?? null));

/** The shared variables as steps read them, by group and name. */
export function variableValues(db: Db): Record<string, Record<string, unknown>> {
  const out: Record<string, Record<string, unknown>> = {};
  for (const v of state(db)) (out[v.group] ??= {})[v.name] = v.value;
  return out;
}

const unfinished = new Set(["pending_approval", "scheduled", "queued", "running", "cancelling"]);

// As store.VariableUsers: a failed request isn't counted, or it would keep
// the variable for ever.
function served(db: Db, v: Stored): S["Variable"] {
  const key = keyOf(v);
  const reading = itemBlueprints(db).filter((x) => reads(x.blueprint, key));
  const deprecated = new Set(reading.filter((x) => x.state === "deprecated").map((x) => x.id));
  const usedBy = [
    ...new Set([
      ...reading.filter((x) => x.state === "draft" || x.state === "published").map((x) => `item ${x.slug}`),
      ...db.requests
        .filter((r) => unfinished.has(r.request.status) && deprecated.has(r.request.version.id))
        .map((r) => `request ${r.request.number}`),
    ]),
  ].sort();
  return { ...v, key, used_by: usedBy };
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

const notFound = () => problem(404, "Not found", undefined, "It does not exist, or you cannot see it.");

function checkValue(value: unknown, description: string | undefined): S["FieldError"][] {
  const errors: S["FieldError"][] = [];
  if (value === undefined || value === null)
    errors.push({
      field: "/value",
      code: "required",
      message: "Give it a value: text, a number, true or false, a list or an object.",
    });
  else if (new TextEncoder().encode(JSON.stringify(value)).length > maxValue)
    errors.push({ field: "/value", code: "too_long", message: "At most 16 KiB of JSON: keep files elsewhere." });
  if ([...(description ?? "").trim()].length > 500)
    errors.push({ field: "/description", code: "too_long", message: "At most 500 characters." });
  return errors;
}

const forbidden = () => problem(403, "Not allowed", undefined, "You do not have permission to do this.");

export function variableHandlers(db: Db, base: string, wait: () => Promise<void>) {
  const manage = () => can(db.me, "catalog.manage");
  // Auditors read them, as they read the run data (core.readsRunData).
  const read = () => manage() || can(db.me, "extensibility.read");
  const find = (key: unknown) => state(db).findIndex((v) => keyOf(v) === key);
  return [
    http.get(`${base}/admin/variables`, async () => {
      await wait();
      if (!read()) return forbidden();
      return HttpResponse.json({
        items: [...state(db)].sort((a, b) => keyOf(a).localeCompare(keyOf(b))).map((v) => served(db, v)),
      });
    }),
    http.post(`${base}/admin/variables`, async ({ request }) => {
      await wait();
      const w = (await request.json()) as S["VariableCreate"];
      const strict = unknownField(w, ["group", "name", "value", "description"]);
      if (strict) return strict;
      if (!manage()) return forbidden();
      const group = (w.group ?? "").trim();
      const name = (w.name ?? "").trim();
      const errors: S["FieldError"][] = [];
      for (const [field, value] of [
        ["/group", group],
        ["/name", name],
      ] as const) {
        if (!part.test(value))
          errors.push({
            field,
            code: "pattern",
            message:
              "1 to 41 lower-case letters, digits and underscores, starting with a letter: steps read vars.<group>.<name>.",
          });
        else if (secretLike.test(value))
          errors.push({
            field,
            code: "secret",
            message: "A variable isn't masked anywhere: store a password, a token or a key as a secret instead.",
          });
      }
      errors.push(...checkValue(w.value, w.description));
      if (errors.length > 0) return problem(422, "Some inputs need attention", errors);
      if (find(`${group}.${name}`) >= 0)
        return problem(
          409,
          "Not possible right now",
          undefined,
          `There is a variable ${group}.${name} already: change its value instead.`,
        );
      const v: Stored = {
        group,
        name,
        value: w.value,
        description: (w.description ?? "").trim(),
        created_at: now(),
        updated_at: now(),
        updated_by: db.me.user.name,
      };
      state(db).push(v);
      audited(db, "variable.create", "variable", keyOf(v), { value: v.value });
      return HttpResponse.json(served(db, v), { status: 201 });
    }),
    http.get(`${base}/admin/variables/:key`, async ({ params }) => {
      await wait();
      if (!read()) return forbidden();
      const v = state(db)[find(params.key)];
      return v ? HttpResponse.json(served(db, v)) : notFound();
    }),
    http.put(`${base}/admin/variables/:key`, async ({ params, request }) => {
      await wait();
      const w = (await request.json()) as S["VariableUpdate"];
      const strict = unknownField(w, ["value", "description"]);
      if (strict) return strict;
      if (!manage()) return forbidden();
      const list = state(db);
      const i = find(params.key);
      if (i < 0) return notFound();
      const errors = checkValue(w.value, w.description);
      if (errors.length > 0) return problem(422, "Some inputs need attention", errors);
      const was = list[i]!;
      const v: Stored = {
        ...was,
        value: w.value,
        description: (w.description ?? "").trim(),
        updated_at: now(),
        updated_by: db.me.user.name,
      };
      list[i] = v;
      audited(db, "variable.update", "variable", keyOf(v), { value: v.value, was: was.value });
      return HttpResponse.json(served(db, v));
    }),
    http.delete(`${base}/admin/variables/:key`, async ({ params }) => {
      await wait();
      if (!manage()) return forbidden();
      const list = state(db);
      const i = find(params.key);
      if (i < 0) return notFound();
      const v = served(db, list[i]!);
      if (v.used_by.length > 0)
        return problem(
          409,
          "Not possible right now",
          undefined,
          `It is read by ${v.used_by.join(", ")}: ${
            v.used_by.some((u) => u.startsWith("request "))
              ? "change the items' steps, and let the requests finish, first."
              : "change their steps first."
          }`,
        );
      list.splice(i, 1);
      audited(db, "variable.delete", "variable", v.key);
      return new HttpResponse(null, { status: 204 });
    }),
  ];
}
