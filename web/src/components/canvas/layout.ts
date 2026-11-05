// The canvas's automatic layout (ADR-0032): dagre's layered layout, in the
// graph's direction. A parallel group is laid out as one node, the size of
// its steps side by side (top to bottom) or stacked (left to right), and its
// steps placed inside it; groups don't nest (ADR-0022), so no compound
// layout is needed. Sizes are fixed by what a node shows, so the layout
// needs no measuring and is the same in a test as in a browser.
import dagre, { type EdgeLabel, type GraphLabel, type NodeLabel } from "@dagrejs/dagre";
import type { CanvasDirection, CanvasGraph, CanvasNode, CanvasNodeKind } from "./graph";

export interface Box {
  x: number;
  y: number;
  width: number;
  height: number;
}

export interface CanvasLayout {
  /** Each node's box, top left: within its group's when it is in one (`parent`), else on the canvas. */
  nodes: Map<string, Box & { parent?: string }>;
  groups: Map<string, Box>;
  /** Each node's box on the canvas, its group's position added: arrow keys move between them. */
  boxes: Map<string, Box>;
  /** Edges within a column (rules between a VM item's groups): drawn as a bracket on the nodes' right. */
  side: Set<string>;
  width: number;
  height: number;
}

export const nodeWidth: Record<CanvasNodeKind, number> = {
  inputs: 200,
  end: 200,
  step: 240,
  component: 240,
  network: 200,
  segment: 200,
  security_group: 200,
  nsx_group: 200,
  any: 140,
};

// A card: 8 px padding and a 1 px border above and below, a 20 px title
// row, then 16 px rows (text-xs): the subtitle, each line, the problems.
const CHROME = 18;
const TITLE = 20;
const ROW = 16;

export function nodeHeight(n: CanvasNode): number {
  const rows = (n.subtitle ? 1 : 0) + n.lines.length + (n.problems.length > 0 ? 1 : 0);
  return CHROME + TITLE + ROW * rows;
}

/** A group's frame: its label row, then its steps with this padding and gap. */
export const FRAME = { label: 22, pad: 12, gap: 16 };

export function layoutGraph(graph: CanvasGraph): CanvasLayout {
  const g = new dagre.graphlib.Graph<GraphLabel, NodeLabel, EdgeLabel>({ multigraph: true });
  const tb = graph.direction === "TB";
  g.setGraph({ rankdir: graph.direction, nodesep: 28, ranksep: tb ? 44 : 80, edgesep: 16, marginx: 16, marginy: 16 });
  g.setDefaultEdgeLabel(() => ({}));

  const size = new Map(graph.nodes.map((n) => [n.id, { width: nodeWidth[n.kind], height: nodeHeight(n) }]));
  const parentOf = new Map<string, string>();
  const nodes = new Map<string, Box & { parent?: string }>();
  for (const grp of graph.groups) {
    const members = grp.nodes.filter((id) => size.has(id));
    let offset = 0;
    let across = 0;
    for (const id of members) {
      const s = size.get(id)!;
      parentOf.set(id, grp.id);
      nodes.set(id, {
        ...s,
        parent: grp.id,
        x: FRAME.pad + (tb ? offset : 0),
        y: FRAME.label + FRAME.pad + (tb ? 0 : offset),
      });
      offset += (tb ? s.width : s.height) + FRAME.gap;
      across = Math.max(across, tb ? s.height : s.width);
    }
    const along = Math.max(0, offset - FRAME.gap) + 2 * FRAME.pad;
    g.setNode(
      grp.id,
      tb
        ? { width: Math.max(along, 160), height: FRAME.label + 2 * FRAME.pad + across }
        : { width: Math.max(across + 2 * FRAME.pad, 160), height: FRAME.label + along },
    );
  }
  for (const n of graph.nodes) if (!parentOf.has(n.id)) g.setNode(n.id, { ...size.get(n.id)! });

  const top = (id: string) => parentOf.get(id) ?? id;
  // A VM item's columns: networks, then components, then the groups they
  // are in. A rule between two of those groups is left out of the layout, so
  // it doesn't push one group a column further (dagre can't keep an edge
  // within a column), and drawn beside them; a rule's other sources (any, an
  // existing NSX group only allowed in) sit before the groups.
  const holds = new Set(graph.edges.filter((e) => e.kind === "member").map((e) => e.target));
  const side = new Set<string>();
  for (const e of graph.edges) {
    const v = top(e.source);
    const w = top(e.target);
    if (v === w || !g.hasNode(v) || !g.hasNode(w)) continue;
    if (e.kind === "rule" && holds.has(e.source) && holds.has(e.target)) {
      side.add(e.id);
      continue;
    }
    // Room for the label, so it doesn't sit on a node.
    const label = e.label ? { width: e.label.length * 6.5 + 12, height: 18, labelpos: "c" as const } : {};
    g.setEdge(v, w, label, e.id);
  }
  dagre.layout(g);

  const groups = new Map<string, Box>();
  const boxes = new Map<string, Box>();
  const topLeft = (id: string): Box => {
    const l = g.node(id);
    const width = l.width;
    const height = l.height;
    return { x: (l.x ?? 0) - width / 2, y: (l.y ?? 0) - height / 2, width, height };
  };
  for (const grp of graph.groups) if (g.hasNode(grp.id)) groups.set(grp.id, topLeft(grp.id));
  for (const n of graph.nodes) {
    const parent = parentOf.get(n.id);
    if (parent) {
      const { parent: _, ...rel } = nodes.get(n.id)!;
      const at = groups.get(parent)!;
      boxes.set(n.id, { ...rel, x: at.x + rel.x, y: at.y + rel.y });
    } else {
      const box = topLeft(n.id);
      nodes.set(n.id, box);
      boxes.set(n.id, box);
    }
  }
  const { width = 0, height = 0 } = g.graph();
  // Room on the right for the brackets.
  return { nodes, groups, boxes, side, width: width + (side.size > 0 ? 24 + 14 * side.size : 0), height };
}

export type ArrowDirection = "up" | "down" | "left" | "right";

/**
 * The node an arrow key moves to: of the nodes wholly that way, those in the
 * nearest row (or column), then the one most in line. In a layered layout
 * that is the next layer, or the next node in a layer.
 */
export function nearest(boxes: Map<string, Box>, from: string, direction: ArrowDirection): string | undefined {
  const a = boxes.get(from);
  if (!a) return undefined;
  const scored: { id: string; gap: number; aside: number }[] = [];
  for (const [id, b] of boxes) {
    if (id === from) continue;
    const gap =
      direction === "down"
        ? b.y - (a.y + a.height)
        : direction === "up"
          ? a.y - (b.y + b.height)
          : direction === "right"
            ? b.x - (a.x + a.width)
            : a.x - (b.x + b.width);
    if (gap < -1) continue;
    const across =
      direction === "down" || direction === "up"
        ? b.x + b.width / 2 - (a.x + a.width / 2)
        : b.y + b.height / 2 - (a.y + a.height / 2);
    scored.push({ id, gap, aside: Math.abs(across) });
  }
  if (scored.length === 0) return undefined;
  const row = Math.min(...scored.map((s) => s.gap)) + 24;
  return scored.filter((s) => s.gap <= row).reduce((best, s) => (s.aside < best.aside ? s : best)).id;
}

export const arrowKeys: Record<string, ArrowDirection> = {
  ArrowUp: "up",
  ArrowDown: "down",
  ArrowLeft: "left",
  ArrowRight: "right",
};

/** Which sides edges leave and arrive at. */
export function sides(direction: CanvasDirection) {
  return direction === "TB"
    ? ({ target: "top", source: "bottom" } as const)
    : ({ target: "left", source: "right" } as const);
}
