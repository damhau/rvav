// A request's run on its graph (D1.4, ADR-0032): which of its steps each
// node draws, and their status. A composite's steps run as step:<key>, its
// nodes' IDs; the steps before them (submitted, approval, the checks of
// blocking subscriptions) are the request's, the ones after (ready, a
// rollback) its end's.
import type { CanvasGraph, CanvasStatus } from "@/components/canvas/graph";
import type { ServiceRequest, Step } from "@/lib/api/types";

// The server's (submit, policy, checks) and the mock's.
const before = new Set(["submit", "policy", "checks", "request.submit", "policy.approval"]);
const ready = new Set(["ready", "request.ready"]);

/**
 * A run's top-level steps by the node that draws them, in the graph's order.
 * A step no node draws goes on the request's when it comes before the
 * composite's first step (the server adds the checks there), else on its end.
 */
export function runSteps(graph: CanvasGraph, steps: Step[]): Map<string, Step[]> {
  const out = new Map<string, Step[]>(graph.nodes.map((n) => [n.id, []]));
  const start = graph.nodes.find((n) => n.kind === "inputs")?.id;
  const end = graph.nodes.find((n) => n.kind === "end")?.id;
  const first = steps.findIndex((s) => s.key.startsWith("step:"));
  steps.forEach((s, i) => {
    const id = out.has(s.key) ? s.key : before.has(s.key) || i < first ? start : end;
    if (id) out.get(id)!.push(s);
  });
  return out;
}

/** The step of a rollback that finished: what the request built is deleted. */
export const rollbackOf = (steps: Step[]) => steps.find((s) => s.key === "rollback" && s.status === "done");

/**
 * A step's status on the graph. Undone, where the request said so: a step
 * whose undo ran (cancelled, with its `/undo` step done), or the VMs of a
 * request rolled back.
 */
export function stepStatus(s: Step, rolledBack = false): CanvasStatus {
  if (s.status === "cancelled" && s.children.some((c) => c.key.endsWith("/undo") && c.status === "done"))
    return "undone";
  if (rolledBack && s.status === "done" && s.system === "vcenter" && s.key.startsWith("step:")) return "undone";
  return s.status;
}

// The status of several steps: the one that says most of where they are.
const weight: CanvasStatus[] = ["failed", "running", "pending", "cancelled", "undone", "skipped", "done"];

/** Each node's status: its steps', or not started while it has none. The end is the ready step's. */
export function runStatus(graph: CanvasGraph, byNode: Map<string, Step[]>): Record<string, CanvasStatus> {
  const all = [...byNode.values()].flat();
  const rolledBack = Boolean(rollbackOf(all));
  const out: Record<string, CanvasStatus> = {};
  for (const n of graph.nodes) {
    const own = (byNode.get(n.id) ?? []).filter((s) => n.kind !== "end" || ready.has(s.key));
    const statuses = own.map((s) => stepStatus(s, rolledBack));
    out[n.id] = weight.find((w) => statuses.includes(w)) ?? "pending";
  }
  return out;
}

const contains = (s: Step, id: string): boolean => s.id === id || s.children.some((c) => contains(c, id));

/** The node of the step a failed request failed at, or the first failed one. */
export function failedNode(request: ServiceRequest, byNode: Map<string, Step[]>): string | undefined {
  if (request.status !== "failed") return undefined;
  const at = request.error?.step_id;
  const nodes = [...byNode.entries()];
  return (
    (at ? nodes.find(([, steps]) => steps.some((s) => contains(s, at)))?.[0] : undefined) ??
    nodes.find(([, steps]) => steps.some((s) => s.status === "failed"))?.[0]
  );
}

/** Steps and the steps under them, in order. */
export function flatten(steps: Step[]): Step[] {
  return steps.flatMap((s) => [s, ...flatten(s.children)]);
}

/** The AWX job whose output to show: the one chosen, else the running one, else the last one run. */
export function pickLogStep(steps: Step[], chosen: string | null): Step | undefined {
  const awx = flatten(steps).filter((s) => s.has_log);
  return (
    awx.find((s) => s.id === chosen) ??
    awx.find((s) => s.status === "running") ??
    awx.findLast((s) => s.status !== "pending") ??
    awx[0]
  );
}
