// What the item builds, on the canvas (D1.2, ADR-0032): the request form
// draws the topology its form carries (a VM item's components, networks,
// segments and groups; a composite's steps), with what the preview says of
// the values as they are: how many VMs each component builds and their
// names. Loaded with the form only for an item it has something to draw for,
// so React Flow stays out of the other pages.
import { useMemo } from "react";
import { Canvas, type CanvasGraph, type CanvasLine, type CanvasNode } from "@/components/canvas";
import type { PreviewComponent } from "@/lib/api/types";

const vms = (n: number) => (n === 0 ? "No VMs" : n === 1 ? "1 VM" : `${n} VMs`);

const names = (components: PreviewComponent[]): CanvasLine[] => {
  const all = components.flatMap((c) => c.names);
  return all.length > 0 ? [{ label: all.length > 1 ? "Names" : "Name", text: all.join(", "), mono: true }] : [];
};

/**
 * The graph with the preview's counts and names: a component's badge its
 * count rather than the expression it comes from, a composite's step what
 * the item it requests builds. Nodes the preview says nothing of stay.
 */
export function withPreview(graph: CanvasGraph, components: PreviewComponent[] | undefined): CanvasGraph {
  if (!components || components.length === 0) return graph;
  const node = (n: CanvasNode): CanvasNode => {
    if (graph.kind === "vm" && n.kind === "component") {
      const c = components.find((x) => `component:${x.key}` === n.id);
      if (!c) return n;
      return { ...n, badge: vms(c.count), lines: [...n.lines.filter((l) => l.label !== "Count"), ...names([c])] };
    }
    if (graph.kind === "composite" && n.kind === "step") {
      const key = n.id.slice("step:".length);
      const built = components.filter((x) => x.key.startsWith(`${key}/`));
      if (built.length === 0) return n;
      const total = built.reduce((sum, c) => sum + c.count, 0);
      return { ...n, lines: [...n.lines, { label: "Builds", text: vms(total) }, ...names(built)] };
    }
    return n;
  };
  return { ...graph, nodes: graph.nodes.map(node) };
}

export default function RequestTopology({
  graph,
  name,
  components,
}: {
  graph: CanvasGraph;
  name: string;
  components: PreviewComponent[] | undefined;
}) {
  const drawn = useMemo(() => withPreview(graph, components), [graph, components]);
  const composite = graph.kind === "composite";
  return (
    <section aria-labelledby="request-topology" className="min-w-0 rounded-lg border border-line bg-surface">
      <h2 id="request-topology" className="border-b border-line px-4 py-2.5 text-[13px] font-semibold">
        {composite ? "Steps, in the order they run" : "Topology"}
      </h2>
      <div className="p-4">
        <Canvas graph={drawn} label={`${composite ? "Steps" : "Topology"} of ${name}`} />
      </div>
    </section>
  );
}
