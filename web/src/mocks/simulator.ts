// Simulates the Temporal provisioning workflow: steps advance on a timer,
// resources appear in the deployment, and every change is published as an event.
import type {
  ApprovalQuestionField,
  DeploymentDetail,
  DnsRecordResource,
  DrsRuleResource,
  EnvironmentKey,
  IpAddressResource,
  LiveEvent,
  LogLine,
  Project,
  Resource,
  Step,
  VmResource,
} from "@/lib/api/types";
import { goDuration, parseUrl } from "@/lib/composite";
import { maskLiterals } from "./blueprints";
import type { StoredApproval } from "./approvals";
import { completeDay2Step, day2Finished } from "./day2";
import { publishedBlueprint, secretInputsOf } from "./catalogAdmin";
import { celBool, celEval } from "./cel";
import { bus } from "./events";
import { clusters, DOMAIN, networks, sizes, templates, VCENTER, type SizeKey } from "./inventory";
import { multiName, multiParts } from "./components";
import { reserveIp, summarize, vmNames } from "./logic";
import { db, type StoredItem, type StoredRequest } from "./seed";

export const timing = { tickMs: 900 };
const timers = new Map<string, ReturnType<typeof setTimeout>>();
/** The VM name chosen when the request was planned, by request ID. */
const hosts = new Map<string, string>();
/** The address reserved for each VM of a multi-VM deployment, by name. */
const ips = new Map<string, string>();
/** What each step that waits for a person asks, by step ID. */
const asks = new Map<string, Rec>();
/**
 * What each step that waits for a time or a condition says, by step ID, and how
 * far it has got. A tick counts as a minute of its wait: `for: 72h` stays until
 * cancelled, `for: 2m` is over in two ticks. A poll's condition holds at its
 * third check: `every: 1m` gets there in two ticks, `every: 6h` stays a good
 * while, and a `timeout` of 1m gets to its deadline first.
 */
const waits = new Map<
  string,
  { wait?: Rec; poll?: Rec; minutes: number; last: number; checks: number; left: number }
>();

const now = () => new Date().toISOString();
const text = (v: unknown, fallback = "") => (typeof v === "string" ? v : fallback);
const newId = (prefix: string) => `${prefix}_${(db.seq.id++).toString(36)}`;

const awxTasks: Record<string, string[]> = {
  "linux-baseline": [
    "Gathering Facts",
    "baseline : configure chrony",
    "baseline : harden sshd",
    "baseline : set journald retention",
    "baseline : register with Satellite",
  ],
  "idm-join": ["Gathering Facts", "idm : install ipa-client", "idm : join realm", "idm : apply HBAC rules"],
  "monitoring-agent": [
    "Gathering Facts",
    "monitoring : install node_exporter",
    "monitoring : open port 9100",
    "monitoring : register target",
  ],
  "role-nginx": ["Gathering Facts", "nginx : install packages", "nginx : deploy TLS defaults", "nginx : start service"],
  "windows-baseline": ["Gathering Facts", "baseline : apply CIS GPOs", "baseline : configure WSUS"],
  "ad-join": ["Gathering Facts", "ad : join domain", "ad : move to project OU"],
};

function step(key: string, title: string, system: Step["system"], extra: Partial<Step> = {}): Step {
  return {
    id: newId("stp"),
    key,
    title,
    system,
    status: "pending",
    children: [],
    has_log: false,
    started_at: null,
    finished_at: null,
    ...extra,
  };
}

function awxJob(template: string, host: string, jobId: number): Step {
  return step("awx.job", `AWX job ${jobId} · ${template}`, "awx", {
    detail: `limit=${host}`,
    has_log: true,
    external_ref: {
      kind: "awx_job",
      id: String(jobId),
      url: `https://awx.corp.example/#/jobs/playbook/${jobId}/output`,
    },
    children: (awxTasks[template] ?? ["Gathering Facts", `${template} : run`]).map((t) => step("awx.task", t, "awx")),
  });
}

type Rec = Record<string, unknown>;

/**
 * What a step gives later steps' CEL once it ran (`steps.<key>`): a person's
 * answer, a REST call's status, a branch's case. Other steps give nothing here.
 */
const outputs = new WeakMap<Step, Rec>();

/** What the request's earlier steps give, as a later step reads them: those that ran. */
function stepsOf(r: StoredRequest): Rec {
  return Object.fromEntries(
    r.steps.flatMap((s) => {
      const out = outputs.get(s);
      return s.key.startsWith("step:") && s.status === "done" && out ? [[s.key.slice(5), out]] : [];
    }),
  );
}

type Vars = { inputs: Rec; environment: EnvironmentKey; project: Rec; steps: Rec };

/**
 * A branch's cases and the steps of each side, the last what runs otherwise.
 * It chooses when it runs, from the form and the steps before it (a person's
 * answer), as the server does.
 */
const branches = new WeakMap<Step, { cases: Rec[]; hasElse: boolean; sides: Step[][]; vars: Vars }>();
/** What a message says, filled in when it is sent; its vars hold a secret input as a mark. */
const messages = new WeakMap<Step, { template: string; vars: Vars; requester: boolean }>();

/** A message's text with its values, or why it can't have them (provision.fillMessage). */
function fillMessage(template: string, vars: Rec): { text?: string; error?: string } {
  const { parts, error } = parseUrl(template);
  if (error) return { error: `The message wasn't sent: ${error}.` };
  let filled = "";
  for (const p of parts) {
    if ("text" in p) {
      filled += p.text;
      continue;
    }
    const why = (reason: string) => ({
      error: `The message wasn't sent: its \${${maskLiterals(p.expr, false)}}: ${reason}.`,
    });
    let v: unknown;
    try {
      v = celEval(p.expr, vars);
    } catch {
      return why("it can't be read");
    }
    if (v === null || v === undefined) continue;
    if (v === secretMark) return why("it is a secret input, which a message can't show");
    if (typeof v !== "string" && typeof v !== "number" && typeof v !== "boolean") return why("it isn't a single value");
    filled += String(v);
  }
  return { text: filled };
}

// A secret input, as a message reads it: not its value.
const secretMark = Object.freeze({ secret: true });

/**
 * A composite item's steps, as its published blueprint lists them (ADR-0022):
 * one step:<key> each, the key the canvas gives its node (ADR-0032); one whose
 * condition is false is skipped (read when planned, from the form only).
 * Parallel groups run one after the other here; a branch's sides come after
 * it, flat, as the server's do (ADR-0033).
 */
function compositeSteps(item: StoredItem, project: Project, env: EnvironmentKey, inputs: Rec, jobBase: number): Step[] {
  const bp = publishedBlueprint(db, item);
  const groups = (Array.isArray(bp?.steps) ? bp.steps : []) as Rec[];
  const vars: Vars = {
    inputs,
    environment: env,
    project: { id: project.id, code: project.code, name: project.name },
    steps: {},
  };
  const secrets = secretInputsOf(db, item);
  const messageVars: Vars = {
    ...vars,
    inputs: Object.fromEntries(Object.entries(inputs).map(([k, v]) => [k, secrets.has(k) ? secretMark : v])),
  };
  const out: Step[] = [];
  const plan = (st: Rec) => {
    const key = `step:${text(st.key)}`;
    const awx = st.awx as Rec | undefined;
    const target = db.items.find((i) => i.slug === st.item);
    const title =
      text(st.title) ||
      target?.name ||
      text(awx?.title) ||
      text(awx?.job_template) ||
      text(st.workflow) ||
      text(st.key);
    const when = text(st.when);
    if (when && !celBool(when, vars, true)) {
      out.push(step(key, title, "rvav", { status: "skipped", detail: `Skipped: ${when} is false` }));
      return;
    }
    if (st.branch) {
      const b = st.branch as Rec;
      const cases = (Array.isArray(b.cases) ? b.cases : []) as Rec[];
      const s = step(key, title, "branch", { detail: "Chooses which side runs", branch_case: null });
      out.push(s);
      const sides = [...cases.map((c) => (c.steps ?? []) as Rec[]), (b.else ?? []) as Rec[]].map((side) => {
        const from = out.length;
        side.forEach(plan);
        return out.slice(from);
      });
      branches.set(s, { cases, hasElse: Array.isArray(b.else) && b.else.length > 0, sides, vars });
      return;
    }
    if (st.notify) {
      const n = st.notify as Rec;
      const s = step(key, title, "rvav", { detail: "Sends a message" });
      messages.set(s, { template: text(n.message), vars: messageVars, requester: n.requester === true });
      out.push(s);
      return;
    }
    if (target?.type === "vm_blueprint") {
      out.push(step(key, title, "vcenter", { detail: `${target.name}: its VMs` }));
    } else if (awx || target?.source.kind === "awx") {
      const template = text(awx?.job_template) || target?.source.awx_template_name || target?.slug || "job";
      out.push({ ...awxJob(template, "localhost", jobBase + out.length), key, title: `${title} · ${template}` });
    } else if (st.rest) {
      out.push(step(key, title, "rest", { detail: `REST call · ${text((st.rest as Rec).method)}` }));
    } else if (st.ask) {
      const s = step(key, title, "person", { detail: "Waits for a person" });
      asks.set(s.id, st.ask as Rec);
      out.push(s);
    } else if (st.wait) {
      const wait = st.wait as Rec;
      const s = step(key, title, "wait", {
        detail: text(wait.for) ? `Waits for ${text(wait.for)}` : "Waits until a time",
      });
      waits.set(s.id, { wait, minutes: 0, last: 0, checks: 0, left: 0 });
      out.push(s);
    } else if (st.poll) {
      const poll = st.poll as Rec;
      const s = step(key, title, "wait", {
        detail: `Waits for a condition: ${text((poll.rest as Rec).method)} every ${text(poll.every)}`,
      });
      waits.set(s.id, { poll, minutes: 0, last: 0, checks: 0, left: 0 });
      out.push(s);
    } else {
      out.push(step(key, title, "rvav", { detail: `Custom workflow ${text(st.workflow) || target?.slug || ""}` }));
    }
  };
  groups.flatMap((g) => (Array.isArray(g.parallel) ? (g.parallel as Rec[]) : [g])).forEach(plan);
  return out;
}

/** An AWX job's step: a template's own, or a composite's AWX step (step:<key>). */
const isJob = (s: Step) => s.key === "awx.job" || (s.key.startsWith("step:") && s.system === "awx");

/** Build the step plan for a new request, and the name its VM will get. */
export function planSteps(
  item: StoredItem,
  project: Project,
  env: EnvironmentKey,
  inputs: Record<string, unknown>,
  approval: string | null,
): { steps: Step[]; host: string | null } {
  let host: string | null = null;
  const submitted = step("request.submit", "Submitted", "rvav", {
    status: "done",
    started_at: now(),
    finished_at: now(),
  });
  const policy = approval
    ? step("policy.approval", "Waiting for approval", "policy", {
        status: "running",
        started_at: now(),
        detail: `${approval} · Infrastructure leads`,
      })
    : step("policy.approval", "Approval not required", "policy", {
        status: "done",
        started_at: now(),
        finished_at: now(),
        detail: `No approval policy applies in ${env}`,
      });
  const steps = [submitted, policy];
  const jobBase = 18240 + (db.seq.request % 50) * 3;

  const parts = multiParts(item.slug, inputs);
  if (parts) {
    // One deployment for every component's VMs (ADR-0024); each VM's steps
    // carry its name after "@".
    host = multiName(item.slug, project, env, inputs);
    const subnet = networks.find((n) => n.name === text(inputs.network))?.subnet ?? "";
    for (const p of parts) {
      const names = vmNames(db, project, env, p.role, p.count);
      for (const name of names) {
        steps.push(
          step(`ipam.reserve@${name}`, `Reserve an IP address · ${name}`, "efficientip", {
            detail: p.segment ? `In the new NSX segment ${p.segment}` : `Subnet ${subnet}`,
          }),
          step(`dns.create@${name}`, `Create A and PTR records · ${name}`, "efficientip", {
            detail: `${name}.${DOMAIN}`,
          }),
          step(`vsphere.clone@${name}`, `Clone from ${p.template} · ${name}`, "vcenter", {
            detail: `${text(inputs.cluster)} · ${text(inputs.datastore)}`,
          }),
          step(`vsphere.customize@${name}`, `Apply guest customization · ${name}`, "vcenter"),
          step(`vsphere.power_on@${name}`, `Power on and wait for guest IP · ${name}`, "vcenter"),
        );
      }
      if (p.antiAffinity) {
        steps.push(
          step(`vsphere.drs_rule@${p.key}`, "Keep the VMs on separate hosts", "vcenter", {
            detail: names.join(", "),
          }),
        );
      }
      p.post.forEach((t, i) => steps.push(awxJob(t, names.join(","), jobBase + i)));
    }
  } else if (item.type === "vm_blueprint") {
    const name = vmNames(db, project, env, text(inputs.role, "app"), 1)[0] ?? `${project.code}-${env}-app001`;
    host = name;
    const network = text(inputs.network);
    const subnet = networks.find((n) => n.name === network)?.subnet ?? "";
    const template =
      item.slug === "ubuntu-server"
        ? templates.ubuntu
        : item.slug === "windows-server"
          ? templates.windows
          : templates.rhel9;
    steps.push(
      step("ipam.reserve", "Reserve an IP address", "efficientip", { detail: `Subnet ${subnet}` }),
      step("dns.create", "Create A and PTR records", "efficientip", { detail: `${name}.${DOMAIN}` }),
      step("vsphere.clone", `Clone from ${template.name}`, "vcenter", {
        detail: `${text(inputs.cluster)} · ${text(inputs.datastore)}`,
      }),
      step("vsphere.customize", "Apply guest customization", "vcenter", {
        detail: "cloud-init: hostname, static IP, SSH keys",
      }),
      step("vsphere.power_on", "Power on and wait for the guest IP", "vcenter"),
    );
    const post = Array.isArray(inputs.post_config) ? (inputs.post_config as string[]) : [];
    if (post.length) {
      steps.push(step("awx.inventory_update", "Refresh AWX inventory “rvav”", "awx"));
      post.forEach((t, i) => steps.push(awxJob(t, name, jobBase + i)));
    }
  } else if (item.type === "awx_job_template" || item.type === "awx_workflow") {
    const template = item.source.awx_template_name ?? item.slug;
    const job = awxJob(template, "localhost", jobBase);
    job.children = [
      "Gathering Facts",
      `${template} : validate input`,
      `${template} : apply change`,
      `${template} : verify`,
    ].map((t) => step("awx.task", t, "awx"));
    steps.push(job);
  } else if (item.type === "composite") {
    steps.push(...compositeSteps(item, project, env, inputs, jobBase));
  } else {
    steps.push(step("vsphere.clone", "Provision 3 VMs", "vcenter"), awxJob("patroni-cluster", "pg nodes", jobBase));
  }
  steps.push(step("request.ready", "Ready", "rvav"));
  return { steps, host };
}

export function rememberHost(requestId: string, host: string) {
  hosts.set(requestId, host);
}

// ── Event publishing ──────────────────────────────────────────────────────
function publish(stored: StoredRequest | null, event: Omit<LiveEvent, "id" | "at">, dep?: DeploymentDetail) {
  const full = { ...event, id: bus.nextId(), at: now() } as LiveEvent;
  bus.publish({
    event: full,
    projectId: stored?.request.project.id ?? dep?.project.id,
    requestId: stored?.request.id,
    requestNumber: stored?.request.number,
    deploymentId: dep?.id,
    userId: stored?.request.requester.id,
  });
}

function refreshProgress(r: StoredRequest) {
  const running = r.steps.find((s) => s.status === "running");
  r.request.current_step = running ? { id: running.id, title: running.title } : null;
  r.request.progress = { done: r.steps.filter((s) => s.status === "done").length, total: r.steps.length };
}

export function publishRequest(r: StoredRequest) {
  refreshProgress(r);
  publish(r, { type: "request.updated", data: structuredClone(r.request) } as Omit<LiveEvent, "id" | "at">);
}

function publishStep(r: StoredRequest, s: Step) {
  publish(r, {
    type: "request.step",
    data: { request_id: r.request.id, parent_step_id: null, step: structuredClone(s) },
  } as Omit<LiveEvent, "id" | "at">);
}

function publishDeployment(r: StoredRequest | null, d: DeploymentDetail) {
  publish(r, { type: "deployment.updated", data: summarize(d) } as Omit<LiveEvent, "id" | "at">, d);
}

function publishResource(r: StoredRequest | null, d: DeploymentDetail, res: Resource) {
  publish(
    r,
    { type: "resource.updated", data: { deployment_id: d.id, resource: structuredClone(res) } } as Omit<
      LiveEvent,
      "id" | "at"
    >,
    d,
  );
}

function log(r: StoredRequest, s: Step, lines: [LogLine["kind"], string][]) {
  const existing = (r.logs[s.id] ??= []);
  for (const [kind, text] of lines) existing.push({ n: existing.length, text, kind });
  publish(r, {
    type: "request.log",
    data: { request_id: r.request.id, step_id: s.id, next_offset: existing.length },
  } as Omit<LiveEvent, "id" | "at">);
}

const stars = (title: string) => "*".repeat(Math.max(4, 56 - title.length));

// ── Side effects when a step finishes ──────────────────────────────────────
function vmOf(d: DeploymentDetail) {
  return d.resources.find((x): x is VmResource => x.type === "vsphere.vm");
}

/** Finish a step; one of several VMs' steps keeps saying which VM it was for. */
function complete(r: StoredRequest, s: Step) {
  const message = messages.get(s);
  const filled = message && fillMessage(message.template, { ...message.vars, steps: stepsOf(r) });
  if (filled?.error) return failStep(r, s, filled.error);
  // A branch chooses once, from what the steps before it gave.
  const branch = branches.get(s);
  const taken = branch?.cases.findIndex((c) => celBool(text(c.when), { ...branch.vars, steps: stepsOf(r) }, false));
  completeStep(r, s);
  if (message) {
    s.detail = "Message sent.";
    if (message.requester)
      publish(r, {
        type: "notification",
        data: {
          id: newId("ntf"),
          title: `${r.request.number} · ${r.request.title}`,
          body: filled?.text ?? "",
          level: "info",
          link: `/requests/${r.request.number}`,
        },
      } as Omit<LiveEvent, "id" | "at">);
  }
  // The steps of the sides it left out are skipped: all but the side taken, or but what runs otherwise.
  if (branch && taken !== undefined) {
    s.branch_case = taken + 1;
    s.detail =
      taken >= 0
        ? `Took: ${text(branch.cases[taken]!.title) || `case ${taken + 1}`}`
        : branch.hasElse
          ? "Took: otherwise"
          : "No case held: nothing ran here.";
    outputs.set(s, { case: taken + 1 });
    const runs = branch.sides[taken < 0 ? branch.cases.length : taken];
    for (const other of branch.sides.flat())
      if (!runs?.includes(other) && other.status !== "skipped") {
        Object.assign(other, { status: "skipped", detail: "Its side of the branch wasn't taken." });
        publishStep(r, other);
      }
  }
  // A REST call says what it answered.
  if (s.system === "rest" && s.detail?.startsWith("REST call · ")) {
    const method = s.detail.slice("REST call · ".length);
    const status = method === "POST" ? 201 : 200;
    s.detail = `${method} answered ${status}`;
    outputs.set(s, { status, json: null });
  }
  // A step that isn't a job is done with what it did (a composite's VM step run again).
  for (const c of s.children)
    if (c.status === "pending" || c.status === "running") Object.assign(c, { status: "done", finished_at: now() });
  const vm = s.key.split("@")[1];
  if (vm && !s.key.startsWith("vsphere.drs_rule") && !s.title.includes(vm)) s.title = `${s.title} · ${vm}`;
}

function completeStep(r: StoredRequest, s: Step) {
  const d = db.deployments.find((x) => x.id === r.request.deployment_ids?.[0]);
  const inputs = r.request.inputs ?? {};
  s.status = "done";
  s.finished_at = now();
  if (!d) return;
  if (r.request.kind === "day2") {
    if (completeDay2Step(d, s)) {
      publishDeployment(r, d);
      d.resources.forEach((res) => publishResource(r, d, res));
    }
    return;
  }
  // A multi-VM deployment's steps name their VM (or component) after "@";
  // a composite's VM step clones its VM.
  const [own, at] = s.key.split("@") as [string, string | undefined];
  const key = own.startsWith("step:") && s.system === "vcenter" ? "vsphere.clone" : own;
  const title = s.title;
  const host = at ?? hosts.get(r.request.id) ?? `${d.name}001`;
  const multi = at !== undefined;
  switch (key) {
    case "ipam.reserve": {
      const ip = reserveIp(db, text(inputs.network));
      if (ip) {
        ips.set(host, ip.ip);
        s.title = `Reserved ${ip.ip}`;
        const res: IpAddressResource = {
          id: newId("res"),
          type: "ipam.address",
          name: ip.ip,
          sync_state: "in_sync",
          address: ip.ip,
          subnet: ip.subnet,
          efficientip_id: String(880000 + db.seq.id),
        };
        d.resources.push(res);
        publishResource(r, d, res);
      }
      break;
    }
    case "dns.create": {
      const ip = multi
        ? (ips.get(host) ?? "")
        : (d.resources.find((x): x is IpAddressResource => x.type === "ipam.address")?.address ?? "");
      s.title = "Created A and PTR records";
      const a: DnsRecordResource = {
        id: newId("res"),
        type: "dns.record",
        name: `${host}.${DOMAIN}`,
        sync_state: "in_sync",
        fqdn: `${host}.${DOMAIN}`,
        record_type: "A",
        value: ip,
        zone: DOMAIN,
      };
      d.resources.push(a);
      publishResource(r, d, a);
      break;
    }
    case "vsphere.clone": {
      // Its component: the one whose role is in its name.
      const part = multi
        ? multiParts(r.request.catalog_item.slug, inputs)?.find((p) => host.includes(`-${p.role}`))
        : undefined;
      const component = part?.key ?? "vm";
      const size = sizes[part?.size ?? (inputs.size as SizeKey) ?? "M"] ?? sizes.M;
      const template =
        r.request.catalog_item.slug === "ubuntu-server"
          ? templates.ubuntu
          : r.request.catalog_item.slug === "windows-server"
            ? templates.windows
            : templates.rhel9;
      db.seq.moref += 17;
      const vm: VmResource = {
        id: newId("res"),
        type: "vsphere.vm",
        name: host,
        sync_state: "in_sync",
        vm: {
          moref: `vm-${db.seq.moref}`,
          instance_uuid: `50290000-1c2d-4e5f-8a9b-${String(db.seq.moref).padStart(12, "0")}`,
          vcenter: VCENTER,
          power_state: "off",
          guest_state: "not_running",
          guest_os: template.os,
          cpu_count: size.vcpu,
          memory_mb: size.memoryGb * 1024,
          disks: [{ label: "Hard disk 1", capacity_gb: size.diskGb, datastore: text(inputs.datastore) }],
          nics: [{ network: text(inputs.network), mac: "00:50:56:a1:7e:3c", ip_addresses: [] }],
          fqdn: `${host}.${DOMAIN}`,
          cluster: text(inputs.cluster, clusters[0]!.name),
          host: `esx-gva-${d.environment}-0${multi ? (d.resources.filter((x) => x.type === "vsphere.vm").length % 3) + 1 : 3}.${DOMAIN}`,
          component,
          folder: `/GVA/vm/rvav/${d.project.code}/${d.environment}`,
          template: template.name,
          snapshot_count: 0,
        },
      };
      if (multi) d.resources.push(vm);
      else d.resources.unshift(vm);
      s.title = `Cloned from ${template.name}`;
      s.external_ref = { kind: "vcenter_vm", id: vm.vm.moref };
      publishResource(r, d, vm);
      publishDeployment(r, d);
      break;
    }
    case "vsphere.customize":
      s.title = "Applied guest customization";
      break;
    case "vsphere.power_on": {
      const vm = multi ? d.resources.find((x): x is VmResource => x.type === "vsphere.vm" && x.name === host) : vmOf(d);
      const ip = multi
        ? (ips.get(host) ?? "")
        : (d.resources.find((x): x is IpAddressResource => x.type === "ipam.address")?.address ?? "");
      if (vm) {
        vm.vm.power_state = "on";
        vm.vm.guest_state = "running";
        vm.vm.nics[0]!.ip_addresses = [ip];
        vm.vm.usage = { cpu_pct: 21, memory_pct: 18, sampled_at: now() };
        s.title = `Powered on · guest IP ${ip}`;
        publishResource(r, d, vm);
        publishDeployment(r, d);
      }
      break;
    }
    case "vsphere.drs_rule": {
      const vms = d.resources.filter((x): x is VmResource => x.type === "vsphere.vm" && x.vm.component === host);
      const rule: DrsRuleResource = {
        id: newId("res"),
        type: "vsphere.drs_rule",
        name: `rvav-${d.id}-${host}`,
        sync_state: "in_sync",
        rule: {
          kind: "vm_anti_affinity",
          cluster: vms[0]?.vm.cluster ?? "",
          vcenter: VCENTER,
          component: host,
          vms: vms.map((v) => v.name),
        },
      };
      d.resources.push(rule);
      s.title = `Kept ${rule.rule.vms.join(", ")} on separate hosts`;
      s.detail = `DRS rule ${rule.name} in ${rule.rule.cluster}`;
      publishResource(r, d, rule);
      break;
    }
    case "awx.inventory_update":
      s.title = "Refreshed AWX inventory “rvav”";
      s.detail = `Host ${host} in groups project_${d.project.code}, env_${d.environment}`;
      break;
    case "request.ready":
      d.status = "ready";
      s.detail = d.lease.expires_at ? "Deployment is ready. The lease timer has started." : "Deployment is ready.";
      publishDeployment(r, d);
      break;
  }
  if (own.startsWith("step:")) s.title = title;
}

export function publishApproval(a: StoredApproval) {
  bus.publish({
    event: {
      id: bus.nextId(),
      type: "approval.updated",
      at: now(),
      data: { approval_id: a.id, request_id: a.request.id, status: a.status },
    },
    projectId: a.project.id,
    requestId: a.request.id,
    userIds: [...a.approvers, ...a.delegates].map((u) => u.id),
  });
}

const stepTimeoutAction: Record<string, StoredApproval["on_timeout"]> = {
  continue: "approve",
  escalate: "escalate",
};

/**
 * A step starts waiting for a person: it asks those it names, as an approval
 * row with its form (provision.OpenAsk). A group stands for everyone but the
 * requester here: the mocks have no groups, and nobody answers their own request.
 */
function openAsk(r: StoredRequest, s: Step) {
  const ask = asks.get(s.id) ?? {};
  const requester = r.request.requester;
  const named = (Array.isArray(ask.approvers) ? ask.approvers : []).map(String);
  const approvers = db.users.filter(
    (u) =>
      (ask.group && u.id !== requester.id) ||
      (named.includes(u.id) && u.id !== requester.id) ||
      ((ask.requester || ask.owner) && u.id === requester.id),
  );
  const form = (Array.isArray(ask.form) ? ask.form : []) as ApprovalQuestionField[];
  const ms = goDuration(text(ask.timeout));
  const row: StoredApproval = {
    id: newId("apr"),
    request: { id: r.request.id, number: r.request.number, title: r.request.title, status: r.request.status },
    project: r.request.project,
    environment: r.request.environment,
    requester,
    submitted_at: r.request.submitted_at,
    level: { index: 1, count: 1, name: s.title },
    reasons: [],
    rules: [],
    status: "pending",
    due_at: ms ? new Date(Date.now() + ms).toISOString() : null,
    escalated: false,
    facts: [],
    group: text(ask.group) || null,
    approvers,
    mode: "any",
    votes: [],
    delegates: [],
    on_timeout: ms ? (stepTimeoutAction[text(ask.on_timeout)] ?? "reject") : null,
    escalate_to: null,
    decided_at: null,
    outcome: null,
    step_key: s.key,
    question: { ...(text(ask.message) ? { message: text(ask.message) } : {}), form },
  };
  db.approvals.push(row);
  s.detail = "Waiting for a person";
  publishApproval(row);
  if (approvers.some((u) => u.id === db.me.user.id))
    publish(r, {
      type: "notification",
      data: {
        id: newId("ntf"),
        title: `Your answer is needed: ${s.title}`,
        body: text(ask.message) || `${r.request.number} · ${r.request.title}`,
        level: "warning",
        link: `/requests/${r.request.number}`,
      },
    } as Omit<LiveEvent, "id" | "at">);
}

/** A step's wait was answered: it says so, gives what later steps read of it, and the run goes on. */
export function stepAnswered(r: StoredRequest, key: string, outcome: string, given: Rec) {
  const s = r.steps.find((x) => x.key === key && x.status === "running");
  if (!s) return;
  complete(r, s);
  outputs.set(s, given);
  s.detail = outcome;
  publishStep(r, s);
  schedule(r.request.id);
}

/** A step's wait was rejected: the step fails, and with it the request. A Retry asks again. */
export function stepRejected(r: StoredRequest, key: string, error: string) {
  const s = r.steps.find((x) => x.key === key && x.status === "running");
  if (s) failStep(r, s, error);
}

function failStep(r: StoredRequest, s: Step, error: string) {
  Object.assign(s, { status: "failed", finished_at: now(), error });
  publishStep(r, s);
  Object.assign(r.request, { status: "failed", finished_at: now(), error: { step_id: s.id, message: error } });
  publishRequest(r);
  timers.delete(r.request.id);
}

// What the server reads as a time (provision.timeOf): this has a zone or a date alone, read as UTC in JavaScript too.
const timeText = /^\d{4}-\d{2}-\d{2}(T\d{2}:\d{2}(:\d{2}(\.\d+)?)?(Z|[+-]\d{2}:\d{2}))?$/;

const inMinutes = (ms: number | null) => Math.max(1, Math.ceil((ms ?? 60_000) / 60_000));

/** A step starts to wait for a time or a condition (provision.OpenWait, OpenPoll, with its first CheckPoll). */
function openWait(r: StoredRequest, s: Step) {
  const w = waits.get(s.id);
  if (!w) return;
  Object.assign(w, { minutes: 0, last: 0, checks: 0 });
  if (w.poll) {
    s.due_at = new Date(Date.now() + (goDuration(text(w.poll.timeout)) ?? 0)).toISOString();
    w.left = inMinutes(goDuration(text(w.poll.timeout)));
    checkPoll(s, w);
    return;
  }
  const forMs = goDuration(text(w.wait?.for));
  if (forMs !== null) {
    s.due_at = new Date(Date.now() + forMs).toISOString();
    s.detail = `Waiting for ${text(w.wait?.for)}`;
    w.left = inMinutes(forMs);
    return;
  }
  // Until a time: the one its expression gives, if the mocks can read it (their
  // steps have no outputs, their CEL no timestamps); else in an hour.
  const until = text(w.wait?.until);
  let value: unknown = new Date(Date.now() + 3_600_000).toISOString();
  if (!/\bsteps\./.test(until))
    try {
      value = celEval(until, { inputs: r.request.inputs ?? {} });
    } catch {
      // In an hour.
    }
  const due = typeof value === "string" && timeText.test(value) ? new Date(value) : null;
  if (!due) {
    failStep(
      r,
      s,
      `Its time can't be read: ${maskLiterals(until, false)} doesn't give a time. Ask a catalog admin to check this item.`,
    );
    return;
  }
  s.due_at = due.toISOString();
  const left = due.getTime() - Date.now();
  s.detail = left > 0 ? "Waiting until the time it was given" : "That time had already passed, so it went on at once.";
  w.left = left > 0 ? inMinutes(left) : 0;
}

/** One check of a poll: true when its condition holds, at its third. */
function checkPoll(s: Step, w: NonNullable<ReturnType<typeof waits.get>>) {
  const method = text((w.poll?.rest as Rec | undefined)?.method);
  w.checks++;
  w.last = w.minutes;
  if (w.checks >= 3) {
    s.detail = `Its condition holds: ${method} answered 200.`;
    return true;
  }
  s.detail = `Checks every ${text(w.poll?.every)}. Last check: ${method} answered 200, not yet.`;
  return false;
}

/** A wait's tick: it says whether the step is over. A poll that fails at its deadline ends the request. */
function waitTick(r: StoredRequest, s: Step): boolean {
  const w = waits.get(s.id);
  if (!w) return true;
  w.minutes++;
  if (!w.poll) return w.minutes >= w.left;
  const due = w.minutes >= w.left;
  const every = inMinutes(goDuration(text(w.poll.every)));
  let met = false;
  if (due || w.minutes - w.last >= every) met = checkPoll(s, w);
  if (met) {
    s.due_at = null;
    return true;
  }
  if (!due) return false;
  s.due_at = null;
  const timeout = text(w.poll.timeout);
  if (text(w.poll.on_timeout) !== "continue") {
    failStep(r, s, `Its condition wasn't met within ${timeout}.`);
    return false;
  }
  s.detail = `Its condition wasn't met within ${timeout}, so it went on.`;
  return true;
}

/** One tick: finish the running unit of work and start the next one. */
function tick(requestId: string) {
  const r = db.requests.find((x) => x.request.id === requestId);
  if (!r) return;
  if (r.request.status === "cancelling") return cancelNow(r);
  if (r.request.status !== "running") return;

  const s = r.steps.find((x) => x.status === "running") ?? r.steps.find((x) => x.status === "pending");
  if (!s) return finish(r);
  // It waits for a person: the answer starts it again, as a cancel does.
  if (s.status === "running" && s.system === "person") return;
  // It waits for a time or a condition: a tick is a minute of its wait.
  if (s.status === "running" && s.system === "wait" && !waitTick(r, s)) {
    if (s.status !== "running") return;
    publishStep(r, s);
    return schedule(requestId);
  }

  if (s.status === "pending") {
    s.status = "running";
    s.started_at = now();
    if (s.system === "person") openAsk(r, s);
    if (s.system === "wait") {
      openWait(r, s);
      if (s.status !== "running") return;
    }
    if (isJob(s)) {
      s.children[0]!.status = "running";
      s.children[0]!.started_at = now();
      log(r, s, [
        ["header", `PLAY [${s.title.split(" · ")[1] ?? "job"}] ${stars("PLAY [x]")}`],
        ["plain", ""],
      ]);
    }
  } else if (isJob(s)) {
    const i = s.children.findIndex((c) => c.status === "running");
    const c = s.children[i]!;
    c.status = "done";
    c.finished_at = now();
    const host = s.detail?.replace("limit=", "") ?? "host";
    log(r, s, [
      ["header", `TASK [${c.title}] ${stars(c.title)}`],
      c.title === "Gathering Facts" ? ["ok", `ok: [${host}]`] : ["changed", `changed: [${host}]`],
      ["plain", ""],
    ]);
    const next = s.children[i + 1];
    if (next) {
      next.status = "running";
      next.started_at = now();
    } else {
      const n = s.children.length;
      log(r, s, [
        ["recap", `PLAY RECAP ${stars("PLAY RECAP")}`],
        ["ok", `${host.padEnd(26)} : ok=${n}    changed=${n - 1}    unreachable=0    failed=0    skipped=0`],
      ]);
      complete(r, s);
    }
  } else {
    complete(r, s);
    // A message that can't be sent has ended the request.
    if (s.status === "failed") return;
  }
  publishStep(r, s);

  // Start the following step in the same tick so there is always one running.
  if (s.status === "done") {
    const next = r.steps.find((x) => x.status === "pending");
    if (next) {
      if (next.key === "request.ready") {
        complete(r, next);
        publishStep(r, next);
        return finish(r);
      }
      next.status = "running";
      next.started_at = now();
      if (next.system === "person") openAsk(r, next);
      if (next.system === "wait") {
        openWait(r, next);
        if (next.status !== "running") return;
      }
      if (isJob(next)) {
        next.children[0]!.status = "running";
        next.children[0]!.started_at = now();
        log(r, next, [
          ["header", `PLAY [${next.title.split(" · ")[1] ?? "job"}] ${stars("PLAY [x]")}`],
          ["plain", ""],
        ]);
      }
      publishStep(r, next);
    }
  }
  publishRequest(r);
  schedule(requestId);
}

function finish(r: StoredRequest) {
  r.request.status = "succeeded";
  r.request.finished_at = now();
  publishRequest(r);
  const d = db.deployments.find((x) => x.id === r.request.deployment_ids?.[0]);
  if (r.request.kind === "day2") {
    if (d) {
      day2Finished(db, d, r.request);
      publishDeployment(r, d);
    }
    publish(r, {
      type: "notification",
      data: {
        id: newId("ntf"),
        title: `Done: ${r.request.title}`,
        body: r.request.number,
        level: "success",
        link: `/requests/${r.request.number}`,
      },
    } as Omit<LiveEvent, "id" | "at">);
    timers.delete(r.request.id);
    return;
  }
  const vm = d ? vmOf(d) : undefined;
  const several = (d?.resources.filter((x) => x.type === "vsphere.vm").length ?? 0) > 1;
  publish(r, {
    type: "notification",
    data: {
      id: newId("ntf"),
      title: vm ? `${several ? d!.name : vm.name} is ready` : `${r.request.title} is done`,
      body: `${r.request.number} · ${r.request.catalog_item.name}`,
      level: "success",
      link: `/requests/${r.request.number}`,
    },
  } as Omit<LiveEvent, "id" | "at">);
  timers.delete(r.request.id);
}

function cancelNow(r: StoredRequest) {
  for (const s of r.steps) {
    if (s.status === "running") {
      s.status = "cancelled";
      s.finished_at = now();
      s.children.forEach((c) => c.status !== "done" && (c.status = "cancelled"));
      publishStep(r, s);
    } else if (s.status === "pending") {
      s.status = "skipped";
      publishStep(r, s);
    }
  }
  r.request.status = "cancelled";
  r.request.finished_at = now();
  const d = db.deployments.find((x) => x.id === r.request.deployment_ids?.[0]);
  if (d) {
    d.status = "deleted";
    const q = db.quota[d.project.id];
    const vm = vmOf(d);
    if (q && vm) {
      q.used.vcpu -= vm.vm.cpu_count;
      q.used.memory_gb -= vm.vm.memory_mb / 1024;
      q.used.vm_count -= 1;
    }
    publishDeployment(r, d);
  }
  publishRequest(r);
  timers.delete(r.request.id);
}

function schedule(requestId: string) {
  clearTimeout(timers.get(requestId));
  timers.set(
    requestId,
    setTimeout(() => tick(requestId), timing.tickMs),
  );
}

export function start(requestId: string) {
  schedule(requestId);
}

/** Cancel: immediate when nothing runs yet, otherwise after the current step. */
export function cancel(r: StoredRequest) {
  if (r.request.status === "running") {
    r.request.status = "cancelling";
    publishRequest(r);
    schedule(r.request.id);
  } else {
    cancelNow(r);
  }
}

export function stopAll() {
  timers.forEach((t) => clearTimeout(t));
  timers.clear();
  hosts.clear();
}

export function newRequestNumber() {
  return `REQ-${db.seq.request++}`;
}
