// Day-2 actions in the mock backend: a subset of the server's catalogue
// (internal/day2), the same shapes, and their effect on the mock VMs.
import type {
  ActionForm,
  ApprovalPreview,
  Day2Action,
  DeploymentDetail,
  ServiceRequest,
  Step,
  UiField,
  UserRef,
  VmResource,
} from "@/lib/api/types";
import type { Db, StoredRequest } from "./seed";

const now = () => new Date().toISOString();

type Inputs = Record<string, unknown>;

interface Op {
  key: string;
  title: string;
  system: Step["system"];
  apply: (d: DeploymentDetail, vm: VmResource | undefined) => void;
}

interface MockAction {
  key: string;
  name: string;
  group: Day2Action["group"];
  deployment?: boolean;
  direct?: boolean;
  destructive?: boolean;
  confirm?: string;
  typeName?: boolean;
  unavailable?: (vm: VmResource | undefined, d: DeploymentDetail) => string | null;
  form?: (vm: VmResource | undefined) => ActionForm;
  validate?: (vm: VmResource | undefined, inputs: Inputs) => string | null;
  ops: (vm: VmResource | undefined, inputs: Inputs, d: DeploymentDetail) => Op[];
}

const on = (vm?: VmResource) => vm?.vm.power_state === "on";
const poweredOn = (vm?: VmResource) => (on(vm) ? null : "The VM is powered off.");
const tools = (verb: string) => (vm?: VmResource) =>
  poweredOn(vm) ??
  (vm?.vm.guest_state === "running" ? null : `VMware Tools is not running, so the guest OS can't be asked to ${verb}.`);
const power = (state: "on" | "off") => (_d: DeploymentDetail, vm?: VmResource) => {
  if (!vm) return;
  vm.vm.power_state = state;
  vm.vm.guest_state = state === "on" ? "running" : "not_running";
};
const snapshots = (vm?: VmResource) => vm?.vm.snapshots ?? [];

function form(title: string, fields: [string, Record<string, unknown>, UiField, unknown?][]): ActionForm {
  return {
    schema: {
      type: "object",
      required: fields.filter(([, s]) => s.type !== "boolean").map(([n]) => n),
      properties: Object.fromEntries(fields.map(([n, s]) => [n, s])),
      additionalProperties: false,
    },
    ui_schema: {
      fields: Object.fromEntries(fields.map(([n, , ui]) => [n, ui])),
      layout: { kind: "page", sections: [{ key: "action", title, columns: 1, fields: fields.map(([n]) => n) }] },
    },
    initial_values: Object.fromEntries(fields.filter((f) => f[3] !== undefined).map(([n, , , v]) => [n, v])),
  };
}

const snapshotChoice = (vm?: VmResource) => {
  const list = snapshots(vm);
  return form("Snapshot", [
    [
      "snapshot",
      { type: "string", enum: list.map((s) => s.name) },
      {
        label: "Snapshot",
        widget: "select",
        options: list.map((s) => ({ value: s.name, label: s.name, meta: s.created_at.slice(0, 16).replace("T", " ") })),
      },
      list.at(-1)?.name,
    ],
  ]);
};

const vcenter = (key: string, title: string, apply: Op["apply"]): Op => ({ key, title, system: "vcenter", apply });

export const catalogue: MockAction[] = [
  {
    key: "power_on",
    name: "Power on",
    group: "power",
    unavailable: (vm) => (on(vm) ? "The VM is already powered on." : null),
    ops: (vm) => [vcenter("vsphere.power_on", `Power on ${vm?.name}`, power("on"))],
  },
  {
    key: "shutdown_guest",
    name: "Shut down guest",
    group: "power",
    unavailable: tools("shut down"),
    ops: (vm) => [vcenter("vsphere.shutdown_guest", `Shut down the guest OS of ${vm?.name}`, power("off"))],
  },
  {
    key: "restart_guest",
    name: "Restart guest",
    group: "power",
    unavailable: tools("restart"),
    ops: (vm) => [vcenter("vsphere.restart_guest", `Restart the guest OS of ${vm?.name}`, () => {})],
  },
  {
    key: "power_off",
    name: "Power off",
    group: "power",
    destructive: true,
    confirm:
      "Powering off is like pulling the plug: unsaved work in the guest is lost. Shut down guest stops it cleanly.",
    unavailable: poweredOn,
    ops: (vm) => [vcenter("vsphere.power_off", `Power off ${vm?.name}`, power("off"))],
  },
  {
    key: "reset",
    name: "Reset",
    group: "power",
    destructive: true,
    confirm:
      "Reset restarts the VM without asking the guest OS: unsaved work is lost. Restart guest restarts it cleanly.",
    unavailable: poweredOn,
    ops: (vm) => [vcenter("vsphere.reset", `Reset ${vm?.name}`, () => {})],
  },
  {
    key: "resize",
    name: "Resize",
    group: "configuration",
    form: (vm) =>
      form("New size", [
        [
          "cpu_count",
          { type: "integer", minimum: 1, maximum: 64 },
          { label: "vCPU", widget: "number" },
          vm?.vm.cpu_count,
        ],
        [
          "memory_gb",
          { type: "integer", minimum: 1, maximum: 512 },
          { label: "Memory (GB)", widget: "number" },
          Math.round((vm?.vm.memory_mb ?? 0) / 1024),
        ],
        ...(on(vm)
          ? ([
              [
                "restart",
                { type: "boolean" },
                {
                  label: "Restart the VM if needed",
                  widget: "toggle",
                  help: "Shut it down, resize it and power it on again when the change can't be made while it runs.",
                },
                false,
              ],
            ] as [string, Record<string, unknown>, UiField, unknown][])
          : []),
      ]),
    validate: (vm, i) => {
      if (Number(i.cpu_count) === vm?.vm.cpu_count && Number(i.memory_gb) * 1024 === vm?.vm.memory_mb)
        return "This is the VM's current size.";
      if (on(vm) && !i.restart) return "CPU hot add is off on this VM. Allow the restart, or power the VM off first.";
      return null;
    },
    ops: (vm, i) => {
      const set = vcenter(
        "vsphere.reconfigure",
        `Set ${Number(i.cpu_count)} vCPU and ${Number(i.memory_gb)} GB`,
        (_d, v) => {
          if (!v) return;
          v.vm.cpu_count = Number(i.cpu_count);
          v.vm.memory_mb = Number(i.memory_gb) * 1024;
        },
      );
      if (!on(vm)) return [set];
      return [
        vcenter("vsphere.shutdown_guest", `Shut down the guest OS of ${vm?.name}`, power("off")),
        set,
        vcenter("vsphere.power_on", `Power on ${vm?.name}`, power("on")),
      ];
    },
  },
  {
    key: "disk_add",
    name: "Add a disk",
    group: "configuration",
    form: () =>
      form("New disk", [
        [
          "size_gb",
          { type: "integer", minimum: 1, maximum: 4096 },
          { label: "Size (GB)", widget: "number", help: "Thin provisioned, on the VM's datastore." },
          50,
        ],
      ]),
    ops: (_vm, i) => [
      vcenter("vsphere.disk_add", `Add a ${Number(i.size_gb)} GB disk`, (_d, v) => {
        v?.vm.disks.push({
          label: `Hard disk ${(v.vm.disks.length ?? 0) + 1}`,
          capacity_gb: Number(i.size_gb),
          datastore: v.vm.disks[0]?.datastore,
        });
      }),
    ],
  },
  {
    key: "snapshot_create",
    name: "Take a snapshot",
    group: "snapshots",
    unavailable: (vm) =>
      snapshots(vm).length >= 3 ? `The VM has 3 snapshots, the most allowed. Delete one first.` : null,
    form: () =>
      form("Snapshot", [
        [
          "name",
          { type: "string", minLength: 1, maxLength: 80 },
          { label: "Name", widget: "text", help: "rvav deletes snapshots after 7 days in Development." },
          `Before change ${now().slice(0, 16).replace("T", " ")}`,
        ],
      ]),
    validate: (vm, i) =>
      snapshots(vm).some((s) => s.name === i.name) ? `The VM already has a snapshot named “${String(i.name)}”.` : null,
    ops: (_vm, i) => [
      vcenter("vsphere.snapshot_create", `Take snapshot ${String(i.name)}`, (_d, v) => {
        if (!v) return;
        v.vm.snapshots = [...snapshots(v), { name: String(i.name), created_at: now() }];
        v.vm.snapshot_count = v.vm.snapshots.length;
      }),
    ],
  },
  {
    key: "snapshot_revert",
    name: "Revert to a snapshot",
    group: "snapshots",
    destructive: true,
    confirm: "Everything that changed in the VM since the snapshot is lost.",
    unavailable: (vm) => (snapshots(vm).length ? null : "The VM has no snapshot."),
    form: snapshotChoice,
    ops: (_vm, i) => [vcenter("vsphere.snapshot_revert", `Revert to snapshot ${String(i.snapshot)}`, () => {})],
  },
  {
    key: "snapshot_delete",
    name: "Delete a snapshot",
    group: "snapshots",
    unavailable: (vm) => (snapshots(vm).length ? null : "The VM has no snapshot."),
    form: snapshotChoice,
    ops: (_vm, i) => [
      vcenter("vsphere.snapshot_delete", `Delete snapshot ${String(i.snapshot)}`, (_d, v) => {
        if (!v) return;
        v.vm.snapshots = snapshots(v).filter((s) => s.name !== i.snapshot);
        v.vm.snapshot_count = v.vm.snapshots.length;
      }),
    ],
  },
  {
    key: "console",
    name: "Open the console",
    group: "access",
    direct: true,
    unavailable: poweredOn,
    ops: () => [],
  },
  {
    key: "delete",
    name: "Delete",
    group: "lifecycle",
    deployment: true,
    destructive: true,
    typeName: true,
    confirm: "Deletes the VMs and releases their IP addresses and DNS records. This can't be undone.",
    ops: (_vm, _i, d) => [
      ...d.resources
        .filter((r): r is VmResource => r.type === "vsphere.vm")
        .map((r) => vcenter(`vsphere.destroy@${r.name}`, `Delete VM ${r.name}`, () => {})),
      {
        key: "dns.delete",
        title: "Delete the DNS records",
        system: "efficientip",
        apply: () => {},
      },
      {
        key: "ipam.release",
        title: "Release the IP addresses",
        system: "efficientip",
        apply: (dep) => {
          dep.resources = [];
        },
      },
    ],
  },
];

// The request in the way of an action on vm (or on the whole deployment):
// actions on different VMs run side by side, as on the server.
const busy = (db: Db, d: DeploymentDetail, vm?: VmResource) =>
  db.requests.find(
    (r) =>
      r.request.deployment_ids?.includes(d.id) &&
      ["pending_approval", "scheduled", "queued", "running", "cancelling"].includes(r.request.status) &&
      (!vm || !r.request.resource || r.request.resource.id === vm.id),
  );

const approvalNeeded = (a: MockAction, d: DeploymentDetail) => a.key === "delete" && d.environment === "prd";

/** The actions list of a deployment, as GET /deployments/{id}/actions answers. */
export function actionsFor(db: Db, d: DeploymentDetail): Day2Action[] {
  const role = db.me.projects.find((p) => p.project.id === d.project.id)?.role;
  const vms = d.resources.filter((r): r is VmResource => r.type === "vsphere.vm");
  const targets: (VmResource | undefined)[] = [undefined, ...vms];
  const out: Day2Action[] = [];
  for (const vm of targets) {
    for (const a of catalogue) {
      if (Boolean(a.deployment) !== !vm) continue;
      const active = busy(db, d, vm);
      const reason =
        (active ? `${active.request.number} is still running on this ${vm ? "VM" : "deployment"}.` : null) ??
        (d.status === "deleted"
          ? "The deployment was deleted."
          : d.status !== "ready" && !(d.status === "updating" && vm) && !(d.status === "failed" && a.key === "delete")
            ? `The deployment is ${d.status}.`
            : null) ??
        (role === "viewer" || !role ? `Your role in ${d.project.name} does not allow ${a.name}.` : null) ??
        a.unavailable?.(vm, d) ??
        null;
      out.push({
        key: a.key,
        name: a.name,
        group: a.group,
        resource_id: vm?.id ?? null,
        resource_name: vm?.name ?? d.name,
        enabled: reason === null,
        ...(reason ? { disabled_reason: reason } : {}),
        destructive: Boolean(a.destructive),
        approval: approvalNeeded(a, d)
          ? { required: true, reasons: ["Deleting a production deployment needs sign-off"] }
          : { required: false, reasons: [] },
        ...(a.confirm || a.typeName
          ? { confirm: { message: a.confirm ?? "", ...(a.typeName ? { type_to_confirm: vm?.name ?? d.name } : {}) } }
          : {}),
        ...(a.form && reason === null ? { form: a.form(vm) } : {}),
      });
    }
  }
  return out;
}

// Operations of running day-2 requests, by step ID.
const pending = new Map<string, Op>();

const step = (key: string, title: string, system: Step["system"], extra: Partial<Step> = {}): Step => ({
  id: `stp_d2_${Math.random().toString(36).slice(2, 10)}`,
  key,
  title,
  system,
  status: "pending",
  children: [],
  has_log: false,
  started_at: null,
  finished_at: null,
  ...extra,
});

export interface RunResult {
  error?: { status: number; title: string; detail?: string; field?: string };
  stored?: StoredRequest;
  approval?: ApprovalPreview;
}

/** Validate a draft and build its request, as POST /deployments/{id}/actions does. */
export function runAction(
  db: Db,
  d: DeploymentDetail,
  draft: { action: string; resource_id?: string; inputs?: Inputs; justification?: string },
  number: string,
): RunResult {
  const a = catalogue.find((x) => x.key === draft.action);
  if (!a || a.direct)
    return { error: { status: 422, title: `“${draft.action}” is not a day-2 action.`, field: "/action" } };
  const vm = d.resources.find((r): r is VmResource => r.id === draft.resource_id && r.type === "vsphere.vm");
  if (Boolean(a.deployment) === Boolean(vm))
    return {
      error: {
        status: 422,
        title: `${a.name} needs one of this deployment's resources it applies to.`,
        field: "/resource_id",
      },
    };
  const listed = actionsFor(db, d).find((x) => x.key === a.key && (x.resource_id ?? undefined) === vm?.id);
  if (!listed?.enabled) {
    const reason = listed?.disabled_reason ?? "Not available.";
    return { error: { status: reason.startsWith("Your role") ? 403 : 409, title: reason } };
  }
  const inputs = draft.inputs ?? {};
  const invalid = a.validate?.(vm, inputs);
  if (invalid) return { error: { status: 422, title: invalid, field: "/inputs" } };

  const needsApproval = approvalNeeded(a, d);
  const marc: UserRef = db.users.find((u) => u.id === "usr_mkeller")!;
  const ops = a.ops(vm, inputs, d);
  const steps: Step[] = [
    step("request.submit", "Submitted", "rvav", { status: "done", started_at: now(), finished_at: now() }),
    needsApproval
      ? step("policy.approval", "Waiting for approval", "policy", { status: "running", started_at: now() })
      : step("policy.approval", "Approval not required", "policy", {
          status: "done",
          started_at: now(),
          finished_at: now(),
          detail: `No day-2 policy asks for approval in ${d.environment}`,
        }),
  ];
  for (const op of ops) {
    const s = step(op.key, op.title, op.system);
    pending.set(s.id, op);
    steps.push(s);
  }
  steps.push(step("request.ready", "Done", "rvav"));
  const request: ServiceRequest = {
    id: `req_${number.slice(4)}`,
    number,
    kind: "day2",
    action: { key: a.key, name: a.name },
    deployment_id: d.id,
    ...(vm ? { resource: { id: vm.id, name: vm.name } } : {}),
    title: `${a.name} · ${vm?.name ?? d.name}`,
    status: needsApproval ? "pending_approval" : "running",
    catalog_item: { ...d.catalog_item, type: db.items.find((i) => i.id === d.catalog_item.id)?.type ?? "vm_blueprint" },
    version: d.version,
    project: d.project,
    environment: d.environment,
    requester: db.me.user,
    inputs,
    count: 1,
    justification: draft.justification,
    submitted_at: now(),
    scheduled_at: null,
    started_at: needsApproval ? null : now(),
    finished_at: null,
    deployment_ids: [d.id],
    current_step: null,
    progress: { done: 0, total: steps.length },
    error: null,
  };
  if (!needsApproval) d.status = a.key === "delete" ? "deleting" : "updating";
  return {
    stored: { request, steps, logs: {} },
    approval: needsApproval
      ? {
          required: true,
          policies: [
            {
              policy_id: "d2p_prd_delete",
              name: "Deleting in production",
              reason: "Deleting a production deployment needs sign-off",
              levels: [{ name: "Infrastructure leads", approvers: [marc], mode: "any" }],
            },
          ],
        }
      : undefined,
  };
}

/** Apply a finished day-2 step to the mock deployment; false when the step is not one. */
export function completeDay2Step(d: DeploymentDetail, s: Step): boolean {
  const op = pending.get(s.id);
  if (!op) return false;
  pending.delete(s.id);
  const vm = d.resources.find((r): r is VmResource => r.type === "vsphere.vm");
  op.apply(d, vm);
  return true;
}

/** The deployment's status once a day-2 request is done. */
export function day2Finished(db: Db, d: DeploymentDetail, r: ServiceRequest) {
  const others = db.requests.some(
    (x) => x.request.id !== r.id && x.request.deployment_id === d.id && x.request.status === "running",
  );
  if (r.action?.key === "delete") d.status = "deleted";
  else if (!others) d.status = "ready";
}
