// The canvas's model (ADR-0032): what a blueprint is drawn as, before any
// layout or React Flow. The catalog's topology, the composite designer and a
// request's run all build one of these from the blueprint and draw it with
// <Canvas>; the list view reads the same model.
import type { FieldError, StepStatus } from "@/lib/api/types";

/** TB: top to bottom (a composite's steps); LR: left to right (a VM item's topology). */
export type CanvasDirection = "TB" | "LR";

/** A node's live status in a run: a step's, plus undone (rolled back after a failure). */
export type CanvasStatus = StepStatus | "undone";

export type CanvasNodeKind =
  | "inputs" // a composite's form: where bindings to `inputs.` come from
  | "end" // a composite's result
  | "step" // a composite's step
  | "component" // a VM item's group of identical VMs
  | "network" // a vSphere network an adapter is on
  | "segment" // an NSX segment made for each deployment
  | "security_group" // an NSX group made for each deployment
  | "nsx_group" // an existing NSX group, or groups chosen in the form
  | "any"; // any source, in a firewall rule

/** A line of detail under a node's title. */
export interface CanvasLine {
  label?: string;
  text: string;
  mono?: boolean;
}

export interface CanvasNode {
  /** Stable: `step:<key>`, `component:<key>`, `segment:<key>`, `sg:<key>`, `nsx:<id>`, `network:<expr>`, `inputs`, `end`, `any`. */
  id: string;
  kind: CanvasNodeKind;
  title: string;
  /** The key or name, in monospace. */
  subtitle?: string;
  /** A short tag before the title: what the step runs, a component's count. */
  badge?: string;
  lines: CanvasLine[];
  /** How it relates to the others, in words: the list view and screen readers say these. */
  relations: string[];
  /** The structural checks' messages about it. */
  problems: string[];
  /** The parallel group it runs in. */
  group?: string;
  /** Where it is in the definition (a JSON pointer, as the server's problems give them). */
  pointer?: string;
}

/** A composite's parallel group: a frame around steps that run at the same time. */
export interface CanvasGroup {
  id: string;
  label: string;
  nodes: string[];
  problems: string[];
  pointer?: string;
}

export type CanvasEdgeKind =
  | "sequence" // runs after
  | "network" // a component's adapter on a network
  | "member" // a component's VMs in a group
  | "rule"; // a firewall rule into a group

export interface CanvasEdge {
  id: string;
  /** A node's or a group's ID. */
  source: string;
  target: string;
  kind: CanvasEdgeKind;
  /** Drawn on the edge. */
  label?: string;
  /** Said by screen readers. */
  description: string;
  /** Into a step that runs only when its condition holds. */
  conditional?: boolean;
  /** A rule that drops or rejects. */
  deny?: boolean;
}

export interface CanvasGraph {
  kind: "composite" | "vm";
  direction: CanvasDirection;
  /** In reading order: Tab, Home and End, and the list view follow it. */
  nodes: CanvasNode[];
  edges: CanvasEdge[];
  groups: CanvasGroup[];
  /** Every problem found, with its pointer. */
  problems: FieldError[];
}

/** The node a pointer is about: the one whose pointer is its longest prefix. */
export function nodeForPointer(graph: CanvasGraph, pointer: string): CanvasNode | undefined {
  let best: CanvasNode | undefined;
  for (const n of graph.nodes) {
    if (!n.pointer || !within(pointer, n.pointer)) continue;
    if (!best || n.pointer.length > (best.pointer?.length ?? 0)) best = n;
  }
  return best;
}

/** Whether pointer is at or under prefix. */
export function within(pointer: string, prefix: string): boolean {
  return pointer === prefix || pointer.startsWith(prefix + "/");
}

const statusWords: Record<CanvasStatus, string> = {
  pending: "Not started",
  running: "Running",
  done: "Done",
  failed: "Failed",
  skipped: "Skipped",
  cancelled: "Cancelled",
  undone: "Undone",
};

export const statusLabel = (s: CanvasStatus) => statusWords[s];

/** Everything a node shows, as one sentence list: its accessible name. */
export function describeNode(n: CanvasNode, status?: CanvasStatus): string {
  const parts = [[n.badge, n.title].filter(Boolean).join(" ")];
  if (n.subtitle && n.subtitle !== n.title) parts.push(n.subtitle);
  for (const l of n.lines) parts.push(l.label ? `${l.label}: ${l.text}` : l.text);
  parts.push(...n.relations);
  if (status) parts.push(statusLabel(status));
  if (n.problems.length > 0)
    parts.push(`${n.problems.length === 1 ? "1 problem" : `${n.problems.length} problems`}: ${n.problems.join(" ")}`);
  return parts.join(". ");
}
