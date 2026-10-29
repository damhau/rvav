// Simulates the Temporal provisioning workflow: steps advance on a timer,
// resources appear in the deployment, and every change is published as an event.
import type {
  DeploymentDetail,
  DnsRecordResource,
  DrsRuleResource,
  EnvironmentKey,
  IpAddressResource,
  LiveEvent,
  LogLine,
  Project,
  Step,
  VmResource,
} from "@/lib/api/types";
import { completeDay2Step, day2Finished } from "./day2";
import { bus } from "./events";
import { clusters, DOMAIN, networks, sizes, templates, VCENTER, type SizeKey } from "./inventory";
import { MULTI_SLUG, webAppName, webAppParts } from "./components";
import { reserveIp, summarize, vmNames } from "./logic";
import { db, type StoredItem, type StoredRequest } from "./seed";

export const timing = { tickMs: 900 };
const timers = new Map<string, ReturnType<typeof setTimeout>>();
/** The VM name chosen when the request was planned, by request ID. */
const hosts = new Map<string, string>();
/** The address reserved for each VM of a multi-VM deployment, by name. */
const ips = new Map<string, string>();

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

  if (item.slug === MULTI_SLUG) {
    // One deployment for every component's VMs (ADR-0024); each VM's steps
    // carry its name after "@".
    host = webAppName(project, env, inputs);
    const subnet = networks.find((n) => n.name === text(inputs.network))?.subnet ?? "";
    for (const p of webAppParts(inputs)) {
      const names = vmNames(db, project, env, p.role, p.count);
      for (const name of names) {
        steps.push(
          step(`ipam.reserve@${name}`, `Reserve an IP address · ${name}`, "efficientip", {
            detail: `Subnet ${subnet}`,
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

function publishResource(
  r: StoredRequest | null,
  d: DeploymentDetail,
  res: VmResource | IpAddressResource | DnsRecordResource | DrsRuleResource,
) {
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
  completeStep(r, s);
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
  // A multi-VM deployment's steps name their VM (or component) after "@".
  const [key, at] = s.key.split("@") as [string, string | undefined];
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
      const part = multi ? webAppParts(inputs).find((p) => host.includes(`-${p.role}`)) : undefined;
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
}

/** One tick: finish the running unit of work and start the next one. */
function tick(requestId: string) {
  const r = db.requests.find((x) => x.request.id === requestId);
  if (!r) return;
  if (r.request.status === "cancelling") return cancelNow(r);
  if (r.request.status !== "running") return;

  const s = r.steps.find((x) => x.status === "running") ?? r.steps.find((x) => x.status === "pending");
  if (!s) return finish(r);

  if (s.status === "pending") {
    s.status = "running";
    s.started_at = now();
    if (s.key === "awx.job") {
      s.children[0]!.status = "running";
      s.children[0]!.started_at = now();
      log(r, s, [
        ["header", `PLAY [${s.title.split(" · ")[1] ?? "job"}] ${stars("PLAY [x]")}`],
        ["plain", ""],
      ]);
    }
  } else if (s.key === "awx.job") {
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
      if (next.key === "awx.job") {
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
