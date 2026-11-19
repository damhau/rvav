// Updates in the mock backend (ADR-0042): what internal/core/update.go
// plans, for the mock's VM items, run with the day-2 operations.
import type {
  DeploymentDetail,
  DnsRecordResource,
  FieldError,
  IpAddressResource,
  QuotaImpact,
  QuotaResource,
  ServiceRequest,
  UpdateChange,
  UpdateDraft,
  UpdatePreview,
  UpdateRefusal,
  VmResource,
} from "@/lib/api/types";
import { requestableVersions, staleVersion } from "./catalogAdmin";
import { multiParts } from "./components";
import { actionWarnings, busy, requestSteps, type Op, type RunResult } from "./day2";
import { unaccepted } from "./warnings";
import { DOMAIN, sizes, type SizeKey } from "./inventory";
import { approvalFor, reserveIp, ruled, vmNames } from "./logic";
import { userQuotaCheck } from "./quotas";
import { limitCheck } from "./limits";
import type { Db, StoredItem } from "./seed";

type Inputs = Record<string, unknown>;

interface Component {
  key: string;
  title: string;
  count: number;
  size: SizeKey;
  role: string;
  post: string[];
}

interface Plan {
  preview: UpdatePreview;
  item: StoredItem;
  /** The version updated to: the one asked for when it is requestable, else the default. */
  version: UpdatePreview["to_version"];
  inputs: Inputs;
  same: boolean;
  ops: (names: (c: Component, n: number) => string[]) => Op[];
  delta: Record<QuotaResource, number>;
}

const now = () => new Date().toISOString();
const text = (v: unknown) => (typeof v === "string" ? v : "");
const list = (v: unknown) => (Array.isArray(v) ? v.filter((x): x is string => typeof x === "string") : []);
const sizeOf = (v: unknown): SizeKey => (typeof v === "string" && v in sizes ? (v as SizeKey) : "M");
const byName = (a: VmResource, b: VmResource) => a.name.localeCompare(b.name, "en", { numeric: true });
// As the server: the VMs built by their number, then those adopted (no template), which scale in removes first.
const byIndex = (a: VmResource, b: VmResource) => Number(!a.vm.template) - Number(!b.vm.template) || byName(a, b);

// Each input in a stable order, so that two equal inputs compare equal.
const stable = (v: unknown): string =>
  JSON.stringify(v, (_k, x: unknown) =>
    x && typeof x === "object" && !Array.isArray(x)
      ? Object.fromEntries(Object.entries(x).sort(([a], [b]) => a.localeCompare(b)))
      : x,
  );

/** A VM item's components for these inputs; a single-VM item's is `vm`, with the VMs it has. */
function componentsOf(item: StoredItem, inputs: Inputs, vms: number): Component[] {
  const parts = multiParts(item.slug, inputs);
  if (parts)
    return parts.map((p) => ({ key: p.key, title: p.title, count: p.count, size: p.size, role: p.role, post: p.post }));
  return [
    {
      key: "vm",
      title: item.name,
      count: Math.max(1, vms),
      size: sizeOf(inputs.size),
      role: text(inputs.role),
      post: list(inputs.post_config),
    },
  ];
}

function vmsOf(d: DeploymentDetail, item: StoredItem, c: Component): VmResource[] {
  const all = d.resources.filter((r): r is VmResource => r.type === "vsphere.vm");
  const mine = multiParts(item.slug, d.inputs) ? all.filter((v) => v.name.includes(`-${c.role}`)) : all;
  return mine.sort(byIndex);
}

/** The names the next VMs of a component take: its counter continues, never reusing one. */
function nextNames(db: Db, d: DeploymentDetail, vms: VmResource[], c: Component, n: number): string[] {
  const last = vms.filter((v) => v.vm.template).at(-1)?.name;
  const m = last ? /^(.*?)(\d+)$/.exec(last) : null;
  const project = db.projects.find((p) => p.id === d.project.id);
  if (!m || !project) return project ? vmNames(db, project, d.environment, c.role, n) : [];
  const [, prefix, digits] = m as unknown as [string, string, string];
  const pattern = new RegExp(`^${prefix}(\\d+)$`);
  const highest = db.deployments
    .flatMap((x) => x.resources.map((r) => pattern.exec(r.name)?.[1]))
    .reduce((max, seq) => (seq ? Math.max(max, Number(seq)) : max), 0);
  return Array.from({ length: n }, (_, i) => `${prefix}${String(highest + 1 + i).padStart(digits.length, "0")}`);
}

/** Where a VM is, as its properties say. */
function placed(v: VmResource, setting: "cluster" | "datastore" | "network") {
  if (setting === "cluster") return v.vm.cluster ?? "";
  if (setting === "datastore") return v.vm.disks[0]?.datastore ?? "";
  return v.vm.nics[0]?.network ?? "";
}

function findVm(d: DeploymentDetail, name: string) {
  return d.resources.find((r): r is VmResource => r.type === "vsphere.vm" && r.name === name);
}

/** Why the caller can't update the deployment now ("" when they can). */
function unavailable(db: Db, d: DeploymentDetail): string {
  const active = busy(db, d);
  if (active) return `${active.request.number} is still running on this deployment.`;
  if (d.status === "deleted") return "The deployment was deleted.";
  if (d.status !== "ready" && d.status !== "failed") return `The deployment is ${d.status}.`;
  return "";
}

function plan(db: Db, d: DeploymentDetail, draft: UpdateDraft): Plan | { status: number; title: string } {
  const item = db.items.find((i) => i.id === d.catalog_item.id);
  const project = db.projects.find((p) => p.id === d.project.id);
  if (!item || !project) return { status: 404, title: "Catalog item not found" };
  if (item.type !== "vm_blueprint") return { status: 409, title: "Only deployments of VM items can be updated." };
  const target = requestableVersions(db, item).find((v) => v.id === draft.version_id) ?? item.version;
  const version = target.id;
  const base = { environment: d.environment, catalog_item_id: item.id, project_id: project.id, count: 1 };
  // The seed's deployments have inputs their form doesn't: only the form's count.
  const props = (db.forms[item.id]?.schema as { properties?: Inputs } | undefined)?.properties ?? {};
  // As the server: a deployment adopted without inputs had its version's initial values. One with some
  // keeps them as they are, as a key left out can be a default the requester emptied.
  const kept =
    Object.keys(d.inputs ?? {}).length === 0
      ? { ...(db.forms[item.id]?.initial_values ?? {}) }
      : Object.fromEntries(Object.entries(d.inputs).filter(([k]) => k in props));
  const before = ruled(db, item, project, { ...base, version_id: d.version.id, inputs: kept }).inputs;
  const { inputs, fields, errors } = ruled(db, item, project, { ...base, version_id: version, inputs: draft.inputs });
  const stale = staleVersion(db, item, draft.version_id);
  if (stale) errors.push(stale);

  const refused: UpdateRefusal[] = [];
  const changes: UpdateChange[] = [];
  const delta: Record<QuotaResource, number> = { vcpu: 0, memory_gb: 0, storage_gb: 0, vm_count: 0 };
  const failed = d.status === "failed";
  const vmCount = d.resources.filter((r) => r.type === "vsphere.vm").length;
  const had = new Map(componentsOf(item, before, vmCount).map((c) => [c.key, c]));
  const builds: { c: Component; vms: VmResource[]; add: number; remove: VmResource[]; jobs: string[] }[] = [];
  const resized: { vm: string; cpu: number; memGb: number }[] = [];
  const grown: { vm: string; disk: string; gb: number }[] = [];
  const removeVm = (component: string, vm: VmResource) => {
    changes.push({
      kind: "vm_remove",
      component,
      vm: vm.name,
      title: `Delete ${vm.name}`,
      detail: "With its disks, address and DNS records.",
      destructive: true,
    });
    delta.vcpu -= vm.vm.cpu_count;
    delta.memory_gb -= vm.vm.memory_mb / 1024;
    delta.storage_gb -= vm.vm.disks.reduce((s, x) => s + x.capacity_gb, 0);
    delta.vm_count -= 1;
  };

  for (const c of componentsOf(item, inputs, vmCount)) {
    const old = had.get(c.key);
    const vms = old ? vmsOf(d, item, old) : [];
    const kept = vms.slice(0, c.count);
    const prefix = c.key === "vm" ? "" : `${c.title}: `;
    const refuse = (setting: string, reason: string) =>
      refused.push({ component: c.key, setting, reason: prefix + reason });
    if (old && kept.length) {
      for (const setting of ["cluster", "datastore", "network"] as const) {
        const [was, is] = [text(before[setting]), text(inputs[setting])];
        if (was === is) continue;
        // Never recorded, as for a VM adopted: nothing moves where every VM is already there.
        if (was === "" && vms.every((v) => placed(v, setting) === is)) continue;
        refuse(
          setting,
          setting === "network"
            ? `The network changes from ${was || "none"} to ${is || "none"}: the first adapter carries the VMs' addresses and DNS records, which rvav doesn't move.`
            : `The ${setting} changes from ${was || "none"} to ${is || "none"}: rvav doesn't move existing VMs.`,
        );
      }
      const [o, n] = [sizes[old.size], sizes[c.size]];
      if (n.diskGb < o.diskGb)
        refuse("disk_gb", `The first disk shrinks from ${o.diskGb} to ${n.diskGb} GB: vCenter can't shrink a disk.`);
      for (const vm of kept) {
        // Only the disk rvav recorded as the blueprint's first grows, as on the server.
        const disk = vm.vm.disks.find((x) => x.blueprint_disk === 0);
        if (n.diskGb > o.diskGb && !disk)
          refuse(
            "disk_gb",
            `${vm.name} was onboarded or built before rvav recorded which of its disks are the blueprint's, so rvav can't tell which disk to grow. Change its disks with the day-2 actions instead.`,
          );
        else if (n.diskGb > o.diskGb && disk && disk.capacity_gb < n.diskGb) {
          if ((vm.vm.snapshot_count ?? 0) > 0)
            refuse(
              "disks",
              `${vm.name} has snapshots, and vCenter can't grow the disks of a VM with snapshots. Delete them first.`,
            );
          else {
            changes.push({
              kind: "disk_extend",
              component: c.key,
              vm: vm.name,
              title: `Grow ${disk.label} of ${vm.name} to ${n.diskGb} GB`,
              detail: `Was ${disk.capacity_gb} GB.`,
              destructive: false,
            });
            grown.push({ vm: vm.name, disk: disk.label, gb: n.diskGb });
            delta.storage_gb += n.diskGb - disk.capacity_gb;
          }
        }
        const memGb = vm.vm.memory_mb / 1024;
        if ((o.vcpu !== n.vcpu || o.memoryGb !== n.memoryGb) && (vm.vm.cpu_count !== n.vcpu || memGb !== n.memoryGb)) {
          const cold = n.vcpu < vm.vm.cpu_count || n.memoryGb < memGb;
          changes.push({
            kind: "resize",
            component: c.key,
            vm: vm.name,
            title: `Set ${n.vcpu} vCPU and ${n.memoryGb} GB on ${vm.name}`,
            detail: `Was ${vm.vm.cpu_count} vCPU and ${memGb} GB.${cold && vm.vm.power_state === "on" ? " Restarts the VM: removing CPUs or memory needs it powered off." : ""}`,
            destructive: false,
          });
          resized.push({ vm: vm.name, cpu: n.vcpu, memGb: n.memoryGb });
          delta.vcpu += n.vcpu - vm.vm.cpu_count;
          delta.memory_gb += n.memoryGb - memGb;
        }
      }
    }
    const add = Math.max(0, c.count - vms.length);
    const size = sizes[c.size];
    for (let i = 0; i < add; i++)
      changes.push({
        kind: "vm_add",
        component: c.key,
        title: c.key === "vm" ? "Build the deployment's VM again" : `Add a VM to ${c.title}`,
        detail: `VM ${vms.length + i + 1} of ${c.count}: ${size.vcpu} vCPU, ${size.memoryGb} GB of memory, ${size.diskGb} GB of storage, named when the update is asked for.`,
        destructive: false,
      });
    delta.vcpu += add * size.vcpu;
    delta.memory_gb += add * size.memoryGb;
    delta.storage_gb += add * size.diskGb;
    delta.vm_count += add;
    const remove = vms.slice(c.count);
    for (const vm of remove) removeVm(c.key, vm);
    const jobs = c.post.filter((j) => failed || !old?.post.includes(j));
    if (kept.length)
      for (const j of jobs)
        changes.push({
          kind: "job",
          component: c.key,
          title: `Run the AWX job ${j}`,
          detail: `On ${kept.map((v) => v.name).join(", ")}.`,
          destructive: false,
        });
    builds.push({ c, vms: kept, add, remove, jobs: kept.length ? jobs : [] });
  }
  for (const [key, old] of had) {
    if (builds.some((b) => b.c.key === key)) continue;
    const remove = vmsOf(d, item, old);
    for (const vm of remove) removeVm(key, vm);
    builds.push({ c: { ...old, count: 0 }, vms: [], add: 0, remove, jobs: [] });
  }
  // As the server lists them: each kept VM's changes in place, then the VMs added, those removed, the jobs.
  const group: Partial<Record<UpdateChange["kind"], number>> = { vm_add: 1, vm_remove: 2, job: 3 };
  changes.sort((a, b) => (group[a.kind] ?? 0) - (group[b.kind] ?? 0));

  const q = db.quota[project.id];
  const quota: QuotaImpact[] = q
    ? (Object.keys(delta) as QuotaResource[])
        .filter((r) => delta[r] !== 0)
        .map((resource) => {
          const limit = q.limits[resource];
          const used = q.used[resource];
          return {
            resource,
            level: "project",
            limit,
            used,
            adding: delta[resource],
            exceeds: limit !== null && delta[resource] > 0 && used + delta[resource] > limit,
            enforcement: "hard",
          };
        })
    : [];
  for (const l of quota)
    if (l.exceeds)
      errors.push({
        field: "",
        code: "quota_exceeded",
        message: `This update needs ${l.adding} more ${l.resource.replace("_", " ")} than ${project.name} has left.`,
      });
  const levels = userQuotaCheck(
    db,
    { project, owner: d.owner, you: d.owner.id === db.me.user.id },
    "This update",
    delta,
  );
  quota.push(...levels.lines);
  for (const message of levels.hard) errors.push({ field: "", code: "quota_exceeded", message });
  // As the server's updateLimits: the deployment's size after, held to a limit only when it grows.
  const now = d.resources.reduce(
    (n, r) => (r.type === "vsphere.vm" && "vm" in r ? { vcpu: n.vcpu + r.vm.cpu_count, memory: n.memory + Math.round(r.vm.memory_mb / 1024) } : n), // prettier-ignore
    { vcpu: 0, memory: 0 },
  );
  const limits = limitCheck(
    db,
    {
      project,
      env: db.environments.find((e) => e.key === d.environment) ?? { key: d.environment, name: d.environment },
      owner: d.owner,
      you: d.owner.id === db.me.user.id,
    },
    { toOwner: 0, toProject: 0, vcpu: now.vcpu + delta.vcpu, memory: now.memory + delta.memory_gb, addVcpu: delta.vcpu, addMemory: delta.memory_gb }, // prettier-ignore
  );
  for (const message of limits.hard) errors.push({ field: "", code: "limit_exceeded", message });

  const network = text(inputs.network);
  const ops = (names: (c: Component, n: number) => string[]): Op[] => {
    const out: Op[] = [];
    for (const g of grown)
      out.push({
        key: `vsphere.disk_extend@${g.vm}`,
        title: `Grow ${g.disk} to ${g.gb} GB`,
        system: "vcenter",
        apply: (dep) => {
          const disk = findVm(dep, g.vm)?.vm.disks.find((x) => x.blueprint_disk === 0);
          if (disk) disk.capacity_gb = g.gb;
        },
      });
    for (const r of resized)
      out.push({
        key: `vsphere.reconfigure@${r.vm}`,
        title: `Resize to ${r.cpu} vCPU and ${r.memGb} GB`,
        system: "vcenter",
        apply: (dep) => {
          const vm = findVm(dep, r.vm);
          if (vm) Object.assign(vm.vm, { cpu_count: r.cpu, memory_mb: r.memGb * 1024 });
        },
      });
    for (const b of builds) for (const vm of b.remove) out.push(...removeOps(vm));
    for (const b of builds) {
      const template = b.vms[0] ?? vmsOf(d, item, b.c)[0];
      for (const name of names(b.c, b.add)) out.push(...addOps(db, name, network, b.c, template));
    }
    for (const b of builds)
      for (const j of b.jobs)
        out.push({
          key: `awx.job:${j}@${b.c.key}`,
          title: `Run ${j}`,
          detail: `limit=${b.vms.map((v) => v.name).join(",")}`,
          system: "awx",
          apply: () => undefined,
        });
    return out;
  };

  const reason = unavailable(db, d);
  // A failed deployment's update with its own inputs is a repair: its jobs run again.
  const same = !failed && draft.version_id === d.version.id && stable(before) === stable(inputs);
  const warnings = reason ? [] : [...actionWarnings(db, "update", d), ...limits.soft];
  return {
    item,
    version: target,
    inputs,
    same,
    delta,
    ops,
    preview: {
      from_version: d.version,
      to_version: target,
      allowed: reason === "",
      unchanged: same,
      ...(reason ? { reason } : {}),
      changes,
      refused,
      fields,
      errors,
      quota,
      ...(limits.lines.length ? { limits: limits.lines } : {}),
      approval:
        delta.vm_count > 0 || delta.vcpu > 0 || delta.memory_gb > 0
          ? approvalFor(db, item, d.environment, inputs)
          : { required: false, policies: [], typical_wait_seconds: null },
      ...(warnings.length ? { warnings } : {}),
    },
  };
}

function removeOps(vm: VmResource): Op[] {
  const ips = vm.vm.nics.flatMap((n) => n.ip_addresses ?? []);
  return [
    {
      key: `vsphere.destroy@${vm.name}`,
      title: `Delete VM ${vm.name}`,
      system: "vcenter",
      apply: (dep) => {
        dep.resources = dep.resources.filter((r) => r.id !== vm.id);
      },
    },
    {
      key: `dns.delete@${vm.name}`,
      title: `Delete the DNS records of ${vm.name}`,
      system: "efficientip",
      apply: (dep) => {
        dep.resources = dep.resources.filter((r) => r.type !== "dns.record" || !r.name.startsWith(`${vm.name}.`));
      },
    },
    {
      key: `ipam.release@${vm.name}`,
      title: `Release the IP addresses of ${vm.name}`,
      system: "efficientip",
      apply: (dep) => {
        dep.resources = dep.resources.filter((r) => r.type !== "ipam.address" || !ips.includes(r.name));
      },
    },
  ];
}

function addOps(db: Db, name: string, network: string, c: Component, template?: VmResource): Op[] {
  let ip = "";
  const net = network || template?.vm.nics[0]?.network || "";
  const fqdn = `${name}.${DOMAIN}`;
  const size = sizes[c.size];
  return [
    {
      key: `ipam.reserve@${name}`,
      title: `Reserve an IP address for ${name}`,
      system: "efficientip",
      apply: (dep) => {
        const next = reserveIp(db, net);
        if (!next) return;
        ip = next.ip;
        dep.resources.push({
          id: `res_${Math.random().toString(36).slice(2, 10)}`,
          type: "ipam.address",
          name: ip,
          sync_state: "in_sync",
          address: ip,
          subnet: next.subnet,
          efficientip_id: String(Math.floor(Math.random() * 100000) + 900000),
        } satisfies IpAddressResource);
      },
    },
    {
      key: `vsphere.clone@${name}`,
      title: `Clone ${name}`,
      system: "vcenter",
      apply: (dep) => {
        const moref = `vm-${Math.floor(Math.random() * 9000) + 1000}`;
        dep.resources.push({
          id: `res_${Math.random().toString(36).slice(2, 10)}`,
          type: "vsphere.vm",
          name,
          sync_state: "in_sync",
          vm: {
            ...(template?.vm ?? {
              vcenter: "vcenter.corp.example",
              guest_os: "Red Hat Enterprise Linux 9.4",
              template: "tpl-rhel9.4-2026.10",
            }),
            moref,
            instance_uuid: crypto.randomUUID(),
            power_state: "on",
            guest_state: "running",
            cpu_count: size.vcpu,
            memory_mb: size.memoryGb * 1024,
            disks: [
              {
                label: "Hard disk 1",
                key: 2000,
                blueprint_disk: 0,
                capacity_gb: size.diskGb,
                datastore: template?.vm.disks[0]?.datastore,
              },
            ],
            nics: [{ network: net, mac: "00:50:56:a1:7e:3c", ip_addresses: ip ? [ip] : [], dhcp: false }],
            fqdn,
            snapshot_count: 0,
            snapshots: [],
          },
        });
      },
    },
    {
      key: `dns.create@${name}`,
      title: `Create the DNS record of ${name}`,
      system: "efficientip",
      apply: (dep) => {
        dep.resources.push({
          id: `res_${Math.random().toString(36).slice(2, 10)}`,
          type: "dns.record",
          name: fqdn,
          sync_state: "in_sync",
          fqdn,
          record_type: "A",
          value: ip,
          zone: DOMAIN,
        } satisfies DnsRecordResource);
      },
    },
  ];
}

/** What POST /deployments/{id}/update/preview answers. */
export function previewUpdate(
  db: Db,
  d: DeploymentDetail,
  draft: UpdateDraft,
): { preview?: UpdatePreview; error?: { status: number; title: string } } {
  const p = plan(db, d, draft);
  return "preview" in p ? { preview: p.preview } : { error: p };
}

// The net change each running update makes to its project's quota, by request ID.
const deltas = new Map<string, Record<QuotaResource, number>>();

/** Plan an update and build its request, as POST /deployments/{id}/update does. */
export function runUpdate(db: Db, d: DeploymentDetail, draft: UpdateDraft, number: string): RunResult {
  const p = plan(db, d, draft);
  if (!("preview" in p)) return { error: p };
  const { preview } = p;
  if (!preview.allowed) return { error: { status: 409, title: preview.reason ?? "Not available." } };
  const problems: FieldError[] = preview.errors.length
    ? preview.errors
    : preview.refused.length
      ? preview.refused.map((r) => ({ field: "", code: "refused", message: r.reason }))
      : p.same
        ? [{ field: "", code: "no_change", message: `${d.name} already has this version and these inputs.` }]
        : [];
  if (problems.length) return { error: { status: 422, title: "Some values need fixing", errors: problems } };
  const warned = unaccepted(preview.warnings ?? [], draft);
  if (warned.length) return { error: { status: 422, title: "Some values need fixing", errors: warned } };

  const approval = preview.approval.required ? preview.approval : undefined;
  const ops = p.ops((c, n) => nextNames(db, d, vmsOf(d, p.item, c), c, n));
  const steps = requestSteps(ops, Boolean(approval), `No policy asks for approval of this update in ${d.environment}`);
  const request: ServiceRequest = {
    id: `req_${number.slice(4)}`,
    number,
    kind: "update",
    action: { key: "update", name: "Update" },
    deployment_id: d.id,
    title: `Update · ${d.name}`,
    status: approval ? "pending_approval" : "running",
    catalog_item: { ...d.catalog_item, type: p.item.type },
    version: p.version,
    project: d.project,
    environment: d.environment,
    requester: db.me.user,
    inputs: p.inputs,
    count: 1,
    justification: draft.justification,
    submitted_at: now(),
    scheduled_at: null,
    started_at: approval ? null : now(),
    finished_at: null,
    deployment_ids: [d.id],
    current_step: null,
    progress: { done: 0, total: steps.length },
    ...(preview.warnings?.length ? { warnings: preview.warnings } : {}),
    error: null,
  };
  deltas.set(request.id, p.delta);
  if (!approval) d.status = "updating";
  return { stored: { request, steps, logs: {} }, approval };
}

/** The deployment once its update is done: the version, the inputs and the quota it now uses. */
export function updateFinished(db: Db, d: DeploymentDetail, r: ServiceRequest) {
  d.version = r.version;
  d.inputs = r.inputs ?? d.inputs;
  const vms = d.resources.filter((x): x is VmResource => x.type === "vsphere.vm");
  d.vm_count = vms.length;
  d.vms_powered_on = vms.filter((v) => v.vm.power_state === "on").length;
  const q = db.quota[d.project.id];
  const delta = deltas.get(r.id);
  if (q && delta) for (const k of Object.keys(delta) as QuotaResource[]) q.used[k] += delta[k];
  deltas.delete(r.id);
}
