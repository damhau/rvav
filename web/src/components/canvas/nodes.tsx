// How the canvas draws a node, a parallel group's frame and a status, in the
// app's tokens (light and dark follow them). Shared by the graph and the
// list views.
import { Handle, Position, type Node, type NodeProps } from "@xyflow/react";
import {
  CircleCheck,
  CircleDashed,
  CircleMinus,
  CircleSlash,
  CircleX,
  ClipboardList,
  Flag,
  Globe,
  LoaderCircle,
  Network,
  Package,
  Server,
  Shield,
  ShieldCheck,
  SquareTerminal,
  TriangleAlert,
  Undo2,
  Waypoints,
  Workflow,
} from "lucide-react";
import { cn } from "@/lib/utils";
import type { CanvasDirection, CanvasGroup, CanvasNode, CanvasStatus } from "./graph";
import { statusLabel, subtitleOf } from "./graph";
import { sides } from "./layout";

export function NodeIcon({ node, className }: { node: CanvasNode; className?: string }) {
  const Icon =
    node.kind === "step"
      ? node.badge === "AWX"
        ? SquareTerminal
        : node.badge === "Workflow"
          ? Workflow
          : Package
      : {
          inputs: ClipboardList,
          end: Flag,
          component: Server,
          network: Network,
          segment: Waypoints,
          security_group: Shield,
          nsx_group: ShieldCheck,
          any: Globe,
        }[node.kind];
  return <Icon className={cn("size-3.5 shrink-0 text-muted", className)} aria-hidden />;
}

const statusIcon: Record<CanvasStatus, { icon: React.ElementType; tone: string; spin?: boolean }> = {
  pending: { icon: CircleDashed, tone: "text-faint" },
  running: { icon: LoaderCircle, tone: "text-run", spin: true },
  done: { icon: CircleCheck, tone: "text-ok" },
  failed: { icon: CircleX, tone: "text-bad" },
  skipped: { icon: CircleMinus, tone: "text-faint" },
  cancelled: { icon: CircleSlash, tone: "text-faint" },
  undone: { icon: Undo2, tone: "text-warn" },
};

export function StatusIcon({ status, className }: { status: CanvasStatus; className?: string }) {
  const s = statusIcon[status];
  return (
    <s.icon
      className={cn("size-3.5 shrink-0", s.tone, s.spin && "animate-spin motion-reduce:animate-none", className)}
      aria-label={statusLabel(status)}
      role="img"
    />
  );
}

const statusBorder: Partial<Record<CanvasStatus, string>> = {
  running: "border-run",
  done: "border-ok",
  failed: "border-bad",
  undone: "border-warn",
  skipped: "border-dashed",
  cancelled: "border-dashed",
};

export interface CardData extends Record<string, unknown> {
  node: CanvasNode;
  direction: CanvasDirection;
  status?: CanvasStatus;
  selected: boolean;
}
export type CardNode = Node<CardData, "card">;

export interface FrameData extends Record<string, unknown> {
  group: CanvasGroup;
  direction: CanvasDirection;
}
export type FrameNode = Node<FrameData, "frame">;

const position = { top: Position.Top, bottom: Position.Bottom, left: Position.Left, right: Position.Right };

/** The sides edges arrive at (target) and leave from (source). */
export function handleSides(direction: CanvasDirection) {
  const s = sides(direction);
  return { target: position[s.target], source: position[s.source] };
}

/** The handles of an edge within a column (CanvasLayout.side): both on the right. */
export const sideHandles = { source: "side-out", target: "side-in" };

function Handles({ direction }: { direction: CanvasDirection }) {
  const s = handleSides(direction);
  return (
    <>
      <Handle type="target" position={s.target} isConnectable={false} />
      <Handle type="source" position={s.source} isConnectable={false} />
      <Handle id={sideHandles.target} type="target" position={Position.Right} isConnectable={false} />
      <Handle id={sideHandles.source} type="source" position={Position.Right} isConnectable={false} />
    </>
  );
}

/** A node on the canvas. Its size is the layout's (nodeHeight): rows of fixed height, text cut to fit. */
export function CardView({ data }: NodeProps<CardNode>) {
  const { node, status, selected, direction } = data;
  return (
    <div
      className={cn(
        "flex h-full cursor-pointer flex-col overflow-hidden rounded-md border border-line-strong bg-surface px-2.5 py-2 text-xs text-fg shadow-sm",
        node.problems.length > 0 && "border-warn",
        status && statusBorder[status],
        status === "skipped" && "text-muted",
        selected && "ring-2 ring-accent",
      )}
    >
      <Handles direction={direction} />
      {/* Spaces between the words of separate elements: not drawn (in a flex row, or
          ending a line), but read, so the node's text is its name's words in order,
          "1 VM Web", not "1 VMWeb" (WCAG 2.5.3, describeNode). */}
      <div className="flex h-5 items-center gap-1.5">
        <NodeIcon node={node} />
        {node.badge && (
          <>
            <span className="shrink-0 text-[10px] font-medium tracking-wide text-faint uppercase">
              {node.badge}
            </span>{" "}
          </>
        )}
        <span className="min-w-0 flex-1 truncate font-medium" title={node.title}>
          {node.title}
        </span>{" "}
        {status && (
          // Colour is never the only signal: the status has its word too.
          <span className="flex shrink-0 items-center gap-1 text-[10px] text-muted">
            <StatusIcon status={status} />
            <span aria-hidden>{statusLabel(status)}</span>
          </span>
        )}{" "}
      </div>
      {subtitleOf(node) && (
        <div className="h-4 truncate font-mono text-faint" title={node.subtitle}>
          {node.subtitle}{" "}
        </div>
      )}
      {node.lines.map((l, i) => (
        <div key={i} className="h-4 truncate" title={l.label ? `${l.label}: ${l.text}` : l.text}>
          {l.label && <span className="text-faint">{l.label} </span>}
          <span className={cn(l.mono && "font-mono")}>{l.text}</span>{" "}
        </div>
      ))}
      {node.problems.length > 0 && (
        <div className="flex h-4 items-center gap-1 text-warn" title={node.problems.join(" ")}>
          <TriangleAlert className="size-3 shrink-0" aria-hidden />
          {node.problems.length === 1 ? "1 problem" : `${node.problems.length} problems`}
        </div>
      )}
    </div>
  );
}

/** A parallel group: a frame its steps sit in. */
export function FrameView({ data }: NodeProps<FrameNode>) {
  const { group, direction } = data;
  return (
    <div
      className={cn(
        "h-full rounded-lg border border-dashed bg-sunken/60",
        group.problems.length > 0 ? "border-warn" : "border-line-strong",
      )}
    >
      <Handles direction={direction} />
      <div className="flex h-[22px] items-center gap-1 px-3 text-[11px] text-muted">
        {group.label}
        {group.problems.length > 0 && (
          <span className="flex items-center gap-1 text-warn" title={group.problems.join(" ")}>
            <TriangleAlert className="size-3" aria-hidden />
            {group.problems.length === 1 ? "1 problem" : `${group.problems.length} problems`}
          </span>
        )}
      </div>
    </div>
  );
}

export const nodeTypes = { card: CardView, frame: FrameView };
