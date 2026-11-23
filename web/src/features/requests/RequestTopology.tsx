// What the item builds, on the canvas (D1.2, ADR-0032): the request form
// draws the topology its form carries (a VM item's components, networks,
// segments and groups; a composite's steps), with what the preview says of
// the values as they are: how many VMs each component builds, their names
// and their disks' sizes. Loaded with the form only for an item it has
// something to draw for, so React Flow stays out of the other pages.
import { useMemo } from "react";
import { useTranslation } from "react-i18next";
import { Canvas, type CanvasGraph, type CanvasLine, type CanvasNode } from "@/components/canvas";
import type { PreviewComponent } from "@/lib/api/types";
import { i18n } from "@/lib/i18n";
import { diskParts } from "./ComponentsPreview";

const vms = (n: number) =>
  n === 0 ? i18n.t("requests:topology.noVms") : i18n.t("requests:topology.vms", { count: n });

const names = (components: PreviewComponent[]): CanvasLine[] => {
  const all = components.flatMap((c) => c.names);
  const label = i18n.t(all.length > 1 ? "requests:topology.names" : "requests:topology.name");
  return all.length > 0 ? [{ label, text: all.join(", "), mono: true }] : [];
};

const disksLine = (disks: PreviewComponent["disks"]) =>
  diskParts(disks)
    .map((part, i) => (disks[i]?.storage_policy ? `${part} (${disks[i].storage_policy})` : part))
    .join(", ");

/**
 * The graph with the preview's counts, names and disks: a component's badge
 * its count rather than the expression it comes from, its disks their sizes,
 * a composite's step what the item it requests builds. Nodes the preview
 * says nothing of stay.
 */
export function withPreview(graph: CanvasGraph, components: PreviewComponent[] | undefined): CanvasGraph {
  if (!components || components.length === 0) return graph;
  // The graph is drawn in the language shown (its memo lists i18n.language): its Count and Disks lines have these labels.
  const count = i18n.t("canvas:lines.count");
  const disks = i18n.t("canvas:lines.disks");
  const node = (n: CanvasNode): CanvasNode => {
    if (graph.kind === "vm" && n.kind === "component") {
      const c = components.find((x) => `component:${x.key}` === n.id);
      if (!c) return n;
      const lines = n.lines
        .filter((l) => l.label !== count)
        .map((l) => (l.label === disks && c.disks.length > 0 ? { ...l, text: disksLine(c.disks) } : l));
      return { ...n, badge: vms(c.count), lines: [...lines, ...names([c])] };
    }
    if (graph.kind === "composite" && n.kind === "step") {
      const key = n.id.slice("step:".length);
      const built = components.filter((x) => x.key.startsWith(`${key}/`));
      if (built.length === 0) return n;
      const total = built.reduce((sum, c) => sum + c.count, 0);
      return {
        ...n,
        lines: [...n.lines, { label: i18n.t("requests:topology.builds"), text: vms(total) }, ...names(built)],
      };
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
  const { t } = useTranslation("requests");
  // eslint-disable-next-line react-hooks/exhaustive-deps
  const drawn = useMemo(() => withPreview(graph, components), [graph, components, i18n.language]);
  const composite = graph.kind === "composite";
  return (
    <section aria-labelledby="request-topology" className="min-w-0 rounded-lg border border-line bg-surface">
      <h2 id="request-topology" className="border-b border-line px-4 py-2.5 text-body font-semibold">
        {composite ? t("topology.stepsTitle") : t("topology.topology")}
      </h2>
      <div className="p-4">
        <Canvas graph={drawn} label={t(composite ? "topology.stepsOf" : "topology.topologyOf", { name })} />
      </div>
    </section>
  );
}
