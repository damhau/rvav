// A deployment's Topology tab (ADR-0032): what it holds, on the canvas,
// read only. Selecting a VM opens its properties; a component drawn as one
// node lists its VMs under the canvas. Loaded only for the tab, so React
// Flow stays out of the page's other tabs.
import { useQuery } from "@tanstack/react-query";
import { useMemo, useState } from "react";
import { Canvas, NodeDetails } from "@/components/canvas";
import type { CanvasNode } from "@/components/canvas/graph";
import { Panel } from "@/components/ui/panel";
import type { DeploymentDetail, VmResource } from "@/lib/api/types";
import { q } from "@/lib/query";
import { deploymentGraph, vmNodeId, vmsByComponent } from "./topology";

function VmButton({ vm, onOpen }: { vm: VmResource; onOpen: (vm: VmResource) => void }) {
  return (
    <button
      type="button"
      onClick={() => onOpen(vm)}
      className="text-left font-mono text-accent underline underline-offset-2"
    >
      {vm.name}
      <span className="sr-only"> properties</span>
    </button>
  );
}

export default function DeploymentTopology({
  deployment: d,
  onOpen,
}: {
  deployment: DeploymentDetail;
  onOpen: (vm: VmResource) => void;
}) {
  const byComponent = useMemo(() => vmsByComponent(d), [d]);
  const request = d.created_by_request_id;
  // Only a deployment of several components has titles and a build order to learn from it.
  const topology = useQuery({
    ...q.requestTopology(request ?? ""),
    enabled: Boolean(request) && byComponent.size > 1,
    retry: false,
  });
  const graph = useMemo(() => deploymentGraph(d, topology.data?.topology), [d, topology.data]);
  const vms = useMemo(() => new Map([...byComponent.values()].flat().map((v) => [vmNodeId(v.id), v])), [byComponent]);
  const [selected, setSelected] = useState<string | null>(null);
  const select = (id: string | null) => {
    setSelected(id);
    const vm = id ? vms.get(id) : undefined;
    if (vm) onOpen(vm);
  };
  const details = (n: CanvasNode) => {
    const vm = vms.get(n.id);
    const grouped = n.kind === "component" ? byComponent.get(n.id.slice("component:".length)) : undefined;
    return (
      <div className="flex flex-col gap-2">
        <NodeDetails node={n} />
        {vm && (
          <button
            type="button"
            onClick={() => onOpen(vm)}
            className="self-start text-secondary text-accent underline underline-offset-2"
          >
            Show the properties of {vm.name}
          </button>
        )}
        {grouped && (
          <section aria-label={`VMs of ${n.title}`} className="flex flex-col gap-1 text-secondary">
            <span className="text-muted">Open a VM's properties:</span>
            <ul className="flex flex-wrap gap-x-3 gap-y-1">
              {grouped.map((v) => (
                <li key={v.id}>
                  <VmButton vm={v} onOpen={onOpen} />
                </li>
              ))}
            </ul>
          </section>
        )}
      </div>
    );
  };
  return (
    <Panel title="Topology" description="What the deployment holds, as rvav last read it from vCenter and NSX.">
      {graph.nodes.length === 0 ? (
        <p className="text-secondary text-muted">Nothing to draw: the deployment holds no VMs or NSX resources.</p>
      ) : (
        <Canvas
          graph={graph}
          label={`Topology of ${d.name}`}
          selected={selected}
          onSelect={select}
          renderDetails={details}
        />
      )}
    </Panel>
  );
}
