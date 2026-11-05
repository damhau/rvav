// The canvas as a list (ADR-0032): the same model in reading order, for
// screen readers and anyone who would rather read than look. A composite's
// steps are an ordered list, a parallel group a list within it; each node is
// a button, as on the graph, followed by everything the graph shows of it.
import { TriangleAlert } from "lucide-react";
import { useId } from "react";
import { cn } from "@/lib/utils";
import type { CanvasGraph, CanvasGroup, CanvasNode, CanvasStatus } from "./graph";
import { statusLabel, subtitleOf } from "./graph";
import { NodeIcon, StatusIcon } from "./nodes";

interface ListProps {
  graph: CanvasGraph;
  label: string;
  status?: Partial<Record<string, CanvasStatus>>;
  selected: string | null;
  onSelect: (id: string) => void;
}

function Problems({ problems }: { problems: string[] }) {
  if (problems.length === 0) return null;
  return (
    <ul className="mt-1 flex flex-col gap-0.5 text-warn">
      {problems.map((p, i) => (
        <li key={i} className="flex items-start gap-1">
          <TriangleAlert className="mt-0.5 size-3 shrink-0" aria-hidden />
          <span>{p}</span>
        </li>
      ))}
    </ul>
  );
}

/** What a node shows, in full: its lines, how it relates to the others, its problems. */
export function NodeFacts({ node }: { node: CanvasNode }) {
  const labelled = node.lines.filter((l) => l.label);
  return (
    <>
      {subtitleOf(node) && <div className="font-mono text-faint">{node.subtitle}</div>}
      {labelled.length > 0 && (
        <dl className="mt-0.5 grid grid-cols-[auto_1fr] gap-x-2">
          {labelled.map((l, i) => (
            <div key={i} className="contents">
              <dt className="text-faint">{l.label}</dt>
              <dd className={cn("break-words", l.mono && "font-mono")}>{l.text}</dd>
            </div>
          ))}
        </dl>
      )}
      {node.lines
        .filter((l) => !l.label)
        .map((l, i) => (
          <div key={i} className={cn("break-words", l.mono && "font-mono")}>
            {l.text}
          </div>
        ))}
      {node.relations.length > 0 && (
        <ul className="mt-0.5 text-muted">
          {node.relations.map((r, i) => (
            <li key={i}>{r}</li>
          ))}
        </ul>
      )}
      <Problems problems={node.problems} />
    </>
  );
}

function Item({
  node,
  status,
  selected,
  onSelect,
}: {
  node: CanvasNode;
  status?: CanvasStatus;
  selected: boolean;
  onSelect: (id: string) => void;
}) {
  return (
    <li
      className={cn(
        "rounded-md border border-line bg-surface px-2.5 py-2 text-xs",
        node.problems.length > 0 && "border-warn",
        selected && "ring-2 ring-accent",
      )}
    >
      <button
        type="button"
        aria-pressed={selected}
        onClick={() => onSelect(node.id)}
        className="flex w-full items-center gap-1.5 rounded-sm text-left font-medium hover:underline"
      >
        <NodeIcon node={node} />
        {node.badge && (
          <>
            <span className="text-[10px] font-medium tracking-wide text-faint uppercase">{node.badge}</span>{" "}
          </>
        )}
        <span className="min-w-0 flex-1">{node.title}</span>{" "}
        {status && (
          <span className="flex items-center gap-1 font-normal text-muted">
            <StatusIcon status={status} />
            <span aria-hidden>{statusLabel(status)}</span>
          </span>
        )}
      </button>
      <NodeFacts node={node} />
    </li>
  );
}

export function CanvasList({ graph, label, status, selected, onSelect }: ListProps) {
  const uid = useId();
  const byId = new Map(graph.nodes.map((n) => [n.id, n]));
  const groups = new Map(graph.groups.map((g) => [g.id, g]));
  const slots: ({ group: CanvasGroup } | { node: CanvasNode })[] = [];
  const seen = new Set<string>();
  for (const n of graph.nodes) {
    const group = n.group ? groups.get(n.group) : undefined;
    if (!group) slots.push({ node: n });
    else if (!seen.has(group.id)) {
      seen.add(group.id);
      slots.push({ group });
    }
  }
  const item = (n: CanvasNode) => (
    <Item key={n.id} node={n} status={status?.[n.id]} selected={selected === n.id} onSelect={onSelect} />
  );
  const Outer = graph.kind === "composite" ? "ol" : "ul";
  return (
    <Outer aria-label={label} className="flex flex-col gap-1.5">
      {slots.map((s) =>
        "group" in s ? (
          <li
            key={s.group.id}
            className={cn(
              "rounded-md border border-dashed p-2",
              s.group.problems.length > 0 ? "border-warn" : "border-line-strong",
            )}
          >
            <span id={`${uid}-${s.group.id}`} className="text-[11px] text-muted">
              {s.group.label}
            </span>
            <Problems problems={s.group.problems} />
            <ul aria-labelledby={`${uid}-${s.group.id}`} className="mt-1 flex flex-col gap-1.5">
              {s.group.nodes.flatMap((id) => {
                const n = byId.get(id);
                return n ? [item(n)] : [];
              })}
            </ul>
          </li>
        ) : (
          item(s.node)
        ),
      )}
    </Outer>
  );
}
