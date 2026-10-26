// In-memory stand-in for the rvav API. Async functions mirror the REST
// endpoints; `events` mirrors the SSE stream (/api/v1/events/stream).
import type {
  Deployment,
  Env,
  PowerState,
  ServiceRequest,
  TimelineStep,
  VM,
} from "@/lib/types";
import * as db from "./data";

const latency = () => new Promise((r) => setTimeout(r, 120 + Math.random() * 220));
const clone = <T>(v: T): T => structuredClone(v);
const nowIso = () => new Date().toISOString();

export type LiveEvent =
  | { type: "request.updated"; id: string }
  | { type: "deployment.updated"; id: string }
  | { type: "approval.changed" };

type Listener = (e: LiveEvent) => void;
const listeners = new Set<Listener>();
export const events = {
  subscribe(fn: Listener) {
    listeners.add(fn);
    return () => listeners.delete(fn);
  },
  emit(e: LiveEvent) {
    listeners.forEach((fn) => fn(e));
  },
};

// ── reads ──────────────────────────────────────────────────────────────────
export async function getProjects() {
  await latency();
  return clone(db.projects);
}
export async function getCatalog() {
  await latency();
  return clone(db.catalog);
}
export async function getCatalogItem(slug: string) {
  await latency();
  const item = db.catalog.find((c) => c.slug === slug);
  if (!item) throw new Error(`Catalog item "${slug}" not found`);
  return clone(item);
}
export async function getDeployments() {
  await latency();
  return clone(db.deployments);
}
export async function getDeployment(id: string) {
  await latency();
  const d = db.deployments.find((x) => x.id === id);
  if (!d) throw new Error(`Deployment ${id} not found`);
  return clone(d);
}
export async function getRequests() {
  await latency();
  return clone(db.requests);
}
export async function getRequest(id: string) {
  await latency();
  const r = db.requests.find((x) => x.id === id);
  if (!r) throw new Error(`Request ${id} not found`);
  return clone(r);
}
export async function getApprovals() {
  await latency();
  return clone(db.approvals);
}
export async function getAudit() {
  await latency();
  return clone(db.audit);
}

// Form value sources (vCenter inventory cache, EfficientIP).
export async function getClusters(env: Env) {
  await latency();
  return db.clusters.filter((c) => c.env === env);
}
export async function getDatastores(cluster: string) {
  await new Promise((r) => setTimeout(r, 450));
  return db.datastores[cluster] ?? [];
}
export async function getNetworks(env: Env) {
  await latency();
  return db.networks.filter((n) => n.env === env);
}
export async function getNextName(project: string, env: Env, role: string) {
  await latency();
  const taken = db.deployments.flatMap((d) => d.vms.map((v) => v.name));
  const base = `${project}-${env}-${role || "app"}`;
  let n = 1;
  while (taken.includes(`${base}${String(n).padStart(3, "0")}`)) n++;
  return `${base}${String(n).padStart(3, "0")}`;
}
export async function getFreeIp(network: string) {
  await new Promise((r) => setTimeout(r, 380));
  const net = db.networks.find((n) => n.name === network);
  if (!net) return null;
  const prefix = net.subnet.split(".").slice(0, 3).join(".");
  return { ip: `${prefix}.${57 + (network.length % 40)}`, subnet: net.subnet, free: net.free };
}

// ── writes ─────────────────────────────────────────────────────────────────
export interface VmRequestInput {
  item: string;
  project: string;
  env: Env;
  role: string;
  name: string;
  vcpu: number;
  ramGB: number;
  diskGB: number;
  cluster: string;
  datastore: string;
  network: string;
  ip: string;
  postConfig: string[];
  leaseDays: number | null;
  justification: string;
}

let reqSeq = 10483;
let depSeq = 0xa100;

export function needsApproval(i: Pick<VmRequestInput, "env" | "ramGB">) {
  if (i.env === "prd" && i.ramGB > 32) return "Production VM above 32 GB RAM";
  if (i.env === "prd") return "Production VMs need approval";
  return null;
}

export async function submitVmRequest(input: VmRequestInput) {
  await latency();
  const item = db.catalog.find((c) => c.slug === input.item)!;
  const template = item.slug === "ubuntu-server" ? db.templates[1] : item.slug === "windows-server" ? db.templates[2] : db.templates[0];
  const depId = `dep-${(depSeq++).toString(16)}`;
  const reqId = `REQ-${reqSeq++}`;
  const approval = needsApproval(input);

  const vm: VM = {
    moref: "",
    name: input.name,
    fqdn: `${input.name}.${db.DOMAIN}`,
    ip: input.ip,
    power: "off",
    os: template.os,
    vcpu: input.vcpu,
    ramGB: input.ramGB,
    diskGB: input.diskGB,
    cluster: input.cluster,
    datastore: input.datastore,
    network: input.network,
    template: template.name,
    tools: "notRunning",
    cpuPct: 0,
    memPct: 0,
    snapshots: [],
  };
  const dep: Deployment = {
    id: depId,
    name: input.name.replace(/\d+$/, ""),
    item: item.slug,
    itemVersion: item.version,
    project: input.project,
    env: input.env,
    owner: db.currentUser.id,
    status: "provisioning",
    created: nowIso(),
    leaseExpires: input.leaseDays ? new Date(Date.now() + input.leaseDays * 86_400_000).toISOString() : null,
    vms: [vm],
    inputs: {
      environment: input.env,
      role: input.role,
      vcpu: input.vcpu,
      memoryGB: input.ramGB,
      diskGB: input.diskGB,
      postConfig: input.postConfig.join(", ") || "none",
    },
  };
  db.deployments.unshift(dep);

  const req: ServiceRequest = {
    id: reqId,
    item: item.slug,
    itemVersion: item.version,
    title: `${item.name} · ${input.name}`,
    project: input.project,
    env: input.env,
    requester: db.currentUser.id,
    submitted: nowIso(),
    status: approval ? "pending_approval" : "running",
    deployment: depId,
    steps: buildSteps(input, template.name, approval),
    awxJob: input.postConfig.length ? { id: 18240 + (reqSeq % 50), template: input.postConfig[0]!, log: [] } : undefined,
  };
  db.requests.unshift(req);

  if (approval) {
    db.approvals.unshift({
      id: `APR-${300 + reqSeq - 10470}`,
      request: reqId,
      reason: approval,
      rule: input.ramGB > 32 ? 'inputs.environment == "prd" && inputs.memoryGB > 32' : 'inputs.environment == "prd"',
      level: "Infrastructure leads · 1 of 1",
      due: new Date(Date.now() + 86_400_000).toISOString(),
      summary: [
        { label: "Project", value: db.projects.find((p) => p.id === input.project)?.name ?? input.project },
        { label: "Size", value: `${input.vcpu} vCPU / ${input.ramGB} GB / ${input.diskGB} GB` },
        { label: "Cluster", value: input.cluster },
        { label: "Network", value: input.network },
      ],
    });
    events.emit({ type: "approval.changed" });
  } else {
    runProvisioning(reqId);
  }
  return { request: reqId, deployment: depId };
}

function buildSteps(i: VmRequestInput, template: string, approval: string | null): TimelineStep[] {
  const subnet = db.networks.find((n) => n.name === i.network)?.subnet ?? "";
  const steps: TimelineStep[] = [
    { key: "submit", title: "Submitted", status: "done", started: nowIso(), finished: nowIso() },
    approval
      ? { key: "policy", title: "Waiting for approval", detail: `${approval} · Infrastructure leads`, system: "Policy", status: "running", started: nowIso() }
      : { key: "policy", title: "Approval not required", detail: `environment = ${i.env}`, system: "Policy", status: "done", started: nowIso(), finished: nowIso() },
    { key: "ip", title: `Reserve ${i.ip}`, detail: `Subnet ${subnet}`, system: "EfficientIP", status: "pending" },
    { key: "dns", title: "Create A and PTR records", detail: `${i.name}.${db.DOMAIN}`, system: "EfficientIP", status: "pending" },
    { key: "clone", title: `Clone from ${template}`, detail: `${i.cluster} · ${i.datastore}`, system: "vCenter", status: "pending" },
    { key: "custom", title: "Apply guest customization", detail: "cloud-init · static IP, hostname, SSH keys", system: "vCenter", status: "pending" },
    { key: "power", title: "Power on and wait for guest IP", system: "vCenter", status: "pending" },
  ];
  if (i.postConfig.length) {
    steps.push({ key: "inv", title: "Refresh AWX inventory “rvav”", system: "AWX", status: "pending" });
    for (const t of i.postConfig) {
      steps.push({
        key: `awx-${t}`,
        title: `AWX job · ${t}`,
        system: "AWX",
        status: "pending",
        children: (awxTasks[t] ?? ["Gathering Facts", `${t} : apply`]).map((title, n) => ({
          key: `${t}-${n}`,
          title,
          status: "pending" as const,
        })),
      });
    }
  }
  steps.push({ key: "ready", title: "Ready", status: "pending" });
  return steps;
}

const awxTasks: Record<string, string[]> = {
  "linux-baseline": [
    "Gathering Facts",
    "baseline : configure chrony",
    "baseline : harden sshd",
    "baseline : set journald retention",
    "baseline : register with Satellite",
  ],
  "idm-join": ["Gathering Facts", "idm : install ipa-client", "idm : join realm", "idm : apply HBAC rules"],
  "monitoring-agent": ["Gathering Facts", "monitoring : install node_exporter", "monitoring : open port 9100", "monitoring : register target"],
};

export function runProvisioning(reqId: string) {
  const tick = () => {
    const req = db.requests.find((r) => r.id === reqId);
    if (!req || req.status !== "running") return;
    const dep = db.deployments.find((d) => d.id === req.deployment);
    const i = req.steps.findIndex((s) => s.status === "running" || s.status === "pending");
    const step = req.steps[i];
    if (!step) return;

    if (step.status === "pending") {
      step.status = "running";
      step.started = nowIso();
    } else if (step.children?.some((c) => c.status !== "done")) {
      const c = step.children.find((x) => x.status !== "done")!;
      if (c.status === "pending") c.status = "running";
      else {
        c.status = "done";
        req.awxJob?.log.push(
          `TASK [${c.title}] ${"*".repeat(Math.max(4, 70 - c.title.length))}`,
          c.title === "Gathering Facts" ? `ok: [${dep?.vms[0]?.name}]` : `changed: [${dep?.vms[0]?.name}]`,
          "",
        );
      }
    } else {
      step.status = "done";
      step.finished = nowIso();
      if (step.key === "ip" || step.key === "dns") step.title = step.title.replace(/^Reserve/, "Reserved").replace(/^Create/, "Created");
      if (step.key === "clone" && dep?.vms[0]) {
        dep.vms[0].moref = `vm-${48900 + (reqId.length * 7) % 90}`;
        step.title = step.title.replace(/^Clone/, "Cloned");
      }
      if (step.key === "power" && dep?.vms[0]) {
        Object.assign(dep.vms[0], { power: "on" as PowerState, tools: "running" as const, cpuPct: 21, memPct: 18 });
        step.title = `Powered on, guest IP ${dep.vms[0].ip}`;
        events.emit({ type: "deployment.updated", id: dep.id });
      }
      if (step.key.startsWith("awx-") && req.awxJob) {
        req.awxJob.log.push(
          "PLAY RECAP *********************************************************************",
          `${dep?.vms[0]?.name}          : ok=${step.children?.length ?? 1}    changed=${(step.children?.length ?? 1) - 1}    unreachable=0    failed=0`,
          "",
        );
      }
      if (step.key === "ready") {
        req.status = "succeeded";
        if (dep) dep.status = "ready";
        if (dep) events.emit({ type: "deployment.updated", id: dep.id });
      }
    }
    if (step.key.startsWith("awx-") && step.status === "running" && req.awxJob && !step.children?.some((c) => c.status !== "pending")) {
      req.awxJob.log.push(`PLAY [${step.title.replace("AWX job · ", "")}] ${"*".repeat(58)}`, "");
    }
    events.emit({ type: "request.updated", id: reqId });
    if (req.status === "running") setTimeout(tick, step.children ? 700 : 1100);
  };
  setTimeout(tick, 600);
}

export async function decideApproval(id: string, decision: "approve" | "reject", comment: string) {
  await latency();
  const a = db.approvals.find((x) => x.id === id);
  if (!a) throw new Error("Approval not found");
  db.approvals.splice(db.approvals.indexOf(a), 1);
  const req = db.requests.find((r) => r.id === a.request);
  if (req) {
    const step = req.steps.find((s) => s.key === "policy");
    if (step) {
      step.status = decision === "approve" ? "done" : "failed";
      step.finished = nowIso();
      step.title = decision === "approve" ? `Approved by ${db.currentUser.name}` : `Rejected by ${db.currentUser.name}`;
      if (comment) step.detail = `“${comment}”`;
    }
    req.status = decision === "approve" ? "running" : "rejected";
    if (decision === "approve") runProvisioning(req.id);
    events.emit({ type: "request.updated", id: req.id });
  }
  events.emit({ type: "approval.changed" });
}

export async function setPower(depId: string, moref: string, power: PowerState) {
  await latency();
  const vm = db.deployments.find((d) => d.id === depId)?.vms.find((v) => v.moref === moref);
  if (!vm) throw new Error("VM not found");
  vm.power = power;
  vm.tools = power === "on" ? "running" : "notRunning";
  vm.cpuPct = power === "on" ? 14 : 0;
  vm.memPct = power === "on" ? 22 : 0;
  events.emit({ type: "deployment.updated", id: depId });
}

export async function createSnapshot(depId: string, moref: string, name: string) {
  await new Promise((r) => setTimeout(r, 900));
  const vm = db.deployments.find((d) => d.id === depId)?.vms.find((v) => v.moref === moref);
  if (!vm) throw new Error("VM not found");
  vm.snapshots.unshift({ name, created: nowIso(), sizeGB: 0.1 });
  events.emit({ type: "deployment.updated", id: depId });
}

export async function extendLease(depId: string, days: number) {
  await latency();
  const d = db.deployments.find((x) => x.id === depId);
  if (!d || !d.leaseExpires) throw new Error("This deployment has no lease");
  d.leaseExpires = new Date(new Date(d.leaseExpires).getTime() + days * 86_400_000).toISOString();
  events.emit({ type: "deployment.updated", id: depId });
}
