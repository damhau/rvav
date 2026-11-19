// Day-2 actions in the mock backend: a subset of the server's catalogue
// (internal/day2), the same shapes, and their effect on the mock VMs.
import type {
  ActionForm,
  ApprovalPreview,
  Day2Action,
  DeploymentDetail,
  FieldError,
  GroupMembershipResource,
  GroupRef,
  SecurityGroupResource,
  ServiceRequest,
  Step,
  UiField,
  UserRef,
  VmResource,
} from "@/lib/api/types";
import type { components } from "@/lib/api/schema";
import { can } from "@/lib/can";
import { adminState, placed, rolesGiven } from "./administration";
import { leaseChoices } from "./leases";
import { celBool } from "./cel";
import {
  editedDetails,
  editProblem,
  MAX_DEPLOYMENT_DESCRIPTION,
  MAX_DEPLOYMENT_NAME,
  takenName,
} from "./deploymentNames";
import { type LimitAsk, limitCheck } from "./limits";
import { userGroups } from "./logic";
import { deploymentUsage, quotaCheck, type QuotaScope } from "./quotas";
import type { Db, StoredRequest } from "./seed";
import { unaccepted, warning } from "./warnings";

type PolicyWarning = components["schemas"]["PolicyWarning"];

const now = () => new Date().toISOString();

type Inputs = Record<string, unknown>;

export interface Op {
  key: string;
  title: string;
  detail?: string;
  system: Step["system"];
  apply: (d: DeploymentDetail, vm: VmResource | undefined) => void;
  // The step's detail once it is done.
  done?: string;
}

interface MockAction {
  key: string;
  name: string;
  group: Day2Action["group"];
  deployment?: boolean;
  direct?: boolean;
  // Offered on deployments of VM items only.
  vmItems?: boolean;
  // Offered on a failed deployment too.
  onFailed?: boolean;
  // Offered only to those who change the project: administrators.
  admin?: boolean;
  applies?: (vm: VmResource | undefined, d: DeploymentDetail) => boolean;
  destructive?: boolean;
  confirm?: string;
  typeName?: boolean;
  unavailable?: (vm: VmResource | undefined, d: DeploymentDetail, db: Db) => string | null;
  form?: (vm: VmResource | undefined, d: DeploymentDetail, db: Db) => ActionForm;
  validate?: (vm: VmResource | undefined, inputs: Inputs, d: DeploymentDetail, db: Db) => FieldError | null;
  ops: (vm: VmResource | undefined, inputs: Inputs, d: DeploymentDetail, db: Db) => Op[];
}

const on = (vm?: VmResource) => vm?.vm.power_state === "on";
const poweredOn = (vm?: VmResource) =>
  on(vm)
    ? null
    : vm?.vm.power_state === "suspended"
      ? "The VM is suspended. Power on resumes it."
      : "The VM is powered off.";
const suspended = (vm?: VmResource) =>
  vm?.vm.power_state === "suspended" ? "The VM is suspended: power it on or off first." : null;
const tools = (verb: string) => (vm?: VmResource) =>
  poweredOn(vm) ??
  (vm?.vm.guest_state === "running" ? null : `VMware Tools is not running, so the guest OS can't be asked to ${verb}.`);
const power = (state: VmResource["vm"]["power_state"]) => (_d: DeploymentDetail, vm?: VmResource) => {
  if (!vm) return;
  vm.vm.power_state = state;
  vm.vm.guest_state = state === "on" ? "running" : "not_running";
};
const snapshots = (vm?: VmResource) => vm?.vm.snapshots ?? [];

const invalid = (name: string, code: string, message: string): FieldError => ({
  field: `/inputs/${name}`,
  code,
  message,
});

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

// NSX security groups (ADR-0028): a VM's, and those its item offers (its
// deployment's own, and the existing ones its VMs are in). A drifted VM may
// join its existing groups again.
const membership = (d: DeploymentDetail, vm?: VmResource) =>
  d.resources.find((r): r is GroupMembershipResource => r.type === "nsx.membership" && r.membership.vm === vm?.name);
const offeredGroups = (d: DeploymentDetail): GroupRef[] => {
  const out: GroupRef[] = d.resources
    .filter((r): r is SecurityGroupResource => r.type === "nsx.group")
    .map((g) => ({ id: g.group.id, name: g.group.id }));
  for (const r of d.resources)
    if (r.type === "nsx.membership")
      for (const g of r.membership.groups) if (!out.some((o) => o.id === g.id)) out.push(g);
  return out;
};
const joinable = (d: DeploymentDetail, vm?: VmResource) => {
  const m = membership(d, vm);
  if (!m) return [];
  return offeredGroups(d).filter(
    (g) =>
      !m.membership.groups.some((j) => j.id === g.id) ||
      (m.sync_state === "drifted" && !g.id.startsWith("rvav-") && Boolean(m.membership.missing?.length)),
  );
};
const groupChoice = (groups: GroupRef[]) =>
  form("Security group", [
    [
      "group",
      { type: "string", enum: groups.map((g) => g.id) },
      { label: "Group", widget: "select", options: groups.map((g) => ({ value: g.id, label: g.name })) },
    ],
  ]);
const nsxOp = (key: string, title: string, apply: Op["apply"]): Op => ({ key, title, system: "nsx", apply });

type QuotaLevel = components["schemas"]["QuotaLevel"];

const vmsOf = (d: DeploymentDetail) => d.resources.filter((r): r is VmResource => r.type === "vsphere.vm");

const envOf = (db: Db, key: string) => db.environments.find((e) => e.key === key) ?? { key, name: key };

/** Where the deployment counts, in a project and for an owner, as the server's ownerScope. */
const ownerScope = (db: Db, d: DeploymentDetail, project = d.project, owner: UserRef = d.owner): QuotaScope => ({
  project,
  env: envOf(db, d.environment),
  owner,
  you: owner.id === db.me.user.id,
});

// As the server's: a move checks the new project's quotas, a handover the new owner's (ADR-0047).
const movedLevels: QuotaLevel[] = ["project", "environment", "project_user"];
const handedLevels: QuotaLevel[] = ["project_user", "global_user"];

/** Who may own the deployment: its project's members but its owner. */
const memberChoices = (db: Db, d: DeploymentDetail) =>
  db.users
    .filter((u) => u.id !== d.owner.id && rolesGiven(db, d.project.id, u.id).length > 0)
    .map((u) => ({ value: u.id, label: u.name, ...(u.email ? { meta: u.email } : {}) }));

/** Why the deployment can't move to the project ("" when it can), as the server's cannotMove. */
function cannotMove(db: Db, p: Db["projects"][number], d: DeploymentDetail): string {
  const taken = takenName(db, p.id, [d.name], d.id);
  if (taken) return `${p.name} already has a deployment named ${taken}. Rename this one first (Edit deployment).`;
  for (const { vm } of vmsOf(d)) {
    const cluster = vm.cluster ?? "";
    if (!placed(db, p.id, d.environment, "cluster", cluster)) return `${p.name} does not place VMs on ${cluster}.`;
    for (const disk of vm.disks)
      if (disk.datastore && !placed(db, p.id, d.environment, "datastore", disk.datastore))
        return `${p.name} does not use datastore ${disk.datastore}.`;
    for (const n of vm.nics)
      if (!placed(db, p.id, d.environment, "network", n.network)) return `${p.name} does not use network ${n.network}.`;
  }
  return quotaCheck(db, ownerScope(db, d, p), "It", deploymentUsage(d), movedLevels).hard[0] ?? "";
}

/**
 * The projects the deployment may move to: where the caller can request,
 * with its environment. Those that can't take its VMs are offered disabled,
 * with the reason.
 */
const projectChoices = (db: Db, d: DeploymentDetail) =>
  db.projects
    .filter(
      (p) =>
        p.id !== d.project.id &&
        !adminState(db).extra[p.id]?.archived_at &&
        p.environments.includes(d.environment) &&
        can(db.me, "request.submit", p.id),
    )
    .map((p) => {
      const why = cannotMove(db, p, d);
      return { value: p.id, label: p.name, meta: p.code, ...(why ? { disabled: true, disabled_reason: why } : {}) };
    });

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
    unavailable: (vm) => (vm?.vm.power_state === "suspended" ? null : poweredOn(vm)),
    ops: (vm) => [vcenter("vsphere.power_off", `Power off ${vm?.name}`, power("off"))],
  },
  {
    key: "suspend",
    name: "Suspend",
    group: "power",
    confirm: "The VM stops where it is, and the connections to it are lost. Power on resumes it.",
    unavailable: poweredOn,
    ops: (vm) => [vcenter("vsphere.suspend", `Suspend ${vm?.name}`, power("suspended"))],
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
    unavailable: suspended,
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
        return invalid("cpu_count", "no_change", "This is the VM's current size.");
      if (on(vm) && !i.restart)
        return invalid(
          "restart",
          "needs_power_off",
          "CPU hot add is off on this VM. Allow the restart, or power the VM off first.",
        );
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
    unavailable: suspended,
    form: (vm) => {
      const first = vm?.vm.disks[0]?.datastore ?? "vsan-gva-dev-cl01";
      const datastores = [...new Set([first, "nfs-gva-bronze-01"])].sort();
      return form("New disk", [
        ["size_gb", { type: "integer", minimum: 1, maximum: 4096 }, { label: "Size (GB)", widget: "number" }, 50],
        [
          "datastore",
          { type: "string", enum: datastores },
          {
            label: "Datastore",
            widget: "select",
            options: datastores.map((d) => ({
              value: d,
              label: d,
              meta: d === first ? "2140 GB free" : "9800 GB free",
            })),
          },
          first,
        ],
        [
          "storage_policy",
          { type: "string", enum: ["datastore_default", "gold", "silver"] },
          {
            label: "Storage policy",
            widget: "select",
            options: [
              { value: "datastore_default", label: "The datastore's default" },
              { value: "gold", label: "gold" },
              { value: "silver", label: "silver" },
            ],
          },
          "datastore_default",
        ],
        [
          "provisioning",
          { type: "string", enum: ["as_first_disk", "thin", "thick_lazy_zeroed", "thick_eager_zeroed"] },
          {
            label: "Provisioning",
            widget: "select",
            options: [
              { value: "as_first_disk", label: "As the VM's first disk" },
              { value: "thin", label: "Thin" },
              { value: "thick_lazy_zeroed", label: "Thick, lazily zeroed" },
              { value: "thick_eager_zeroed", label: "Thick, eagerly zeroed" },
            ],
          },
          "as_first_disk",
        ],
      ]);
    },
    ops: (_vm, i) => [
      vcenter("vsphere.disk_add", `Add a ${Number(i.size_gb)} GB disk on ${String(i.datastore)}`, (_d, v) => {
        v?.vm.disks.push({
          label: `Hard disk ${(v.vm.disks.length ?? 0) + 1}`,
          key: Math.max(1999, ...v.vm.disks.map((x) => x.key ?? 1999)) + 1,
          capacity_gb: Number(i.size_gb),
          datastore: String(i.datastore),
        });
      }),
    ],
  },
  {
    key: "disk_remove",
    name: "Remove a disk",
    group: "configuration",
    destructive: true,
    confirm:
      "The disk and its data are deleted from the datastore, and the guest loses it at once: unmount its file systems first. This can't be undone.",
    unavailable: (vm) =>
      suspended(vm) ??
      ((vm?.vm.snapshot_count ?? 0) > 0
        ? "Disks of a VM with snapshots can't be removed. Delete its snapshots first."
        : (vm?.vm.disks.length ?? 0) < 2
          ? "The VM has only its boot disk, which can't be removed."
          : null),
    form: (vm) => {
      const disks = vm?.vm.disks.slice(1) ?? [];
      return form("Disk", [
        [
          "disk",
          { type: "string", enum: disks.map((x) => x.label) },
          {
            label: "Disk",
            widget: "select",
            options: disks.map((x) => ({
              value: x.label,
              label: x.label,
              meta: `${x.capacity_gb} GB${x.datastore ? ` on ${x.datastore}` : ""}`,
            })),
          },
        ],
      ]);
    },
    ops: (_vm, i) => [
      vcenter("vsphere.disk_remove", `Remove ${String(i.disk)}`, (_d, v) => {
        if (v) v.vm.disks = v.vm.disks.filter((x) => x.label !== i.disk);
      }),
    ],
  },
  {
    key: "tags_edit",
    name: "Edit tags",
    group: "configuration",
    unavailable: (vm) =>
      vm?.vm.tags ? null : "rvav hasn't read the VM's tags yet. Try again after the next inventory sync.",
    form: (vm) => {
      const offered = ["backup/daily", "backup/weekly"];
      return form("Tags", [
        [
          "tags",
          { type: "array", uniqueItems: true, items: { type: "string", enum: offered } },
          {
            label: "Tags",
            widget: "checklist",
            help: "Tags of other categories stay as they are.",
            options: offered.map((t) => ({ value: t, label: t })),
          },
          (vm?.vm.tags ?? []).filter((t) => t.startsWith("backup/")),
        ],
      ]);
    },
    validate: (vm, i) => {
      const now = (vm?.vm.tags ?? []).filter((t) => t.startsWith("backup/")).sort();
      const picked = (Array.isArray(i.tags) ? i.tags.map(String) : []).sort();
      return now.join() === picked.join() ? invalid("tags", "unchanged", "The VM already has these tags.") : null;
    },
    ops: (vm, i) => {
      const picked = Array.isArray(i.tags) ? i.tags.map(String) : [];
      return [
        vcenter("vsphere.tags", `Change the tags of ${vm?.name}`, (_d, v) => {
          if (v) v.vm.tags = [...(v.vm.tags ?? []).filter((t) => !t.startsWith("backup/")), ...picked].sort();
        }),
      ];
    },
  },
  {
    key: "accept_changes",
    name: "Accept the changes made in vCenter",
    group: "configuration",
    unavailable: (vm) => (vm?.drift?.length ? null : "The VM is as rvav left it."),
    ops: (vm) => [
      {
        key: "rvav.accept",
        title: `Take ${vm?.name} as it is in vCenter`,
        detail: (vm?.drift ?? []).map((x) => x.label).join(", "),
        system: "rvav",
        apply: (d, v) => {
          if (!v) return;
          delete v.drift;
          v.sync_state = "in_sync";
          d.sync_state = d.resources.some((r) => r.sync_state === "missing")
            ? "missing"
            : d.resources.some((r) => r.sync_state === "drifted")
              ? "drifted"
              : "in_sync";
        },
      },
    ],
  },
  {
    // An AWX template with the day2 label (ADR-0018).
    key: "awx.install-monitoring-agent",
    name: "Install monitoring agent",
    group: "automation",
    unavailable: poweredOn,
    form: () =>
      form("Install monitoring agent", [
        [
          "monitoring_agent",
          { type: "string", enum: ["node_exporter", "telegraf"] },
          {
            label: "Agent",
            widget: "select",
            options: [
              { value: "node_exporter", label: "node_exporter" },
              { value: "telegraf", label: "telegraf" },
            ],
          },
          "node_exporter",
        ],
        [
          "listen_port",
          { type: "integer", minimum: 1024, maximum: 65535 },
          { label: "Port", widget: "number", help: "The port the agent listens on." },
          9100,
        ],
      ]),
    ops: (vm) => [
      { key: "awx.inventory", title: "Refresh the AWX inventory", system: "awx", apply: () => {} },
      {
        key: "awx.job:install-monitoring-agent",
        title: `Install monitoring agent on ${vm?.name}`,
        system: "awx",
        apply: () => {},
      },
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
      snapshots(vm).some((s) => s.name === i.name)
        ? invalid("name", "taken", `The VM already has a snapshot named “${String(i.name)}”.`)
        : null,
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
    key: "nsx_group_add",
    name: "Add to a security group",
    group: "configuration",
    unavailable: (vm, d) =>
      !membership(d, vm)
        ? "The VM isn't in NSX security groups."
        : joinable(d, vm).length
          ? null
          : "The VM is in every security group its item offers.",
    form: (vm, d) => groupChoice(joinable(d, vm)),
    ops: (vm, i) => [
      // The VM asked for: completing a step passes the deployment's first.
      nsxOp("nsx.group_add", `Add ${vm?.name} to NSX group ${String(i.group)}`, (dep) => {
        const m = membership(dep, vm);
        const g = offeredGroups(dep).find((x) => x.id === i.group);
        if (!m || !g) return;
        if (!m.membership.groups.some((x) => x.id === g.id)) m.membership.groups.push(g);
        // Its tags set again: no longer drifted.
        m.sync_state = "in_sync";
        delete m.membership.missing;
        delete m.membership.out_of;
        if (!dep.resources.some((r) => r.sync_state === "drifted")) dep.sync_state = "in_sync";
      }),
    ],
  },
  {
    key: "nsx_group_remove",
    name: "Remove from a security group",
    group: "configuration",
    confirm: "The firewall rules of the group no longer apply to the VM.",
    unavailable: (vm, d) =>
      membership(d, vm)?.membership.groups.length ? null : "The VM isn't in NSX security groups.",
    form: (vm, d) => groupChoice(membership(d, vm)?.membership.groups ?? []),
    ops: (vm, i) => [
      nsxOp("nsx.group_remove", `Remove ${vm?.name} from NSX group ${String(i.group)}`, (dep) => {
        const m = membership(dep, vm);
        if (m) m.membership.groups = m.membership.groups.filter((g) => g.id !== i.group);
      }),
    ],
  },
  {
    key: "update",
    name: "Update",
    group: "lifecycle",
    deployment: true,
    direct: true,
    vmItems: true,
    onFailed: true,
    ops: () => [],
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
    key: "change_owner",
    name: "Change owner",
    group: "access",
    deployment: true,
    unavailable: (_vm, d, db) => (memberChoices(db, d).length ? null : "No other member of the project can own it."),
    form: (_vm, d, db) =>
      form("New owner", [
        ["owner", { type: "string", enum: memberChoices(db, d).map((o) => o.value) }, { label: "Owner", widget: "select", options: memberChoices(db, d) }], // prettier-ignore
      ]),
    validate: (_vm, i, d, db) =>
      memberChoices(db, d).some((o) => o.value === i.owner)
        ? null
        : invalid("owner", "enum", "Choose one of the project's members."),
    ops: (_vm, i, d, db) => {
      const owner = db.users.find((u) => u.id === i.owner);
      return [
        {
          key: "rvav.owner",
          title: `Hand ${d.name} over to its new owner`,
          system: "rvav",
          apply: (dep) => {
            if (owner) dep.owner = owner;
          },
        },
      ];
    },
  },
  {
    key: "change_project",
    name: "Move to another project",
    group: "access",
    deployment: true,
    unavailable: (_vm, d, db) => {
      const choices = projectChoices(db, d);
      if (choices.some((p) => !p.disabled)) return null;
      return choices[0]
        ? `No other project can take it: ${choices[0].disabled_reason}`
        : "You can request in no other project with this environment.";
    },
    form: (_vm, d, db) => {
      const choices = projectChoices(db, d);
      return form("Project", [
        [
          "project",
          { type: "string", enum: choices.filter((p) => !p.disabled).map((p) => p.value) },
          { label: "Project", widget: "select", options: choices, help: "The project's quota and placement rules must allow it." }, // prettier-ignore
        ],
      ]);
    },
    validate: (_vm, i, d, db) => {
      const p = projectChoices(db, d).find((x) => x.value === i.project);
      if (!p) return invalid("project", "enum", "Choose one of the projects offered.");
      return p.disabled ? invalid("project", "not_allowed", p.disabled_reason ?? "") : null;
    },
    ops: (_vm, i, d, db) => {
      const to = db.projects.find((p) => p.id === i.project);
      return [
        vcenter("vsphere.move", "Move the VMs to the project's folder", () => {}),
        {
          key: "rvav.project",
          title: `Move ${d.name} to its new project`,
          system: "rvav",
          apply: (dep) => {
            if (!to) return;
            // The mock's project usage is a figure of its own: what the deployment holds goes with it.
            const held = deploymentUsage(dep);
            const from = db.quota[dep.project.id];
            const into = db.quota[to.id];
            for (const r of Object.keys(held) as (keyof typeof held)[]) {
              if (from) from.used[r] = Math.max(from.used[r] - held[r], 0);
              if (into) into.used[r] += held[r];
            }
            dep.project = { id: to.id, code: to.code, name: to.name };
          },
        },
      ];
    },
  },
  {
    key: "edit",
    name: "Edit deployment",
    group: "configuration",
    deployment: true,
    form: (_vm, d) => {
      const f = form("Deployment", [
        [
          "name",
          { type: "string", minLength: 1, maxLength: MAX_DEPLOYMENT_NAME },
          { label: "Name", widget: "text", help: "Unique among the project's deployments. Its VMs keep their names." },
          d.name,
        ],
        [
          "description",
          { type: "string", maxLength: MAX_DEPLOYMENT_DESCRIPTION },
          { label: "Description", widget: "textarea" },
          d.description || undefined,
        ],
      ]);
      return { ...f, schema: { ...f.schema, required: ["name"] } };
    },
    validate: (_vm, i, d, db) => editProblem(db, d, i),
    ops: (_vm, i, d) => {
      const { name, description } = editedDetails(i);
      return [
        {
          key: "rvav.details",
          title: name === d.name ? `Record ${name}'s new description` : `Rename ${d.name} to ${name}`,
          system: "rvav",
          done:
            name === d.name
              ? "New description"
              : `From ${d.name} to ${name}${description === (d.description ?? "") ? "" : ", with a new description"}`,
          apply: (dep) => {
            dep.name = name;
            dep.description = description;
          },
        },
      ];
    },
  },
  {
    key: "extend_lease",
    name: "Extend the lease",
    group: "lifecycle",
    deployment: true,
    unavailable: (_vm, d) => (d.lease.expires_at ? null : "The deployment has no lease."),
    // As the server's: within the lease the deployment is under now, and its lifetime left (ADR-0048).
    form: (_vm, d, db) => {
      const c = leaseChoices(db, d);
      let days = c.defaultDays ?? 30;
      let help = `${d.lease.extensions_used ?? 0} of ${c.maxExtensions} extensions used.`;
      if (c.maxDays !== null) {
        days = Math.min(days, c.maxDays);
        help = `Up to ${c.maxDays} days ${c.soft ? "without a warning" : "without approval"}. ${help}`;
      }
      if (c.left !== undefined) {
        days = Math.max(Math.min(days, c.left), 1);
        help += ` ${c.left === 1 ? "1 day" : `${c.left} days`} of its ${c.lifetime}-day lifetime left.`;
      }
      return form("New lease", [
        [
          "days",
          { type: "integer", minimum: 1, maximum: 3650 },
          { label: "Days from now", widget: "number", help },
          days,
        ],
      ]);
    },
    ops: (_vm, i, d) => [
      {
        key: "rvav.lease",
        title: `Extend the lease of ${d.name} by ${Number(i.days)} days`,
        system: "rvav",
        apply: (dep) => {
          dep.lease = {
            ...dep.lease,
            expires_at: new Date(Date.now() + Number(i.days) * 86_400_000).toISOString(),
            extensions_used: (dep.lease.extensions_used ?? 0) + 1,
            expired_at: null,
            deletes_at: null,
          };
        },
      },
    ],
  },
  {
    key: "vm_unregister",
    name: "Unregister",
    group: "lifecycle",
    admin: true,
    confirm:
      "rvav forgets the VM: it stays in vCenter as it is, and its IP addresses, DNS records and NSX group memberships stay in EfficientIP and NSX. It no longer counts in the project's quota. An administrator can onboard it again.",
    applies: (vm, d) => d.resources.some((r) => r.type === "vsphere.vm" && r.id !== vm?.id),
    ops: (vm) => [
      {
        key: "rvav.unregister",
        title: `Release ${vm?.name} from rvav`,
        detail: "Stays in vCenter",
        system: "rvav",
        apply: (dep) => {
          dep.resources = dep.resources.filter((r) => r.id !== vm?.id);
        },
      },
    ],
  },
  {
    key: "unregister",
    name: "Unregister",
    group: "lifecycle",
    deployment: true,
    admin: true,
    confirm:
      "rvav forgets the deployment: its VMs stay in vCenter as they are, and their IP addresses, DNS records, NSX groups, memberships, rules and segments stay in EfficientIP and NSX. Its lease ends and it no longer counts in the project's quota. An administrator can onboard its VMs again.",
    ops: (_vm, _i, d) => [
      {
        key: "rvav.unregister",
        title: `Release ${d.name} from rvav`,
        detail: `Stay in vCenter: ${d.resources
          .filter((r) => r.type === "vsphere.vm")
          .map((r) => r.name)
          .join(", ")}`,
        system: "rvav",
        apply: (dep) => {
          dep.resources = [];
        },
      },
    ],
  },
  {
    key: "delete",
    name: "Delete",
    group: "lifecycle",
    deployment: true,
    destructive: true,
    typeName: true,
    confirm: "Deletes the VMs and releases their IP addresses and DNS records. This can't be undone.",
    form: () =>
      form("If a step fails", [
        [
          "ignore_failures",
          { type: "boolean" },
          {
            label: "Go on past failures",
            widget: "toggle",
            help: "Go on past steps in vCenter, EfficientIP or NSX that fail. What they don't delete stays there: the request lists it, and rvav forgets it.",
          },
          false,
        ],
      ]),
    ops: (_vm, inputs, d) => [
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
      ...(inputs.ignore_failures === true
        ? [
            {
              key: "rvav.left",
              title: "Forget what was left",
              detail: "What failed steps didn't delete",
              system: "rvav" as const,
              done: "Nothing was left",
              apply: () => {},
            },
          ]
        : []),
    ],
  },
];

// The request in the way of an action on vm (or on the whole deployment):
// actions on different VMs run side by side, as on the server.
export const busy = (db: Db, d: DeploymentDetail, vm?: VmResource) =>
  db.requests.find(
    (r) =>
      r.request.deployment_ids?.includes(d.id) &&
      ["pending_approval", "scheduled", "queued", "running", "cancelling"].includes(r.request.status) &&
      (!vm || !r.request.resource || r.request.resource.id === vm.id),
  );

// The server's seeded day-2 policies that ask approval over a soft quota (d2p_soft_quota_*):
// administrators' own policy decides first, so they don't ask them.
const softQuota: Record<string, { id: string; reason: string; roles: string[] }> = {
  resize: { id: "d2p_soft_quota_resize", reason: "Over the project's soft quota", roles: ["owner", "member", "project-admin"] }, // prettier-ignore
  change_project: { id: "d2p_soft_quota_move", reason: "Over the new project's soft quota", roles: ["owner", "project-admin"] }, // prettier-ignore
};

function softQuotaPolicy(db: Db, a: MockAction, d: DeploymentDetail, overSoft: string[]) {
  const pol = softQuota[a.key];
  if (!pol || overSoft.length === 0 || db.me.roles.some((r) => r.key === "admin")) return undefined;
  const roles = [
    ...(db.me.projects.find((p) => p.project.id === d.project.id)?.roles.map((r) => r.key) ?? []),
    ...(d.owner.id === db.me.user.id ? ["owner"] : []),
  ];
  return pol.roles.some((r) => roles.includes(r)) ? pol : undefined;
}

// Why the action needs approval ("" when it doesn't); inputs are unknown when listing.
function approvalReason(db: Db, a: MockAction, d: DeploymentDetail, inputs?: Inputs, overSoft: string[] = []): string {
  if (a.key === "delete" && d.environment === "prd") return "Deleting a production deployment needs sign-off";
  const soft = softQuotaPolicy(db, a, d, overSoft);
  if (soft) return soft.reason;
  if (a.key === "extend_lease") {
    const c = leaseChoices(db, d);
    const days = Number(inputs?.days);
    if ((d.lease.extensions_used ?? 0) >= c.maxExtensions)
      return c.soft
        ? `All ${c.maxExtensions} extensions the lease policy allows are used.`
        : `All ${c.maxExtensions} extensions without approval are used`;
    if (inputs && c.maxDays !== null && days > c.maxDays)
      return c.soft
        ? `Longer than the ${c.maxDays} days the lease policy allows.`
        : `Longer than the ${c.maxDays} days allowed without approval`;
    if (c.left !== undefined && (inputs ? days > c.left : c.left === 0))
      return c.soft
        ? `Past the ${c.lifetime}-day lifetime the lease policy allows.`
        : `Past the ${c.lifetime}-day lifetime allowed without approval`;
  }
  return "";
}

const osFamily = (guestOS?: string | null) => (guestOS?.toLowerCase().includes("windows") ? "windows" : "linux");

/** What a day-2 policy's condition sees, as the server's day2Vars; inputs are unknown when listing. */
function day2Vars(
  db: Db,
  a: MockAction,
  d: DeploymentDetail,
  roles: Set<string>,
  vm?: VmResource,
  inputs?: Inputs,
  overSoft: string[] = [],
) {
  const env = db.environments.find((e) => e.key === d.environment);
  const families = [
    ...new Set(d.resources.filter((r): r is VmResource => r.type === "vsphere.vm").map((r) => osFamily(r.vm.guest_os))),
  ];
  return {
    action: { key: a.key, group: a.group, destructive: Boolean(a.destructive) },
    resource: vm
      ? {
          type: "vsphere.vm",
          name: vm.name,
          power_state: vm.vm.power_state,
          cpu_count: vm.vm.cpu_count,
          memory_gb: Math.floor(vm.vm.memory_mb / 1024),
          guest_os: vm.vm.guest_os ?? "",
          os_family: osFamily(vm.vm.guest_os),
        }
      : {
          type: "deployment",
          name: d.name,
          guest_os: "",
          os_family: families.length > 1 ? "mixed" : (families[0] ?? ""),
        },
    deployment: { id: d.id, name: d.name, owner_id: d.owner.id },
    project: { id: d.project.id, code: d.project.code, name: d.project.name },
    environment: { key: d.environment, name: env?.name ?? d.environment, production: env?.production ?? false },
    user: {
      id: db.me.user.id,
      roles: [...roles],
      groups: userGroups[db.me.user.id] ?? [],
      permissions: db.me.projects.find((p) => p.project.id === d.project.id)?.permissions ?? db.me.permissions,
    },
    inputs: inputs ?? null,
    quota: { over_soft: overSoft.length > 0, over: overSoft },
  };
}

/**
 * As the server's day2Quota and day2Limits: what resizing, moving or
 * handing the deployment over adds to the quotas and deployment limits that
 * apply. A hard one passed refuses it at the input it is about; it returns
 * the resources over a soft quota and the soft limits' warnings.
 */
function day2Checks(db: Db, a: MockAction, d: DeploymentDetail, vm: VmResource | undefined, inputs: Inputs) {
  const none = { errors: [] as FieldError[], overSoft: [] as string[], warnings: [] as PolicyWarning[] };
  const now = deploymentUsage(d);
  let ask: LimitAsk = { toOwner: 0, toProject: 0, vcpu: now.vcpu, memory: now.memory_gb, addVcpu: 0, addMemory: 0 };
  let sc = ownerScope(db, d);
  let add: Partial<typeof now> = now;
  let levels: QuotaLevel[] | undefined;
  let what: string;
  let field = (kind?: string): string =>
    kind === "memory_gb_per_deployment" ? "/inputs/memory_gb" : "/inputs/cpu_count";
  switch (a.key) {
    case "resize": {
      const cpu = Number(inputs.cpu_count) - (vm?.vm.cpu_count ?? 0);
      const mem = Number(inputs.memory_gb) - Math.floor((vm?.vm.memory_mb ?? 0) / 1024);
      add = { vcpu: Math.max(cpu, 0), memory_gb: Math.max(mem, 0) };
      ask = { ...ask, vcpu: now.vcpu + cpu, memory: now.memory_gb + mem, addVcpu: cpu, addMemory: mem };
      what = "The new size";
      break;
    }
    case "change_project": {
      const p = db.projects.find((x) => x.id === inputs.project);
      if (!p) return none;
      sc = ownerScope(db, d, p);
      levels = movedLevels;
      ask = { ...ask, toOwner: 1, toProject: 1, addVcpu: now.vcpu, addMemory: now.memory_gb };
      what = "Moving it";
      field = () => "/inputs/project";
      break;
    }
    case "change_owner": {
      const owner = db.users.find((u) => u.id === inputs.owner);
      if (!owner) return none;
      sc = ownerScope(db, d, d.project, owner);
      levels = handedLevels;
      ask = { toOwner: 1, toProject: 0, vcpu: 0, memory: 0, addVcpu: 0, addMemory: 0 };
      what = "Handing it over";
      field = () => "/inputs/owner";
      break;
    }
    default:
      return none;
  }
  const q = quotaCheck(db, sc, what, add, levels);
  if (q.hard.length)
    return { ...none, errors: q.hard.map((message) => ({ field: field(), code: "quota_exceeded", message })) };
  const l = limitCheck(db, sc, ask);
  // limitCheck says each hard limit passed in the order of its lines.
  const refused = l.lines.filter((x) => x.exceeds && x.enforcement !== "soft");
  if (l.hard.length)
    return {
      ...none,
      errors: l.hard.map((message, i) => ({ field: field(refused[i]?.kind), code: "limit_exceeded", message })),
    };
  return { ...none, overSoft: q.overSoft, warnings: l.soft };
}

/**
 * The day-2 policy that decides an action, as the server's first match by
 * priority (ADR-0045), with the mock's CEL. A condition that fails to
 * evaluate leaves its policy out, as when the server lists actions.
 */
function deciding(db: Db, a: MockAction, d: DeploymentDetail, vm?: VmResource, inputs?: Inputs) {
  const roles = new Set([
    ...db.me.roles.map((r) => r.key),
    ...(db.me.projects.find((p) => p.project.id === d.project.id)?.roles.map((r) => r.key) ?? []),
    ...(d.owner.id === db.me.user.id ? ["owner"] : []),
  ]);
  const names = (p: string) => p === "*" || p === a.key || (p.endsWith("*") && a.key.startsWith(p.slice(0, -1)));
  const vars = day2Vars(db, a, d, roles, vm, inputs);
  return adminState(db)
    .day2.filter(
      (p) =>
        p.enabled &&
        (!p.project_id || p.project_id === d.project.id) &&
        (p.actions ?? []).some(names) &&
        ((p.roles ?? []).length === 0 || (p.roles ?? []).some((r) => roles.has(r))) &&
        celBool(p.condition, vars, false),
    )
    .sort((x, y) => x.priority - y.priority)[0];
}

/** What a soft policy, or a soft lease, warns about instead of refusing or asking for approval. */
function warningsFor(db: Db, a: MockAction, d: DeploymentDetail, leaseWhy: string, vm?: VmResource): PolicyWarning[] {
  const out: PolicyWarning[] = [];
  const pol = deciding(db, a, d, vm);
  if (pol && pol.effect !== "allow" && pol.enforcement === "soft")
    out.push(
      warning(
        pol.name,
        pol.reason ||
          (pol.effect === "deny"
            ? `${a.name} goes against the policy ${pol.name}.`
            : `${a.name} would need approval under ${pol.name}.`),
      ),
    );
  if (leaseWhy && leaseSoft(db, d))
    out.push(
      warning(`Lease in ${db.environments.find((e) => e.key === d.environment)?.name ?? d.environment}`, leaseWhy),
    );
  return out;
}

/** What soft day-2 policies warn about the deployment's action, such as an update. */
export function actionWarnings(db: Db, key: string, d: DeploymentDetail): PolicyWarning[] {
  const a = catalogue.find((x) => x.key === key && x.deployment);
  return a ? warningsFor(db, a, d, "") : [];
}

const leaseSoft = (db: Db, d: DeploymentDetail) => leaseChoices(db, d).soft;

/** The actions list of a deployment, as GET /deployments/{id}/actions answers. */
export function actionsFor(db: Db, d: DeploymentDetail): Day2Action[] {
  // The seeded day-2 policies let administrators, members and project
  // admins run them; with none matching, the roles' day-2 permissions decide.
  const held = db.me.projects.find((p) => p.project.id === d.project.id);
  const isAdmin = db.me.roles.some((r) => r.key === "admin");
  const runs = (a: MockAction) =>
    db.me.roles.some((r) => r.key === "admin") ||
    Boolean(held?.roles.some((r) => r.key === "member" || r.key === "project-admin")) ||
    can(db.me, `day2.${a.group}`, d.project.id) ||
    can(db.me, `day2.${a.group}:${a.key}`, d.project.id);
  const hardDeny = (a: MockAction, vm?: VmResource) => {
    const pol = deciding(db, a, d, vm);
    return pol?.effect === "deny" && pol.enforcement !== "soft" ? pol.reason || `${pol.name} doesn't allow it.` : null;
  };
  const vms = d.resources.filter((r): r is VmResource => r.type === "vsphere.vm");
  const targets: (VmResource | undefined)[] = [undefined, ...vms];
  const out: Day2Action[] = [];
  for (const vm of targets) {
    for (const a of catalogue) {
      if (Boolean(a.deployment) !== !vm || (a.admin && !isAdmin) || a.applies?.(vm, d) === false) continue;
      if (a.vmItems && db.items.find((i) => i.id === d.catalog_item.id)?.type !== "vm_blueprint") continue;
      const active = busy(db, d, vm);
      const reason =
        (active ? `${active.request.number} is still running on this ${vm ? "VM" : "deployment"}.` : null) ??
        (d.status === "deleted"
          ? "The deployment was deleted."
          : d.status !== "ready" &&
              !(d.status === "updating" && vm) &&
              !(d.status === "failed" && (a.key === "delete" || a.onFailed))
            ? `The deployment is ${d.status}.`
            : null) ??
        (runs(a) ? null : `Your role in ${d.project.name} does not allow ${a.name}.`) ??
        hardDeny(a, vm) ??
        a.unavailable?.(vm, d, db) ??
        null;
      const why = approvalReason(db, a, d);
      const lease = a.key === "extend_lease" ? why : "";
      const warnings = reason === null ? warningsFor(db, a, d, lease, vm) : [];
      const asks = why !== "" && !(lease && leaseSoft(db, d));
      out.push({
        key: a.key,
        name: a.name,
        group: a.group,
        resource_id: vm?.id ?? null,
        resource_name: vm?.name ?? d.name,
        enabled: reason === null,
        ...(reason ? { disabled_reason: reason } : {}),
        destructive: Boolean(a.destructive),
        approval: asks ? { required: true, reasons: [why] } : { required: false, reasons: [] },
        ...(warnings.length ? { warnings } : {}),
        ...(a.confirm || a.typeName
          ? { confirm: { message: a.confirm ?? "", ...(a.typeName ? { type_to_confirm: vm?.name ?? d.name } : {}) } }
          : {}),
        ...(a.form && reason === null ? { form: a.form(vm, d, db) } : {}),
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

/**
 * The steps of a request that runs these operations: submitted, its
 * approval (`none` says why there is none), the operations, then done.
 */
export function requestSteps(ops: Op[], approval: boolean, none: string): Step[] {
  const steps: Step[] = [
    step("request.submit", "Submitted", "rvav", { status: "done", started_at: now(), finished_at: now() }),
    approval
      ? step("policy.approval", "Waiting for approval", "policy", { status: "running", started_at: now() })
      : step("policy.approval", "Approval not required", "policy", {
          status: "done",
          started_at: now(),
          finished_at: now(),
          detail: none,
        }),
  ];
  for (const op of ops) {
    const s = step(op.key, op.title, op.system, op.detail ? { detail: op.detail } : {});
    pending.set(s.id, op);
    steps.push(s);
  }
  steps.push(step("request.ready", "Done", "rvav"));
  return steps;
}

export interface RunResult {
  error?: { status: number; title: string; detail?: string; field?: string; errors?: FieldError[] };
  stored?: StoredRequest;
  approval?: ApprovalPreview;
}

/** Validate a draft and build its request, as POST /deployments/{id}/actions does. */
export function runAction(
  db: Db,
  d: DeploymentDetail,
  draft: {
    action: string;
    resource_id?: string;
    inputs?: Inputs;
    justification?: string;
    accept_warnings?: boolean;
    accepted_warnings?: string[];
  },
  number: string,
): RunResult {
  const vm = d.resources.find((r): r is VmResource => r.id === draft.resource_id && r.type === "vsphere.vm");
  const a =
    catalogue.find((x) => x.key === draft.action && Boolean(x.deployment) === !vm) ??
    catalogue.find((x) => x.key === draft.action);
  if (!a || a.direct)
    return { error: { status: 422, title: `“${draft.action}” is not a day-2 action.`, field: "/action" } };
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
  // As the server: the form's schema first, then the action's own checks, each on the input it is about.
  const required = (a.form?.(vm, d, db).schema as { required?: string[] } | undefined)?.required ?? [];
  const missing = required
    .filter((n) => inputs[n] === undefined)
    .map((n) => invalid(n, "required", "This field is required."));
  const problems = missing.length
    ? missing
    : [a.validate?.(vm, inputs, d, db)].filter((e): e is FieldError => Boolean(e));
  if (problems.length) return { error: { status: 422, title: "Some inputs need attention", errors: problems } };
  const checked = day2Checks(db, a, d, vm, inputs);
  if (checked.errors.length)
    return { error: { status: 422, title: "Some inputs need attention", errors: checked.errors } };

  const why = approvalReason(db, a, d, inputs, checked.overSoft);
  const warnings = [...warningsFor(db, a, d, a.key === "extend_lease" ? why : ""), ...checked.warnings];
  const warned = unaccepted(warnings, draft);
  if (warned.length) return { error: { status: 422, title: "Some values need fixing", errors: warned } };
  const needsApproval = why !== "" && !(a.key === "extend_lease" && leaseSoft(db, d));
  const soft = softQuotaPolicy(db, a, d, checked.overSoft);
  const marc: UserRef = db.users.find((u) => u.id === "usr_mkeller")!;
  const steps = requestSteps(
    a.ops(vm, inputs, d, db),
    needsApproval,
    `No day-2 policy asks for approval in ${d.environment}`,
  );
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
    ...(warnings.length ? { warnings } : {}),
  };
  if (!needsApproval) d.status = a.key === "delete" ? "deleting" : "updating";
  return {
    stored: { request, steps, logs: {} },
    approval: needsApproval
      ? {
          required: true,
          policies: [
            {
              policy_id: a.key === "delete" ? "d2p_prd_delete" : (soft?.id ?? "lease_policy"),
              name: a.key === "delete" ? "Deleting in production" : soft ? "Over a soft quota" : "Lease policy",
              reason: why,
              levels: [
                a.key === "delete" || soft
                  ? { name: "Infrastructure leads", approvers: [marc], mode: "any" }
                  : { name: "Project approvers", approvers: [marc], mode: "any" },
              ],
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
  if (op.done) s.detail = op.done;
  return true;
}

/** The deployment's status once a day-2 request is done. */
export function day2Finished(db: Db, d: DeploymentDetail, r: ServiceRequest) {
  const others = db.requests.some(
    (x) => x.request.id !== r.id && x.request.deployment_id === d.id && x.request.status === "running",
  );
  if (r.action?.key === "delete" || r.action?.key === "unregister") d.status = "deleted";
  else if (!others) d.status = "ready";
}
