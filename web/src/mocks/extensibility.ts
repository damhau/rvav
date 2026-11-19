// Event subscriptions and custom workflows in the mock backend (S10,
// ADR-0021 and ADR-0022). Checks follow the server's where the UI shows
// them; deliveries are made up.
import { http, HttpResponse } from "msw";
import type { components } from "@/lib/api/schema";
import { celCheck } from "./cel";
import { audited } from "./governance";
import { typesUsingWorkflow } from "./resourceTypes";
import type { Db } from "./seed";

type S = components["schemas"];
type Run = S["SubscriptionRunDetail"];

interface ExtState {
  subs: S["Subscription"][];
  runs: Run[];
  workflows: S["WorkflowRegistration"][];
  seq: number;
}

const now = () => new Date().toISOString();
const ago = (minutes: number) => new Date(Date.now() - minutes * 60_000).toISOString();
const webhookHosts = ["cmdb.corp.example", "hooks.corp.example", "servicenow.corp.example"];

const topics: { key: S["EventTopicKey"]; group: S["EventTopicGroup"]; description: string }[] = [
  { key: "request.submitted", group: "request", description: "A request was submitted, before any approval." },
  {
    key: "approval.requested",
    group: "approval",
    description: "An approval level of a request waits for a decision.",
  },
  { key: "approval.decided", group: "approval", description: "An approval level was approved or rejected." },
  {
    key: "provisioning.pre",
    group: "request",
    description: "A catalog request is about to build: a blocking subscription may stop it.",
  },
  {
    key: "compute.allocation.pre",
    group: "machine",
    description:
      "A VM of the request is placed and sized, before anything of it is built. A blocking subscription may rename or move it.",
  },
  {
    key: "network.configure",
    group: "machine",
    description:
      "A VM's networks are known, before its address is reserved. A blocking subscription may change its networks or choose its addresses.",
  },
  { key: "compute.provision.pre", group: "machine", description: "A VM is about to be cloned." },
  { key: "compute.provision.post", group: "machine", description: "A VM is built and powered on." },
  {
    key: "custom.create.pre",
    group: "custom",
    description: "A custom resource is about to be made, its inputs worked out (ADR-0056).",
  },
  {
    key: "custom.create.post",
    group: "custom",
    description: "A custom resource is made, with the properties its type's create gave.",
  },
  { key: "provisioning.post", group: "request", description: "A catalog request ended: built, failed or cancelled." },
  {
    key: "day2.pre",
    group: "request",
    description: "A day-2 action is about to run: a blocking subscription may stop it.",
  },
  { key: "day2.post", group: "request", description: "A day-2 action ended." },
  {
    key: "delete.pre",
    group: "request",
    description: "A deployment is about to be deleted: a blocking subscription may stop it.",
  },
  {
    key: "custom.delete.pre",
    group: "custom",
    description:
      "A custom resource is about to be deleted: by a deletion, an update that removes it, or a failed request's rollback.",
  },
  { key: "custom.delete.post", group: "custom", description: "A custom resource is deleted." },
  { key: "compute.removal.pre", group: "machine", description: "A VM is about to be deleted." },
  { key: "compute.removal.post", group: "machine", description: "A VM is deleted from vCenter." },
  { key: "delete.post", group: "request", description: "A deployment was deleted, or its deletion failed." },
  { key: "lease.expiring", group: "lease", description: "A deployment's lease ends in a few days." },
  { key: "lease.expired", group: "lease", description: "A deployment's lease ended." },
  { key: "project.created", group: "project", description: "A project was created." },
  { key: "project.updated", group: "project", description: "A project's settings changed." },
];

/** A custom resource as a custom topic's event has it: no properties before its create ran. */
function sampleCustom(topic: S["EventTopicKey"]) {
  return {
    id: "res_sample_lb",
    type: { key: "load-balancer", title: "Load balancer" },
    key: "lb",
    name: "web-dev-app-lb",
    deployment_id: "dep_sample",
    phase: topic.startsWith("custom.delete") ? "delete" : "create",
    inputs: { members: ["10.31.10.57"], port: 443 },
    properties: topic === "custom.create.pre" ? {} : { vip: "10.31.20.10" },
  };
}

/** The topics a blocking subscription may hear, as the server says, and what their answers may change (ADR-0054). */
const blockable: Partial<Record<S["EventTopicKey"], S["SubscriptionChange"][]>> = {
  "provisioning.pre": ["properties"],
  "compute.allocation.pre": ["name", "placement"],
  "network.configure": ["networks"],
  "day2.pre": [],
  "delete.pre": [],
};
const mayBlock = (topic: S["EventTopicKey"]) => topic in blockable;

/** A VM as a machine topic's event has it: no address before the clone, no moref before it is on. */
function sampleMachine(topic: S["EventTopicKey"]) {
  const planned = topic === "compute.allocation.pre" || topic === "network.configure";
  const built = topic === "compute.provision.post" || topic.startsWith("compute.removal");
  const address = planned ? null : "10.31.10.57";
  return {
    name: "web-dev-app001",
    fqdn: "web-dev-app001.corp.example",
    component: "app",
    index: 0,
    deployment_id: "dep_sample",
    resource_id: built ? "res_sample" : null,
    moref: built ? "vm-1042" : null,
    address,
    template: "rhel9-template",
    vcpu: 4,
    memory_gb: 16,
    disk_gb: 80,
    placement: {
      vcenter: "vc-gva-01",
      cluster: "cl-gva-dev-01",
      resource_pool: "",
      hosts: [],
      datastore: "ds-gva-dev-01",
      datastore_cluster: "",
      folder: "rvav/web",
    },
    networks: [
      {
        index: 0,
        key: "",
        network: "pg-web-dev",
        segment: "",
        ipam: "efficientip",
        subnet: "10.31.10.0/24",
        gateway: "10.31.10.1",
        prefix_length: 24,
        subnet6: "",
        dns_zone: "corp.example",
        fqdn: "web-dev-app001.corp.example",
        address,
      },
    ],
    tags: ["env/dev"],
    properties: { cost_center: "CC-4410" },
  };
}

const sampleAdmin = { id: "usr_admin", name: "Marc Keller", email: "mkeller@corp.example" };

/** An approval level as its events have it, decided or not. */
function sampleApproval(topic: S["EventTopicKey"]) {
  const decided = topic === "approval.decided";
  return {
    id: "apr_sample",
    name: "Production change",
    level: 1,
    kind: "policy",
    mode: "any",
    reasons: ["Production environment"],
    roles: [],
    manager: false,
    groups: ["rvav-approvers-web"],
    approvers: [sampleAdmin],
    escalated: false,
    status: decided ? "approved" : "pending",
    due_at: decided ? null : new Date(Date.now() + 48 * 3_600_000).toISOString(),
    decided_at: decided ? now() : null,
    outcome: decided ? "Approved by Marc Keller" : null,
    decided_by: decided ? sampleAdmin : null,
    comment: decided ? "Fine for this sprint." : null,
  };
}

function sample(topic: S["EventTopicKey"]): S["IntegrationEvent"] {
  const group = topics.find((t) => t.key === topic)?.group;
  if (group === "project")
    return {
      id: "evt_sample",
      topic,
      contract: "rvav.event.v1",
      occurred_at: now(),
      data: {
        request: null,
        item: null,
        project: {
          id: "prj_web",
          code: "WEB",
          name: "Web Platform",
          description: "The web shop's servers.",
          cost_center: "CC-4410",
          environments: ["dev", "prd"],
          properties: {},
          archived: false,
          deployment_sharing: "all_members",
          admins: [sampleAdmin],
        },
        environment: "",
        production: false,
        requester: null,
        owner: null,
        inputs: {},
        deployment: null,
        deployments: [],
        resources: [],
        actor: sampleAdmin,
        changes: topic === "project.updated" ? ["cost_center"] : [],
      },
    };
  return {
    id: "evt_sample",
    topic,
    contract: "rvav.event.v1",
    occurred_at: now(),
    data: {
      request: { id: "req_sample", number: "REQ-2026-000123", kind: "provision", status: "running" },
      item: { slug: "rhel9-server", name: "RHEL 9 server", version: "3.1" },
      project: { code: "WEB", name: "Web Platform", cost_center: "CC-4410" },
      environment: "dev",
      production: false,
      requester: { name: "Tom Nguyen", email: "tnguyen@corp.example" },
      inputs: { role: "web", size: "M" },
      deployment: null,
      lease: null,
      ...(group === "machine" ? { machine: sampleMachine(topic) } : {}),
      ...(group === "approval" ? { approval: sampleApproval(topic) } : {}),
      ...(group === "custom" ? { custom_resource: sampleCustom(topic) } : {}),
    },
  };
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

function state(db: Db): ExtState {
  const d = db as Db & { extensibility?: ExtState };
  if (d.extensibility) return d.extensibility;
  const first = db.requests[0]?.request;
  const request = first ? { id: first.id, number: first.number } : undefined;
  const subs: S["Subscription"][] = [
    {
      id: "sub_cmdb_check",
      name: "CMDB pre-check",
      description: "The CMDB refuses VMs of applications it doesn't know.",
      topic: "provisioning.pre",
      condition: 'environment == "prd"',
      blocking: true,
      on_failure: "stop_request",
      priority: 10,
      timeout_seconds: 20,
      enabled: true,
      target: { webhook: { url: "https://cmdb.corp.example/rvav/check", secret: "webhook-signing" } },
      created_at: ago(60 * 24 * 20),
      runs: { delivered: 41, failed: 0, pending: 0 },
      last_run_at: ago(30),
    },
    {
      id: "sub_servicenow",
      name: "ServiceNow change record",
      description: "Closes the change record when a VM is built.",
      topic: "provisioning.post",
      condition: "",
      blocking: false,
      priority: 100,
      timeout_seconds: 30,
      enabled: true,
      target: { webhook: { url: "https://servicenow.corp.example/api/rvav", secret: "webhook-signing" } },
      created_at: ago(60 * 24 * 12),
      runs: { delivered: 37, failed: 1, pending: 0 },
      last_run_at: ago(12),
    },
    {
      id: "sub_cmdb_naming",
      name: "CMDB naming",
      description: "The CMDB names each VM and gives it its address range's placement.",
      topic: "compute.allocation.pre",
      condition: "",
      blocking: true,
      on_failure: "stop_request",
      priority: 20,
      timeout_seconds: 20,
      enabled: true,
      target: { webhook: { url: "https://cmdb.corp.example/rvav/name", secret: "webhook-signing" } },
      recovery: { webhook: { url: "https://hooks.corp.example/rvav/cmdb-down", secret: "webhook-signing" } },
      created_at: ago(60 * 24 * 3),
      runs: { delivered: 1, failed: 1, pending: 0 },
      last_run_at: ago(45),
    },
  ];
  const event = (topic: S["EventTopicKey"], minutes: number): S["IntegrationEvent"] => ({
    ...sample(topic),
    id: `evt_${minutes}`,
    occurred_at: ago(minutes),
  });
  const runs: Run[] = [
    {
      id: "run_sn_failed",
      subscription_id: "sub_servicenow",
      event_id: "evt_12",
      topic: "provisioning.post",
      status: "failed",
      blocking: false,
      recovery: false,
      test: false,
      request,
      attempts: 6,
      reason: "HTTP 503 from servicenow.corp.example",
      created_at: ago(60 * 25),
      finished_at: ago(12),
      payload: event("provisioning.post", 60 * 25),
      history: [
        { at: ago(60 * 25), ok: false, status_code: 503, response: "Service Unavailable", duration_ms: 812 },
        { at: ago(12), ok: false, status_code: 503, response: "Service Unavailable", duration_ms: 790 },
      ],
    },
    {
      id: "run_sn_ok",
      subscription_id: "sub_servicenow",
      event_id: "evt_90",
      topic: "provisioning.post",
      status: "delivered",
      blocking: false,
      recovery: false,
      test: false,
      request,
      attempts: 1,
      created_at: ago(90),
      finished_at: ago(90),
      payload: event("provisioning.post", 90),
      history: [{ at: ago(90), ok: true, status_code: 200, response: '{"change":"CHG0041207"}', duration_ms: 240 }],
    },
    {
      id: "run_cmdb_ok",
      subscription_id: "sub_cmdb_check",
      event_id: "evt_30",
      topic: "provisioning.pre",
      status: "delivered",
      blocking: true,
      recovery: false,
      test: false,
      request,
      attempts: 1,
      outcome: "continue",
      created_at: ago(30),
      finished_at: ago(30),
      payload: event("provisioning.pre", 30),
      properties: { cmdb_ci: "CI0098812" },
      history: [
        {
          at: ago(30),
          ok: true,
          status_code: 200,
          response: '{"decision":"continue","properties":{"cmdb_ci":"CI0098812"}}',
          duration_ms: 180,
        },
      ],
    },
    {
      id: "run_naming_ok",
      subscription_id: "sub_cmdb_naming",
      event_id: "evt_45",
      topic: "compute.allocation.pre",
      status: "delivered",
      blocking: true,
      recovery: false,
      test: false,
      request,
      attempts: 1,
      outcome: "continue",
      created_at: ago(45),
      finished_at: ago(45),
      payload: event("compute.allocation.pre", 45),
      changes: { name: "web-dev-app742", placement: { datastore: "ds-gva-dev-02" } },
      history: [
        {
          at: ago(45),
          ok: true,
          status_code: 200,
          response: '{"machine":{"name":"web-dev-app742","placement":{"datastore":"ds-gva-dev-02"}}}',
          duration_ms: 210,
        },
      ],
    },
    {
      id: "run_naming_failed",
      subscription_id: "sub_cmdb_naming",
      event_id: "evt_70",
      topic: "compute.allocation.pre",
      status: "failed",
      blocking: true,
      recovery: false,
      test: false,
      request,
      attempts: 2,
      reason: "cmdb.corp.example answered 503",
      created_at: ago(70),
      finished_at: ago(70),
      payload: event("compute.allocation.pre", 70),
      history: [
        { at: ago(70), ok: false, status_code: 503, response: "Service Unavailable", duration_ms: 95 },
        { at: ago(70), ok: false, status_code: 503, response: "Service Unavailable", duration_ms: 90 },
      ],
    },
    {
      id: "run_naming_recovery",
      subscription_id: "sub_cmdb_naming",
      event_id: "evt_70",
      topic: "compute.allocation.pre",
      status: "blocked",
      blocking: true,
      recovery: true,
      test: false,
      request,
      attempts: 1,
      outcome: "stop",
      reason: "The CMDB is down: try again in an hour.",
      created_at: ago(70),
      finished_at: ago(70),
      payload: event("compute.allocation.pre", 70),
      history: [
        {
          at: ago(70),
          ok: true,
          status_code: 200,
          response: '{"outcome":"stop","reason":"The CMDB is down: try again in an hour."}',
          duration_ms: 120,
        },
      ],
    },
  ];
  const workflows: S["WorkflowRegistration"][] = [
    {
      key: "decommission",
      title: "Decommission an application",
      description: "Freezes it, asks for approval, keeps the data for the retention period, then archives it.",
      workflow_type: "decommission.v1",
      task_queue: "platform-workflows",
      input_schema: {
        type: "object",
        required: ["application"],
        properties: {
          application: { type: "string", title: "Application" },
          retention_days: { type: "integer", title: "Retention (days)", default: 30 },
        },
      },
      timeout_seconds: 60 * 86400,
      project_ids: [],
      usages: ["catalog_item", "day2_action"],
      day2: { name: "Decommission", types: ["deployment"], condition: "", offered: true },
      enabled: true,
      running: 1,
      used_by: [],
      created_at: ago(60 * 24 * 5),
    },
    // A step of composite items only (ADR-0022): what the composite designer offers.
    {
      key: "register-monitoring",
      title: "Register in monitoring",
      description: "Adds a host to the monitoring team's inventory and returns its dashboard.",
      workflow_type: "monitoring.register.v1",
      task_queue: "platform-workflows",
      input_schema: {
        type: "object",
        required: ["fqdn"],
        properties: {
          fqdn: { type: "string", title: "Host" },
          team: { type: "string", title: "Team" },
        },
      },
      timeout_seconds: 3600,
      project_ids: [],
      usages: ["composite_step"],
      enabled: true,
      running: 0,
      used_by: [],
      created_at: ago(60 * 24 * 2),
    },
  ];
  // What runs custom resource types (ADR-0056): their delete, and a day-2 action on their resources.
  const resourceWorkflow = (
    key: string,
    title: string,
    properties: Record<string, unknown>,
    more: Partial<S["WorkflowRegistration"]> = {},
  ): S["WorkflowRegistration"] => ({
    key,
    title,
    workflow_type: `${key}.v1`,
    task_queue: "platform-workflows",
    input_schema: { type: "object", properties },
    timeout_seconds: 3600,
    project_ids: [],
    usages: ["resource_type"],
    enabled: true,
    running: 0,
    used_by: [],
    created_at: ago(60 * 24 * 30),
    ...more,
  });
  workflows.push(
    resourceWorkflow("lb-delete", "Delete a load balancer", { vip: { type: "string", title: "Virtual IP" } }),
    resourceWorkflow("dns-alias", "DNS alias", { fqdn: { type: "string", title: "Alias" } }),
    resourceWorkflow(
      "lb-drain",
      "Drain a load balancer",
      { member: { type: "string", title: "Member" } },
      {
        usages: ["day2_action"],
        day2: { name: "Drain", types: ["deployment"], condition: "", offered: false },
      },
    ),
  );
  d.extensibility = { subs, runs, workflows, seq: 0 };
  return d.extensibility;
}

function checkSubscription(db: Db, w: S["SubscriptionWrite"]): S["FieldError"][] {
  const errors: S["FieldError"][] = [];
  const err = (field: string, code: string, message: string) => errors.push({ field, code, message });
  if (!w.name?.trim()) err("/name", "required", "Name it.");
  const topic = topics.find((t) => t.key === w.topic);
  if (!topic) err("/topic", "unknown", `"${w.topic}" is not a topic.`);
  if (w.condition) {
    const why = celCheck(w.condition);
    if (why) err("/condition", "invalid", `Not valid CEL: ${why}`);
  }
  if (w.blocking && topic && !mayBlock(topic.key))
    err(
      "/blocking",
      "not_pre",
      "Only provisioning.pre, compute.allocation.pre, network.configure, day2.pre and delete.pre subscriptions may block.",
    );
  if (w.timeout_seconds < 1 || w.timeout_seconds > 3600)
    err("/timeout_seconds", "range", "Between 1 and 3600 seconds.");
  checkTarget(db, w.target, "/target", err);
  if (w.recovery && !w.blocking)
    err(
      "/recovery",
      "not_blocking",
      "Only a blocking subscription has a recovery target: it is told when the subscription fails.",
    );
  else if (w.recovery) checkTarget(db, w.recovery, "/recovery", err);
  return errors;
}

function checkTarget(
  db: Db,
  t: S["SubscriptionTarget"],
  at: string,
  err: (field: string, code: string, message: string) => void,
) {
  const st = state(db);
  const kinds = [t.webhook, t.awx, t.workflow].filter(Boolean).length;
  if (kinds !== 1) err(at, "one_of", "Choose one target: a webhook, an AWX template or a custom workflow.");
  if (t.webhook) {
    let host = "";
    try {
      const u = new URL(t.webhook.url);
      host = u.protocol === "https:" ? u.hostname : "";
    } catch {
      host = "";
    }
    if (!host) err(`${at}/webhook/url`, "invalid", "An https URL.");
    else if (!webhookHosts.includes(host))
      err(`${at}/webhook/url`, "not_allowed", `${host} is not an allowed webhook host (RVAV_WEBHOOK_HOSTS).`);
    const secrets = (db as Db & { catalogAdmin?: { secrets: S["Secret"][] } }).catalogAdmin?.secrets ?? [];
    if (!t.webhook.secret) err(`${at}/webhook/secret`, "required", "Choose the secret it is signed with.");
    else if (secrets.length > 0 && !secrets.some((s) => s.name === t.webhook?.secret))
      err(`${at}/webhook/secret`, "unknown", `No secret is named ${t.webhook.secret}.`);
  }
  if (t.awx && !t.awx.job_template && !t.awx.template_id) err(`${at}/awx`, "required", "Choose the AWX template.");
  if (t.workflow) {
    const wf = st.workflows.find((x) => x.key === t.workflow?.key);
    if (!wf) err(`${at}/workflow/key`, "unknown", "Choose a registered workflow.");
    else if (!wf.usages.includes("subscription_target"))
      err(`${at}/workflow/key`, "not_allowed", `${wf.title} isn't registered as a subscription target.`);
  }
}

function checkWorkflow(w: S["WorkflowRegistrationWrite"], create: boolean, taken: boolean): S["FieldError"][] {
  const errors: S["FieldError"][] = [];
  const err = (field: string, code: string, message: string) => errors.push({ field, code, message });
  if (create && !/^[a-z][a-z0-9_-]{1,40}$/.test(w.key ?? ""))
    err("/key", "pattern", "Lower-case letters, digits, dashes and underscores.");
  else if (create && taken) err("/key", "duplicate", `${w.key} is registered already.`);
  if (!w.title?.trim()) err("/title", "required", "Give it a title.");
  if (!/\.v[0-9]+$/.test(w.workflow_type ?? ""))
    err("/workflow_type", "pattern", "The type ends with its major version: decommission.v1.");
  if (!w.task_queue?.trim()) err("/task_queue", "required", "The task queue its workers poll.");
  if (w.input_schema && w.input_schema.type !== "object")
    err("/input_schema", "invalid", 'The input schema is an object schema: "type": "object".');
  if (w.usages.length === 0) err("/usages", "required", "Say how it is used.");
  if (w.usages.includes("day2_action")) {
    if (!w.day2?.name?.trim()) err("/day2/name", "required", "Name the action.");
    if (!w.day2 || w.day2.types.length === 0) err("/day2/types", "required", "Say what it runs on.");
    const why = w.day2?.condition ? celCheck(w.day2.condition) : null;
    if (why) err("/day2/condition", "invalid", `Not valid CEL: ${why}`);
  }
  return errors;
}

/** The custom workflows registered. */
export const workflowRegistrations = (db: Db) => state(db).workflows;

export function extensibilityHandlers(db: Db, base: string, wait: () => Promise<void>) {
  const findSub = (id: string) => state(db).subs.find((s) => s.id === id);
  return [
    http.get(`${base}/admin/event-topics`, async () => {
      await wait();
      return HttpResponse.json({
        items: topics.map((t) => ({
          ...t,
          may_block: mayBlock(t.key),
          may_change: blockable[t.key] ?? [],
          sample: sample(t.key),
        })),
      });
    }),
    http.get(`${base}/admin/subscriptions`, async () => {
      await wait();
      return HttpResponse.json({ items: state(db).subs });
    }),
    http.post(`${base}/admin/subscriptions`, async ({ request }) => {
      await wait();
      const w = (await request.json()) as S["SubscriptionWrite"];
      const errors = checkSubscription(db, w);
      if (errors.length > 0) return problem(422, "Some inputs need attention", errors);
      const st = state(db);
      const sub: S["Subscription"] = {
        ...w,
        id: `sub_${++st.seq}`,
        created_at: now(),
        runs: { delivered: 0, failed: 0, pending: 0 },
      };
      st.subs.push(sub);
      audited(db, "subscription.created", "subscription", sub.id, { name: sub.name });
      return HttpResponse.json(sub, { status: 201 });
    }),
    http.get(`${base}/admin/subscriptions/:id`, async ({ params }) => {
      await wait();
      const sub = findSub(String(params.id));
      return sub ? HttpResponse.json(sub) : problem(404, "Not found");
    }),
    http.put(`${base}/admin/subscriptions/:id`, async ({ params, request }) => {
      await wait();
      const sub = findSub(String(params.id));
      if (!sub) return problem(404, "Not found");
      const w = (await request.json()) as S["SubscriptionWrite"];
      const errors = checkSubscription(db, w);
      if (errors.length > 0) return problem(422, "Some inputs need attention", errors);
      Object.assign(sub, w, { updated_at: now() });
      audited(db, "subscription.updated", "subscription", sub.id, { name: sub.name });
      return HttpResponse.json(sub);
    }),
    http.delete(`${base}/admin/subscriptions/:id`, async ({ params }) => {
      await wait();
      const st = state(db);
      const i = st.subs.findIndex((s) => s.id === params.id);
      if (i < 0) return problem(404, "Not found");
      const gone = st.subs.splice(i, 1)[0]!;
      st.runs = st.runs.filter((r) => r.subscription_id !== gone.id);
      audited(db, "subscription.deleted", "subscription", gone.id, { name: gone.name });
      return new HttpResponse(null, { status: 204 });
    }),
    http.post(`${base}/admin/subscriptions/:id/test`, async ({ params }) => {
      await wait();
      const st = state(db);
      const sub = findSub(String(params.id));
      if (!sub) return problem(404, "Not found");
      const ok = !sub.target.webhook?.url.includes("fail");
      const at = now();
      const run: Run = {
        id: `run_${++st.seq}`,
        subscription_id: sub.id,
        event_id: `evt_test_${st.seq}`,
        topic: sub.topic,
        status: ok ? "delivered" : "failed",
        blocking: sub.blocking,
        recovery: false,
        test: true,
        attempts: 1,
        ...(sub.blocking && ok ? { outcome: "continue" } : {}),
        ...(ok ? {} : { reason: "HTTP 500 from the target" }),
        created_at: at,
        finished_at: at,
        payload: { ...sample(sub.topic), id: `evt_test_${st.seq}` },
        history: [
          ok
            ? {
                at,
                ok: true,
                ...(sub.target.webhook ? { status_code: 200, response: '{"decision":"continue"}' } : {}),
                ...(sub.target.awx ? { job_id: 4120 + st.seq } : {}),
                ...(sub.target.workflow ? { workflow_id: `rvav-subscription/run_${st.seq}` } : {}),
                duration_ms: 150,
              }
            : { at, ok: false, status_code: 500, error: "HTTP 500 from the target", duration_ms: 95 },
        ],
      };
      st.runs.unshift(run);
      sub.runs[ok ? "delivered" : "failed"]++;
      sub.last_run_at = at;
      return HttpResponse.json(run);
    }),
    http.get(`${base}/admin/subscriptions/:id/runs`, async ({ params, request }) => {
      await wait();
      if (!findSub(String(params.id))) return problem(404, "Not found");
      const status = new URL(request.url).searchParams.get("status");
      const items = state(db)
        .runs.filter((r) => r.subscription_id === params.id && (!status || r.status === status))
        .map(({ payload: _p, history: _h, properties: _x, changes: _c, ...run }) => run);
      return HttpResponse.json({ items, next_cursor: null });
    }),
    http.get(`${base}/admin/subscription-runs/:id`, async ({ params }) => {
      await wait();
      const run = state(db).runs.find((r) => r.id === params.id);
      return run ? HttpResponse.json(run) : problem(404, "Not found");
    }),
    http.post(`${base}/admin/subscription-runs/:id/retry`, async ({ params }) => {
      await wait();
      const st = state(db);
      const run = st.runs.find((r) => r.id === params.id);
      if (!run) return problem(404, "Not found");
      if (run.status !== "failed") return problem(409, "Conflict", undefined, "Only a failed run is tried again.");
      const at = now();
      run.status = "delivered";
      run.attempts++;
      run.reason = undefined;
      run.finished_at = at;
      run.history.push({ at, ok: true, status_code: 200, response: '{"change":"CHG0041301"}', duration_ms: 210 });
      const sub = findSub(run.subscription_id);
      if (sub) {
        sub.runs.failed = Math.max(0, sub.runs.failed - 1);
        sub.runs.delivered++;
      }
      const { payload: _p, history: _h, properties: _x, changes: _c, ...out } = run;
      return HttpResponse.json(out);
    }),

    http.get(`${base}/admin/workflows`, async () => {
      await wait();
      const st = state(db);
      return HttpResponse.json({
        items: st.workflows.map((w) => ({
          ...w,
          used_by: [
            ...w.used_by,
            ...st.subs.filter((s) => s.target.workflow?.key === w.key).map((s) => `subscription ${s.name}`),
            ...typesUsingWorkflow(db, w.key ?? ""),
          ],
        })),
      });
    }),
    http.post(`${base}/admin/workflows`, async ({ request }) => {
      await wait();
      const st = state(db);
      const w = (await request.json()) as S["WorkflowRegistrationWrite"];
      const errors = checkWorkflow(
        w,
        true,
        st.workflows.some((x) => x.key === w.key),
      );
      if (errors.length > 0) return problem(422, "Some inputs need attention", errors);
      const reg: S["WorkflowRegistration"] = {
        ...w,
        input_schema: w.input_schema ?? {},
        project_ids: w.project_ids ?? [],
        running: 0,
        used_by: [],
        created_at: now(),
      };
      st.workflows.push(reg);
      audited(db, "workflow.registered", "workflow", reg.key ?? "", { type: reg.workflow_type });
      return HttpResponse.json(reg, { status: 201 });
    }),
    http.get(`${base}/admin/workflows/:key`, async ({ params }) => {
      await wait();
      const w = state(db).workflows.find((x) => x.key === params.key);
      return w ? HttpResponse.json(w) : problem(404, "Not found");
    }),
    http.put(`${base}/admin/workflows/:key`, async ({ params, request }) => {
      await wait();
      const w = state(db).workflows.find((x) => x.key === params.key);
      if (!w) return problem(404, "Not found");
      const body = (await request.json()) as S["WorkflowRegistrationWrite"];
      const errors = checkWorkflow(body, false, false);
      if (errors.length > 0) return problem(422, "Some inputs need attention", errors);
      Object.assign(w, body, { key: w.key, updated_at: now() });
      audited(db, "workflow.updated", "workflow", w.key ?? "", { type: w.workflow_type });
      return HttpResponse.json(w);
    }),
    http.delete(`${base}/admin/workflows/:key`, async ({ params }) => {
      await wait();
      const st = state(db);
      const i = st.workflows.findIndex((x) => x.key === params.key);
      if (i < 0) return problem(404, "Not found");
      const w = st.workflows[i]!;
      if (w.running > 0)
        return problem(
          409,
          "Conflict",
          undefined,
          "Runs of it haven't ended: disable it instead, and remove it once they have.",
        );
      const users = [
        ...st.subs.filter((s) => s.target.workflow?.key === w.key).map((s) => `subscription ${s.name}`),
        ...typesUsingWorkflow(db, w.key ?? ""),
      ];
      if (users.length > 0)
        return problem(409, "Conflict", undefined, `It is used by ${users.join(", ")}: disable it instead.`);
      st.workflows.splice(i, 1);
      audited(db, "workflow.deleted", "workflow", w.key ?? "");
      return new HttpResponse(null, { status: 204 });
    }),
  ];
}
