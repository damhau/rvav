// Onboarding in the mock backend (S13.3, X1.5): the VMs rvav doesn't
// manage, adopting them into new or existing deployments, and onboarding
// plans whose runs adopt what their filter matches, with the server's checks.
import { http, HttpResponse } from "msw";
import type { components } from "@/lib/api/schema";
import type { DeploymentDetail, DnsRecordResource, IpAddressResource, Project, VmResource } from "@/lib/api/types";
import { leasePolicyOf, rolesGiven } from "./administration";
import { seededBlueprint } from "./blueprints";
import { clusters, datastores, networks } from "./inventory";
import { heldIn } from "./roles";
import type { Db, StoredItem } from "./seed";

type S = components["schemas"];
type FieldError = S["FieldError"];

interface Unmanaged {
  vm: S["OnboardingVm"];
  /** EfficientIP has its address and A and PTR records. */
  ipam: boolean;
}

interface StoredRun {
  run: S["OnboardingRun"] & { vms: S["OnboardingRunVm"][] };
  /** When the run's work is done: runs end on the next read after it. */
  due: number;
}

interface StoredPlan {
  plan: Omit<S["OnboardingPlan"], "matching" | "running" | "last_run" | "deployment">;
  /** The deployment the plan made or joins. */
  deployment?: S["DeploymentRef"];
  runs: StoredRun[];
}

interface State {
  unmanaged: Unmanaged[];
  /** The VMs adopted, for runs that match them again. */
  adopted: (Unmanaged & { deployment: S["DeploymentRef"] })[];
  plans: StoredPlan[];
  seq: number;
}

const VCENTER = "vc01.corp.example";
/** How long a run takes in the mocks. */
const RUN_MS = 700;
const pad = (n: number, w = 2) => String(n).padStart(w, "0");

function state(db: Db): State {
  const d = db as Db & { onboarding?: State };
  if (d.onboarding) return d.onboarding;
  let moref = 9100;
  const add = (name: string, ip: string | undefined, os: string, cpu: number, memGb: number, cluster: string, on: boolean, ipam: boolean, folder: string, tags: string[]) => ({
    vm: {
      vcenter: VCENTER, moref: `vm-${moref++}`, name, power_state: on ? "on" : "off", guest_os: os, cpu_count: cpu,
      memory_mb: memGb * 1024, cluster, host: `esx-gva-${cluster.slice(4, 7).toLowerCase()}-0${(moref % 4) + 1}.corp.example`,
      folder, tags, ...(ip ? { ip_address: ip, hostname: `${name}.corp.example` } : {}),
    } satisfies S["OnboardingVm"],
    ipam: ipam && Boolean(ip),
  }); // prettier-ignore
  const unmanaged = [
    ...Array.from({ length: 12 }, (_, i) =>
      add(`erp-app${pad(i + 1)}`, `10.31.20.${11 + i}`, "Red Hat Enterprise Linux 8 (64-bit)", 4, 16, "GVA-PRD-CL01", true, true, "/GVA/vm/erp/prd", ["app/erp", "env/prd"]),
    ),
    ...Array.from({ length: 40 }, (_, i) =>
      add(`fs-gva${pad(i + 1, 3)}`, `10.31.30.${11 + i}`, "Microsoft Windows Server 2019 (64-bit)", 2, 8, "GVA-PRD-CL02", (i + 1) % 7 !== 0, i % 2 === 0, "/GVA/vm/files", ["app/files"]),
    ),
    ...Array.from({ length: 8 }, (_, i) =>
      add(`app-old${pad(i + 1)}`, i % 3 ? `10.31.10.${201 + i}` : undefined, "CentOS 7 (64-bit)", 2, 4, "GVA-DEV-CL01", i % 2 === 0, false, "/GVA/vm/legacy", []),
    ),
  ].sort((a, b) => a.vm.name.localeCompare(b.vm.name)); // prettier-ignore
  d.onboarding = { unmanaged, adopted: [], plans: [], seq: 1 };
  return d.onboarding;
}

function problem(status: number, title: string, errors?: FieldError[]) {
  return HttpResponse.json(
    { type: "https://rvav.dev/problems/x", title, status, request_id: "req_mock", ...(errors ? { errors } : {}) },
    { status, headers: { "Content-Type": "application/problem+json" } },
  );
}
const err = (field: string, code: string, message: string): FieldError => ({ field, code, message });
const invalid = (errors: FieldError[]) => problem(422, "Some inputs need attention", errors);
const vmsText = (n: number) => `${n} VM${n === 1 ? "" : "s"}`;
const orList = (s: string[]) => (s.length < 2 ? s.join("") : `${s.slice(0, -1).join(", ")} or ${s.at(-1)}`);
const namePattern = /^[a-z0-9][a-z0-9._-]{0,62}$/;

function matches(vm: S["OnboardingVm"], f: S["OnboardingFilter"]): boolean {
  const q = (f.q ?? "").toLowerCase();
  const folder = (f.folder ?? "").replace(/\/+$/, "");
  return (
    (!q || vm.name.includes(q) || Boolean(vm.ip_address?.startsWith(q)) || Boolean(vm.hostname?.includes(q))) &&
    (!f.vcenter || vm.vcenter === f.vcenter) &&
    (!f.cluster || vm.cluster === f.cluster) &&
    (!f.power_state || vm.power_state === f.power_state) &&
    (!folder || vm.folder === folder || Boolean(vm.folder?.startsWith(`${folder}/`))) &&
    (!f.tag || Boolean(vm.tags?.includes(f.tag)))
  );
}

const hasCriterion = (f: S["OnboardingFilter"]) =>
  [f.q, f.vcenter, f.cluster, f.power_state, f.folder, f.tag].some((v) => v !== undefined && v.trim() !== "");

interface Component {
  key: string;
  title?: string;
  count?: unknown;
}

function componentsOf(item: StoredItem): Component[] {
  return (seededBlueprint(item.slug)?.components as Component[] | undefined) ?? [];
}

/** The component VMs join, as the server picks it: "" when there is none to pick. */
function componentOf(item: StoredItem, key: string | undefined, at: string, errors: FieldError[]): string {
  const list = componentsOf(item);
  if (list.length === 0) {
    if (key && key !== "vm")
      errors.push(
        err(`${at}/component`, "invalid", `${item.name}'s blueprint has no components: leave the component out.`),
      );
    return "vm";
  }
  const found = list.find((c) => c.key === key) ?? (!key && list.length === 1 ? list[0] : undefined);
  if (found) return found.key;
  const keys = orList(list.map((c) => c.key));
  errors.push(
    key
      ? err(`${at}/component`, "invalid", `${item.name} has no component ${key}: ${keys}.`)
      : err(`${at}/component`, "required", `Choose the component of ${item.name} they are VMs of: ${keys}.`),
  );
  return "";
}

interface NewTarget {
  project_id: string;
  environment?: string;
  owner_id?: string;
  catalog_item_id?: string;
  lease_days?: number;
  deployment_name?: string;
  component?: string;
  inputs?: Record<string, unknown>;
}

interface Destination {
  project: Project;
  environment: string;
  owner: S["UserRef"];
  item: StoredItem;
  component: string;
  days?: number;
  policy?: S["LeasePolicy"];
  name: string;
  inputs: Record<string, unknown>;
}

function newDestination(db: Db, t: NewTarget, at: string, errors: FieldError[]): Destination | undefined {
  const project = db.projects.find((p) => p.id === t.project_id);
  const env = t.environment ?? "";
  if (!project) errors.push(err(`${at}/project_id`, "not_found", "No such project."));
  else {
    if (env === "")
      errors.push(err(`${at}/environment`, "required", `Choose the environment of ${project.name} the deployments are in.`)); // prettier-ignore
    else if (!project.environments.includes(env))
      errors.push(err(`${at}/environment`, "invalid", `${project.name} has no environment ${env}.`));
    if (!heldIn(db, [], rolesGiven(db, project.id, t.owner_id ?? "")).includes("request.submit"))
      errors.push(err(`${at}/owner_id`, "invalid", `Choose a member of ${project.name} who requests there: owners run the actions on their deployments.`)); // prettier-ignore
  }
  const item = db.items.find((i) => i.id === t.catalog_item_id || i.slug === t.catalog_item_id);
  if (!item || item.type !== "vm_blueprint")
    errors.push(err(`${at}/catalog_item_id`, "invalid", "Choose a published VM item: it gives the deployments their OS family and day-2 actions.")); // prettier-ignore
  else if (project && !item.projectIds.includes(project.id))
    errors.push(err(`${at}/catalog_item_id`, "invalid", `${item.name} isn't offered in ${project.name}.`));
  // The lease policy's default when none is given, and its maximum.
  const policy = project ? leasePolicyOf(db, project.id, env) : undefined;
  const days = t.lease_days ?? policy?.default_days ?? undefined;
  if (t.lease_days !== undefined && t.lease_days < 1)
    errors.push(err(`${at}/lease_days`, "too_short", "A lease is at least one day."));
  else if (days && policy?.max_days && days > policy.max_days)
    errors.push(err(`${at}/lease_days`, "too_long", `Leases in ${env} can be at most ${policy.max_days} days.`));
  if (t.deployment_name && !namePattern.test(t.deployment_name))
    errors.push(err(`${at}/deployment_name`, "pattern", "Lower-case letters, digits, dots and dashes, up to 63, for example web-prd-erp.")); // prettier-ignore
  const component = item ? componentOf(item, t.component, at, errors) : "";
  const owner = db.users.find((u) => u.id === t.owner_id);
  if (!project || !item || !owner) return undefined;
  return {
    project, environment: env, owner, item, component, days: days ?? undefined, policy, name: t.deployment_name ?? "",
    inputs: t.inputs ?? {},
  }; // prettier-ignore
}

/** An existing deployment VMs may join, with the component they join. */
function existingDestination(
  db: Db,
  id: string,
  projectId: string | undefined,
  key: string | undefined,
  at: string,
  errors: FieldError[],
): { dep: DeploymentDetail; component: string; item: StoredItem } | undefined {
  const dep = db.deployments.find((d) => d.id === id);
  if (!dep) {
    errors.push(err(`${at}/deployment_id`, "not_found", "No such deployment."));
    return undefined;
  }
  if (projectId && dep.project.id !== projectId) {
    errors.push(err(`${at}/deployment_id`, "invalid", `${dep.name} is a deployment of ${dep.project.name}.`));
    return undefined;
  }
  const item = db.items.find((i) => i.id === dep.catalog_item.id || i.slug === dep.catalog_item.slug);
  if (!item || item.type !== "vm_blueprint") {
    errors.push(err(`${at}/deployment_id`, "invalid", `${dep.name} is a deployment of ${dep.catalog_item.name}, not of a VM item: it takes no VMs.`)); // prettier-ignore
    return undefined;
  }
  return { dep, component: componentOf(item, key, at, errors), item };
}

/** The first network of a cluster's environment, as the inventory has no adapters. */
const networkOf = (cluster = "") => {
  const env = clusters.find((c) => c.name === cluster)?.env;
  return networks.find((n) => n.env === env)?.name ?? "";
};

function resourcesOf(group: Unmanaged[], component: string) {
  const resources: DeploymentDetail["resources"] = [];
  let addresses = 0;
  let records = 0;
  for (const { vm, ipam } of group) {
    const fqdn = vm.hostname;
    const ip = vm.ip_address;
    resources.push({
      id: `res_ob${vm.moref}`, type: "vsphere.vm", name: vm.name, sync_state: "in_sync",
      vm: {
        moref: vm.moref, instance_uuid: `4219${vm.moref.slice(3)}-0000-4000-8000-000000000000`, vcenter: vm.vcenter,
        power_state: vm.power_state, guest_state: vm.power_state === "on" ? "running" : "not_running", guest_os: vm.guest_os,
        cpu_count: vm.cpu_count, memory_mb: vm.memory_mb, disks: [{ label: "Hard disk 1", key: 2000, capacity_gb: 80, datastore: datastores[vm.cluster ?? ""]?.[0]?.name }],
        nics: [{ network: networkOf(vm.cluster), label: "Network adapter 1", ...(ip ? { ip_addresses: [ip] } : {}) }],
        cluster: vm.cluster, host: vm.host, ...(ipam && fqdn ? { fqdn } : {}), snapshot_count: 0, component,
      },
    } satisfies VmResource); // prettier-ignore
    if (ipam && ip && fqdn) {
      addresses += 1;
      records += 2;
      resources.push(
        { id: `res_obip${vm.moref}`, type: "ipam.address", name: ip, sync_state: "in_sync", address: ip, subnet: `${ip.split(".").slice(0, 3).join(".")}.0/24` } satisfies IpAddressResource,
        { id: `res_oba${vm.moref}`, type: "dns.record", name: `A ${fqdn}`, sync_state: "in_sync", fqdn, record_type: "A", value: ip, zone: "corp.example" } satisfies DnsRecordResource,
        { id: `res_obp${vm.moref}`, type: "dns.record", name: `PTR ${ip}`, sync_state: "in_sync", fqdn: ip.split(".").reverse().join(".") + ".in-addr.arpa", record_type: "PTR", value: fqdn, zone: "in-addr.arpa" } satisfies DnsRecordResource,
      ); // prettier-ignore
    }
  }
  return { resources, addresses, records };
}

type Adopted = S["OnboardingResult"]["deployments"][number];

/** The adopted VMs leave the unmanaged ones. */
function take(db: Db, group: Unmanaged[], dep: DeploymentDetail) {
  const s = state(db);
  const keys = new Set(group.map(({ vm }) => `${vm.vcenter}/${vm.moref}`));
  s.unmanaged = s.unmanaged.filter(({ vm }) => !keys.has(`${vm.vcenter}/${vm.moref}`));
  s.adopted.push(...group.map((x) => ({ ...x, deployment: { id: dep.id, name: dep.name } })));
}

function newDeployment(db: Db, d: Destination, name: string, group: Unmanaged[]): Adopted {
  const { resources, addresses, records } = resourcesOf(group, d.component);
  const first = group[0]!.vm;
  const vms = resources.filter((r): r is VmResource => r.type === "vsphere.vm");
  const expires = d.days ? new Date(Date.now() + d.days * 86_400_000).toISOString() : null;
  const p = d.policy;
  const dep: DeploymentDetail = {
    id: `dep_ob${first.moref.slice(3)}`, name, description: "", status: "ready",
    project: { id: d.project.id, code: d.project.code, name: d.project.name }, environment: d.environment,
    catalog_item: { id: d.item.id, slug: d.item.slug, name: d.item.name }, version: d.item.version, owner: d.owner,
    created_at: new Date().toISOString(),
    lease: { expires_at: expires, extensions_used: 0, extensions_max: expires ? (p?.max_extensions ?? 0) : null, max_days: p?.max_days ?? null, grace_days: p?.grace_days ?? 7, expiry_action: p?.expiry_action ?? "notify_only" },
    vm_count: vms.length, vms_powered_on: vms.filter((v) => v.vm.power_state === "on").length,
    primary_address: first.ip_address ? { ip: first.ip_address, fqdn: first.hostname ?? "" } : null,
    sync_state: "in_sync", inputs: { ...d.inputs }, resources,
  }; // prettier-ignore
  db.deployments.unshift(dep);
  take(db, group, dep);
  return { id: dep.id, name: dep.name, vms: group.map((x) => x.vm.name), addresses, records };
}

/** Why the VMs can't join the deployment's component, by VM: "" when they fit. */
function misfit(
  dep: DeploymentDetail,
  item: StoredItem,
  component: string,
  group: Unmanaged[],
): [number, string] | undefined {
  const vms = dep.resources.filter((r): r is VmResource => r.type === "vsphere.vm");
  const names = new Set(vms.map((v) => v.name));
  const i = group.findIndex(({ vm }) => names.has(vm.name));
  if (i >= 0) return [i, `The deployment has a VM named ${group[i]!.vm.name} already.`];
  const comp = componentsOf(item).find((c) => c.key === component);
  if (typeof comp?.count !== "number") return undefined;
  const have = vms.filter((v) => (v.vm.component ?? "vm") === component).length;
  if (have + group.length <= comp.count) return undefined;
  return [Math.max(0, comp.count - have), `${comp.title ?? comp.key} has ${vmsText(comp.count)} (its count), and has ${have} already.`]; // prettier-ignore
}

function joinDeployment(dep: DeploymentDetail, component: string, group: Unmanaged[]): Adopted {
  const { resources, addresses, records } = resourcesOf(group, component);
  dep.resources.push(...resources);
  const vms = dep.resources.filter((r): r is VmResource => r.type === "vsphere.vm");
  dep.vm_count = vms.length;
  dep.vms_powered_on = vms.filter((v) => v.vm.power_state === "on").length;
  return { id: dep.id, name: dep.name, vms: group.map((x) => x.vm.name), addresses, records };
}

function pick(db: Db, refs: { vcenter: string; moref: string }[], errors: FieldError[]): Unmanaged[] {
  if (refs.length === 0 || refs.length > 200) {
    errors.push(err("/vms", "out_of_range", "Adopt from 1 to 200 VMs at once."));
    return [];
  }
  const all = state(db).unmanaged;
  return refs.flatMap((r, i) => {
    const x = all.find(({ vm }) => vm.vcenter === r.vcenter && vm.moref === r.moref);
    if (!x)
      errors.push(err(`/vms/${i}`, "not_found", `${r.vcenter} has no VM ${r.moref} that rvav doesn't manage: a deployment has it, or the inventory hasn't read it yet.`)); // prettier-ignore
    return x ? [x] : [];
  });
}

// ---- Plans and their runs ---------------------------------------------------

function presentPlan(db: Db, p: StoredPlan): S["OnboardingPlan"] {
  const dep = p.deployment && db.deployments.find((d) => d.id === p.deployment!.id);
  const last = p.runs[0]?.run;
  return {
    ...p.plan,
    matching: state(db).unmanaged.filter(({ vm }) => matches(vm, p.plan.filter)).length,
    running: p.runs.some((r) => r.run.status === "running"),
    ...(dep ? { deployment: { id: dep.id, name: dep.name } } : {}),
    ...(last ? { last_run: summary(last) } : {}),
  };
}

const summary = ({ vms: _, ...run }: StoredRun["run"]): S["OnboardingRun"] => run;

function checkPlan(db: Db, b: S["OnboardingPlanWrite"], id: string | undefined): FieldError[] {
  const errors: FieldError[] = [];
  const name = b.name.trim();
  if (name === "" || name.length > 80) errors.push(err("/name", "invalid", "Name the plan, in 80 characters at most."));
  else if (state(db).plans.some((p) => p.plan.name === name && p.plan.id !== id))
    errors.push(err("/name", "taken", "A plan has this name already."));
  if ((b.description ?? "").length > 500) errors.push(err("/description", "too_long", "500 characters at most."));
  const most = { q: 200, folder: 500, tag: 200 } as const;
  for (const f of ["q", "folder", "tag"] as const)
    if ((b.filter[f] ?? "").length > most[f]) errors.push(err(`/filter/${f}`, "too_long", `${most[f]} characters at most.`)); // prettier-ignore
  if (!hasCriterion(b.filter))
    errors.push(err("/filter", "required", "Give the filter a criterion at least: without one, the plan would adopt every VM rvav doesn't manage.")); // prettier-ignore
  if (b.every_hours !== undefined && (b.every_hours < 1 || b.every_hours > 168))
    errors.push(err("/every_hours", "out_of_range", "From every hour to every week (168 hours)."));
  const t = b.target;
  if (t.deployment_id) {
    for (const f of ["environment", "owner_id", "catalog_item_id", "lease_days", "deployment_name", "inputs"] as const)
      if (t[f] !== undefined) errors.push(err(`/target/${f}`, "not_allowed", "VMs joining a deployment take its own: leave it out.")); // prettier-ignore
    existingDestination(db, t.deployment_id, t.project_id, t.component, "/target", errors);
  } else newDestination(db, t, "/target", errors);
  return errors;
}

/** A run's work, done on the first read after it is due. */
function finish(db: Db, p: StoredPlan, r: StoredRun) {
  const run = r.run;
  const t = p.plan.target;
  const s = state(db);
  const outcome = (x: Unmanaged, o: S["OnboardingRunVm"]["outcome"], code = "", reason = "", dep?: S["DeploymentRef"]) =>
    run.vms.push({ vcenter: x.vm.vcenter, moref: x.vm.moref, name: x.vm.name, outcome: o, code, reason, ...(dep ? { deployment: dep } : {}) }); // prettier-ignore
  const errors: FieldError[] = [];
  const existing = t.deployment_id
    ? existingDestination(db, t.deployment_id, t.project_id, t.component, "/target", errors)
    : undefined;
  const dest = t.deployment_id ? undefined : newDestination(db, t, "/target", errors);
  const end = (status: S["OnboardingRun"]["status"], error?: string) => {
    run.status = status;
    run.finished_at = new Date().toISOString();
    if (error) run.error = error;
    run.adopted = run.vms.filter((v) => v.outcome === "adopted").length;
    run.skipped = run.vms.filter((v) => v.outcome === "skipped").length;
    run.failed = run.vms.filter((v) => v.outcome === "failed").length;
  };
  if (errors.length > 0)
    return end("failed", `Where its VMs go doesn't work now: ${errors.map((e) => e.message).join(" ")}`);
  const held = s.adopted.filter((x) => matches(x.vm, p.plan.filter) && db.deployments.some((d) => d.id === x.deployment.id)); // prettier-ignore
  const fresh = s.unmanaged.filter(({ vm }) => matches(vm, p.plan.filter));
  const found: (Unmanaged & { deployment?: S["DeploymentRef"] })[] = [...held, ...fresh];
  for (const x of found.sort((a, b) => a.vm.name.localeCompare(b.vm.name))) {
    if (x.deployment) {
      outcome(x, "skipped", "managed", `${x.deployment.name} has it.`, x.deployment);
      continue;
    }
    const named = dest?.name ? p.deployment && db.deployments.find((d) => d.id === p.deployment!.id) : undefined;
    const into = existing ?? (named && dest ? { dep: named, component: dest.component, item: dest.item } : undefined);
    if (into) {
      if (into.dep.status !== "ready") {
        outcome(x, "failed", "busy", `${into.dep.name} is ${into.dep.status}: VMs join a ready deployment.`);
        continue;
      }
      const m = misfit(into.dep, into.item, into.component, [x]);
      if (m) {
        outcome(x, "failed", "no_fit", m[1]);
        continue;
      }
      joinDeployment(into.dep, into.component, [x]);
      take(db, [x], into.dep);
      outcome(x, "adopted", "", "", { id: into.dep.id, name: into.dep.name });
      continue;
    }
    const made = newDeployment(db, dest!, dest!.name || x.vm.name, [x]);
    if (dest!.name) p.deployment = { id: made.id, name: made.name };
    outcome(x, "adopted", "", "", { id: made.id, name: made.name });
  }
  end(run.vms.some((v) => v.outcome === "failed") ? "partial" : "succeeded");
}

function settle(db: Db) {
  for (const p of state(db).plans)
    for (const r of p.runs) if (r.run.status === "running" && Date.now() >= r.due) finish(db, p, r);
}

function startRun(db: Db, p: StoredPlan, trigger: S["OnboardingRun"]["trigger"]): StoredRun {
  const s = state(db);
  const r: StoredRun = {
    run: {
      id: `obr_${pad(s.seq++, 4)}`, plan_id: p.plan.id, plan_name: p.plan.name, trigger, status: "running",
      started_at: new Date().toISOString(), adopted: 0, skipped: 0, failed: 0, vms: [],
      ...(trigger === "manual" ? { started_by: { id: db.me.user.id, name: db.me.user.name } } : {}),
    },
    due: Date.now() + RUN_MS,
  }; // prettier-ignore
  p.runs.unshift(r);
  p.runs.splice(20);
  return r;
}

export function onboardingHandlers(db: Db, base: string, wait: () => Promise<void>) {
  const plan = (id: string | readonly string[] | undefined) => state(db).plans.find((p) => p.plan.id === id);
  return [
    http.get(`${base}/admin/onboarding/vms`, async ({ request }) => {
      await wait();
      settle(db);
      const u = new URL(request.url);
      const p = u.searchParams;
      const filter: S["OnboardingFilter"] = {
        q: p.get("q") ?? undefined,
        vcenter: p.get("vcenter") ?? undefined,
        cluster: p.get("cluster") ?? undefined,
        power_state: (p.get("power_state") as S["VmPowerState"] | null) ?? undefined,
        folder: p.get("folder") ?? undefined,
        tag: p.get("tag") ?? undefined,
      };
      const match = state(db).unmanaged.filter(({ vm }) => matches(vm, filter));
      const limit = Number(p.get("limit") ?? 50);
      const from = Number(p.get("cursor") ?? 0);
      const items = match.slice(from, from + limit).map((x) => x.vm);
      return HttpResponse.json({
        items,
        total: match.length,
        ...(from + limit < match.length ? { next_cursor: String(from + limit) } : {}),
      } satisfies S["OnboardingVmPage"]);
    }),

    http.post(`${base}/admin/onboarding`, async ({ request }) => {
      await wait();
      const b = (await request.json()) as S["OnboardingDraft"];
      const errors: FieldError[] = [];
      const dest = newDestination(db, b, "", errors);
      const picked = pick(db, b.vms, errors);
      if (errors.length || !dest) return invalid(errors);
      const groups = b.deployment_name ? [picked] : picked.map((x) => [x]);
      const out: S["OnboardingResult"] = { deployments: [] };
      for (const group of groups) out.deployments.push(newDeployment(db, dest, b.deployment_name || group[0]!.vm.name, group)); // prettier-ignore
      return HttpResponse.json(out);
    }),

    http.post(`${base}/admin/onboarding/into`, async ({ request }) => {
      await wait();
      const b = (await request.json()) as S["OnboardingIntoDraft"];
      const errors: FieldError[] = [];
      const into = existingDestination(db, b.deployment_id, undefined, b.component, "", errors);
      const picked = pick(db, b.vms, errors);
      if (errors.length || !into) return invalid(errors);
      if (into.dep.status !== "ready")
        return problem(409, `${into.dep.name} is ${into.dep.status}: VMs join a ready deployment.`);
      const m = misfit(into.dep, into.item, into.component, picked);
      if (m) return invalid([err(`/vms/${m[0]}`, "no_fit", m[1])]);
      const out = joinDeployment(into.dep, into.component, picked);
      take(db, picked, into.dep);
      return HttpResponse.json({ deployments: [out] } satisfies S["OnboardingResult"]);
    }),

    http.get(`${base}/admin/onboarding/plans`, async () => {
      await wait();
      settle(db);
      const items = state(db)
        .plans.map((p) => presentPlan(db, p))
        .sort((a, b) => a.name.localeCompare(b.name));
      return HttpResponse.json({ items } satisfies S["OnboardingPlanList"]);
    }),

    http.post(`${base}/admin/onboarding/plans`, async ({ request }) => {
      await wait();
      const b = (await request.json()) as S["OnboardingPlanWrite"];
      const errors = checkPlan(db, b, undefined);
      if (errors.length) return invalid(errors);
      const s = state(db);
      const now = new Date().toISOString();
      const p: StoredPlan = {
        plan: {
          id: `obp_${pad(s.seq++, 4)}`, name: b.name.trim(), description: b.description ?? "", filter: b.filter,
          target: b.target, created_at: now, updated_at: now, updated_by: { id: db.me.user.id, name: db.me.user.name },
          ...(b.every_hours ? { every_hours: b.every_hours, next_run_at: new Date(Date.now() + b.every_hours * 3_600_000).toISOString() } : {}),
        },
        runs: [],
      }; // prettier-ignore
      if (b.target.deployment_id) {
        const dep = db.deployments.find((d) => d.id === b.target.deployment_id)!;
        p.deployment = { id: dep.id, name: dep.name };
      }
      s.plans.push(p);
      return HttpResponse.json(presentPlan(db, p), { status: 201 });
    }),

    http.get(`${base}/admin/onboarding/plans/:planId`, async ({ params }) => {
      await wait();
      settle(db);
      const p = plan(params.planId);
      return p ? HttpResponse.json(presentPlan(db, p)) : problem(404, "No such plan.");
    }),

    http.put(`${base}/admin/onboarding/plans/:planId`, async ({ params, request }) => {
      await wait();
      const p = plan(params.planId);
      if (!p) return problem(404, "No such plan.");
      const b = (await request.json()) as S["OnboardingPlanWrite"];
      const errors = checkPlan(db, b, p.plan.id);
      if (errors.length) return invalid(errors);
      const { every_hours: was, next_run_at: next, ...rest } = p.plan;
      p.plan = {
        ...rest, name: b.name.trim(), description: b.description ?? "", filter: b.filter, target: b.target,
        updated_at: new Date().toISOString(), updated_by: { id: db.me.user.id, name: db.me.user.name },
        ...(b.every_hours ? { every_hours: b.every_hours, next_run_at: b.every_hours === was && next ? next : new Date(Date.now() + b.every_hours * 3_600_000).toISOString() } : {}),
      }; // prettier-ignore
      if (b.target.deployment_id) {
        const dep = db.deployments.find((d) => d.id === b.target.deployment_id)!;
        p.deployment = { id: dep.id, name: dep.name };
      } else if (!b.target.deployment_name) p.deployment = undefined;
      return HttpResponse.json(presentPlan(db, p));
    }),

    http.delete(`${base}/admin/onboarding/plans/:planId`, async ({ params }) => {
      await wait();
      settle(db);
      const p = plan(params.planId);
      if (!p) return problem(404, "No such plan.");
      if (p.runs.some((r) => r.run.status === "running"))
        return problem(409, `A run of ${p.plan.name} goes on: delete the plan once it ends.`);
      const s = state(db);
      s.plans = s.plans.filter((x) => x !== p);
      return new HttpResponse(null, { status: 204 });
    }),

    http.get(`${base}/admin/onboarding/plans/:planId/runs`, async ({ params }) => {
      await wait();
      settle(db);
      const p = plan(params.planId);
      if (!p) return problem(404, "No such plan.");
      return HttpResponse.json({ items: p.runs.map((r) => summary(r.run)) } satisfies S["OnboardingRunList"]);
    }),

    http.post(`${base}/admin/onboarding/plans/:planId/runs`, async ({ params }) => {
      await wait();
      settle(db);
      const p = plan(params.planId);
      if (!p) return problem(404, "No such plan.");
      if (p.runs.some((r) => r.run.status === "running"))
        return problem(409, `A run of ${p.plan.name} goes on: wait for it to end.`);
      return HttpResponse.json(summary(startRun(db, p, "manual").run), { status: 202 });
    }),

    http.get(`${base}/admin/onboarding/runs/:runId`, async ({ params }) => {
      await wait();
      settle(db);
      for (const p of state(db).plans) {
        const r = p.runs.find((x) => x.run.id === params.runId);
        if (r) return HttpResponse.json(r.run satisfies S["OnboardingRun"]);
      }
      return problem(404, "No such run.");
    }),
  ];
}
