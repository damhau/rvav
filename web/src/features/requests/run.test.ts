import { describe, expect, it } from "vitest";
import { blueprintGraph } from "@/components/canvas/blueprint";
import type { Step } from "@/lib/api/types";
import { seededBlueprint, topologyOf } from "@/mocks/blueprints";
import { db } from "@/mocks/seed";
import { failedNode, pickLogStep, runStatus, runSteps, stepStatus } from "./run";

const graph = blueprintGraph(topologyOf(seededBlueprint("postgres-cluster")), "/topology")!;
const stored = () => db.requests.find((r) => r.request.number === "REQ-10484")!;

const step = (key: string, status: Step["status"], o: Partial<Step> = {}): Step => ({
  id: `stp_${key}`,
  key,
  title: key,
  status,
  children: [],
  has_log: false,
  ...o,
});

describe("a composite's run on its graph", () => {
  it("puts each step on its node: the request's before, its end's after", () => {
    const byNode = runSteps(graph, stored().steps);
    const keys = (id: string) => byNode.get(id)!.map((s) => s.key);
    expect([...byNode.keys()]).toEqual(["inputs", "step:nodes", "step:owner", "step:backups", "end"]);
    expect(keys("inputs")).toEqual(["request.submit", "policy.approval"]);
    expect(keys("step:owner")).toEqual(["step:owner"]);
    expect(keys("end")).toEqual(["request.ready", "rollback"]);
  });

  it("says what was undone: a step whose undo ran, the VMs of a request rolled back", () => {
    const byNode = runSteps(graph, stored().steps);
    expect(runStatus(graph, byNode)).toEqual({
      inputs: "done",
      "step:nodes": "undone",
      "step:owner": "failed",
      "step:backups": "undone",
      end: "pending",
    });
    // Cancelled without its undo, it was only cancelled; done without a rollback, done.
    expect(stepStatus(step("step:x", "cancelled"))).toBe("cancelled");
    expect(stepStatus(step("step:x", "done", { system: "vcenter" }))).toBe("done");
    expect(stepStatus(step("step:x", "done", { system: "awx" }), true)).toBe("done");
  });

  it("follows the steps as they run, and waits on the approval", () => {
    const steps = [
      step("submit", "done"),
      step("policy", "running"),
      step("step:nodes", "pending"),
      step("ready", "pending"),
    ];
    const status = runStatus(graph, runSteps(graph, steps));
    expect(status.inputs).toBe("running");
    expect(status["step:nodes"]).toBe("pending");
    // Not planned yet: not started.
    expect(status["step:owner"]).toBe("pending");
    expect(status.end).toBe("pending");
    steps[1]!.status = "done";
    steps[2]!.status = "running";
    expect(runStatus(graph, runSteps(graph, steps))).toMatchObject({ inputs: "done", "step:nodes": "running" });
  });

  it("finds the node the request failed at, by the step it names, however deep", () => {
    const r = stored();
    expect(failedNode(r.request, runSteps(graph, r.steps))).toBe("step:owner");
    const clone = step("step:nodes/vsphere.clone@a", "failed", { id: "stp_clone" });
    const steps = [step("step:nodes", "failed", { children: [clone] }), step("step:owner", "pending")];
    const failed = { ...r.request, error: { step_id: "stp_clone", message: "No room" } };
    expect(failedNode(failed, runSteps(graph, steps))).toBe("step:nodes");
    expect(failedNode({ ...failed, error: null }, runSteps(graph, steps))).toBe("step:nodes");
    expect(failedNode({ ...failed, status: "running" }, runSteps(graph, steps))).toBeUndefined();
  });

  it("picks an AWX job's output among the steps and those under them", () => {
    const job = step("step:nodes/awx.job:baseline@db", "running", { has_log: true });
    const steps = [
      step("step:nodes", "running", { children: [job] }),
      step("step:owner", "pending", { has_log: true }),
    ];
    expect(pickLogStep(steps, null)).toBe(job);
    expect(pickLogStep(steps, "stp_step:owner")?.key).toBe("step:owner");
    expect(pickLogStep([], null)).toBeUndefined();
  });
});
