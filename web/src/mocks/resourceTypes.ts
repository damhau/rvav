// Custom resource types in the mock backend (ADR-0056): what creates,
// deletes, updates and reads a type's resources, its properties and the
// day-2 actions offered on them. Checks follow the server's.
import { http, HttpResponse } from "msw";
import type { components } from "@/lib/api/schema";
import { can } from "@/lib/can";
import { itemBlueprints } from "./catalogAdmin";
import { workflowRegistrations } from "./extensibility";
import { audited, auditTargetNames } from "./governance";
import type { Db } from "./seed";
import { unknownField } from "./strict";

type S = components["schemas"];
type Stored = Omit<S["ResourceType"], "resources" | "used_by">;
type Driver = S["ResourceTypeDriver"];

const now = () => new Date().toISOString();
const ago = (days: number) => new Date(Date.now() - days * 86_400_000).toISOString();

/** The AWX templates and day-2 actions the mocks' AWX has, as /admin/awx/templates and /admin/awx/actions list them. */
export const awxJobTemplates = ["install-monitoring-agent", "create-load-balancer", "delete-load-balancer"];
export const awxActionKeys = ["awx.install-monitoring-agent", "awx.patch-now"];

auditTargetNames("resource_type", (db, key) => state(db).find((t) => t.key === key)?.title);

function state(db: Db): Stored[] {
  const d = db as Db & { resourceTypes?: Stored[] };
  d.resourceTypes ??= [
    {
      key: "load-balancer",
      title: "Load balancer",
      description: "A virtual server on the F5 pair, with a pool of the deployment's VMs.",
      create: { kind: "awx", name: "create-load-balancer" },
      delete: { kind: "workflow", name: "lb-delete" },
      property_schema: {
        type: "object",
        properties: {
          vip: { type: "string", title: "Virtual IP" },
          pool: { type: "string", title: "Pool" },
          port: { type: "integer", title: "Port" },
        },
      },
      actions: ["wf.lb-drain"],
      enabled: true,
      created_at: ago(30),
      updated_at: ago(3),
    },
    {
      key: "dns-alias",
      title: "DNS alias",
      description: "A CNAME in the corporate zone.",
      create: { kind: "workflow", name: "dns-alias" },
      delete: { kind: "workflow", name: "dns-alias" },
      property_schema: { type: "object", properties: { fqdn: { type: "string", title: "Alias" } } },
      actions: [],
      enabled: false,
      created_at: ago(12),
      updated_at: ago(12),
    },
  ];
  return d.resourceTypes;
}

/** The custom resource types the mocks keep. */
export const resourceTypesOf = (db: Db) => state(db);

/** The resource types a custom workflow runs or offers as an action, as its used_by says them. */
export const typesUsingWorkflow = (db: Db, key: string) =>
  state(db)
    .filter(
      (t) =>
        [t.create, t.delete, t.update, t.read].some((d) => d?.kind === "workflow" && d.name === key) ||
        (t.actions ?? []).includes(`wf.${key}`),
    )
    .map((t) => `resource type ${t.key}`);

const names = (bp: unknown, key: string): boolean => {
  if (!bp || typeof bp !== "object") return false;
  if (Array.isArray(bp)) return bp.some((x) => names(x, key));
  const o = bp as Record<string, unknown>;
  const res = o.resource as { type?: unknown } | undefined;
  if (res && typeof res === "object" && res.type === key) return true;
  if (Array.isArray(o.resources) && o.resources.some((r) => (r as { type?: unknown } | null)?.type === key))
    return true;
  return Object.values(o).some((v) => names(v, key));
};

function served(db: Db, t: Stored): S["ResourceType"] {
  const resources = db.deployments
    .filter((d) => d.status !== "deleted")
    .flatMap((d) => d.resources)
    .filter((r) => r.type === "custom" && r.resource_type.key === t.key).length;
  const usedBy = [
    ...new Set(
      itemBlueprints(db)
        .filter((v) => (v.state === "draft" || v.state === "published") && names(v.blueprint, t.key ?? ""))
        .map((v) => `item ${v.slug}`),
    ),
  ];
  return { ...t, resources, used_by: usedBy };
}

function check(db: Db, w: S["ResourceTypeWrite"], create: boolean): S["FieldError"][] {
  const errors: S["FieldError"][] = [];
  const err = (field: string, code: string, message: string) => errors.push({ field, code, message });
  if (create && !/^[a-z][a-z0-9-]{1,40}$/.test(w.key ?? ""))
    err("/key", "pattern", "2 to 41 lower-case letters, digits and dashes, starting with a letter.");
  if (!w.title?.trim()) err("/title", "required", "Give it a title.");
  const workflows = workflowRegistrations(db);
  const driver = (phase: string, d: Driver | undefined) => {
    if (!d) return;
    const name = d.name.trim();
    if (!name) return err(`/${phase}/name`, "required", "Name the custom workflow or the AWX template.");
    if (d.kind === "awx") {
      if (!awxJobTemplates.includes(name)) err(`/${phase}/name`, "unknown", `AWX has no job template ${name}.`);
      return;
    }
    const wf = workflows.find((x) => x.key === name);
    if (!wf) err(`/${phase}/name`, "unknown", `There is no custom workflow ${name} registered.`);
    else if (!wf.enabled) err(`/${phase}/name`, "unknown", `The custom workflow ${name} is disabled.`);
    else if (!wf.usages.includes("resource_type"))
      err(`/${phase}/name`, "unknown", `The custom workflow ${name} isn't registered for this use (resource_type).`);
  };
  driver("create", w.create);
  driver("delete", w.delete);
  driver("update", w.update);
  driver("read", w.read);
  if (w.property_schema && w.property_schema.type !== undefined && w.property_schema.type !== "object")
    err("/property_schema/type", "invalid", "A resource's properties are an object.");
  const seen: string[] = [];
  (w.actions ?? []).forEach((raw, i) => {
    const k = raw.trim();
    const at = `/actions/${i}`;
    if (seen.includes(k)) return err(at, "duplicate", "Listed twice.");
    seen.push(k);
    if (k.startsWith("wf.")) {
      const wf = workflows.find((x) => x.key === k.slice(3));
      if (!wf) err(at, "unknown", `There is no custom workflow ${k.slice(3)} registered.`);
      else if (!wf.usages.includes("day2_action"))
        err(at, "unknown", `The custom workflow ${k.slice(3)} isn't registered as a day-2 action.`);
    } else if (k.startsWith("awx.")) {
      if (!awxActionKeys.includes(k)) err(at, "unknown", `There is no AWX action ${k}.`);
    } else err(at, "invalid", "A custom workflow (wf.<key>) or an AWX action (awx.<template>).");
  });
  return errors;
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

const stored = (w: S["ResourceTypeWrite"], key: string, created: string): Stored => ({
  key,
  title: w.title.trim(),
  ...(w.description?.trim() ? { description: w.description.trim() } : {}),
  create: { ...w.create, name: w.create.name.trim() },
  delete: { ...w.delete, name: w.delete.name.trim() },
  ...(w.update ? { update: { ...w.update, name: w.update.name.trim() } } : {}),
  ...(w.read ? { read: { ...w.read, name: w.read.name.trim() } } : {}),
  property_schema: w.property_schema ?? { type: "object" },
  actions: (w.actions ?? []).map((a) => a.trim()),
  enabled: w.enabled ?? true,
  created_at: created,
  updated_at: now(),
});

const writeFields = [
  "key",
  "title",
  "description",
  "create",
  "delete",
  "update",
  "read",
  "property_schema",
  "actions",
  "enabled",
];
const forbidden = () => problem(403, "Not allowed", undefined, "You do not have permission to do this.");
const notFound = () => problem(404, "Not found", undefined, "It does not exist, or you cannot see it.");

export function resourceTypeHandlers(db: Db, base: string, wait: () => Promise<void>) {
  const read = () => can(db.me, "extensibility.read");
  const manage = () => can(db.me, "extensibility.manage");
  return [
    http.get(`${base}/admin/resource-types`, async () => {
      await wait();
      if (!read()) return forbidden();
      return HttpResponse.json({ items: state(db).map((t) => served(db, t)) });
    }),
    http.post(`${base}/admin/resource-types`, async ({ request }) => {
      await wait();
      const w = (await request.json()) as S["ResourceTypeWrite"];
      const strict = unknownField(w, writeFields);
      if (strict) return strict;
      if (!manage()) return forbidden();
      const key = (w.key ?? "").trim();
      const errors = check(db, { ...w, key }, true);
      if (!errors.some((e) => e.field === "/key") && state(db).some((t) => t.key === key))
        return problem(409, "Not possible right now", undefined, `A resource type is defined as ${key}.`);
      if (errors.length > 0) return problem(422, "Some inputs need attention", errors);
      const t = stored(w, key, now());
      state(db).push(t);
      audited(db, "resource_type.create", "resource_type", key, { create: t.create, delete: t.delete });
      return HttpResponse.json(served(db, t), { status: 201 });
    }),
    http.get(`${base}/admin/resource-types/:key`, async ({ params }) => {
      await wait();
      if (!read()) return forbidden();
      const t = state(db).find((x) => x.key === params.key);
      return t ? HttpResponse.json(served(db, t)) : notFound();
    }),
    http.put(`${base}/admin/resource-types/:key`, async ({ params, request }) => {
      await wait();
      const w = (await request.json()) as S["ResourceTypeWrite"];
      const strict = unknownField(w, writeFields);
      if (strict) return strict;
      if (!manage()) return forbidden();
      const list = state(db);
      const i = list.findIndex((x) => x.key === params.key);
      if (i < 0) return notFound();
      const errors = check(db, w, false);
      if (errors.length > 0) return problem(422, "Some inputs need attention", errors);
      const t = stored(w, list[i]!.key ?? "", list[i]!.created_at);
      list[i] = t;
      audited(db, "resource_type.update", "resource_type", t.key ?? "", { create: t.create, delete: t.delete });
      return HttpResponse.json(served(db, t));
    }),
    http.delete(`${base}/admin/resource-types/:key`, async ({ params }) => {
      await wait();
      if (!manage()) return forbidden();
      const list = state(db);
      const i = list.findIndex((x) => x.key === params.key);
      if (i < 0) return notFound();
      const t = served(db, list[i]!);
      if (t.resources > 0)
        return problem(
          409,
          "Not possible right now",
          undefined,
          `Deployments have ${t.resources} resource${t.resources === 1 ? "" : "s"} of it: disable it instead, and remove it once they are deleted.`,
        );
      if (t.used_by.length > 0)
        return problem(
          409,
          "Not possible right now",
          undefined,
          `It is used by ${t.used_by.join(", ")}: disable it instead.`,
        );
      list.splice(i, 1);
      audited(db, "resource_type.delete", "resource_type", t.key ?? "", {}, t.title);
      return new HttpResponse(null, { status: 204 });
    }),
  ];
}
