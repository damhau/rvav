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
import { toText } from "@/lib/utils";
import { namingTemplate, placed, placementFolder } from "./administration";
import { celBool, celEval } from "./cel";
import { MULTI_SLUG, previewComponents, webAppParts, webAppVMs } from "./components";
import { clusters, datastores, dnsZones, networks, prefix, sizes, type SizeKey } from "./inventory";
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
): FieldValues | null {
  const ui = (designer?.form ?? db.forms[item.id])?.ui_schema.fields[field];
  const source = ui?.value_source;
  if (!ui || !source) return null;
  const params = resolveParams(source.params, inputs);
  if (source.kind === "cel") {
    // Throws on a broken expression; the handler answers 400.
    const ctx = {
      inputs,
      environment: env,
      project: { id: project.id, code: project.code, name: project.name },
      user: designer?.user ?? { id: db.me.user.id, roles: [], groups: [] },
    };
    const p = source.params ?? {};
    const value = typeof p.value === "string" ? String(celEval(p.value, ctx)) : undefined;
    const list = typeof p.options === "string" ? celEval(p.options, ctx) : undefined;
    const options = Array.isArray(list)
      ? list.map((x): Option => (typeof x === "string" ? { value: x, label: x } : (x as Option)))
      : undefined;
    return { field, ...(options ? { options } : {}), ...(value !== undefined ? { value } : {}), cache_seconds: 30 };
  }
  if (source.kind === "rest") {
    const host = params.url ? new URL(params.url, "https://cmdb.corp.example").host : "the service";
    return withDefaultOptions(field, inputs, [
      { value: "svc-01", label: "Service 01", meta: `from ${host}` },
      { value: "svc-02", label: "Service 02", meta: `from ${host}` },
    ]);
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
  required?: string[];
  pattern?: string;
  minLength?: number;
  maxLength?: number;
  minimum?: number;
  maximum?: number;
  enum?: unknown[];
  items?: JsonSchema;
  minItems?: number;
  readOnly?: boolean;
};

const fieldLabel = (form: RequestForm | undefined, field: string) => form?.ui_schema.fields[field]?.label ?? field;

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
    const v = inputs[name];
    if (v === undefined || v === null || v === "" || (Array.isArray(v) && v.length === 0)) {
      add(name, "required", `${fieldLabel(form, name)} is required.`);
    }
  }
  for (const [name, prop] of Object.entries(schema.properties ?? {})) {
    const v = inputs[name];
    if (v === undefined || v === null || v === "" || prop.readOnly) continue;
    const label = fieldLabel(form, name);
    if (prop.type === "string") {
      if (typeof v !== "string") add(name, "type", `${label} must be text.`);
      else if (prop.pattern && !new RegExp(prop.pattern).test(v)) {
        add(
          name,
          "pattern",
          name === "role" ? "Use 2–12 lower-case letters, for example “api”." : `${label} has an invalid format.`,
        );
      } else if (prop.maxLength !== undefined && v.length > prop.maxLength) {
        add(name, "maxLength", `${label} can be at most ${prop.maxLength} characters.`);
      } else if (prop.enum && !prop.enum.includes(v))
        add(name, "enum", `Choose one of the listed values for ${label}.`);
    }
    if (prop.type === "integer" || prop.type === "number") {
      const n = Number(v);
      if (!Number.isFinite(n)) add(name, "type", `${label} must be a number.`);
      else if (prop.minimum !== undefined && n < prop.minimum)
        add(name, "minimum", `${label} must be at least ${prop.minimum}.`);
      else if (prop.maximum !== undefined && n > prop.maximum)
        add(name, "maximum", `${label} can be at most ${prop.maximum}.`);
    }
    if (prop.type === "array" && Array.isArray(v) && prop.minItems && v.length < prop.minItems) {
      add(name, "minItems", `Pick at least ${prop.minItems} for ${label}.`);
    }
  }
  return errors;
}

export function vmCount(item: StoredItem, count: number, inputs: Inputs = {}) {
  if (item.slug === MULTI_SLUG) return webAppVMs(inputs);
  if (item.type === "vm_blueprint") return count;
  if (item.slug === "postgres-cluster") return 3 * count;
  return 0;
}

export function demand(item: StoredItem, inputs: Inputs, count: number): Record<QuotaResource, number> {
  if (item.slug === MULTI_SLUG) {
    // Every component's VMs (ADR-0024).
    const out = { vcpu: 0, memory_gb: 0, storage_gb: 0, vm_count: 0 };
    for (const p of webAppParts(inputs)) {
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

export function approvalFor(db: Db, item: StoredItem, env: EnvironmentKey, inputs: Inputs): ApprovalPreview {
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

/**
 * What the server's applyStates does with the rule results: a hidden input
 * is dropped, a read-only one is its computed or initial value, a computed
 * value that failed and an empty field a rule requires are errors. Schema
 * errors about hidden fields are dropped.
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
    if (st.value !== undefined && st.value !== null) inputs[name] = st.value;
    else if (form?.ui_schema.fields[name]?.rules?.value)
      add("rule_failed", `This value couldn't be computed: ${st.error ?? ""}.`);
    else if (st.read_only) {
      if (name in (form?.initial_values ?? {})) inputs[name] = form!.initial_values[name];
      else delete inputs[name];
    }
    const v = inputs[name];
    if (st.required && (v === undefined || v === null || v === "" || (Array.isArray(v) && v.length === 0)))
      add("required", `${form?.ui_schema.fields[name]?.label ?? name} is required.`);
  }
  // Then the schema, on the inputs the rules left; hidden fields aren't checked.
  const hidden = (e: FieldError) => states[e.field.replace(/^\/inputs\//, "").split("/")[0]!]?.visible === false;
  return [...validate(inputs).filter((e) => !hidden(e) && !errors.some((x) => x.field === e.field)), ...errors];
}

export function preview(db: Db, item: StoredItem, project: Project, draft: RequestDraft): RequestPreview {
  const inputs = { ...draft.inputs };
  const count = draft.count ?? 1;
  const fields = fieldStates(db.forms[item.id], inputs, {
    environment: draft.environment,
    project: { id: project.id, code: project.code, name: project.name },
    user: { id: db.me.user.id, roles: [], groups: userGroups[db.me.user.id] ?? [] },
  });
  const errors = applyStates(db.forms[item.id], fields, inputs, (x) => validateInputs(db, item, x));
  if (count < 1 || count > 20) errors.push({ field: "/count", code: "range", message: "Between 1 and 20." });
  if (item.slug === MULTI_SLUG && count !== 1)
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
  const role = typeof inputs.role === "string" ? inputs.role.toLowerCase().replace(/[^a-z]/g, "") : "";
  const vms = vmCount(item, count, inputs);
  const components =
    item.type === "vm_blueprint" ? previewComponents(db, item, project, draft.environment, inputs, count) : undefined;
  const names = components
    ? components.flatMap((c) => c.names)
    : vms > 0
      ? vmNames(db, project, draft.environment, item.slug === "postgres-cluster" ? `pg${role}` : role, vms)
      : [];
  const network = typeof inputs.network === "string" ? inputs.network : "";
  const next = network ? nextFreeIp(db, network) : null;
  const production = db.environments.find((e) => e.key === draft.environment)?.production ?? false;
  const leaseDefault = production || vms === 0 ? null : 30;
  return {
    valid: errors.length === 0,
    errors,
    fields,
    names,
    addresses: next ? [next.ip] : [],
    placement:
      vms > 0
        ? {
            cluster:
              typeof inputs.cluster === "string"
                ? inputs.cluster
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
    approval: approvalFor(db, item, draft.environment, inputs),
    lease: {
      default_days: leaseDefault,
      max_days: production || vms === 0 ? null : 90,
      expires_at:
        (draft.lease_days ?? leaseDefault)
          ? new Date(Date.now() + (draft.lease_days ?? leaseDefault ?? 0) * 86_400_000).toISOString()
          : null,
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

/** The rule results the server computes (core.fieldStates), with the mock's CEL. */
export function fieldStates(
  form: RequestForm | undefined,
  inputs: Record<string, unknown>,
  vars: Record<string, unknown>,
): Record<string, FieldState> {
  const out: Record<string, FieldState> = {};
  if (!form) return out;
  const schema = form.schema as { required?: string[]; properties?: Record<string, { readOnly?: boolean }> };
  const ctx = { ...vars, inputs };
  for (const [name, ui] of Object.entries(form.ui_schema.fields)) {
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
    out[name] = state;
  }
  return out;
}
