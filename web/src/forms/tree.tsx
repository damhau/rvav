// A node picked in a hierarchy (F1.2): vSphere folders, directory units,
// any options that name their parent. Keyboard as the WAI-ARIA tree view.
import { ChevronRight } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import type { Option } from "@/lib/api/types";
import { cn, withUnits } from "@/lib/utils";
import { SourceFailed } from "./failed";
import { sourceFailed, useFieldValues } from "./useFieldValues";
import type { WidgetProps } from "./widgets";

interface Node {
  option: Option;
  index: number;
  depth: number;
  parent?: Node;
  children: Node[];
}

/**
 * The options as trees: each under its parent when that one is offered,
 * else at the top, as is an option whose parents loop back to it.
 */
function forest(options: Option[]): Node[] {
  const byValue = new Map<string, Node>(
    options.map((o, index) => [o.value, { option: o, index, depth: 0, children: [] }]),
  );
  const roots: Node[] = [];
  for (const node of byValue.values()) {
    const seen = new Set([node.option.value]);
    let up = node.option.parent;
    while (up !== undefined && byValue.has(up) && !seen.has(up)) {
      seen.add(up);
      up = byValue.get(up)!.option.parent;
    }
    const parent = node.option.parent !== undefined ? byValue.get(node.option.parent) : undefined;
    if (parent && (up === undefined || !seen.has(up))) {
      node.parent = parent;
      parent.children.push(node);
    } else roots.push(node);
  }
  const depths = (nodes: Node[], depth: number) =>
    nodes.forEach((n) => {
      n.depth = depth;
      depths(n.children, depth + 1);
    });
  depths(roots, 0);
  return roots;
}

/** The nodes shown, in order: the children of expanded ones. */
function shown(nodes: Node[], expanded: Set<string>): Node[] {
  return nodes.flatMap((n) => [n, ...(expanded.has(n.option.value) ? shown(n.children, expanded) : [])]);
}

function pathOf(node: Node | undefined): string {
  const labels: string[] = [];
  for (let n = node; n; n = n.parent) labels.unshift(n.option.label);
  return labels.join(" › ");
}

/**
 * A tree: the arrow keys move and open or close, Home and End go to the
 * first and last node shown, a letter to the next node starting with it,
 * Enter or Space picks. A click picks a node and opens it; its arrow opens
 * or closes it. The value is the node's.
 */
export function TreeWidget({
  id,
  name,
  ui,
  value,
  values,
  onChange,
  ctx,
  invalid,
  describedBy,
  readOnly,
}: WidgetProps) {
  const sourced = useFieldValues(name, ui, values, ctx);
  const failed = Boolean(ui.value_source) && sourceFailed(sourced);
  const fetched = sourced.data?.options;
  const loaded = ui.value_source ? fetched : (ui.options ?? []);
  const options = useMemo(() => (ui.value_source ? fetched : ui.options) ?? [], [ui.value_source, ui.options, fetched]);
  const roots = useMemo(() => forest(options), [options]);
  const nodes = useMemo(() => {
    const all = new Map<string, Node>();
    const walk = (list: Node[]) =>
      list.forEach((n) => {
        all.set(n.option.value, n);
        walk(n.children);
      });
    walk(roots);
    return all;
  }, [roots]);
  const picked = typeof value === "string" && value !== "" ? value : undefined;
  const pickedNode = picked !== undefined ? nodes.get(picked) : undefined;

  // Until a node is opened or closed: the picked node's parents, and a lone top node.
  const [opened, setOpened] = useState<Set<string> | null>(null);
  const expanded = useMemo(() => {
    if (opened) return opened;
    const open = new Set<string>();
    for (let n = pickedNode?.parent; n; n = n.parent) open.add(n.option.value);
    if (roots.length === 1) open.add(roots[0]!.option.value);
    return open;
  }, [opened, pickedNode, roots]);
  const visible = useMemo(() => shown(roots, expanded), [roots, expanded]);
  const [focused, setFocused] = useState<string>();
  const active =
    visible.find((n) => n.option.value === focused) ?? visible.find((n) => n.option.value === picked) ?? visible[0];
  const tree = useRef<HTMLUListElement>(null);
  const itemId = (n: Node) => `${id}-node-${n.index}`;

  const valid = Boolean(pickedNode && !pickedNode.option.disabled);
  const stale = loaded !== undefined && picked !== undefined && !valid && !readOnly;
  const onChangeRef = useRef(onChange);
  onChangeRef.current = onChange;
  useEffect(() => {
    if (stale) onChangeRef.current(undefined);
  }, [stale]);

  const focus = (n: Node | undefined) => {
    if (!n) return;
    setFocused(n.option.value);
    document.getElementById(itemId(n))?.focus();
  };
  const toggle = (n: Node, open: boolean) => {
    const next = new Set(expanded);
    if (open) next.add(n.option.value);
    else next.delete(n.option.value);
    setOpened(next);
  };
  const pick = (n: Node) => {
    if (readOnly || n.option.disabled) return;
    onChange(n.option.value);
  };

  const onKeyDown = (e: React.KeyboardEvent) => {
    if (!active) return;
    const at = visible.indexOf(active);
    const open = expanded.has(active.option.value);
    const parent = active.children.length > 0;
    switch (e.key) {
      case "ArrowDown":
        focus(visible[Math.min(at + 1, visible.length - 1)]);
        break;
      case "ArrowUp":
        focus(visible[Math.max(at - 1, 0)]);
        break;
      case "ArrowRight":
        if (parent && !open) toggle(active, true);
        else if (parent) focus(active.children[0]);
        break;
      case "ArrowLeft":
        if (parent && open) toggle(active, false);
        else focus(active.parent);
        break;
      case "Home":
        focus(visible[0]);
        break;
      case "End":
        focus(visible.at(-1));
        break;
      case "Enter":
      case " ":
        pick(active);
        break;
      default: {
        if (e.key.length !== 1 || e.ctrlKey || e.metaKey || e.altKey) return;
        const letter = e.key.toLowerCase();
        const after = [...visible.slice(at + 1), ...visible.slice(0, at + 1)];
        focus(after.find((n) => n.option.label.toLowerCase().startsWith(letter)));
      }
    }
    e.preventDefault();
  };
  const onClick = (e: React.MouseEvent) => {
    const target = e.target as HTMLElement;
    const item = target.closest<HTMLElement>('[role="treeitem"]');
    const n = item?.dataset.value !== undefined ? nodes.get(item.dataset.value) : undefined;
    if (!n) return;
    e.stopPropagation();
    setFocused(n.option.value);
    if (target.closest("[data-toggle]")) toggle(n, !expanded.has(n.option.value));
    else {
      pick(n);
      if (n.children.length > 0 && !expanded.has(n.option.value)) toggle(n, true);
    }
  };

  const failId = `${id}-source`;
  const chosenId = `${id}-chosen`;
  const empty = ui.value_source ? sourced.isSuccess && options.length === 0 : options.length === 0;
  const loading = Boolean(ui.value_source) && sourced.isFetching && !sourced.data && !failed;

  const siblings = (n: Node) => n.parent?.children ?? roots;
  const items = visible.map((n) => {
    const open = expanded.has(n.option.value);
    const hasChildren = n.children.length > 0;
    const o = n.option;
    const detail = o.disabled ? o.disabled_reason : o.meta;
    return (
      <li
        key={o.value}
        id={itemId(n)}
        role="treeitem"
        data-value={o.value}
        aria-level={n.depth + 1}
        aria-setsize={siblings(n).length}
        aria-posinset={siblings(n).indexOf(n) + 1}
        aria-expanded={hasChildren ? open : undefined}
        aria-selected={o.value === picked}
        aria-disabled={o.disabled || undefined}
        tabIndex={n === active ? 0 : -1}
        className={cn(
          "flex items-baseline gap-1.5 py-1 pr-2.5 text-body outline-none focus-visible:ring-2 focus-visible:ring-accent focus-visible:ring-inset",
          o.disabled ? "text-faint" : "cursor-pointer",
          o.value === picked ? "bg-accent-soft font-medium" : !o.disabled && "hover:bg-sunken",
        )}
        style={{ paddingLeft: `${0.5 + n.depth * 1.25}rem` }}
      >
        {hasChildren ? (
          <span data-toggle className="self-center">
            <ChevronRight className={cn("size-3.5 text-muted transition-transform", open && "rotate-90")} aria-hidden />
          </span>
        ) : (
          <span className="w-3.5 shrink-0" />
        )}
        <span className="truncate">{o.label}</span>
        {detail && (
          <span className="shrink-0 font-mono text-caption text-faint">{o.disabled ? detail : withUnits(detail)}</span>
        )}
      </li>
    );
  });

  return (
    <div className="flex flex-col gap-1">
      {visible.length === 0 ? (
        <p
          id={id}
          className="flex h-24 items-center justify-center rounded-md border border-dashed border-line px-3 text-secondary text-muted"
          aria-describedby={[describedBy, failed && failId].filter(Boolean).join(" ") || undefined}
        >
          {loading ? "Loading…" : failed ? "Not loaded" : empty ? "Nothing available" : ""}
        </p>
      ) : (
        <ul
          ref={tree}
          id={id}
          role="tree"
          tabIndex={-1}
          aria-labelledby={`${id}-label`}
          aria-describedby={
            [describedBy, failed && failId, pickedNode && chosenId].filter(Boolean).join(" ") || undefined
          }
          onKeyDown={onKeyDown}
          onClick={onClick}
          onFocus={(e) => {
            if (e.target === tree.current) focus(active);
          }}
          className={cn(
            "flex max-h-72 flex-col overflow-y-auto rounded-md border border-line-strong bg-surface py-1",
            invalid && "border-bad",
          )}
        >
          {items}
        </ul>
      )}
      {pickedNode && (
        <p id={chosenId} className="text-secondary text-muted">
          Chosen: {pathOf(pickedNode)}
        </p>
      )}
      {failed && (
        <SourceFailed
          id={failId}
          message="Couldn't load the options."
          onRetry={() => void sourced.refetch()}
          retrying={sourced.isFetching}
          focusTo={() => document.getElementById(id)}
        />
      )}
    </div>
  );
}
