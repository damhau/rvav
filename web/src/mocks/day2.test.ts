import { describe, expect, it } from "vitest";
import type { VmResource } from "@/lib/api/types";
import { db } from "./seed";

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
