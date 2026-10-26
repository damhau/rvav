import { QueryClient, type InfiniteData } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";
import type { DeploymentDetail, DeploymentPage, RequestPage, ServiceRequest, Step } from "@/lib/api/types";
import { keys } from "@/lib/query";
import { db } from "@/mocks/seed";
import { applyEvent, upsertStep } from "./bridge";

const at = "2026-10-26T14:00:00Z";

function sampleRequest(): ServiceRequest {
  return structuredClone(db.requests[0]!.request);
}

const step = (id: string, status: Step["status"], children: Step[] = []): Step => ({
  id,
  key: "x",
  title: id,
  status,
  children,
  has_log: false,
});

describe("applyEvent", () => {
  it("request.updated replaces the detail and patches infinite lists in place", () => {
    const qc = new QueryClient();
    const r = sampleRequest();
    const listKey = keys.requests.list({ projectId: "prj_web" });
    qc.setQueryData<InfiniteData<RequestPage>>(listKey, {
      pages: [{ items: [r], next_cursor: null }],
      pageParams: [undefined],
    });
    const invalidate = vi.spyOn(qc, "invalidateQueries");

    const updated = { ...r, title: "Renamed", progress: { done: 8, total: 9 } };
    applyEvent(qc, { id: "e1", at, type: "request.updated", data: updated });

    expect(qc.getQueryData(keys.requests.detail(r.number))).toEqual(updated);
    expect(qc.getQueryData(keys.requests.detail(r.id))).toEqual(updated);
    expect(qc.getQueryData<InfiniteData<RequestPage>>(listKey)!.pages[0]!.items[0]!.title).toBe("Renamed");
    expect(invalidate).not.toHaveBeenCalled();
  });

  it("refetches lists when the status changes or the item is new", () => {
    const qc = new QueryClient();
    const r = sampleRequest();
    qc.setQueryData<RequestPage>(keys.requests.list({ projectId: "prj_web" }), { items: [r], next_cursor: null });
    const invalidate = vi.spyOn(qc, "invalidateQueries");
    applyEvent(qc, { id: "e2", at, type: "request.updated", data: { ...r, status: "succeeded" } });
    expect(invalidate).toHaveBeenCalledWith({ queryKey: keys.requests.lists() });
  });

  it("request.step updates a nested step in the cached tree", () => {
    const qc = new QueryClient();
    const tree = [step("a", "done"), step("job", "running", [step("t1", "done"), step("t2", "running")])];
    qc.setQueryData(keys.requests.steps("req_1"), { items: tree });
    const job = step("job", "running", [step("t1", "done"), step("t2", "done"), step("t3", "running")]);
    applyEvent(qc, {
      id: "e3",
      at,
      type: "request.step",
      data: { request_id: "req_1", parent_step_id: null, step: job },
    });
    const items = qc.getQueryData<{ items: Step[] }>(keys.requests.steps("req_1"))!.items;
    expect(items[1]!.children.map((c) => `${c.id}:${c.status}`)).toEqual(["t1:done", "t2:done", "t3:running"]);
  });

  it("request.log bumps the log head for that step", () => {
    const qc = new QueryClient();
    applyEvent(qc, {
      id: "e4",
      at,
      type: "request.log",
      data: { request_id: "req_1", step_id: "s9", next_offset: 42 },
    });
    expect(qc.getQueryData(keys.requests.logHead("req_1", "s9"))).toBe(42);
  });

  it("deployment and resource events patch the deployment detail", () => {
    const qc = new QueryClient();
    const d = structuredClone<DeploymentDetail>(db.deployments[0]!);
    qc.setQueryData(keys.deployments.detail(d.id), d);
    qc.setQueryData<DeploymentPage>(keys.deployments.list({ projectId: d.project.id }), {
      items: [d],
      next_cursor: null,
    });

    applyEvent(qc, { id: "e5", at, type: "deployment.updated", data: { ...d, vms_powered_on: 0 } });
    expect(qc.getQueryData<DeploymentDetail>(keys.deployments.detail(d.id))!.vms_powered_on).toBe(0);
    expect(qc.getQueryData<DeploymentDetail>(keys.deployments.detail(d.id))!.resources).toHaveLength(
      d.resources.length,
    );

    const vm = d.resources[0]!;
    if (vm.type !== "vsphere.vm") throw new Error("expected a VM first");
    const off = { ...vm, vm: { ...vm.vm, power_state: "off" as const } };
    applyEvent(qc, { id: "e6", at, type: "resource.updated", data: { deployment_id: d.id, resource: off } });
    const res = qc.getQueryData<DeploymentDetail>(keys.deployments.detail(d.id))!.resources[0]!;
    expect(res.type === "vsphere.vm" && res.vm.power_state).toBe("off");
  });

  it("hands notifications to the callback and resync refetches everything", () => {
    const qc = new QueryClient();
    const notify = vi.fn();
    applyEvent(
      qc,
      { id: "e7", at, type: "notification", data: { id: "n1", title: "Ready", level: "success" } },
      notify,
    );
    expect(notify).toHaveBeenCalledWith({ id: "n1", title: "Ready", level: "success" });
    const invalidate = vi.spyOn(qc, "invalidateQueries");
    applyEvent(qc, { id: "e8", at, type: "resync" });
    expect(invalidate).toHaveBeenCalledWith();
  });
});

describe("events that arrive during a fetch", () => {
  it("refetch instead of patching, so the response can't overwrite them", async () => {
    const qc = new QueryClient();
    let resolve: (v: { items: Step[] }) => void = () => {};
    const key = keys.requests.steps("req_9");
    const first = qc.fetchQuery({ queryKey: key, queryFn: () => new Promise<{ items: Step[] }>((r) => (resolve = r)) });
    const invalidate = vi.spyOn(qc, "invalidateQueries");
    applyEvent(qc, {
      id: "e9",
      at,
      type: "request.step",
      data: { request_id: "req_9", parent_step_id: null, step: step("clone", "done") },
    });
    expect(invalidate).toHaveBeenCalledWith({ queryKey: key, exact: true });
    resolve({ items: [step("clone", "running")] });
    await first.catch(() => {});
  });
});

describe("upsertStep", () => {
  it("inserts under the parent, or appends when the step is new at the top", () => {
    const tree = [step("job", "running", [step("t1", "done")])];
    expect(upsertStep(tree, step("t2", "running"), "job")[0]!.children.map((c) => c.id)).toEqual(["t1", "t2"]);
    expect(upsertStep(tree, step("ready", "pending"), null).map((s) => s.id)).toEqual(["job", "ready"]);
  });
});
