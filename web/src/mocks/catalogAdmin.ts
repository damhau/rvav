// Catalog administration in the mock backend (ADR-0019): versions with
// publish, deprecate and roll back, entitlements, categories, YAML. A
// published version also becomes what the user-facing catalog serves.
import { http, HttpResponse } from "msw";
import { parse, stringify } from "yaml";
import type { components } from "@/lib/api/schema";
import { shownUrl } from "@/lib/utils";
import { ownSources } from "@/forms/useFieldValues";
import { parseUrl, readComposite, secretInMessages } from "@/lib/composite";
import { seededBlueprint, topologyOf } from "./blueprints";
import { celCheck } from "./cel";
import { constraintWarnings } from "./constraints";
import { draftDiscarded, gitOf, markGit } from "./catalogGit";
import { workflowRegistrations } from "./extensibility";
import { applyStates, fieldStates, userGroups, validateInputs } from "./logic";
import type { Db, StoredItem } from "./seed";

type S = components["schemas"];
type Version = S["CatalogItemVersionDetail"];
type Definition = S["CatalogVersionDefinition"];

interface AdminState {
  secrets: S["Secret"][];
  /** Every version of every property group, without what is worked out when served. */
  propertyGroups: Omit<S["PropertyGroup"], "versions" | "used_by">[];
  versions: Record<string, Version[]>; // by item ID, newest first
  allProjects: Set<string>;
  sortOrder: Record<string, number>;
}

const storedGroup = (
  key: string,
  version: number,
  w: S["PropertyGroupWrite"],
): AdminState["propertyGroups"][number] => ({
  key,
  version,
  name: w.name,
  description: w.description ?? "",
  inputs: w.inputs,
  constants: w.constants,
  updated_at: now(),
});

const groupProp = /^[A-Za-z][A-Za-z0-9_]{0,62}$/;

const ofType = (v: unknown, t: S["PropertyGroupInput"]["type"]) =>
  typeof v === "string"
    ? t === "string"
    : typeof v === "boolean"
      ? t === "boolean"
      : typeof v === "number" && (t === "number" || (t === "integer" && Number.isInteger(v)));

/** A property group's inputs and constants, checked as the server's checkGroup does. */
function checkGroup(w: S["PropertyGroupWrite"]): S["FieldError"][] {
  const errors: S["FieldError"][] = [];
  const add = (field: string, code: string, message: string) => errors.push({ field, code, message });
  if (!w.name?.trim()) add("/name", "required", "Give the property group a name.");
  const names = new Set<string>();
  (w.inputs ?? []).forEach((i, n) => {
    const at = `/inputs/${n}`;
    if (!groupProp.test(i.name))
      add(
        `${at}/name`,
        "pattern",
        "Letters, digits and underscores, starting with a letter: bindings read it as inputs.<name>.",
      );
    else if (names.has(i.name)) add(`${at}/name`, "duplicate", `The group has ${i.name} twice.`);
    names.add(i.name);
    if (!i.label?.trim()) add(`${at}/label`, "required", "Give the input a label.");
    const str = i.type === "string";
    if (!["string", "integer", "number", "boolean"].includes(i.type))
      add(`${at}/type`, "invalid", "An input is a string, an integer, a number or a boolean.");
    const options = i.options ?? [];
    if (options.length > 0 && !str) add(`${at}/options`, "invalid", "Only a string input is chosen from options.");
    if (i.secret && (!str || options.length > 0))
      add(`${at}/secret`, "invalid", "A secret input is a string typed in, without options.");
    if (i.default === undefined || i.default === null) return;
    if (i.secret) add(`${at}/default`, "invalid", "A secret input has no default: the group would hold its value.");
    else if (!ofType(i.default, i.type)) add(`${at}/default`, "invalid", `The default must be a ${i.type}.`);
    else if (options.length > 0 && !options.some((o) => o === i.default))
      add(`${at}/default`, "invalid", "The default must be one of the options.");
  });
  for (const [k, v] of Object.entries(w.constants ?? {}).sort(([a], [b]) => a.localeCompare(b))) {
    if (!groupProp.test(k))
      add(
        `/constants/${k}`,
        "pattern",
        "Letters, digits and underscores, starting with a letter: bindings read it as propgroup.<key>.<name>.",
      );
    else if (!["string", "number", "boolean"].includes(typeof v))
      add(`/constants/${k}`, "invalid", "A constant is text, a number or a boolean.");
  }
  return errors;
}

/** A property group at a version (its latest by default), with its versions and the items whose saved versions name it. */
function groupAt(db: Db, key: string, version = 0): S["PropertyGroup"] | undefined {
  const st = state(db);
  const all = st.propertyGroups.filter((g) => g.key === key).sort((a, b) => a.version - b.version);
  const g = version ? all.find((x) => x.version === version) : all.at(-1);
  if (!g) return undefined;
  const names = (v: Version) => {
    const refs = (v.definition.blueprint as { property_groups?: { key?: string }[] }).property_groups;
    return Array.isArray(refs) && refs.some((r) => r.key === key);
  };
  const usedBy = db.items
    .filter((i) => (st.versions[i.id] ?? []).some((v) => v.state !== "draft" && names(v)))
    .map((i) => i.slug);
  return { ...g, versions: all.map((x) => x.version), used_by: usedBy };
}

const builtinSources = [
  "vsphere.clusters", "vsphere.datastores", "vsphere.networks", "vsphere.templates", "vsphere.folders",
  "ipam.subnets", "ipam.next_free_address", "dns.zones", "awx.inventories", "awx.credentials", "naming.preview",
  "rvav.templates", "rvav.sizes", "rvav.projects", "rvav.users", "rvav.deployments", "rvav.vms",
  "nsx.segments", "nsx.tier1s", "nsx.groups", "nsx.services",
]; // prettier-ignore

/** Who may be previewed as, per project (as the server's seed). */
const previewMembers: Record<string, { id: string; role: string }[]> = {
  web: [
    { id: "usr_lmartin", role: "Project admin" },
    { id: "usr_tnguyen", role: "Member" },
  ],
  data: [{ id: "usr_sbrunner", role: "Project admin" }],
  fin: [{ id: "usr_sbrunner", role: "Member" }],
  core: [{ id: "usr_afavre", role: "Project admin" }],
};

/** The server's answer to a preview as someone outside the project. */
export function notMember(db: Db, userId: string) {
  const name = db.users.find((u) => u.id === userId)?.name ?? userId;
  return problem(422, "Some inputs need attention", [
    {
      field: "/as_user_id",
      code: "not_member",
      message: `${name} is not a member of this project: preview as one of its members.`,
    },
  ]);
}

const now = () => new Date().toISOString();
const uid = (prefix: string) => `${prefix}_${Math.random().toString(36).slice(2, 12)}`;

const iconSet = [
  "server", "monitor", "database", "key-round", "shield", "globe", "lock", "users", "terminal", "workflow",
  "box", "container", "hard-drive", "network", "cloud", "cpu", "package", "wrench", "mail", "file-text",
  "archive", "git-branch", "layers", "activity",
]; // prettier-ignore

function blueprintFor(item: StoredItem): Record<string, unknown> {
  if (item.type === "composite") return { kind: "composite", rollback_on_failure: true, steps: [] };
  if (item.type === "workflow") return { kind: "workflow", workflow: "" };
  return item.type === "vm_blueprint"
    ? { kind: "vm", template: "tpl-rhel9.4-2026.10", guest: { os_family: "linux", domain: "corp.example" } }
    : { kind: "awx", awx: { title: item.name, job_template: item.source.awx_template_name ?? item.slug } };
}

function state(db: Db): AdminState {
  const d = db as Db & { catalogAdmin?: AdminState };
  if (!d.catalogAdmin) {
    const versions: Record<string, Version[]> = {};
    for (const item of db.items) {
      const form = db.forms[item.id];
      versions[item.id] = [
        {
          id: item.version.id,
          number: item.version.number,
          state: "published",
          changelog: "",
          created_at: item.version.published_at ?? now(),
          published_at: item.version.published_at ?? now(),
          deployments: db.deployments.filter((x) => x.catalog_item.id === item.id).length,
          revision: 1,
          definition: {
            form_schema: form?.schema ?? { type: "object", properties: {} },
            ui_schema: form?.ui_schema ?? { layout: { kind: "page", sections: [] }, fields: {} },
            initial_values: form?.initial_values ?? {},
            blueprint: seededBlueprint(item.slug) ?? blueprintFor(item),
          },
        },
      ];
    }
    d.catalogAdmin = {
      secrets: [
        {
          name: "webhook-signing",
          kind: "token",
          description: "Signs rvav's webhooks to the CMDB and ServiceNow.",
          created_at: now(),
          updated_at: now(),
        },
      ],
      propertyGroups: [
        storedGroup("corp_ops", 1, {
          name: "Corporate operations",
          description: "What every VM of the shop is billed and backed up by.",
          inputs: [
            {
              name: "cost_center",
              label: "Cost centre",
              type: "string",
              required: true,
              options: ["CC-1001", "CC-2002", "CC-3003"],
            },
            { name: "backup", label: "Back up nightly", type: "boolean", default: true },
          ],
          constants: { dns_domain: "corp.example", support_queue: "ops-l2" },
        }),
      ],
      versions,
      allProjects: new Set(db.items.filter((i) => i.source.kind === "awx").map((i) => i.id)),
      sortOrder: Object.fromEntries(db.categories.map((c, i) => [c.key, i * 10])),
    };
  }
  return d.catalogAdmin;
}

const summary = (v: Version): S["CatalogItemVersion"] => {
  const { definition: _, ...rest } = v;
  return rest;
};

/** A version as served, with the tags its constraints name that vCenter doesn't have (ADR-0035). */
const served = (v: Version): Version => {
  const warnings = constraintWarnings(v.definition.blueprint, "/definition/blueprint");
  return warnings.length > 0 ? { ...v, warnings } : v;
};

function adminItem(db: Db, item: StoredItem): S["AdminCatalogItem"] {
  const st = state(db);
  const versions = st.versions[item.id] ?? [];
  const published = versions.find((v) => v.state === "published");
  const draft = versions.find((v) => v.state === "draft");
  return {
    id: item.id,
    slug: item.slug,
    name: item.name,
    summary: item.summary,
    description_md: item.description_md ?? "",
    category: item.category,
    type: item.type,
    icon: item.icon,
    tags: item.tags ?? [],
    featured: item.featured ?? false,
    source: item.source,
    ...(published ? { published_version: summary(published) } : {}),
    ...(draft ? { draft_version: summary(draft) } : {}),
    versions: versions.map(summary),
    entitlements: entitlements(db, item),
    requested: db.requests.some((r) => r.request.catalog_item.id === item.id),
    created_at: versions.at(-1)?.created_at ?? now(),
    ...(gitOf(db, item) ? { git: gitOf(db, item) } : {}),
  };
}

function entitlements(db: Db, item: StoredItem): S["CatalogEntitlements"] {
  return {
    all_projects: state(db).allProjects.has(item.id),
    project_ids: [...item.projectIds].sort(),
    projects: db.projects.map((p) => ({ id: p.id, code: p.code, name: p.name })),
  };
}

function problem(status: number, title: string, errors?: S["FieldError"][], detail?: string) {
  return HttpResponse.json(
    {
      type: "https://rvav.dev/problems/x",
      title,
      status,
      request_id: uid("req"),
      ...(detail ? { detail } : {}),
      ...(errors ? { errors } : {}),
    },
    { status, headers: { "Content-Type": "application/problem+json" } },
  );
}

/** The server's structural checks, enough for the UI: labels, sections, inputs. */
/** The CEL of a REST call, with where each is: its URL's expressions and its body. */
function restCel(call: unknown, at: string): [string, unknown][] {
  const c = (call ?? {}) as Record<string, unknown>;
  const url = typeof c.url === "string" ? c.url : "";
  return [
    ...parseUrl(url).parts.flatMap((p): [string, unknown][] => ("expr" in p ? [[`${at}/url`, p.expr]] : [])),
    [`${at}/body`, c.body],
  ];
}

function check(def: Definition, prefix: string): S["FieldError"][] {
  const errors: S["FieldError"][] = [];
  const props = (def.form_schema as { properties?: Record<string, unknown> }).properties ?? {};
  const placed = new Set(def.ui_schema.layout.sections.flatMap((s) => s.fields));
  for (const [name, f] of Object.entries(def.ui_schema.fields)) {
    if (!f.label?.trim())
      errors.push({
        field: `${prefix}/ui_schema/fields/${name}/label`,
        code: "required",
        message: "Give the field a label.",
      });
    if (!(name in props) && !["info", "image", "link"].includes(f.widget))
      errors.push({
        field: `${prefix}/ui_schema/fields/${name}`,
        code: "unknown_input",
        message: `"${name}" is not an input of the form's schema.`,
      });
    if (!placed.has(name))
      errors.push({
        field: `${prefix}/ui_schema/fields/${name}`,
        code: "not_placed",
        message: `"${name}" is in no section, so it would never show.`,
      });
    for (const [rule, expr] of Object.entries(f.rules ?? {})) {
      const problem = typeof expr === "string" && expr ? celCheck(expr) : null;
      if (problem)
        errors.push({
          field: `${prefix}/ui_schema/fields/${name}/rules/${rule}`,
          code: "invalid",
          message: `This rule is not valid CEL: ${problem}`,
        });
    }
    const src = f.value_source;
    const own = ownSources[f.widget];
    if (own && src && (src.kind !== "builtin" || src.name !== own))
      errors.push({
        field: `${prefix}/ui_schema/fields/${name}/value_source`,
        code: "widget_mismatch",
        message: `A ${f.widget} field always takes its choices from ${own}: leave its value source out, or name that one.`,
      });
    if (src?.kind === "builtin" && !builtinSources.includes(src.name ?? ""))
      errors.push({
        field: `${prefix}/ui_schema/fields/${name}/value_source/name`,
        code: "unknown",
        message: `"${src.name ?? ""}" is not a built-in value source.`,
      });
    if (src?.kind === "cel") {
      const params = src.params ?? {};
      if (!params.options && !params.value)
        errors.push({
          field: `${prefix}/ui_schema/fields/${name}/value_source/params`,
          code: "required",
          message: "Give an options or a value expression.",
        });
      for (const k of ["options", "value"]) {
        const problem = typeof params[k] === "string" && params[k] ? celCheck(params[k]) : null;
        if (problem)
          errors.push({
            field: `${prefix}/ui_schema/fields/${name}/value_source/params/${k}`,
            code: "invalid",
            message: `This expression is not valid CEL: ${problem}`,
          });
      }
    }
    if (
      src?.kind === "rest" &&
      !/^https:\/\/cmdb\.corp\.example\//.test(typeof src.params?.url === "string" ? src.params.url : "")
    )
      errors.push({
        field: `${prefix}/ui_schema/fields/${name}/value_source/params/url`,
        code: "host",
        message:
          "rvav may not call this host: it is not one of the hosts REST value sources may call (Administration › Integrations › Allowed hosts; cmdb.corp.example here).",
      });
  }
  for (const name of Object.keys(props))
    if (!def.ui_schema.fields[name])
      errors.push({
        field: `${prefix}/form_schema/properties/${name}`,
        code: "no_ui_field",
        message: `"${name}" has no UI field, so nobody could fill it in.`,
      });
  errors.push(...tableProblems(def, props, prefix));
  errors.push(...shownProblems(def, prefix, false));
  const required = (def.form_schema as { required?: string[] }).required ?? [];
  required.forEach((name, i) => {
    if (!(name in props))
      errors.push({
        field: `${prefix}/form_schema/required/${i}`,
        code: "unknown_input",
        message: `${name} is required but is not an input of the form's schema, so no request could pass.`,
      });
  });
  const keys = new Set<string>();
  def.ui_schema.layout.sections.forEach((sec, i) => {
    if (!sec.key.trim())
      errors.push({
        field: `${prefix}/ui_schema/layout/sections/${i}/key`,
        code: "required",
        message: "Give the section a key.",
      });
    if (keys.has(sec.key))
      errors.push({
        field: `${prefix}/ui_schema/layout/sections/${i}/key`,
        code: "duplicate",
        message: `Another section has the key "${sec.key}".`,
      });
    keys.add(sec.key);
  });
  for (const name of Object.keys(def.initial_values ?? {}))
    if (!(name in props))
      errors.push({
        field: `${prefix}/initial_values/${name}`,
        code: "unknown_input",
        message: `"${name}" is not an input of the form's schema.`,
      });
  if (def.blueprint.kind === "composite") {
    errors.push(...readComposite(def.blueprint, `${prefix}/blueprint`).problems);
    errors.push(...secretInMessages(def, prefix));
    // CEL, as the server checks it.
    type Rec = Record<string, unknown>;
    const steps = (Array.isArray(def.blueprint.steps) ? def.blueprint.steps : []) as Rec[];
    steps.forEach((st, g) => {
      const list = Array.isArray(st.parallel) ? (st.parallel as Rec[]) : [st];
      const top = list.map((x, j) => ({
        x,
        at: `${prefix}/blueprint/steps/${g}${Array.isArray(st.parallel) ? `/parallel/${j}` : ""}`,
      }));
      const branch = st.branch as Rec | undefined;
      const cases = Array.isArray(branch?.cases) ? (branch.cases as Rec[]) : [];
      const onSides = [
        ...cases.flatMap((c, i) =>
          (Array.isArray(c.steps) ? (c.steps as Rec[]) : []).map((x, j) => ({
            x,
            at: `${prefix}/blueprint/steps/${g}/branch/cases/${i}/steps/${j}`,
          })),
        ),
        ...(Array.isArray(branch?.else) ? (branch.else as Rec[]) : []).map((x, j) => ({
          x,
          at: `${prefix}/blueprint/steps/${g}/branch/else/${j}`,
        })),
      ];
      [...top, ...onSides].forEach(({ x, at }) => {
        const inCases = (x.branch as Rec | undefined)?.cases;
        const whens = Array.isArray(inCases)
          ? (inCases as Rec[]).map((c, i): [string, unknown] => [`${at}/branch/cases/${i}/when`, c.when])
          : [];
        const message = (x.notify as Rec | undefined)?.message;
        const exprs: [string, unknown][] = [
          ...whens,
          ...(typeof message === "string"
            ? parseUrl(message).parts.flatMap((part): [string, unknown][] =>
                "expr" in part ? [[`${at}/notify/message`, part.expr]] : [],
              )
            : []),
          [`${at}/when`, x.when],
          ...Object.entries((x.with as Record<string, unknown> | undefined) ?? {}).map(([k, v]): [string, unknown] => [
            `${at}/with/${k}`,
            v,
          ]),
          ...restCel(x.rest, `${at}/rest`),
          ...restCel((x.undo as Record<string, unknown> | undefined)?.rest, `${at}/undo/rest`),
          [`${at}/wait/until`, (x.wait as Record<string, unknown> | undefined)?.until],
          ...restCel((x.poll as Record<string, unknown> | undefined)?.rest, `${at}/poll/rest`),
          [`${at}/poll/until`, (x.poll as Record<string, unknown> | undefined)?.until],
        ];
        for (const [field, e] of exprs) {
          const problem = typeof e === "string" && e ? celCheck(e) : null;
          if (problem) errors.push({ field, code: "invalid", message: `Not valid CEL: ${problem}` });
        }
      });
    });
  }
  return errors;
}

function nextNumber(versions: Version[]): string {
  const last = versions[0]?.number ?? "0.9";
  const m = /^(\d+)\.(\d+)$/.exec(last);
  return m ? `${m[1]}.${Number(m[2]) + 1}` : `${last}.1`;
}

function diff(
  path: string,
  a: unknown,
  b: unknown,
  area: S["CatalogVersionChange"]["area"],
): S["CatalogVersionChange"][] {
  const name = path.replace(/^\//, "");
  if (a && b && typeof a === "object" && typeof b === "object" && !Array.isArray(a) && !Array.isArray(b)) {
    const ao = a as Record<string, unknown>;
    const bo = b as Record<string, unknown>;
    return [...new Set([...Object.keys(ao), ...Object.keys(bo)])].sort().flatMap((k): S["CatalogVersionChange"][] => {
      const sub = `${path}/${k}`;
      if (!(k in ao)) return [{ area, path: sub, change: "added", to: bo[k], summary: `${sub.slice(1)} added` }];
      if (!(k in bo)) return [{ area, path: sub, change: "removed", from: ao[k], summary: `${sub.slice(1)} removed` }];
      return diff(sub, ao[k], bo[k], area);
    });
  }
  if (
    Array.isArray(a) &&
    Array.isArray(b) &&
    [...(a as unknown[]), ...(b as unknown[])].every((x) => typeof x !== "object")
  ) {
    const av = a as unknown[];
    const bv = b as unknown[];
    return [
      ...bv
        .filter((x) => !av.includes(x))
        .map((x) => ({ area, path, change: "added" as const, to: x, summary: `${name}: ${String(x)} added` })),
      ...av
        .filter((x) => !bv.includes(x))
        .map((x) => ({ area, path, change: "removed" as const, from: x, summary: `${name}: ${String(x)} removed` })),
    ];
  }
  if (JSON.stringify(a) === JSON.stringify(b)) return [];
  return [
    { area, path, change: "changed", from: a, to: b, summary: `${name}: ${JSON.stringify(a)} → ${JSON.stringify(b)}` },
  ];
}

/** Makes a version what the user-facing catalog serves. */
function serve(db: Db, item: StoredItem, v: Version | undefined) {
  if (!v) {
    item.version = { ...item.version, id: "", number: "" };
    return;
  }
  item.version = { id: v.id, number: v.number, published_at: v.published_at };
  db.forms[item.id] = versionForm(db, item, v);
}

/** A property group's input in a form: its schema and its field, as the server's store.GroupInput gives them. */
function groupInput(i: S["PropertyGroupInput"]): [Record<string, unknown>, S["UiField"]] {
  const options = i.options ?? [];
  const schema: Record<string, unknown> = { type: i.type };
  if (options.length > 0) schema.enum = options;
  if (i.type === "string" && i.required) schema.minLength = 1;
  const field: S["UiField"] = { label: i.label, widget: "text" };
  if (i.secret) Object.assign(field, { widget: "secret", secret: true });
  else if (options.length > 0)
    Object.assign(field, { widget: "select", options: options.map((o) => ({ value: o, label: o })) });
  else if (i.type === "boolean") field.widget = "toggle";
  else if (i.type === "integer" || i.type === "number") field.widget = "number";
  if (i.help) field.help = i.help;
  return [schema, field];
}

/**
 * A version's form with the inputs of the property groups its blueprint
 * names, after its own, a section each, as store.WithGroups serves it to
 * requesters and previews (ADR-0038). An input the form has keeps the form's.
 */
function withGroups(db: Db, d: Definition): Pick<S["RequestForm"], "schema" | "ui_schema" | "initial_values"> {
  const refs = (d.blueprint as { property_groups?: unknown }).property_groups;
  const own = { schema: d.form_schema, ui_schema: d.ui_schema, initial_values: d.initial_values };
  if (!Array.isArray(refs) || refs.length === 0) return own;
  const schema = structuredClone(d.form_schema) as { properties?: Record<string, unknown>; required?: string[] };
  const ui = structuredClone(d.ui_schema);
  const initial = structuredClone(d.initial_values);
  const props = (schema.properties ??= {});
  for (const r of refs as { key?: unknown; version?: unknown }[]) {
    if (typeof r?.key !== "string" || typeof r.version !== "number" || r.version < 1) continue;
    const g = groupAt(db, r.key, r.version);
    if (!g) continue;
    const names: string[] = [];
    for (const i of g.inputs) {
      if (i.name in props) continue;
      [props[i.name], ui.fields[i.name]] = groupInput(i);
      if (i.required) (schema.required ??= []).push(i.name);
      if (!(i.name in initial) && i.default !== undefined && i.default !== null) initial[i.name] = i.default;
      names.push(i.name);
    }
    if (names.length > 0)
      ui.layout.sections.push({ key: `propgroup-${g.key}`, title: g.name, columns: 2, fields: names });
  }
  return { schema, ui_schema: ui, initial_values: initial };
}

/** A version's request form, as GET /catalog/items/{id}/form gives it. */
function versionForm(db: Db, item: StoredItem, v: Version): S["RequestForm"] {
  const bp = v.definition.blueprint;
  const topology = topologyOf(bp);
  return {
    item_id: item.id,
    version_id: v.id,
    ...withGroups(db, v.definition),
    ...(bp.kind === "vm" && Array.isArray(bp.components) && bp.components.length > 0 ? { one_deployment: true } : {}),
    ...(topology ? { topology } : {}),
  };
}

/**
 * Any version's form and item, and the user a designer previews as: what
 * `/forms/values` with `as_user_id` resolves against.
 */
export function designerForm(db: Db, versionId: string, projectCode: string, asUserId: string | undefined) {
  const st = state(db);
  for (const item of db.items) {
    const v = st.versions[item.id]?.find((x) => x.id === versionId);
    if (!v) continue;
    const form = versionForm(db, item, v);
    const userId = asUserId ?? db.me.user.id;
    const member = previewMembers[projectCode]?.some((m) => m.id === userId) ?? false;
    return { item, form, member, user: { id: userId, roles: [], groups: userGroups[userId] ?? [] } };
  }
  return undefined;
}

/** The item's file as rvav commits it to the catalog's repository (its
 * published version, or the one deprecated last), or a version's. */
export function itemFile(db: Db, item: StoredItem, versionId?: string): string | undefined {
  const versions = state(db).versions[item.id] ?? [];
  const v = versionId
    ? versions.find((x) => x.id === versionId)
    : (versions.find((x) => x.state === "published") ?? versions.find((x) => x.state === "deprecated"));
  if (!v) return undefined;
  const meta = {
    slug: item.slug,
    name: item.name,
    summary: item.summary,
    category: item.category,
    type: item.type,
    icon: item.icon,
  };
  const version = {
    number: v.number,
    state: v.state,
    ...(v.changelog ? { changelog: v.changelog } : {}),
    ...v.definition,
  };
  return stringify({ apiVersion: "rvav.dev/v1", kind: "CatalogItem", metadata: meta, versions: [version] });
}

/** A draft of the repository's change of the item (rvav's draft
 * discarded): its ID. */
export function draftFromRepository(db: Db, item: StoredItem): string {
  const versions = state(db).versions[item.id] ?? [];
  const open = versions.findIndex((v) => v.state === "draft");
  if (open >= 0) versions.splice(open, 1);
  const from = versions.find((v) => v.state === "published") ?? versions[0]!;
  const v: Version = {
    id: uid("ver"),
    number: nextNumber(versions),
    state: "draft",
    changelog: "Smaller default size",
    created_at: now(),
    created_by: { id: "usr_rvav", name: "rvav" },
    deployments: 0,
    revision: 1,
    definition: structuredClone(from.definition),
  };
  versions.unshift(v);
  return v.id;
}

/** The form's secret inputs in an item's published version: a message can't show them (ADR-0033). */
export function secretInputsOf(db: Db, item: StoredItem): Set<string> {
  const published = (state(db).versions[item.id] ?? []).find((v) => v.state === "published");
  const fields = published?.definition.ui_schema?.fields ?? {};
  return new Set(
    Object.entries(fields)
      .filter(([, f]) => f.secret === true || f.widget === "secret")
      .map(([k]) => k),
  );
}

/** The blueprint of the item's published version. */
export function publishedBlueprint(db: Db, item: StoredItem): Record<string, unknown> | undefined {
  const published = (state(db).versions[item.id] ?? []).find((v) => v.state === "published");
  return published?.definition.blueprint ?? seededBlueprint(item.slug);
}

/**
 * What the version a request was made from builds, as its form draws it
 * (GET /requests/{id}/topology, ADR-0032): not the item's latest. A day-2
 * request runs an action, not the item: none.
 */
export function requestTopology(db: Db, r: S["Request"]): S["RequestTopology"] {
  const item = db.items.find((i) => i.id === r.catalog_item.id);
  if (r.kind === "day2" || !item) return {};
  const v = (state(db).versions[item.id] ?? []).find((x) => x.id === r.version.id);
  const topology = topologyOf(v?.definition.blueprint ?? seededBlueprint(item.slug));
  return topology ? { topology } : {};
}

/** The item as catalog admins see it. */
export const adminItemOf = (db: Db, item: StoredItem) => adminItem(db, item);

// ---- What a composite's steps can run (ADR-0022, ADR-0032) -------------------

type Target = S["CompositeStepTarget"];

/** What a step gives later steps, by what it runs (core.stepOutputs). */
const stepOutputs: Record<Target["runs"], S["CompositeStepOutput"][]> = {
  vm: [
    { name: "name", type: "string", description: "The first VM's name" },
    { name: "fqdn", type: "string", description: "The first VM's FQDN" },
    { name: "ip", type: "string", description: "The first VM's IP address" },
    { name: "moref", type: "string", description: "The first VM's ID in vCenter" },
    { name: "deployment_id", type: "string", description: "The deployment its VMs are in" },
    { name: "vms", type: "array", description: "Every VM it built: a list of name, fqdn, ip, moref and deployment_id" },
  ],
  awx: [{ name: "job_id", type: "integer", description: "The AWX job's ID" }],
  workflow: [],
};

const target = (t: Omit<Target, "planned" | "outputs" | "open_outputs">): Target => ({
  ...t,
  planned: t.runs === "vm",
  outputs: [...stepOutputs[t.runs]],
  open_outputs: t.runs === "workflow",
});

/** A form's inputs a step binds, in the order its sections show them (core.formInputs). */
function formInputs(schema: Record<string, unknown>, ui?: S["UiSchema"]): S["CompositeStepInput"][] {
  type Prop = {
    type?: unknown;
    title?: string;
    description?: string;
    readOnly?: boolean;
    enum?: unknown[];
    items?: { enum?: unknown[] };
  };
  const props = (schema.properties ?? {}) as Record<string, Prop>;
  const required = (schema.required ?? []) as string[];
  const rest = Object.keys(props).sort((a, b) =>
    required.includes(a) === required.includes(b) ? a.localeCompare(b) : required.includes(a) ? -1 : 1,
  );
  const order = [...new Set([...(ui?.layout.sections ?? []).flatMap((x) => x.fields), ...rest])];
  return order.flatMap((name) => {
    const p = props[name];
    if (!p || p.readOnly) return [];
    const types: unknown[] = Array.isArray(p.type) ? p.type : [p.type];
    const type = types.find((t): t is string => typeof t === "string" && t !== "null") ?? "";
    const description = p.description || ui?.fields[name]?.help;
    const values = p.enum ?? p.items?.enum;
    return [
      {
        name,
        title: ui?.fields[name]?.label || p.title || name,
        type,
        required: required.includes(name),
        ...(description ? { description } : {}),
        ...(values?.length ? { enum: values } : {}),
      },
    ];
  });
}

/** AWX templates imported but not made items, with their IDs in AWX and their surveys' questions. */
const otherTemplates: { id: number; name: string; workflow?: boolean; schema: Record<string, unknown> }[] = [
  {
    id: 105,
    name: "monitoring-agent",
    schema: {
      required: ["monitoring_agent", "listen_port"],
      properties: {
        monitoring_agent: { type: "string", title: "Agent", enum: ["node_exporter", "telegraf"] },
        listen_port: { type: "integer", title: "Port" },
      },
    },
  },
  {
    id: 106,
    name: "pgbackrest-stanza",
    schema: {
      required: ["retention_days"],
      properties: { retention_days: { type: "integer", title: "Retention (days)" }, hosts: { type: "string", title: "Hosts" } },
    },
  },
  { id: 107, name: "pgbackrest-stanza-delete", schema: { properties: { hosts: { type: "string", title: "Hosts" } } } },
  { id: 108, name: "pg-drop-role", schema: { required: ["role_name"], properties: { role_name: { type: "string", title: "Role name" } } } },
  { id: 101, name: "linux-baseline", schema: { properties: {} } },
  { id: 102, name: "patroni-cluster", schema: { properties: {} } },
  { id: 103, name: "postgresql-install", schema: { properties: {} } },
]; // prettier-ignore

/** The projects a target is offered to, absent for all (core.projectLimits). */
const projectLimits = (ids: string[] | undefined) => (ids?.length ? { project_ids: [...ids].sort() } : {});

const displayName = (template: string) => {
  const s = template.replace(/[-_]/g, " ");
  return s.charAt(0).toUpperCase() + s.slice(1);
};

export function compositeStepTargets(db: Db, workflows: S["WorkflowRegistration"][]): Target[] {
  const st = state(db);
  const byTitle = (a: Target, b: Target) =>
    a.title.toLowerCase().localeCompare(b.title.toLowerCase()) || a.ref.localeCompare(b.ref);
  const items: Target[] = [];
  const templates: Target[] = [];
  for (const item of db.items) {
    const published = (st.versions[item.id] ?? []).find((v) => v.state === "published");
    const kind = published?.definition.blueprint.kind;
    if (!published || item.type === "composite" || (kind !== "vm" && kind !== "awx" && kind !== "workflow")) continue;
    // A workflow item while its workflow can start (core.usableWorkflow).
    const flow =
      kind === "workflow" ? workflows.find((w) => w.key === published.definition.blueprint.workflow) : undefined;
    if (kind === "workflow" && !(flow?.enabled && flow.usages.includes("catalog_item"))) continue;
    const inputs = formInputs(published.definition.form_schema, published.definition.ui_schema);
    items.push({
      ...target({ kind: "item", ref: item.slug, title: item.name, runs: kind, inputs }),
      ...projectLimits(flow?.project_ids),
      item_type: item.type,
      summary: item.summary,
    });
    if (item.source.kind === "awx" && item.source.awx_template_name)
      templates.push({
        ...target({ kind: "awx", ref: item.source.awx_template_name, title: item.name, runs: "awx", inputs }),
        ...(item.source.awx_template_id ? { awx_id: item.source.awx_template_id } : {}),
        ...(item.type === "awx_workflow" ? { awx_workflow: true } : {}),
      });
  }
  for (const t of otherTemplates)
    templates.push({
      ...target({ kind: "awx", ref: t.name, title: displayName(t.name), runs: "awx", inputs: formInputs(t.schema) }),
      awx_id: t.id,
      ...(t.workflow ? { awx_workflow: true } : {}),
    });
  const flows = workflows
    .filter((w) => w.enabled && w.usages.includes("composite_step"))
    .map((w) => ({
      ...target({
        kind: "workflow",
        ref: w.key ?? "",
        title: w.title,
        runs: "workflow",
        inputs: formInputs(w.input_schema ?? {}),
      }),
      ...projectLimits(w.project_ids),
      ...(w.description ? { summary: w.description } : {}),
    }));
  return [...items.sort(byTitle), ...templates.sort(byTitle), ...flows.sort(byTitle)];
}

export function catalogAdminHandlers(db: Db, base: string, wait: () => Promise<void>) {
  const find = (id: string) => db.items.find((i) => i.id === id || i.slug === id);
  const versionsOf = (item: StoredItem) => state(db).versions[item.id] ?? [];
  return [
    http.get(`${base}/admin/catalog/items`, async ({ request }) => {
      await wait();
      const url = new URL(request.url);
      const q = (url.searchParams.get("q") ?? "").toLowerCase();
      const category = url.searchParams.get("category");
      const source = url.searchParams.get("source");
      const st = url.searchParams.get("state");
      const items = db.items
        .filter((i) => !q || `${i.name} ${i.slug} ${i.summary}`.toLowerCase().includes(q))
        .filter((i) => !category || i.category === category)
        .filter((i) => !source || i.source.kind === source)
        .map((i) => adminItem(db, i))
        .filter((i) => !st || (st === "published") === Boolean(i.published_version))
        .sort((a, b) => a.name.localeCompare(b.name));
      return HttpResponse.json({ items });
    }),
    http.post(`${base}/admin/catalog/items`, async ({ request }) => {
      await wait();
      const c = (await request.json()) as S["CatalogItemCreate"];
      if (find(c.slug)) return problem(409, "Conflict", undefined, `The slug "${c.slug}" is taken.`);
      if (!/^[a-z0-9][a-z0-9-]{1,62}$/.test(c.slug))
        return problem(422, "Some inputs need attention", [
          { field: "/slug", code: "pattern", message: "Lower case letters, digits and dashes, 2 to 63 characters." },
        ]);
      const item: StoredItem = {
        id: uid("itm"),
        slug: c.slug,
        name: c.name,
        summary: c.summary,
        description_md: c.description_md ?? "",
        category: c.category,
        type: c.type,
        icon: c.icon ?? (c.type === "vm_blueprint" ? "server" : "terminal"),
        tags: c.tags ?? [],
        featured: false,
        source: { kind: "native" },
        version: { id: "", number: "" },
        approval: "never",
        favourite: false,
        requests_last_30_days: 0,
        projectIds: [],
      };
      const def: Definition = c.definition ?? {
        form_schema: { type: "object", required: ["name"], properties: { name: { type: "string", minLength: 1 } } },
        ui_schema: {
          layout: { kind: "page", sections: [{ key: "main", title: c.name, columns: 1, fields: ["name"] }] },
          fields: { name: { widget: "text", label: "Name" } },
        },
        initial_values: {},
        blueprint: blueprintFor(item),
      };
      db.items.push(item);
      state(db).versions[item.id] = [
        {
          id: uid("ver"),
          number: "1.0",
          state: "draft",
          changelog: "",
          created_at: now(),
          deployments: 0,
          revision: 1,
          definition: def,
        },
      ];
      return HttpResponse.json(adminItem(db, item), { status: 201 });
    }),
    http.get(`${base}/admin/catalog/items/:id`, async ({ params }) => {
      await wait();
      const item = find(String(params.id));
      return item ? HttpResponse.json(adminItem(db, item)) : problem(404, "Not found");
    }),
    http.patch(`${base}/admin/catalog/items/:id`, async ({ params, request }) => {
      await wait();
      const item = find(String(params.id));
      if (!item) return problem(404, "Not found");
      const u = (await request.json()) as S["CatalogItemUpdate"];
      if (u.icon && !iconSet.includes(u.icon))
        return problem(422, "Some inputs need attention", [
          { field: "/icon", code: "unknown", message: `"${u.icon}" is not one of rvav's icons.` },
        ]);
      Object.assign(item, Object.fromEntries(Object.entries(u).filter(([, v]) => v !== undefined)));
      markGit(db, item);
      return HttpResponse.json(adminItem(db, item));
    }),
    http.delete(`${base}/admin/catalog/items/:id`, async ({ params }) => {
      await wait();
      const item = find(String(params.id));
      if (!item) return problem(404, "Not found");
      if (db.requests.some((r) => r.request.catalog_item.id === item.id))
        return problem(
          409,
          "Conflict",
          undefined,
          `${item.name} was requested: withdraw it instead (deprecate its published version).`,
        );
      db.items.splice(db.items.indexOf(item), 1);
      return new HttpResponse(null, { status: 204 });
    }),
    http.post(`${base}/admin/catalog/items/:id/versions`, async ({ params, request }) => {
      await wait();
      const item = find(String(params.id));
      if (!item) return problem(404, "Not found");
      const versions = versionsOf(item);
      if (versions.some((v) => v.state === "draft"))
        return problem(409, "Conflict", undefined, `${item.name} already has a draft: change it, or discard it first.`);
      const c = (await request.json()) as S["CatalogItemVersionCreate"];
      const from =
        versions.find((v) => v.id === c.from_version_id) ??
        versions.find((v) => v.state === "published") ??
        versions[0];
      const v: Version = {
        id: uid("ver"),
        number: c.number ?? nextNumber(versions),
        state: "draft",
        changelog: c.changelog ?? "",
        created_at: now(),
        created_by: { id: db.me.user.id, name: db.me.user.name },
        deployments: 0,
        revision: 1,
        definition: structuredClone(c.definition ?? from!.definition),
      };
      versions.unshift(v);
      return HttpResponse.json(served(v), { status: 201 });
    }),
    http.get(`${base}/admin/catalog/items/:id/versions/:vid`, async ({ params }) => {
      await wait();
      const item = find(String(params.id));
      const v = item && versionsOf(item).find((x) => x.id === params.vid);
      return v ? HttpResponse.json(served(v)) : problem(404, "Not found");
    }),
    http.put(`${base}/admin/catalog/items/:id/versions/:vid`, async ({ params, request }) => {
      await wait();
      const item = find(String(params.id));
      const v = item && versionsOf(item).find((x) => x.id === params.vid);
      if (!v) return problem(404, "Not found");
      if (v.state !== "draft")
        return problem(409, "Conflict", undefined, "Only a draft can change: make a draft from this version.");
      const u = (await request.json()) as S["CatalogItemVersionUpdate"];
      const errors = check(u.definition, "/definition");
      if (errors.length) return problem(422, "Some inputs need attention", errors);
      if (u.base_revision !== undefined && u.base_revision !== v.revision)
        return problem(
          409,
          "Conflict",
          undefined,
          "This draft was saved elsewhere since you opened it: reload it to see those changes.",
        );
      v.definition = u.definition;
      v.revision += 1;
      if (u.number) v.number = u.number;
      if (u.changelog !== undefined) v.changelog = u.changelog;
      return HttpResponse.json(served(v));
    }),
    http.delete(`${base}/admin/catalog/items/:id/versions/:vid`, async ({ params }) => {
      await wait();
      const item = find(String(params.id));
      const versions = item ? versionsOf(item) : [];
      const v = versions.find((x) => x.id === params.vid);
      if (!v) return problem(404, "Not found");
      if (v.state !== "draft") return problem(409, "Conflict", undefined, "Only a draft can be discarded.");
      versions.splice(versions.indexOf(v), 1);
      draftDiscarded(db, item!, v.id);
      return new HttpResponse(null, { status: 204 });
    }),
    http.post(`${base}/admin/catalog/items/:id/versions/:vid/publish`, async ({ params, request }) => {
      await wait();
      const item = find(String(params.id));
      const v = item && versionsOf(item).find((x) => x.id === params.vid);
      if (!item || !v) return problem(404, "Not found");
      if (v.state === "published")
        return problem(409, "Conflict", undefined, `Version ${v.number} is already published.`);
      const body =
        request.headers.get("content-length") === "0"
          ? {}
          : ((await request.json().catch(() => ({}))) as S["CatalogItemVersionPublish"]);
      const incomplete = shownProblems(v.definition, "/definition", true);
      if (incomplete.length) return problem(422, "Some inputs need attention", incomplete);
      for (const x of versionsOf(item))
        if (x.state === "published") Object.assign(x, { state: "deprecated", deprecated_at: now() });
      if (v.state === "draft" && body.changelog !== undefined) v.changelog = body.changelog;
      Object.assign(v, { state: "published", published_at: now() });
      delete v.deprecated_at;
      serve(db, item, v);
      markGit(db, item);
      return HttpResponse.json(adminItem(db, item));
    }),
    http.post(`${base}/admin/catalog/items/:id/versions/:vid/deprecate`, async ({ params }) => {
      await wait();
      const item = find(String(params.id));
      const v = item && versionsOf(item).find((x) => x.id === params.vid);
      if (!item || !v) return problem(404, "Not found");
      if (v.state !== "published")
        return problem(409, "Conflict", undefined, "Only the published version can be deprecated.");
      Object.assign(v, { state: "deprecated", deprecated_at: now() });
      serve(db, item, undefined);
      markGit(db, item);
      return HttpResponse.json(adminItem(db, item));
    }),
    http.get(`${base}/admin/catalog/items/:id/compare`, async ({ params, request }) => {
      await wait();
      const item = find(String(params.id));
      const url = new URL(request.url);
      const from = item && versionsOf(item).find((x) => x.id === url.searchParams.get("from"));
      const to = item && versionsOf(item).find((x) => x.id === url.searchParams.get("to"));
      if (!from || !to) return problem(404, "Not found");
      const areas = ["form_schema", "ui_schema", "initial_values", "blueprint"] as const;
      const changes = areas.flatMap((a) => diff("", from.definition[a], to.definition[a], a));
      return HttpResponse.json({ from: summary(from), to: summary(to), changes } satisfies S["CatalogVersionDiff"]);
    }),
    http.get(`${base}/admin/catalog/items/:id/entitlements`, async ({ params }) => {
      await wait();
      const item = find(String(params.id));
      return item ? HttpResponse.json(entitlements(db, item)) : problem(404, "Not found");
    }),
    http.put(`${base}/admin/catalog/items/:id/entitlements`, async ({ params, request }) => {
      await wait();
      const item = find(String(params.id));
      if (!item) return problem(404, "Not found");
      const e = (await request.json()) as S["CatalogEntitlements"];
      const st = state(db);
      if (e.all_projects) st.allProjects.add(item.id);
      else st.allProjects.delete(item.id);
      item.projectIds = e.all_projects ? db.projects.map((p) => p.id) : [...e.project_ids];
      return HttpResponse.json({ ...entitlements(db, item), all_projects: e.all_projects, project_ids: e.project_ids });
    }),
    http.get(`${base}/admin/catalog/items/:id/export`, async ({ params, request }) => {
      await wait();
      const item = find(String(params.id));
      if (!item) return problem(404, "Not found");
      const only = new URL(request.url).searchParams.get("version_id");
      const versions = [...versionsOf(item)].reverse().filter((v) => !only || v.id === only);
      const doc = {
        apiVersion: "rvav.dev/v1",
        kind: "CatalogItem",
        metadata: {
          slug: item.slug,
          name: item.name,
          summary: item.summary,
          category: item.category,
          type: item.type,
          icon: item.icon,
        },
        versions: versions.map((v) => ({
          number: v.number,
          state: v.state,
          ...(v.changelog ? { changelog: v.changelog } : {}),
          ...v.definition,
        })),
      };
      return new HttpResponse(stringify(doc), { headers: { "Content-Type": "application/yaml" } });
    }),
    http.post(`${base}/admin/catalog/import`, async ({ request }) => {
      await wait();
      let doc: {
        metadata?: Record<string, string>;
        versions?: (Definition & { number: string; changelog?: string })[];
      };
      try {
        doc = parse(await request.text()) as typeof doc;
      } catch (e) {
        return problem(422, "Some inputs need attention", [
          { field: "", code: "yaml", message: `This is not valid YAML: ${String(e)}` },
        ]);
      }
      const into = new URL(request.url).searchParams.get("item_id");
      const versions = doc.versions ?? [];
      const last = versions.at(-1);
      if (!last)
        return problem(422, "Some inputs need attention", [
          { field: "/versions", code: "required", message: "The item has no version." },
        ]);
      const errors = versions.flatMap((v, i) => check(v, `/versions/${i}`));
      if (errors.length) return problem(422, "Some inputs need attention", errors);
      const def = (v: Definition): Definition => ({
        form_schema: v.form_schema,
        ui_schema: v.ui_schema,
        initial_values: v.initial_values ?? {},
        blueprint: v.blueprint,
      });
      if (into) {
        const item = find(into);
        if (!item) return problem(404, "Not found");
        const existing = versionsOf(item);
        if (existing.some((v) => v.state === "draft"))
          return problem(409, "Conflict", undefined, `${item.name} already has a draft.`);
        existing.unshift({
          id: uid("ver"),
          number: nextNumber(existing),
          state: "draft",
          changelog: last.changelog ?? "",
          created_at: now(),
          deployments: 0,
          revision: 1,
          definition: def(last),
        });
        return HttpResponse.json({ item_id: item.id, created: false, versions: existing.map(summary) });
      }
      const m = doc.metadata ?? {};
      if (find(m.slug ?? "")) return problem(409, "Conflict", undefined, `The slug "${m.slug}" is taken.`);
      const item: StoredItem = {
        id: uid("itm"), slug: m.slug ?? uid("item"), name: m.name ?? "Imported item", summary: m.summary ?? "", description_md: "",
        category: m.category ?? "compute", type: (m.type ?? "vm_blueprint") as StoredItem["type"], icon: m.icon ?? "server", tags: [],
        featured: false, source: { kind: "native" }, version: { id: "", number: "" }, approval: "never",
        favourite: false, requests_last_30_days: 0, projectIds: [],
      }; // prettier-ignore
      db.items.push(item);
      state(db).versions[item.id] = versions
        .map((v, i): Version => ({
          id: uid("ver"),
          number: v.number,
          state: i === versions.length - 1 ? "draft" : "deprecated",
          changelog: v.changelog ?? "",
          created_at: now(),
          deployments: 0,
          revision: 1,
          definition: def(v),
        })) // prettier-ignore
        .reverse();
      return HttpResponse.json({ item_id: item.id, created: true, versions: versionsOf(item).map(summary) });
    }),
    http.get(`${base}/admin/catalog/categories`, async () => {
      await wait();
      const st = state(db);
      return HttpResponse.json({
        items: db.categories
          .map((c) => ({
            key: c.key,
            name: c.name,
            sort_order: st.sortOrder[c.key] ?? 0,
            items: db.items.filter((i) => i.category === c.key).length,
          }))
          .sort((a, b) => a.sort_order - b.sort_order || a.key.localeCompare(b.key)),
      });
    }),
    http.post(`${base}/admin/catalog/categories`, async ({ request }) => {
      await wait();
      const c = (await request.json()) as S["AdminCatalogCategory"];
      if (db.categories.some((x) => x.key === c.key))
        return problem(409, "Conflict", undefined, `The category "${c.key}" exists.`);
      db.categories.push({ key: c.key, name: c.name });
      state(db).sortOrder[c.key] = c.sort_order ?? 0;
      return HttpResponse.json({ ...c, items: 0 }, { status: 201 });
    }),
    http.patch(`${base}/admin/catalog/categories/:key`, async ({ params, request }) => {
      await wait();
      const c = db.categories.find((x) => x.key === params.key);
      if (!c) return problem(404, "Not found");
      const u = (await request.json()) as S["AdminCatalogCategoryUpdate"];
      if (u.name) c.name = u.name;
      if (u.sort_order !== undefined) state(db).sortOrder[c.key] = u.sort_order;
      return HttpResponse.json({
        key: c.key,
        name: c.name,
        sort_order: state(db).sortOrder[c.key] ?? 0,
        items: db.items.filter((i) => i.category === c.key).length,
      });
    }),
    http.delete(`${base}/admin/catalog/categories/:key`, async ({ params }) => {
      await wait();
      const i = db.categories.findIndex((x) => x.key === params.key);
      if (i < 0) return problem(404, "Not found");
      if (db.items.some((x) => x.category === params.key))
        return problem(409, "Conflict", undefined, "Items are in this category: move them to another one first.");
      db.categories.splice(i, 1);
      return new HttpResponse(null, { status: 204 });
    }),
    http.get(`${base}/admin/catalog/icons`, () => HttpResponse.json({ items: iconSet })),
    http.get(`${base}/admin/catalog/composite-steps`, async () => {
      await wait();
      return HttpResponse.json({ items: compositeStepTargets(db, workflowRegistrations(db)) });
    }),
    http.get(`${base}/admin/secrets`, async () => {
      await wait();
      const st = state(db);
      const usedBy = (name: string) =>
        db.items
          .filter((i) =>
            (st.versions[i.id] ?? []).some(
              (v) =>
                Object.values(v.definition.ui_schema.fields).some((f) => f.value_source?.params?.secret === name) ||
                // A composite's REST calls, at any depth.
                JSON.stringify(v.definition.blueprint).includes(`"secret":${JSON.stringify(name)}`),
            ),
          )
          .map((i) => i.slug);
      return HttpResponse.json({
        items: st.secrets.map((x) => ({ ...x, used_by: usedBy(x.name) })),
        projects: db.projects.map((p) => ({ id: p.id, code: p.code, name: p.name })),
      });
    }),
    http.post(`${base}/admin/secrets`, async ({ request }) => {
      await wait();
      const w = (await request.json()) as S["SecretWrite"];
      const st = state(db);
      if (st.secrets.some((x) => x.name === w.name && x.project_id === w.project_id))
        return problem(409, "Conflict", undefined, `${w.name} exists.`);
      const { value: _, ...rest } = w;
      const secret: S["Secret"] = { ...rest, description: w.description ?? "", created_at: now(), updated_at: now() };
      st.secrets.push(secret);
      return HttpResponse.json(secret, { status: 201 });
    }),
    http.put(`${base}/admin/secrets/:name`, async ({ params, request }) => {
      await wait();
      const w = (await request.json()) as S["SecretWrite"];
      const project = new URL(request.url).searchParams.get("project_id") ?? undefined;
      const st = state(db);
      const same = (x: S["Secret"]) => x.name === params.name && x.project_id === project;
      const { value: _, ...rest } = w;
      const secret: S["Secret"] = {
        ...rest,
        ...(project ? { project_id: project } : {}),
        description: w.description ?? "",
        created_at: st.secrets.find(same)?.created_at ?? now(),
        updated_at: now(),
      };
      st.secrets = [...st.secrets.filter((x) => !same(x)), secret];
      return HttpResponse.json(secret);
    }),
    http.delete(`${base}/admin/secrets/:name`, async ({ params, request }) => {
      await wait();
      const project = new URL(request.url).searchParams.get("project_id") ?? undefined;
      const st = state(db);
      st.secrets = st.secrets.filter((x) => !(x.name === params.name && x.project_id === project));
      return new HttpResponse(null, { status: 204 });
    }),
    http.get(`${base}/admin/property-groups`, async () => {
      await wait();
      const st = state(db);
      const keys = [...new Set(st.propertyGroups.map((g) => g.key))].sort();
      return HttpResponse.json({ items: keys.map((k) => groupAt(db, k)!) });
    }),
    http.post(`${base}/admin/property-groups`, async ({ request }) => {
      await wait();
      const w = (await request.json()) as S["PropertyGroupWrite"];
      const st = state(db);
      const key = w.key ?? "";
      const errors = [
        ...(/^[A-Za-z][A-Za-z0-9_]{1,62}$/.test(key)
          ? []
          : [
              {
                field: "/key",
                code: "pattern",
                message:
                  "Letters, digits and underscores, starting with a letter: bindings read it as propgroup.<key>.",
              },
            ]),
        ...checkGroup(w),
      ];
      if (errors.length) return problem(422, "Some inputs need attention", errors);
      if (st.propertyGroups.some((g) => g.key === key))
        return problem(409, "Conflict", undefined, `The property group "${key}" exists.`);
      st.propertyGroups.push(storedGroup(key, 1, w));
      return HttpResponse.json(groupAt(db, key), { status: 201 });
    }),
    http.get(`${base}/admin/property-groups/:key`, async ({ params, request }) => {
      await wait();
      const version = Number(new URL(request.url).searchParams.get("version") ?? 0);
      const g = groupAt(db, String(params.key), version);
      return g ? HttpResponse.json(g) : problem(404, "Not found");
    }),
    http.put(`${base}/admin/property-groups/:key`, async ({ params, request }) => {
      await wait();
      const w = (await request.json()) as S["PropertyGroupWrite"];
      const st = state(db);
      const latest = groupAt(db, String(params.key));
      if (!latest) return problem(404, "Not found");
      const errors = [
        ...(w.key !== undefined && w.key !== latest.key
          ? [{ field: "/key", code: "invalid", message: "A property group's key doesn't change." }]
          : []),
        ...checkGroup(w),
      ];
      if (errors.length) return problem(422, "Some inputs need attention", errors);
      st.propertyGroups.push(storedGroup(latest.key, latest.version + 1, w));
      return HttpResponse.json(groupAt(db, latest.key));
    }),
    http.delete(`${base}/admin/property-groups/:key`, async ({ params }) => {
      await wait();
      const st = state(db);
      const g = groupAt(db, String(params.key));
      if (!g) return problem(404, "Not found");
      if (g.used_by.length)
        return problem(409, "Conflict", undefined, `Items use this property group: ${g.used_by.join(", ")}.`);
      st.propertyGroups = st.propertyGroups.filter((x) => x.key !== g.key);
      return new HttpResponse(null, { status: 204 });
    }),
    http.get(`${base}/admin/catalog/preview-users`, async ({ request }) => {
      await wait();
      const project = db.projects.find((p) => p.id === new URL(request.url).searchParams.get("project_id"));
      if (!project) return problem(404, "Not found");
      const items = (previewMembers[project.code] ?? []).map((m) => ({
        id: m.id,
        name: db.users.find((u) => u.id === m.id)?.name ?? m.id,
        email: db.users.find((u) => u.id === m.id)?.email ?? "",
        role: m.role,
      }));
      return HttpResponse.json({ items });
    }),
    http.post(`${base}/admin/catalog/items/:id/versions/:vid/preview`, async ({ params, request }) => {
      await wait();
      const q = (await request.json()) as S["DesignerPreviewQuery"];
      const project = db.projects.find((p) => p.id === q.project_id);
      if (!project) return problem(404, "Not found");
      if (q.as_user_id && !db.users.some((u) => u.id === q.as_user_id))
        return problem(422, "Some inputs need attention", [
          { field: "/as_user_id", code: "unknown", message: "There is no such user." },
        ]);
      const found = designerForm(db, String(params.vid), project.code, q.as_user_id);
      if (!found) return problem(404, "Not found");
      if (!found.member) return notMember(db, found.user.id);
      const env = q.environment ?? "dev";
      const fields = fieldStates(found.form, q.inputs, {
        environment: env,
        project: { id: project.id, code: project.code, name: project.name },
        user: found.user,
      });
      const inputs = { ...q.inputs };
      const errors = applyStates(found.form, fields, inputs, (x) => validateInputs(db, found.item, x, found.form));
      return HttpResponse.json({ form: found.form, fields, errors } satisfies S["DesignerPreview"]);
    }),
  ];
}

/** What an image or a link shows, as the server checks it (checkShown in internal/core/formfields.go). */
function shownProblems(def: Definition, prefix: string, publish: boolean): S["FieldError"][] {
  const errors: S["FieldError"][] = [];
  const props = (def.form_schema as { properties?: Record<string, unknown> }).properties ?? {};
  for (const [name, f] of Object.entries(def.ui_schema.fields)) {
    const at = `${prefix}/ui_schema/fields/${name}`;
    const media = f.widget === "image" || f.widget === "link";
    if (f.url !== undefined && !media)
      errors.push({ field: `${at}/url`, code: "invalid", message: "Only an image or a link has an address (url)." });
    if (f.alt !== undefined && f.widget !== "image")
      errors.push({ field: `${at}/alt`, code: "invalid", message: "Only an image has a text alternative (alt)." });
    if (!media) continue;
    const what = f.widget === "image" ? "An image" : "A link";
    if (name in props)
      errors.push({
        field: `${prefix}/form_schema/properties/${name}`,
        code: "widget_mismatch",
        message: `${what} takes no input: remove "${name}" from the form's schema.`,
      });
    const url = f.url?.trim() ?? "";
    if (!url && publish)
      errors.push({ field: `${at}/url`, code: "required", message: `${what} needs its address (url).` });
    if (url && !shownUrl(url))
      errors.push({
        field: `${at}/url`,
        code: "invalid",
        message: "Give an address that starts with https:// or http://, or a path on this site that starts with /.",
      });
    if (f.widget === "image" && !f.alt?.trim() && publish)
      errors.push({
        field: `${at}/alt`,
        code: "required",
        message: "Describe the image for those who can't see it: give its text alternative (alt).",
      });
  }
  return errors;
}

const columnTypes: Record<string, string[]> = {
  text: ["string"],
  number: ["integer", "number"],
  toggle: ["boolean"],
  select: ["string"],
};

type Props = { properties?: Record<string, { type?: string; enum?: unknown[] }> };
type Cells = Props & { items?: Props };

/** A table's columns against its rows' properties, an object's sub-fields against its own, as the server checks them (internal/core/formfields.go). */
function tableProblems(def: Definition, props: Record<string, unknown>, prefix: string): S["FieldError"][] {
  const errors: S["FieldError"][] = [];
  for (const [name, f] of Object.entries(def.ui_schema.fields)) {
    if (f.widget !== "datagrid" && f.widget !== "object") continue;
    const object = f.widget === "object";
    const at = `${prefix}/ui_schema/fields/${name}`;
    const prop = props[name] as Cells | undefined;
    const cells = (object ? prop?.properties : prop?.items?.properties) ?? {};
    const columns = f.columns ?? [];
    if (columns.length === 0) {
      errors.push({
        field: `${at}/columns`,
        code: "required",
        message: object
          ? "Give the object its sub-fields, one per property."
          : "Give the table its columns, one per property of its rows.",
      });
      continue;
    }
    columns.forEach((c, i) => {
      const cell = cells[c.key];
      const cat = `${at}/columns/${i}`;
      const types = columnTypes[c.widget] ?? [];
      if (!cell)
        errors.push({
          field: `${cat}/key`,
          code: "unknown_input",
          message: object
            ? `The object has no property "${c.key}": add it to its properties.`
            : `The rows have no property "${c.key}": add it to the schema's items.`,
        });
      else if (!c.label.trim())
        errors.push({ field: `${cat}/label`, code: "required", message: "Give the column a label." });
      else if (!types.includes(cell.type ?? ""))
        errors.push({
          field: `${cat}/widget`,
          code: "widget_mismatch",
          message: `A ${c.widget} column edits ${types.join(" or ")}, and "${c.key}" is ${cell.type ?? "untyped"}.`,
        });
      else if (c.widget === "select" && !cell.enum?.length)
        errors.push({
          field: `${cat}/options`,
          code: "required",
          message: `A select column's property needs its choices as an enum, so the server checks them: "${c.key}" has none.`,
        });
    });
    for (const k of Object.keys(cells).sort())
      if (!columns.some((c) => c.key === k))
        errors.push({
          field: `${prefix}/form_schema/properties/${name}${object ? "" : "/items"}/properties/${k}`,
          code: "no_ui_field",
          message: `"${k}" has no column, so nobody could fill it in.`,
        });
  }
  return errors;
}
