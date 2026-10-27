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
  Project,
  QuotaImpact,
  QuotaResource,
  RequestDraft,
  RequestPreview,
  VmResource,
} from "@/lib/api/types";
import { toText } from "@/lib/utils";
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
  // Like the naming policy's {seq:03} counter: continue after the highest number in use.
  const base = `${project.code}-${env}-${role || "app"}`;
  const pattern = new RegExp(`^${base}(\\d{3})$`);
  const highest = db.deployments
    .flatMap((d) => d.resources.map((r) => pattern.exec(r.name)?.[1]))
    .reduce((max, seq) => (seq ? Math.max(max, Number(seq)) : max), 0);
  return Array.from({ length: count }, (_, i) => `${base}${String(highest + 1 + i).padStart(3, "0")}`);
}

export function resolveField(
  db: Db,
  item: StoredItem,
  project: Project,
  env: EnvironmentKey,
  field: string,
  inputs: Inputs,
): FieldValues | null {
  const ui = db.forms[item.id]?.ui_schema.fields[field];
  const source = ui?.value_source;
  if (!ui || !source) return null;
  const params = resolveParams(source.params, inputs);
  const withDefault = (options: Option[]): FieldValues => {
    const current = typeof inputs[field] === "string" ? inputs[field] : "";
    const valid = options.some((o) => o.value === current && !o.disabled);
    return { field, options, default: valid ? current : options.find((o) => !o.disabled)?.value, cache_seconds: 60 };
  };
  switch (source.name) {
    case "vsphere.clusters":
      return withDefault(
        clusters
          .filter((c) => c.env === env)
          .map((c) => ({ value: c.name, label: c.name, meta: `${c.hosts} hosts · ${c.cpuFreePct}% CPU free` })),
      );
    case "vsphere.datastores":
      return withDefault(
        (datastores[params.cluster ?? ""] ?? []).map((d) => ({
          value: d.name,
          label: d.name,
          meta: `${d.freeTb} TB free`,
        })),
      );
    case "vsphere.networks":
      return withDefault(
        networks.filter((n) => n.env === env).map((n) => ({ value: n.name, label: n.name, meta: n.subnet })),
      );
    case "ipam.subnets":
      return withDefault(
        networks
          .filter((n) => !params.network || n.name === params.network)
          .map((n) => ({ value: n.subnet, label: `${n.subnet} · ${n.name}`, meta: `Gateway ${n.gateway}` })),
      );
    case "dns.zones":
      return withDefault(dnsZones.map((z) => ({ value: z, label: z })));
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

const fieldLabel = (db: Db, item: StoredItem, field: string) =>
  db.forms[item.id]?.ui_schema.fields[field]?.label ?? field;

/** The subset of JSON Schema the mock forms use. Messages are written for end users. */
export function validateInputs(db: Db, item: StoredItem, inputs: Inputs): FieldError[] {
  const schema = db.forms[item.id]?.schema as JsonSchema | undefined;
  if (!schema) return [];
  const errors: FieldError[] = [];
  const add = (field: string, code: string, message: string) =>
    errors.push({ field: `/inputs/${field}`, code, message });
  for (const name of schema.required ?? []) {
    const v = inputs[name];
    if (v === undefined || v === null || v === "" || (Array.isArray(v) && v.length === 0)) {
      add(name, "required", `${fieldLabel(db, item, name)} is required.`);
    }
  }
  for (const [name, prop] of Object.entries(schema.properties ?? {})) {
    const v = inputs[name];
    if (v === undefined || v === null || v === "" || prop.readOnly) continue;
    const label = fieldLabel(db, item, name);
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

export function vmCount(item: StoredItem, count: number) {
  if (item.type === "vm_blueprint") return count;
  if (item.slug === "postgres-cluster") return 3 * count;
  return 0;
}

export function demand(item: StoredItem, inputs: Inputs, count: number): Record<QuotaResource, number> {
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

export function preview(db: Db, item: StoredItem, project: Project, draft: RequestDraft): RequestPreview {
  const inputs = draft.inputs;
  const count = draft.count ?? 1;
  const errors = validateInputs(db, item, inputs);
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
  const vms = vmCount(item, count);
  const names =
    vms > 0 ? vmNames(db, project, draft.environment, item.slug === "postgres-cluster" ? `pg${role}` : role, vms) : [];
  const network = typeof inputs.network === "string" ? inputs.network : "";
  const next = network ? nextFreeIp(db, network) : null;
  const production = db.environments.find((e) => e.key === draft.environment)?.production ?? false;
  const leaseDefault = production || vms === 0 ? null : 30;
  return {
    valid: errors.length === 0,
    errors,
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
            folder: `/GVA/vm/rvav/${project.code}/${draft.environment}`,
            network: network || undefined,
            subnet: next?.subnet,
          }
        : undefined,
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
