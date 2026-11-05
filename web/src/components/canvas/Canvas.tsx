// The canvas (ADR-0032): a blueprint's graph, drawn by React Flow and laid
// out by dagre, or the same as a list. Read only: what it shows comes from
// the graph (blueprintGraph), what changes is the caller's to decide
// through onSelect, and the YAML stays the source of truth.
//
// Keyboard: the graph is one Tab stop, on the node last visited (the first
// at the start). Arrow keys move to the nearest node that way, Home and End
// to the first and last; Enter or Space selects a node (onSelect), Escape
// clears it. Each node's accessible name says everything it shows.
import "@xyflow/react/dist/base.css";
import "./canvas.css";
import {
  Background,
  type Edge,
  MarkerType,
  type Node,
  Panel,
  Position,
  ReactFlow,
  ReactFlowProvider,
  useReactFlow,
} from "@xyflow/react";
import { Maximize, Minus, Plus } from "lucide-react";
import { type ReactNode, useEffect, useMemo, useRef, useState } from "react";
import { cn } from "@/lib/utils";
import { CanvasList, NodeFacts } from "./CanvasList";
import type { CanvasGraph, CanvasNode, CanvasStatus } from "./graph";
import { describeNode, statusLabel } from "./graph";
import { arrowKeys, layoutGraph, nearest, type CanvasLayout } from "./layout";
import { type CardNode, type FrameNode, handleSides, NodeIcon, nodeTypes, sideHandles, StatusIcon } from "./nodes";

export type CanvasView = "graph" | "list";

export interface CanvasProps {
  graph: CanvasGraph;
  /** What it shows, naming its region and its list: "PostgreSQL 16 cluster: steps". */
  label: string;
  /** A run's status of each node, by node ID (`step:<key>`, `end`...). */
  status?: Partial<Record<string, CanvasStatus>>;
  /** The selected node's ID; leave undefined to let the canvas keep it. */
  selected?: string | null;
  /** A node clicked, or Enter or Space on it; null when cleared (Escape). */
  onSelect?: (id: string | null) => void;
  /** The view it opens with (default: the graph); the reader can switch. */
  defaultView?: CanvasView;
  /** Shown under the canvas for the selected node; by default all it shows, in full. Return null for nothing. */
  renderDetails?: (node: CanvasNode) => ReactNode;
  /** Beside the view switch: the caller's own buttons. */
  actions?: ReactNode;
  /** The graph's height in pixels; by default the layout's, between 220 and 560. */
  height?: number;
  className?: string;
}

const ariaLabelConfig = {
  "node.a11yDescription.default": "Arrow keys move between nodes, Enter selects one.",
  "node.a11yDescription.keyboardDisabled": "Arrow keys move between nodes, Enter selects one.",
};

/** The element React Flow draws a node in. */
function nodeElement(root: HTMLElement | null, id: string): HTMLElement | undefined {
  if (!root) return undefined;
  return [...root.querySelectorAll<HTMLElement>(".react-flow__node")].find((el) => el.dataset.id === id);
}

export function Canvas({
  graph,
  label,
  status,
  selected,
  onSelect,
  defaultView = "graph",
  renderDetails,
  actions,
  height,
  className,
}: CanvasProps) {
  const [view, setView] = useState<CanvasView>(defaultView);
  const [own, setOwn] = useState<string | null>(null);
  const controlled = selected !== undefined;
  const current = controlled ? selected : own;
  const selectedNode = graph.nodes.find((n) => n.id === current);
  const select = (id: string | null) => {
    if (!controlled) setOwn(id);
    onSelect?.(id);
  };

  return (
    <section aria-label={label} className={cn("rvav-canvas flex flex-col gap-2", className)}>
      <div className="flex flex-wrap items-center gap-2">
        <div role="group" aria-label="Show as" className="inline-flex rounded-md border border-line bg-surface p-0.5">
          {(["graph", "list"] as const).map((v) => (
            <button
              key={v}
              type="button"
              aria-pressed={view === v}
              onClick={() => setView(v)}
              className={cn(
                "h-6 rounded-sm px-2 text-xs font-medium",
                view === v ? "bg-accent-soft text-accent" : "text-muted hover:text-fg",
              )}
            >
              {v === "graph" ? "Graph" : "List"}
            </button>
          ))}
        </div>
        {view === "graph" && (
          <span className="text-[11px] text-faint">Arrow keys move between nodes; Enter selects one.</span>
        )}
        {actions && <div className="ml-auto flex items-center gap-2">{actions}</div>}
      </div>
      {view === "graph" ? (
        <ReactFlowProvider>
          <Graph
            graph={graph}
            label={label}
            status={status}
            selected={selectedNode?.id ?? null}
            onSelect={select}
            height={height}
          />
        </ReactFlowProvider>
      ) : (
        <CanvasList graph={graph} label={label} status={status} selected={selectedNode?.id ?? null} onSelect={select} />
      )}
      {selectedNode &&
        (renderDetails ? (
          renderDetails(selectedNode)
        ) : (
          <NodeDetails node={selectedNode} status={status?.[selectedNode.id]} />
        ))}
    </section>
  );
}

/** All a node shows, untruncated: the default under the canvas for the selected node. */
export function NodeDetails({ node, status }: { node: CanvasNode; status?: CanvasStatus }) {
  return (
    <div
      aria-label={`Selected: ${node.title}`}
      role="region"
      className="rounded-md border border-line bg-surface p-3 text-xs"
    >
      <div className="flex items-center gap-1.5 text-[13px] font-medium">
        <NodeIcon node={node} />
        {node.badge && <span className="text-[10px] tracking-wide text-faint uppercase">{node.badge}</span>}
        {node.title}
        {status && (
          <span className="ml-auto flex items-center gap-1 text-xs font-normal text-muted">
            <StatusIcon status={status} />
            <span aria-hidden>{statusLabel(status)}</span>
          </span>
        )}
      </div>
      <NodeFacts node={node} />
    </div>
  );
}

function toFlow(
  graph: CanvasGraph,
  layout: CanvasLayout,
  focusable: string | undefined,
  selected: string | null,
  status: Partial<Record<string, CanvasStatus>> | undefined,
): { nodes: Node[]; edges: Edge[] } {
  const s = handleSides(graph.direction);
  // Where edges meet a node, before React Flow has measured its handles.
  const handles = (w: number, h: number) => [
    ...(graph.direction === "TB"
      ? [
          { type: "target" as const, position: s.target, x: w / 2 - 0.5, y: 0, width: 1, height: 1 },
          { type: "source" as const, position: s.source, x: w / 2 - 0.5, y: h - 1, width: 1, height: 1 },
        ]
      : [
          { type: "target" as const, position: s.target, x: 0, y: h / 2 - 0.5, width: 1, height: 1 },
          { type: "source" as const, position: s.source, x: w - 1, y: h / 2 - 0.5, width: 1, height: 1 },
        ]),
    ...[sideHandles.target, sideHandles.source].map((id, i) => ({
      id,
      type: i === 0 ? ("target" as const) : ("source" as const),
      position: Position.Right,
      x: w - 1,
      y: h / 2 - 0.5,
      width: 1,
      height: 1,
    })),
  ];
  const nodes: Node[] = [];
  for (const group of graph.groups) {
    const box = layout.groups.get(group.id);
    if (!box) continue;
    nodes.push({
      id: group.id,
      type: "frame",
      position: { x: box.x, y: box.y },
      width: box.width,
      height: box.height,
      data: { group, direction: graph.direction },
      draggable: false,
      selectable: false,
      focusable: false,
      handles: handles(box.width, box.height),
    } satisfies FrameNode);
  }
  for (const n of graph.nodes) {
    const box = layout.nodes.get(n.id);
    if (!box) continue;
    const st = status?.[n.id];
    nodes.push({
      id: n.id,
      type: "card",
      position: { x: box.x, y: box.y },
      width: box.width,
      height: box.height,
      ...(box.parent ? { parentId: box.parent } : {}),
      data: { node: n, direction: graph.direction, status: st, selected: n.id === selected },
      draggable: false,
      selectable: false,
      focusable: n.id === focusable,
      ariaRole: "button",
      ariaLabel: describeNode(n, st),
      domAttributes: { "aria-pressed": n.id === selected },
      handles: handles(box.width, box.height),
    } satisfies CardNode);
  }
  let bracket = 0;
  const edges: Edge[] = graph.edges.map((e) => ({
    id: e.id,
    source: e.source,
    target: e.target,
    type: "smoothstep",
    // Within a column: a bracket on the right, each further out than the last.
    ...(layout.side.has(e.id)
      ? {
          sourceHandle: sideHandles.source,
          targetHandle: sideHandles.target,
          pathOptions: { offset: 18 + 14 * bracket++ },
        }
      : {}),
    ...(e.label ? { label: e.label, labelBgPadding: [4, 2] as [number, number], labelBgBorderRadius: 3 } : {}),
    ...(e.kind === "sequence" || e.kind === "rule"
      ? { markerEnd: { type: MarkerType.ArrowClosed, width: 16, height: 16 } }
      : {}),
    className: cn(`rvav-edge-${e.kind}`, e.conditional && "rvav-edge-conditional", e.deny && "rvav-edge-deny"),
    ariaLabel: e.description,
    focusable: false,
    selectable: false,
  }));
  return { nodes, edges };
}

function Graph({
  graph,
  label,
  status,
  selected,
  onSelect,
  height,
}: {
  graph: CanvasGraph;
  label: string;
  status?: Partial<Record<string, CanvasStatus>>;
  selected: string | null;
  onSelect: (id: string | null) => void;
  height?: number;
}) {
  const layout = useMemo(() => layoutGraph(graph), [graph]);
  const order = graph.nodes.map((n) => n.id);
  const [visited, setVisited] = useState<string | undefined>(undefined);
  const active = visited && layout.nodes.has(visited) ? visited : order[0];
  const flow = useMemo(
    () => toFlow(graph, layout, active, selected, status),
    [graph, layout, active, selected, status],
  );
  const wrapper = useRef<HTMLDivElement>(null);
  const pending = useRef<string | null>(null);
  const rf = useReactFlow();

  // A node moved to by keyboard: focus it once drawn, and bring it into view.
  useEffect(() => {
    const id = pending.current;
    if (!id) return;
    pending.current = null;
    const el = nodeElement(wrapper.current, id);
    if (!el) return;
    // Not yet focusable if React Flow hasn't drawn the change: it will be.
    if (!el.hasAttribute("tabindex")) el.tabIndex = -1;
    el.focus();
    const b = layout.boxes.get(id);
    const view = wrapper.current?.getBoundingClientRect();
    if (!b || !view || view.width === 0) return;
    const { x, y, zoom } = rf.getViewport();
    const left = b.x * zoom + x;
    const top = b.y * zoom + y;
    const outside = left < 0 || top < 0 || left + b.width * zoom > view.width || top + b.height * zoom > view.height;
    if (outside) void rf.setCenter(b.x + b.width / 2, b.y + b.height / 2, { zoom });
  });

  // A changed graph (an edit in the designer) is fitted again.
  const shape = order.join("|");
  const first = useRef(true);
  useEffect(() => {
    if (first.current) {
      first.current = false;
      return;
    }
    void rf.fitView({ padding: 0.12, maxZoom: 1 });
  }, [shape, rf]);

  const moveTo = (id: string | undefined) => {
    if (!id) return;
    setVisited(id);
    pending.current = id;
    if (id === active) {
      pending.current = null;
      nodeElement(wrapper.current, id)?.focus();
    }
  };

  const onKeyDown = (e: React.KeyboardEvent) => {
    const id = (e.target as HTMLElement).closest<HTMLElement>(".react-flow__node")?.dataset.id;
    if (!id || !layout.nodes.has(id)) return;
    const arrow = arrowKeys[e.key];
    if (arrow) {
      e.preventDefault();
      moveTo(nearest(layout.boxes, id, arrow));
    } else if (e.key === "Home" || e.key === "End") {
      e.preventDefault();
      moveTo(e.key === "Home" ? order[0] : order[order.length - 1]);
    } else if (e.key === "Enter" || e.key === " ") {
      e.preventDefault();
      setVisited(id);
      onSelect(id);
    } else if (e.key === "Escape" && selected) {
      e.preventDefault();
      onSelect(null);
    }
  };

  const h = height ?? Math.min(560, Math.max(220, layout.height + 24));
  return (
    <div ref={wrapper} style={{ height: h }} className="w-full overflow-hidden rounded-md border border-line">
      <ReactFlow
        aria-label={label}
        onKeyDown={onKeyDown}
        nodes={flow.nodes}
        edges={flow.edges}
        nodeTypes={nodeTypes}
        onNodeClick={(_, n) => {
          if (n.type !== "card") return;
          setVisited(n.id);
          onSelect(n.id);
        }}
        nodesDraggable={false}
        nodesConnectable={false}
        nodesFocusable={false}
        edgesFocusable={false}
        elementsSelectable={false}
        deleteKeyCode={null}
        selectionKeyCode={null}
        multiSelectionKeyCode={null}
        panActivationKeyCode={null}
        zoomActivationKeyCode={null}
        zoomOnScroll={false}
        zoomOnDoubleClick={false}
        preventScrolling={false}
        defaultMarkerColor={null}
        minZoom={0.25}
        maxZoom={1.5}
        fitView
        fitViewOptions={{ padding: 0.12, maxZoom: 1 }}
        ariaLabelConfig={ariaLabelConfig}
      >
        <Background gap={18} size={1} />
        <Panel
          position="bottom-left"
          className="flex flex-col overflow-hidden rounded-md border border-line bg-surface shadow-sm"
        >
          <ZoomButton label="Zoom in" onClick={() => void rf.zoomIn()}>
            <Plus />
          </ZoomButton>
          <ZoomButton label="Zoom out" onClick={() => void rf.zoomOut()}>
            <Minus />
          </ZoomButton>
          <ZoomButton label="Fit to view" onClick={() => void rf.fitView({ padding: 0.12, maxZoom: 1 })}>
            <Maximize />
          </ZoomButton>
        </Panel>
      </ReactFlow>
    </div>
  );
}

function ZoomButton({ label, onClick, children }: { label: string; onClick: () => void; children: ReactNode }) {
  return (
    <button
      type="button"
      aria-label={label}
      title={label}
      onClick={onClick}
      className="flex size-7 items-center justify-center text-muted hover:bg-sunken hover:text-fg [&_svg]:size-3.5"
    >
      {children}
    </button>
  );
}
