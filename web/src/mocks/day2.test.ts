import { afterEach, describe, expect, it } from "vitest";
import type { DeploymentDetail, VmResource } from "@/lib/api/types";
import { adminState } from "./administration";
import { actionsFor, runAction } from "./day2";
import { limitState } from "./limits";
import { quotaState } from "./quotas";
import { db, resetDb } from "./seed";

// The mocks' day-2 actions check their inputs as the server's do
// (internal/core/day2.go): the form's schema, then the action's own checks,
// each error on the input it is about.

const act = async (vmName: string, action: string, inputs: Record<string, unknown>) => {
  const d = db.deployments.find((x) => x.resources.some((r) => r.name === vmName))!;
  const vm = d.resources.find((r): r is VmResource => r.type === "vsphere.vm" && r.name === vmName)!;
  const res = await fetch(`http://localhost/api/v1/deployments/${d.id}/actions`, {
    method: "POST",
    headers: {
      Authorization: "Bearer test",
      "Content-Type": "application/json",
      "Idempotency-Key": crypto.randomUUID(),
    },
    body: JSON.stringify({ action, resource_id: vm.id, inputs }),
  });
  return { status: res.status, body: (await res.json()) as { errors?: { field: string; code?: string }[] } };
};

describe("the mocks' day-2 actions", () => {
  it("refuse an input the form requires, on that input", async () => {
    const r = await act("web-dev-legacy001", "disk_remove", {});
    expect(r.status).toBe(422);
    expect(r.body.errors).toEqual([{ field: "/inputs/disk", code: "required", message: "This field is required." }]);
  });

  it("put an action's own check on the input it is about", async () => {
    const r = await act("web-dev-api004", "snapshot_create", { name: "Before change 1" });
    expect(r.status).toBe(422);
    expect(r.body.errors?.map((e) => [e.field, e.code])).toEqual([["/inputs/name", "taken"]]);
  });
});

type LimitPolicy = ReturnType<typeof limitState>["policies"][number];

const dep = (name: string) => db.deployments.find((d) => d.name === name)!;
const vmOf = (d: DeploymentDetail) => d.resources.find((r): r is VmResource => r.type === "vsphere.vm")!;
const run = (d: DeploymentDetail, action: string, inputs: Record<string, unknown>, vm?: VmResource) =>
  runAction(db, d, { action, ...(vm ? { resource_id: vm.id } : {}), inputs }, "REQ-20001");
const grow = (d: DeploymentDetail, cpu: number) => {
  const vm = vmOf(d);
  return run(d, "resize", { cpu_count: vm.vm.cpu_count + cpu, memory_gb: vm.vm.memory_mb / 1024, restart: true }, vm);
};
const limit = (p: Partial<LimitPolicy>): LimitPolicy => ({
  id: "dlp_t",
  name: "Tight",
  project_id: null,
  environment: null,
  priority: 100,
  enabled: true,
  enforcement: "hard",
  deployments_per_user: null,
  deployments_per_project: null,
  vcpu_per_deployment: null,
  memory_gb_per_deployment: null,
  set_by_admin: true,
  ...p,
});
const softVcpu = () => {
  adminState(db).quotas.prj_web = [
    { environment: "", resource: "vcpu", limit: db.quota.prj_web!.used.vcpu, enforcement: "soft" },
  ];
  db.quota.prj_web!.limits.vcpu = db.quota.prj_web!.used.vcpu;
};

// As the server's day2Quota and day2Limits (internal/core): resizing, moving
// and handing a deployment over are held to the quotas and limits that apply.
describe("the mocks' resize, move and handover", () => {
  afterEach(() => localStorage.removeItem("rvav.mock.as"));

  it("refuse a resize past a hard quota or limit, on the input it is about", () => {
    const d = dep("web-dev-api");
    db.quota.prj_web!.limits.vcpu = db.quota.prj_web!.used.vcpu;
    expect(grow(d, 2).error?.errors).toEqual([
      {
        field: "/inputs/cpu_count",
        code: "quota_exceeded",
        message:
          "The new size needs 2 vCPU but Web Platform has 0 left. Choose a smaller size or ask a project admin to raise the quota.",
      },
    ]);
    db.quota.prj_web!.limits.vcpu = null;
    limitState(db).policies.push(limit({ memory_gb_per_deployment: 8 }));
    const vm = vmOf(d);
    const r = run(d, "resize", { cpu_count: vm.vm.cpu_count, memory_gb: 32, restart: true }, vm);
    expect(r.error?.errors?.map((e) => [e.field, e.code])).toEqual([["/inputs/memory_gb", "limit_exceeded"]]);
  });

  it("ask approval for a resize past a soft quota, unless an administrator makes it", () => {
    softVcpu();
    expect(grow(dep("web-dev-api"), 2).stored?.request.status).toBe("running");

    localStorage.setItem("rvav.mock.as", "member");
    resetDb();
    softVcpu();
    const r = grow(dep("web-dev-api"), 2);
    expect(r.stored?.request.status).toBe("pending_approval");
    expect(r.approval?.policies.map((p) => [p.policy_id, p.reason])).toEqual([
      ["d2p_soft_quota_resize", "Over the project's soft quota"],
    ]);
  });

  it("offer the projects a deployment can move to, and hold the move to the new project's quota and limits", () => {
    const d = dep("web-dev-api");
    const listed = () => actionsFor(db, d).find((a) => a.key === "change_project")!;
    const options = () => listed().form?.ui_schema.fields.project?.options ?? [];
    expect(options().map((o) => [o.value, o.disabled ?? false])).toEqual([["prj_data", false]]);

    db.quota.prj_data!.limits.vm_count = db.quota.prj_data!.used.vm_count;
    expect(listed().disabled_reason).toBe(
      "No other project can take it: It needs 1 VM but Data Engineering has 0 left. Choose a smaller size or ask a project admin to raise the quota.",
    );

    db.quota.prj_data!.limits.vm_count = null;
    limitState(db).policies.push(limit({ project_id: "prj_data", deployments_per_project: 0 }));
    expect(run(d, "change_project", { project: "prj_data" }).error?.errors?.map((e) => [e.field, e.code])).toEqual([
      ["/inputs/project", "limit_exceeded"],
    ]);
  });

  it("hold a handover to the new owner's quota", () => {
    const d = dep("web-dev-api");
    const owners = actionsFor(db, d).find((a) => a.key === "change_owner")!.form!.ui_schema.fields.owner!.options!;
    expect(owners.map((o) => o.value)).not.toContain(d.owner.id);
    const to = owners[0]!.value;
    quotaState(db).perUser.prj_web = [{ resource: "vm_count", limit: 0, enforcement: "hard", set_by_admin: true }];
    const r = run(d, "change_owner", { owner: to });
    expect(r.error?.errors?.map((e) => [e.field, e.code])).toEqual([["/inputs/owner", "quota_exceeded"]]);
    expect(r.error?.errors?.[0]?.message).toMatch(
      /^Handing it over needs 1 VM but (your|.+'s) quota in Web Platform has 0 left\./,
    );

    quotaState(db).perUser.prj_web = [];
    expect(run(d, "change_owner", { owner: to }).stored?.request.status).toBe("running");
  });
});
