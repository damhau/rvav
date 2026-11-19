// What the backend computes: dynamic field values, input validation, request
// previews (names, quota, approval path, lease) and deployment summaries.
import type {
  ApprovalPreview,
  Deployment,
  DeploymentDetail,
  EnvironmentKey,
  FieldError,
  FieldValues,
  Option,
  RequestForm,
  Project,
  QuotaImpact,
  QuotaResource,
  RequestDraft,
  RequestPreview,
  VmResource,
} from "@/lib/api/types";
import type { components } from "@/lib/api/schema";

type FieldState = components["schemas"]["FieldState"];
import { computedBy, defaulted, singleSource } from "@/forms/defaults";
import { withOwnSource } from "@/forms/useFieldValues";
import { can } from "@/lib/can";
import { toText } from "@/lib/utils";
import { adminState, namingTemplate, placed, placementFolder, rolesGiven } from "./administration";
import { chooseLease, leaseTooLong } from "./leases";
import { celBool, celEval } from "./cel";
import { warning } from "./warnings";
import { multiParts, partVMs, previewComponents } from "./components";
import { requestedNames } from "./deploymentNames";
import { userQuotaCheck } from "./quotas";
import { roleRefs } from "./roles";
import { bound, entryVars, validationErrors } from "./formLibrary";
import { clusters, datastores, dnsZones, networks, prefix, sizes, vmFolders, type SizeKey } from "./inventory";
import type { Db, StoredItem } from "./seed";

type Inputs = Record<string, unknown>;

/** Substitute `${inputs.x}` references in value-source params. */
export function resolveParams(params: Record<string, unknown> | undefined, inputs: Inputs): Record<string, string> {
  const out: Record<string, string> = {};
  for (const [k, v] of Object.entries(params ?? {})) {
    out[k] = toText(v).replace(/\$\{inputs\.([a-zA-Z0-9_]+)\}/g, (_, name: string) => {
      const value = inputs[name];
      return toText(value);
    });
  }
  return out;
}

export function nextFreeIp(db: Db, network: string) {
  const n = networks.find((x) => x.name === network);
  if (!n) return null;
  const host = db.ipNext[network] ?? n.nextHost;
  return { ip: `${prefix(n.subnet)}.${host}`, subnet: n.subnet, free: n.free - (host - n.nextHost) };
}

export function reserveIp(db: Db, network: string) {
  const next = nextFreeIp(db, network);
  if (!next) return null;
  db.ipNext[network] = (db.ipNext[network] ?? 0) + 1;
  return next;
}

export function vmNames(db: Db, project: Project, env: EnvironmentKey, role: string, count: number): string[] {
  // The naming policy offered here, or the blueprint's; its counter continues after the highest number in use.
  const template = namingTemplate(db, project.id, env) ?? "{project}-{environment}-{role}{seq:03}";
  const counter = /\{seq(?::(\d+))?\}$/.exec(template);
  const width = Number(counter?.[1] ?? 1);
  const base = template
    .slice(0, counter?.index ?? template.length)
    .replaceAll("{project}", project.code)
    .replaceAll("{environment}", env)
    .replaceAll("{inputs.role}", role || "app")
    .replaceAll("{role}", role || "app");
  const pattern = new RegExp(`^${base}(\\d{${width},})$`);
  const highest = db.deployments
    .flatMap((d) => d.resources.map((r) => pattern.exec(r.name)?.[1]))
    .reduce((max, seq) => (seq ? Math.max(max, Number(seq)) : max), 0);
  return Array.from({ length: count }, (_, i) => `${base}${String(highest + 1 + i).padStart(width, "0")}`);
}

/** What an rvav.* source offers the signed-in user requesting in a project, as the server works it out. */
function rvavOptions(
  db: Db,
  project: Project,
  source: string,
  params: Record<string, string>,
  search: string,
): Option[] {
  const live = db.deployments.filter((d) => d.project.id === project.id && d.status !== "deleted");
  switch (source) {
    case "rvav.projects":
      return db.projects
        .filter((p) => can(db.me, "request.submit", p.id))
        .map((p) => ({ value: p.id, label: p.name, meta: p.code }));
    case "rvav.users": {
      const text = search.trim().toLowerCase();
      const byId = db.users.find((u) => u.id === search.trim());
      return (byId ? [byId] : db.users.filter((u) => `${u.name} ${u.email}`.toLowerCase().includes(text)))
        .slice(0, 20)
        .map((u) => ({ value: u.id, label: u.name, meta: u.email }));
    }
    case "rvav.deployments":
      return live
        .filter((d) => !params.item || d.catalog_item.slug === params.item)
        .map((d) => ({ value: d.id, label: d.name, meta: `${d.catalog_item.name} · ${d.environment}` }))
        .sort((a, b) => a.label.localeCompare(b.label));
    default:
      return live
        .filter((d) => !params.deployment || d.id === params.deployment)
        .flatMap((d) =>
          d.resources
            .filter((r) => r.type === "vsphere.vm")
            .map((r) => ({ value: r.name, label: r.name, meta: d.name })),
        )
        .sort((a, b) => a.value.localeCompare(b.value));
  }
}

function withDefaultOptions(field: string, inputs: Inputs, options: Option[]): FieldValues {
  const current = typeof inputs[field] === "string" ? inputs[field] : "";
  const valid = options.some((o) => o.value === current && !o.disabled);
  return { field, options, default: valid ? current : options.find((o) => !o.disabled)?.value, cache_seconds: 60 };
}

export function resolveField(
  db: Db,
  item: StoredItem,
  project: Project,
  env: EnvironmentKey,
  field: string,
  inputs: Inputs,
  designer?: { form: RequestForm; user: Record<string, unknown> },
  search = "",
): FieldValues | null {
  const found = resolveSource(db, item, project, env, field, inputs, designer, search);
  const ui = (designer?.form ?? db.forms[item.id])?.ui_schema.fields[field];
  const source = ui?.value_source;
  // As the server: a default rule wins over the option the source suggests.
  const out = found && ui?.rules?.default ? { ...found, default: undefined } : found;
  const text = search.trim().toLowerCase();
  // As the server: a source that doesn't search itself keeps what contains the text.
  if (!out?.options || !text || source?.search || source?.name === "rvav.users") return out;
  return {
    ...out,
    options: out.options.filter((o) => [o.value, o.label, o.meta ?? ""].some((s) => s.toLowerCase().includes(text))),
  };
}

function resolveSource(
  db: Db,
  item: StoredItem,
  project: Project,
  env: EnvironmentKey,
  field: string,
  inputs: Inputs,
  designer: { form: RequestForm; user: Record<string, unknown> } | undefined,
  search: string,
): FieldValues | null {
  const stored = (designer?.form ?? db.forms[item.id])?.ui_schema.fields[field];
  const ui = stored && withOwnSource(stored);
  let source = ui?.value_source;
  if (!ui || !source) return null;
  let ctx: Record<string, unknown> = {
    inputs,
    search,
    environment: env,
    project: { id: project.id, code: project.code, name: project.name },
    user: designer?.user ?? { id: db.me.user.id, roles: [], groups: [] },
  };
  if (source.kind === "library") {
    // As the server: an entry whose required parameter has no value gives nothing.
    const b = bound(db, source.name ?? "", source.version ?? 0, source.params ?? {}, ctx);
    if (!b) return singleSource(ui) ? { field, cache_seconds: 30 } : { field, options: [], cache_seconds: 30 };
    source = { kind: b.entry.kind, params: b.entry.params, search: b.entry.search };
    ctx = entryVars(b.params, ctx);
  }
  const params = resolveParams(source.params, inputs);
  if (source.kind === "cel") {
    // Throws on a broken expression; the handler answers 400.
    const p = source.params ?? {};
    const value = typeof p.value === "string" ? String(celEval(p.value, ctx)) : undefined;
    const list = typeof p.options === "string" ? celEval(p.options, ctx) : undefined;
    const options = Array.isArray(list)
      ? list.map((x): Option => (typeof x === "string" ? { value: x, label: x } : (x as Option)))
      : undefined;
    return { field, ...(options ? { options } : {}), ...(value !== undefined ? { value } : {}), cache_seconds: 30 };
  }
  if (source.kind === "rest" && singleSource(ui)) return { field, value: restValue, cache_seconds: 60 };
  if (source.kind === "rest") {
    const host = params.url ? new URL(params.url, "https://cmdb.corp.example").host : "the service";
    const services = [
      { value: "svc-01", label: "Service 01", meta: `from ${host}` },
      { value: "svc-02", label: "Service 02", meta: `from ${host}` },
    ];
    // The service searches as rvav would: by what the options contain.
    const text = search.trim().toLowerCase();
    // As the server, a REST source gives options and suggests none of them.
    return {
      field,
      options: source.search ? services.filter((s) => `${s.value} ${s.label}`.toLowerCase().includes(text)) : services,
      cache_seconds: 60,
    };
  }
  const withDefault = (options: Option[]) => withDefaultOptions(field, inputs, options);
  switch (source.name) {
    case "vsphere.clusters":
      return withDefault(
        clusters
          .filter((c) => c.env === env && placed(db, project.id, env, "cluster", c.name))
          .map((c) => ({ value: c.name, label: c.name, meta: `${c.hosts} hosts · ${c.cpuFreePct}% CPU free` })),
      );
    case "vsphere.datastores":
      return withDefault(
        (datastores[params.cluster ?? ""] ?? [])
          .filter((d) => placed(db, project.id, env, "datastore", d.name))
          .map((d) => ({
            value: d.name,
            label: d.name,
            meta: `${d.freeTb} TB free`,
          })),
      );
    case "vsphere.networks":
      return withDefault(
        networks
          .filter((n) => n.env === env && placed(db, project.id, env, "network", n.name))
          .map((n) => ({ value: n.name, label: n.name, meta: n.subnet })),
      );
    case "rvav.projects":
    case "rvav.users":
    case "rvav.deployments":
    case "rvav.vms":
      return { field, options: rvavOptions(db, project, source.name, params, search), cache_seconds: 30 };
    // The VM folders of the placement's datacenters: every mock cluster is in GVA.
    case "vsphere.folders": {
      // As the server: each folder under its parent, named by its own name in a tree.
      const tree = ui.widget === "tree";
      const options = vmFolders.map((f): Option => {
        const parent = f.slice(0, f.lastIndexOf("/"));
        if (!vmFolders.includes(parent)) return { value: f, label: f, meta: "GVA" };
        return { value: f, label: tree ? f.slice(parent.length + 1) : f, meta: "GVA", parent };
      });
      return { field, options, cache_seconds: 60 };
    }
    case "ipam.subnets":
      return withDefault(
        networks
          .filter((n) => !params.network || n.name === params.network)
          .map((n) => ({ value: n.subnet, label: `${n.subnet} · ${n.name}`, meta: `Gateway ${n.gateway}` })),
      );
    case "dns.zones":
      return withDefault(dnsZones.map((z) => ({ value: z, label: z })));
    // What administrators offer this project and environment (ADR-0020); the mock offers the same everywhere.
    case "rvav.templates":
      return withDefault([
        { value: "rhel9", label: "RHEL 9.4", meta: "tpl-rhel9.4-2026.10" },
        { value: "ubuntu", label: "Ubuntu 24.04", meta: "tpl-ubuntu24.04-2026.10" },
        { value: "win2025", label: "Windows Server 2025", meta: "tpl-win2025-std-2026.09" },
      ]);
    case "rvav.sizes":
      return withDefault(
        (Object.entries(sizes) as [SizeKey, (typeof sizes)[SizeKey]][]).map(([key, z]) => ({
          value: key,
          label: z.label,
          meta: `${z.vcpu} vCPU · ${z.memoryGb} GB · ${z.diskGb} GB`,
        })),
      );
    // NSX's (ADR-0028), as the fake NSX has them; never what rvav made for deployments.
    case "nsx.segments":
      return withDefault(
        [
          { value: "seg-gva-dev-app", label: "seg-gva-dev-app", meta: "t1-gva-dev · 10.31.40.1/24" },
          { value: "seg-gva-prd-app", label: "seg-gva-prd-app", meta: "t1-gva-prd · 10.32.40.1/24" },
        ].filter((o) => !params.tier1 || o.meta.startsWith(`${params.tier1} `)),
      );
    case "nsx.tier1s":
      return withDefault(["t1-gva-dev", "t1-gva-prd", "t1-zrh-prd"].map((t) => ({ value: t, label: t.toUpperCase() })));
    case "nsx.groups":
      return withDefault([
        { value: "nsx:grp-backup-clients", label: "grp-backup-clients", meta: "Tag backup|daily" },
        { value: "nsx:grp-web-servers", label: "grp-web-servers", meta: "Tag role|web" },
      ]);
    case "nsx.services":
      return withDefault(
        ["HTTP", "HTTPS", "ICMP-ALL", "MS-SQL-S", "MySQL", "PostgreSQL", "RDP", "SSH"].map((s) => ({
          value: s,
          label: s,
        })),
      );
    case "awx.inventories":
      return withDefault([
        { value: "1", label: "Demo inventory" },
        { value: "2", label: "rvav" },
        { value: "3", label: "Linux servers", meta: "Smart inventory" },
      ]);
    case "awx.credentials":
      return withDefault(
        [
          { value: "4", label: "Linux machine", meta: "Machine", kind: "ssh" },
          { value: "5", label: "Network devices", meta: "Network", kind: "net" },
          { value: "6", label: "Secrets vault", meta: "Vault", kind: "vault" },
        ]
          .filter((c) => !params.kind || c.kind === params.kind)
          .map(({ value, label, meta }) => ({ value, label, meta })),
      );
    case "ipam.next_free_address": {
      const next = params.network ? nextFreeIp(db, params.network) : null;
      return {
        field,
        value: next?.ip ?? "",
        options: next ? [{ value: next.ip, label: next.ip, meta: `${next.subnet} · ${next.free} free` }] : [],
        cache_seconds: 5,
      };
    }
    case "naming.preview": {
      const role = (params.role ?? "").toLowerCase().replace(/[^a-z]/g, "");
      return { field, value: vmNames(db, project, env, role, 1)[0] ?? "", cache_seconds: 5 };
    }
    default:
      return null;
  }
}

type JsonSchema = {
  type?: string;
  properties?: Record<string, JsonSchema>;
  additionalProperties?: JsonSchema | boolean;
  propertyNames?: { minLength?: number };
  required?: string[];
  pattern?: string;
  minLength?: number;
  maxLength?: number;
  minimum?: number;
  maximum?: number;
  multipleOf?: number;
  format?: string;
  formatMinimum?: string;
  formatMaximum?: string;
  enum?: unknown[];
  items?: JsonSchema;
  minItems?: number;
  maxItems?: number;
  uniqueItems?: boolean;
  readOnly?: boolean;
};

const fieldLabel = (form: RequestForm | undefined, field: string) => form?.ui_schema.fields[field]?.label ?? field;

const empty = (v: unknown) => v === undefined || v === null || v === "" || (Array.isArray(v) && v.length === 0);

const dateTime = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}(:\d{2}(\.\d+)?)?(Z|[+-]\d{2}:\d{2})$/i;
const dateOnly = /^\d{4}-\d{2}-\d{2}$/;

/** A value against its schema, as the server's validation words it; `name` is the input, for the role's message. */
function checkValue(
  prop: JsonSchema,
  v: unknown,
  at: string,
  label: string,
  add: (field: string, code: string, message: string) => void,
  name = at,
) {
  if (prop.type === "string") {
    if (typeof v !== "string") return add(at, "type", `${label} must be text.`);
    if (prop.format === "date-time" && (!dateTime.test(v) || Number.isNaN(Date.parse(v))))
      return add(at, "format", "Enter a date and time with its time zone, for example 2026-11-20T14:30:00+01:00.");
    if (prop.format === "date" && (!dateOnly.test(v) || Number.isNaN(Date.parse(v))))
      return add(at, "format", "Enter a date as YYYY-MM-DD, for example 2026-11-20.");
    if (prop.format === "date-time" || prop.format === "date") {
      const t = Date.parse(v);
      const noun = prop.format === "date" ? "a date" : "a time";
      if (prop.formatMinimum && t < Date.parse(prop.formatMinimum))
        return add(at, "out_of_range", `Pick ${noun} on or after ${prop.formatMinimum}.`);
      if (prop.formatMaximum && t > Date.parse(prop.formatMaximum))
        return add(at, "out_of_range", `Pick ${noun} on or before ${prop.formatMaximum}.`);
    }
    if (prop.pattern && !new RegExp(prop.pattern).test(v))
      return add(
        at,
        "pattern",
        name === "role" ? "Use 2–12 lower-case letters, for example “api”." : `${label} has an invalid format.`,
      );
    if (prop.maxLength !== undefined && v.length > prop.maxLength)
      return add(at, "maxLength", `${label} can be at most ${prop.maxLength} characters.`);
    if (prop.enum && !prop.enum.includes(v)) return add(at, "enum", `Choose one of the listed values for ${label}.`);
  }
  if (prop.type === "integer" || prop.type === "number") {
    const n = Number(v);
    if (typeof v === "boolean" || !Number.isFinite(n)) return add(at, "type", `${label} must be a number.`);
    if (prop.type === "integer" && !Number.isInteger(n)) return add(at, "type", "Enter a whole number.");
    if (prop.minimum !== undefined && n < prop.minimum) return add(at, "minimum", `Enter ${prop.minimum} or more.`);
    if (prop.maximum !== undefined && n > prop.maximum) return add(at, "maximum", `Enter ${prop.maximum} or less.`);
    // Compared in whole steps: 0.3 / 0.1 isn't exactly 3 in floating point.
    if (prop.multipleOf && Math.abs(n / prop.multipleOf - Math.round(n / prop.multipleOf)) > 1e-9)
      return add(at, "multiple_of", `Enter a multiple of ${prop.multipleOf}.`);
  }
  if (prop.type === "array" && Array.isArray(v)) {
    if (prop.minItems && v.length < prop.minItems)
      return add(at, "minItems", `Pick at least ${prop.minItems} for ${label}.`);
    if (prop.maxItems !== undefined && v.length > prop.maxItems)
      return add(at, "max_items", `Keep it to ${prop.maxItems} at most.`);
    if (prop.uniqueItems && new Set(v.map((x) => JSON.stringify(x))).size < v.length)
      return add(at, "unique", "The same value is in the list twice.");
    if (prop.items) v.forEach((x, i) => checkValue(prop.items!, x, `${at}/${i}`, label, add));
  }
  if (prop.type === "object" && v && typeof v === "object" && !Array.isArray(v)) {
    const obj = v as Record<string, unknown>;
    if (prop.propertyNames?.minLength && Object.keys(obj).some((k) => k.length < prop.propertyNames!.minLength!))
      add(at, "key", "Give every pair a key.");
    if (prop.additionalProperties === false)
      for (const k of Object.keys(obj))
        if (!prop.properties?.[k]) add(`${at}/${k}`, "unknown_field", `This form has no field named ${k}.`);
    for (const r of prop.required ?? []) if (empty(obj[r])) add(`${at}/${r}`, "required", "This field is required.");
    for (const [k, x] of Object.entries(obj)) {
      const sub =
        prop.properties?.[k] ?? (typeof prop.additionalProperties === "object" ? prop.additionalProperties : undefined);
      if (sub && !empty(x)) checkValue(sub, x, `${at}/${k}`, label, add);
    }
  }
}

/** The subset of JSON Schema the mock forms use. Messages are written for end users. */
export function validateInputs(
  db: Db,
  item: StoredItem,
  inputs: Inputs,
  form: RequestForm | undefined = db.forms[item.id],
): FieldError[] {
  const schema = form?.schema as JsonSchema | undefined;
  if (!schema) return [];
  const errors: FieldError[] = [];
  const add = (field: string, code: string, message: string) =>
    errors.push({ field: `/inputs/${field}`, code, message });
  for (const name of schema.required ?? []) {
    if (empty(inputs[name])) add(name, "required", `${fieldLabel(form, name)} is required.`);
  }
  for (const [name, prop] of Object.entries(schema.properties ?? {})) {
    const v = inputs[name];
    if (v === undefined || v === null || v === "" || prop.readOnly) continue;
    checkValue(prop, v, name, fieldLabel(form, name), add);
  }
  return errors.map((e) => ({ ...e, message: locate(form, e.field, e.message) }));
}

/** A problem inside a list, table or pairs, prefixed with the item, row and column, or key it is in (as the server does). */
function locate(form: RequestForm | undefined, field: string, message: string): string {
  const [name = "", at, column] = field.replace(/^\/inputs\//, "").split("/");
  const ui = form?.ui_schema.fields[name];
  if (!ui || at === undefined) return message;
  if (ui.widget === "key_value") return `${at.replace(/~1/g, "/").replace(/~0/g, "~")}: ${message}`;
  if (ui.widget === "object") return `${ui.columns?.find((c) => c.key === at)?.label ?? at}: ${message}`;
  if (!/^\d+$/.test(at)) return message;
  const n = Number(at) + 1;
  if (ui.widget !== "datagrid") return `Item ${n}: ${message}`;
  const label = column && (ui.columns?.find((c) => c.key === column)?.label ?? column);
  return `Row ${n}${label ? `, ${label}` : ""}: ${message}`;
}

export function vmCount(item: StoredItem, count: number, inputs: Inputs = {}) {
  const parts = multiParts(item.slug, inputs);
  if (parts) return partVMs(parts);
  if (item.type === "vm_blueprint") return count;
  if (item.slug === "postgres-cluster") return 3 * count;
  return 0;
}

export function demand(item: StoredItem, inputs: Inputs, count: number): Record<QuotaResource, number> {
  const parts = multiParts(item.slug, inputs);
  if (parts) {
    // Every component's VMs (ADR-0024).
    const out = { vcpu: 0, memory_gb: 0, storage_gb: 0, vm_count: 0 };
    for (const p of parts) {
      const size = sizes[p.size] ?? sizes.M;
      out.vcpu += size.vcpu * p.count;
      out.memory_gb += size.memoryGb * p.count;
      out.storage_gb += size.diskGb * p.count;
      out.vm_count += p.count;
    }
    return out;
  }
  const vms = vmCount(item, count);
  const size = sizes[(inputs.size as SizeKey) ?? "M"] ?? sizes.M;
  return {
    vcpu: size.vcpu * vms,
    memory_gb: size.memoryGb * vms,
    storage_gb: size.diskGb * vms,
    vm_count: vms,
  };
}

/** What a request's policy conditions read, as the server's PolicyVars. */
export function policyVars(
  db: Db,
  item: StoredItem,
  env: EnvironmentKey,
  inputs: Inputs,
  project: Pick<Project, "id" | "code" | "name">,
) {
  const me = db.me.user.id;
  const size = sizes[inputs.size as SizeKey];
  return {
    environment: env,
    inputs,
    project: { id: project.id, code: project.code, name: project.name, properties: {} },
    item: { slug: item.slug, category: item.category, type: item.type },
    vm: { vcpu: size?.vcpu ?? 0, memory_gb: size?.memoryGb ?? 0, disk_gb: size?.diskGb ?? 0, count: 1 },
    user: {
      id: me,
      roles: db.me.roles.map((r) => r.key),
      groups: userGroups[me] ?? [],
      permissions: db.me.permissions,
    },
    request: { kind: "provision" },
    quota: { over_soft: false, over: [] },
  };
}

/** Whom a preview's level asks besides its named approvers, for the levels a request opens. */
export const levelAsks = new WeakMap<
  object,
  { group: string | null; roles: components["schemas"]["RoleRef"][]; manager: boolean }
>();

/**
 * The approval policies an administrator saved, as the server applies them:
 * enabled, for the project or global, for the item's type, and whose
 * condition holds. Each level asks its users, its group's members, whoever
 * holds its roles in the project and the requester's manager, never the
 * requester. The seeded production policy stays hard-coded in approvalFor.
 */
function savedApprovals(
  db: Db,
  item: StoredItem,
  env: EnvironmentKey,
  inputs: Inputs,
  project: Pick<Project, "id" | "code" | "name">,
): ApprovalPreview["policies"] {
  const me = db.me.user.id;
  const vars = policyVars(db, item, env, inputs, project);
  const out: ApprovalPreview["policies"] = [];
  for (const pol of adminState(db).approvals) {
    if (pol.id === "pol_prd_vm" || !pol.enabled) continue;
    if (pol.project_id && pol.project_id !== project.id) continue;
    if (pol.item_type && pol.item_type !== item.type) continue;
    if (!celBool(pol.condition, vars, false)) continue;
    if (pol.auto_approve && celBool(pol.auto_approve, vars, false)) continue;
    out.push({
      policy_id: pol.id,
      name: pol.name,
      reason: pol.reason || pol.name,
      levels: pol.levels.map((l) => {
        const ids = new Set(l.approvers ?? []);
        for (const u of db.users) {
          if (u.id === me) continue;
          if (l.group && (userGroups[u.id] ?? []).includes(l.group)) ids.add(u.id);
          if (l.roles?.some((k) => rolesGiven(db, project.id, u.id).includes(k))) ids.add(u.id);
        }
        if (l.manager && managers[me]) ids.add(managers[me]);
        const level = {
          name: l.name,
          mode: l.mode ?? "any",
          approvers: db.users.filter((u) => ids.has(u.id)),
        };
        levelAsks.set(level, {
          group: l.group ?? null,
          roles: roleRefs(db, l.roles ?? []),
          manager: Boolean(l.manager),
        });
        return level;
      }),
    });
  }
  return out;
}

export function approvalFor(
  db: Db,
  item: StoredItem,
  env: EnvironmentKey,
  inputs: Inputs,
  project?: Pick<Project, "id" | "code" | "name">,
): ApprovalPreview {
  const leads = {
    name: "Infrastructure leads",
    approvers: db.users.filter((u) => u.id === "usr_mkeller"),
    mode: "any" as const,
  };
  const production = db.environments.find((e) => e.key === env)?.production ?? false;
  const policies: ApprovalPreview["policies"] = [];
  if (item.approval === "always") {
    policies.push({
      policy_id: "pol_always_" + item.slug,
      name: `${item.name} needs approval`,
      reason:
        item.slug === "firewall-flow" ? "Firewall changes always need approval" : `${item.name} always needs approval`,
      levels: [leads],
    });
  } else if (item.approval === "conditional" && production) {
    const big = (sizes[inputs.size as SizeKey]?.memoryGb ?? 0) > 32;
    policies.push({
      policy_id: big ? "pol_prd_bigram" : "pol_prd",
      name: big ? "Production VM above 32 GB RAM" : "Production needs approval",
      reason: big ? "Production VM above 32 GB RAM" : "Production VMs need sign-off",
      levels: [leads],
    });
  }
  if (project) policies.push(...savedApprovals(db, item, env, inputs, project));
  return { required: policies.length > 0, policies, typical_wait_seconds: policies.length ? 7200 : null };
}

/** The OIDC groups of the mock users, as in the server's seed: rules see them as user.groups. */
export const userGroups: Record<string, string[]> = {
  usr_lmartin: ["rvav-web-admins", "rvav-catalog-admins"],
  usr_mkeller: ["rvav-admins", "rvav-infra-leads"],
  usr_tnguyen: ["rvav-web"],
  usr_sbrunner: ["rvav-data"],
  usr_afavre: ["rvav-core", "rvav-network"],
  usr_cdubois: ["rvav-infra-heads"],
};

/** The mock users' managers, as the server's seed and its identity provider give them (ADR-0041). */
export const managers: Record<string, string> = {
  usr_tnguyen: "usr_lmartin",
  usr_lmartin: "usr_mkeller",
  usr_sbrunner: "usr_mkeller",
  usr_afavre: "usr_mkeller",
  usr_mkeller: "usr_cdubois",
};

/**
 * What the server's applyStates does with the rule results: a hidden input
 * is dropped, a read-only one is its computed value, its default or its
 * initial value, one without an input takes its default and one emptied
 * (null) none; a computed value that failed and an empty field a rule
 * requires are errors. Schema errors about hidden fields are dropped.
 */
export function applyStates(
  form: RequestForm | undefined,
  states: Record<string, FieldState>,
  inputs: Inputs,
  validate: (inputs: Inputs) => FieldError[],
): FieldError[] {
  const errors: FieldError[] = [];
  for (const [name, st] of Object.entries(states)) {
    const add = (code: string, message: string) => {
      if (!errors.some((e) => e.field === `/inputs/${name}`)) errors.push({ field: `/inputs/${name}`, code, message });
    };
    if (!st.visible) {
      delete inputs[name];
      continue;
    }
    const ui = form?.ui_schema.fields[name];
    const fallback = st.default !== undefined && st.default !== null;
    if (st.value !== undefined && st.value !== null) inputs[name] = st.value;
    else if (ui && computedBy(ui))
      add(ui.rules?.value ? "rule_failed" : "source_failed", `This value couldn't be computed: ${st.error ?? ""}.`);
    else if (st.read_only && fallback) inputs[name] = st.default;
    else if (st.read_only) {
      if (name in (form?.initial_values ?? {})) inputs[name] = form!.initial_values[name];
      else delete inputs[name];
    } else if (ui && defaulted(ui)) {
      if (!Object.hasOwn(inputs, name)) {
        if (fallback) inputs[name] = st.default;
      } else if (inputs[name] === null) delete inputs[name];
    }
    const v = inputs[name];
    if (st.required && (v === undefined || v === null || v === "" || (Array.isArray(v) && v.length === 0)))
      add("required", `${form?.ui_schema.fields[name]?.label ?? name} is required.`);
  }
  // Then the schema, on the inputs the rules left; hidden fields aren't checked.
  const hidden = (e: FieldError) => states[e.field.replace(/^\/inputs\//, "").split("/")[0]!]?.visible === false;
  return [...validate(inputs).filter((e) => !hidden(e) && !errors.some((x) => x.field === e.field)), ...errors];
}

/** A draft's inputs as the rules leave them, with each field's state and the problems found. */
const varsOf = (db: Db, project: Project, draft: RequestDraft) => ({
  environment: draft.environment,
  project: { id: project.id, code: project.code, name: project.name },
  user: { id: db.me.user.id, roles: [], groups: userGroups[db.me.user.id] ?? [] },
});

export function ruled(db: Db, item: StoredItem, project: Project, draft: RequestDraft) {
  const inputs = { ...draft.inputs };
  const fields = settledStates(db, db.forms[item.id], inputs, varsOf(db, project, draft));
  const errors = applyStates(db.forms[item.id], fields, inputs, (x) => validateInputs(db, item, x));
  return { inputs, fields, errors };
}

/**
 * What the server's evaluation finds: with validate (submit, the review step), the form's
 * validations run once its inputs pass the form's own checks (ADR-0043).
 */
export function preview(
  db: Db,
  item: StoredItem,
  project: Project,
  draft: RequestDraft,
  validate = false,
  strict = false,
): RequestPreview {
  const { inputs, fields, errors } = ruled(db, item, project, draft);
  if (validate && errors.length === 0)
    errors.push(...validationErrors(db, db.forms[item.id], { ...varsOf(db, project, draft), inputs }));
  const count = draft.count ?? 1;
  if (count < 1 || count > 20) errors.push({ field: "/count", code: "range", message: "Between 1 and 20." });
  if (multiParts(item.slug, inputs) && count !== 1)
    errors.push({
      field: "/count",
      code: "out_of_range",
      message: `${item.name} builds one deployment: how many of each VM is in its form.`,
    });
  if (draft.scheduled_at && new Date(draft.scheduled_at).getTime() < Date.now() - 60_000)
    errors.push({ field: "/scheduled_at", code: "in_past", message: "Pick a start time in the future." });
  if (!project.environments.includes(draft.environment)) {
    errors.push({
      field: "/environment",
      code: "environment",
      message: `${project.name} can't deploy to ${draft.environment}.`,
    });
  }
  const need = demand(item, inputs, count);
  const q = db.quota[project.id];
  const quota: QuotaImpact[] =
    need.vm_count === 0 || !q
      ? []
      : (Object.keys(need) as QuotaResource[]).map((resource) => {
          const limit = q.limits[resource];
          const used = q.used[resource];
          return {
            resource,
            level: "project",
            limit,
            used,
            adding: need[resource],
            exceeds: limit !== null && used + need[resource] > limit,
            enforcement: "hard",
          };
        });
  for (const line of quota) {
    if (line.exceeds) {
      errors.push({
        field: "",
        code: "quota_exceeded",
        message: `This request needs ${line.adding} more ${line.resource.replace("_", " ")} than ${project.name} has left.`,
      });
    }
  }
  if (need.vm_count > 0) {
    const owner = (draft.on_behalf_of && db.users.find((u) => u.id === draft.on_behalf_of)) || db.me.user;
    const levels = userQuotaCheck(db, { project, owner, you: owner.id === db.me.user.id }, "This request", need);
    quota.push(...levels.lines);
    for (const message of levels.hard) errors.push({ field: "", code: "quota_exceeded", message });
  }
  const role = typeof inputs.role === "string" ? inputs.role.toLowerCase().replace(/[^a-z]/g, "") : "";
  const vms = vmCount(item, count, inputs);
  // A composite's are its VM steps' (the server's own names and placement
  // are a VM item's).
  const components =
    item.type === "vm_blueprint" || item.slug === "postgres-cluster"
      ? previewComponents(db, item, project, draft.environment, inputs, count)
      : undefined;
  const names = components
    ? components.flatMap((c) => c.names)
    : vms > 0
      ? vmNames(db, project, draft.environment, item.slug === "postgres-cluster" ? `pg${role}` : role, vms)
      : [];
  const named = requestedNames(db, item, project, draft, inputs, names);
  errors.push(...named.errors);
  const network = typeof inputs.network === "string" ? inputs.network : "";
  const next = network ? nextFreeIp(db, network) : null;
  // As the server's evaluateLease (ADR-0048): the lease rules, then the lease policies.
  const lease =
    vms > 0
      ? chooseLease(
          db,
          project.id,
          draft.environment,
          policyVars(db, item, draft.environment, inputs, project),
          strict,
          false,
        )
      : undefined;
  const leaseDays = draft.lease_days ?? lease?.terms.default_days ?? null;
  const soft = lease?.terms.enforcement === "soft";
  const tooLong = lease ? leaseTooLong(db, lease, draft.environment, leaseDays) : "";
  const envName = db.environments.find((e) => e.key === draft.environment)?.name ?? draft.environment;
  const warnings = tooLong && soft ? [warning(`Lease in ${envName}`, tooLong)] : [];
  if (tooLong && !soft) errors.push({ field: "/lease_days", code: "too_long", message: tooLong });
  if (leaseDays !== null && leaseDays < 1)
    errors.push({ field: "/lease_days", code: "too_short", message: "A lease is at least one day." });
  return {
    valid: errors.length === 0,
    errors,
    fields,
    names,
    ...(named.names ? { deployment_names: named.names } : {}),
    addresses: next ? [next.ip] : [],
    placement:
      vms > 0 && item.type === "vm_blueprint"
        ? {
            // As the server: a cluster input left empty places nowhere; placement chooses only where the form has none.
            cluster:
              typeof inputs.cluster === "string"
                ? inputs.cluster
                : db.forms[item.id]?.ui_schema.fields.cluster
                  ? undefined
                  : clusters.find((c) => c.env === draft.environment)?.name,
            datastore: typeof inputs.datastore === "string" ? inputs.datastore : undefined,
            folder:
              placementFolder(db, project.id, draft.environment) ?? `/GVA/vm/rvav/${project.code}/${draft.environment}`,
            network: network || undefined,
            subnet: next?.subnet,
          }
        : undefined,
    ...(components ? { components } : {}),
    quota,
    approval: approvalFor(db, item, draft.environment, inputs, project),
    ...(warnings.length ? { warnings } : {}),
    lease: {
      default_days: lease?.terms.default_days ?? null,
      max_days: lease?.terms.max_days ?? null,
      enforcement: soft ? "soft" : "hard",
      expires_at: leaseDays ? new Date(Date.now() + leaseDays * 86_400_000).toISOString() : null,
      ...(lease
        ? {
            max_extensions: lease.terms.max_extensions,
            max_lifetime_days: lease.terms.max_lifetime_days ?? null,
            ...(lease.policy ? { policy: lease.policy } : {}),
          }
        : {}),
    },
  };
}

export function summarize(d: DeploymentDetail): Deployment {
  const { inputs: _inputs, resources, ...rest } = d;
  const vms = resources.filter((r): r is VmResource => r.type === "vsphere.vm");
  const first = vms[0];
  return {
    ...rest,
    vm_count: vms.length,
    vms_powered_on: vms.filter((v) => v.vm.power_state === "on").length,
    primary_address: first ? { ip: first.vm.nics[0]?.ip_addresses?.[0] ?? "", fqdn: first.vm.fqdn ?? "" } : null,
  };
}

type Props = Record<string, { readOnly?: boolean; type?: string }>;

/** What a mock REST source gives a field that takes one value: the first of the services it lists. */
const restValue = "svc-01";

/**
 * The rule results and defaults the server works out (core.formStates), with
 * the mock's CEL: the default rules, the sources that give a field one value,
 * the default rules again, then the other rules over every default.
 */
export function fieldStates(
  db: Db,
  form: RequestForm | undefined,
  inputs: Record<string, unknown>,
  vars: Record<string, unknown>,
): Record<string, FieldState> {
  const out: Record<string, FieldState> = {};
  if (!form) return out;
  const schema = form.schema as { required?: string[]; properties?: Props };
  const fields = form.ui_schema.fields;
  const has = (n: string) => Object.hasOwn(inputs, n);
  const given = Object.fromEntries(
    Object.entries(inputs).filter(([k, v]) => v !== null || !fields[k] || !defaulted(fields[k])),
  );
  const first = ruleDefaults(fields, vars, given, has).defaults;
  const sources = sourceDefaults(db, fields, schema.properties ?? {}, vars, filled(given, first, has), has);
  const base = { ...filled(given, sources.defaults, has), ...sources.values };
  const { defaults: ruled, failed } = ruleDefaults(fields, vars, base, has);
  const ctx = { ...vars, inputs: filled(base, ruled, has) };
  for (const [name, ui] of Object.entries(fields)) {
    const rules = ui.rules ?? {};
    const state: FieldState = {
      visible: celBool(rules.visible, ctx, true),
      required: celBool(rules.required, ctx, schema.required?.includes(name) ?? false),
      read_only: celBool(rules.read_only, ctx, Boolean(schema.properties?.[name]?.readOnly) || Boolean(rules.value)),
    };
    if (rules.value) {
      try {
        state.value = celEval(rules.value, ctx);
      } catch (e) {
        state.error = `value: ${String(e)}`;
      }
    }
    if (name in ruled) state.default = ruled[name];
    else if (name in sources.defaults) state.default = sources.defaults[name];
    if (name in sources.values) state.value = sources.values[name];
    const why = failed[name] ?? sources.failed[name];
    if (why && !state.error) state.error = why;
    out[name] = state;
  }
  return out;
}

/**
 * As the server's settledStates: the states over the inputs the rules keep,
 * so no rule reads a hidden or read-only field's input, nor a computed
 * one's other than its value; again until those inputs stop changing.
 */
export function settledStates(
  db: Db,
  form: RequestForm | undefined,
  inputs: Record<string, unknown>,
  vars: Record<string, unknown>,
): Record<string, FieldState> {
  let kept = keptInputs(form, inputs);
  let states = fieldStates(db, form, kept, vars);
  for (let i = 0; i <= Object.keys(form?.ui_schema.fields ?? {}).length; i++) {
    const next = keptInputs(form, inputs, states);
    if (JSON.stringify(next) === JSON.stringify(kept)) break;
    kept = next;
    states = fieldStates(db, form, kept, vars);
  }
  return states;
}

function keptInputs(form: RequestForm | undefined, inputs: Inputs, states?: Record<string, FieldState>): Inputs {
  const fields = form?.ui_schema.fields ?? {};
  const out: Inputs = {};
  for (const k of Object.keys(inputs).sort()) {
    const ui = fields[k];
    const st = states?.[k];
    if (ui && (computedBy(ui) || (st && (!st.visible || st.read_only)))) continue;
    out[k] = inputs[k];
  }
  for (const [k, st] of Object.entries(states ?? {})) {
    const ui = fields[k];
    if (!st.visible || !ui) continue;
    if (computedBy(ui)) {
      if (st.value !== undefined && st.value !== null && ui.rules?.value) out[k] = st.value;
    } else if (st.read_only && (st.default === undefined || st.default === null) && k in (form?.initial_values ?? {}))
      out[k] = form!.initial_values[k];
  }
  return Object.fromEntries(Object.entries(out).sort(([a], [b]) => a.localeCompare(b)));
}

/** base with the defaults of the fields that have no input. */
function filled(base: Inputs, defaults: Inputs, has: (n: string) => boolean): Inputs {
  const out = { ...base };
  for (const [k, v] of Object.entries(defaults)) if (!has(k)) out[k] = v;
  return out;
}

/** As the server's ruleDefaults: each rule without its own field's input, until the defaults stop changing. */
function ruleDefaults(
  fields: RequestForm["ui_schema"]["fields"],
  vars: Record<string, unknown>,
  base: Inputs,
  has: (n: string) => boolean,
) {
  const names = Object.keys(fields)
    .sort()
    .filter((n) => fields[n]!.rules?.default && !computedBy(fields[n]!));
  const defaults: Inputs = {};
  const failed: Record<string, string> = {};
  const work = { ...base };
  const set = (name: string, v: unknown) => {
    if (has(name)) return;
    if (v === undefined || v === null) delete work[name];
    else work[name] = v;
  };
  for (let pass = 0; pass <= names.length; pass++) {
    let changed = false;
    for (const name of names) {
      const { [name]: _own, ...others } = work;
      let v: unknown;
      try {
        v = celEval(fields[name]!.rules!.default!, { ...vars, inputs: others }, true);
      } catch (e) {
        failed[name] = `default: ${String(e)}`;
        if (name in defaults) {
          delete defaults[name];
          set(name, undefined);
          changed = true;
        }
        continue;
      }
      delete failed[name];
      if (name in defaults && JSON.stringify(defaults[name]) === JSON.stringify(v)) continue;
      defaults[name] = v;
      set(name, v);
      changed = true;
    }
    if (!changed) break;
  }
  for (const [k, v] of Object.entries(defaults)) if (v === null || v === undefined) delete defaults[k];
  return { defaults, failed };
}

/** As the server's sourceDefaults: a field's after those its depends_on names. */
function sourceDefaults(
  db: Db,
  fields: RequestForm["ui_schema"]["fields"],
  props: Props,
  vars: Record<string, unknown>,
  inputs: Inputs,
  has: (n: string) => boolean,
) {
  const defaults: Inputs = {};
  const values: Inputs = {};
  const failed: Record<string, string> = {};
  const work = { ...inputs };
  let pending = Object.keys(fields)
    .sort()
    .filter((n) => singleSource(fields[n]!) && !fields[n]!.rules?.value);
  while (pending.length > 0) {
    const waiting = pending;
    let ready = waiting.filter((n) => !(fields[n]!.depends_on ?? []).some((d) => d !== n && waiting.includes(d)));
    if (ready.length === 0) ready = waiting;
    for (const name of ready) {
      const ui = fields[name]!;
      let src = ui.value_source;
      let ctx: Record<string, unknown> = { ...vars, inputs: work, search: "" };
      if (src?.kind === "library") {
        const b = bound(db, src.name ?? "", src.version ?? 0, src.params ?? {}, ctx);
        src = b ? { kind: b.entry.kind, params: b.entry.params } : undefined;
        if (b) ctx = entryVars(b.params, ctx);
      }
      const params = src?.params ?? {};
      let v: unknown;
      try {
        v =
          src?.kind === "rest"
            ? restValue
            : typeof params.value === "string"
              ? celEval(params.value, ctx, true)
              : undefined;
      } catch (e) {
        failed[name] = `value source: ${String(e)}`;
        continue;
      }
      if (v === undefined || v === null) continue;
      v = typed(v, props[name]?.type);
      if (ui.widget === "computed") {
        values[name] = v;
        work[name] = v;
      } else {
        defaults[name] = v;
        if (!has(name)) work[name] = v;
      }
    }
    pending = waiting.filter((n) => !ready.includes(n));
  }
  return { defaults, values, failed };
}

/** A source's value as its input's type takes it. */
function typed(v: unknown, type?: string): unknown {
  if (type === "string" && (typeof v === "number" || typeof v === "boolean")) return String(v);
  if ((type === "number" || type === "integer") && typeof v === "string" && v.trim() !== "" && !isNaN(Number(v)))
    return Number(v);
  if (type === "boolean" && (v === "true" || v === "false")) return v === "true";
  return v;
}
