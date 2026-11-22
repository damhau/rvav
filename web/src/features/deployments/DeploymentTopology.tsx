// A deployment's Topology tab (ADR-0032): what it holds, on the canvas,
// read only. Selecting a VM opens its properties; a component drawn as one
// node lists its VMs under the canvas. Loaded only for the tab, so React
// Flow stays out of the page's other tabs.
import { useQuery } from "@tanstack/react-query";
import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { Canvas, NodeDetails } from "@/components/canvas";
import type { CanvasNode } from "@/components/canvas/graph";
import { Panel } from "@/components/ui/panel";
import type { DeploymentDetail, VmResource } from "@/lib/api/types";
import { q } from "@/lib/query";
import { deploymentGraph, vmNodeId, vmsByComponent } from "./topology";

function VmButton({ vm, onOpen }: { vm: VmResource; onOpen: (vm: VmResource) => void }) {
  const { t } = useTranslation("deployments");
  return (
    <button
      type="button"
      onClick={() => onOpen(vm)}
      aria-label={t("vms.properties", { name: vm.name })}
      className="text-left font-mono text-accent underline underline-offset-2"
    >
      {vm.name}
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
  const { t, i18n } = useTranslation("deployments");
  const byComponent = useMemo(() => vmsByComponent(d), [d]);
  const request = d.created_by_request_id;
  // Only a deployment of several components, or with custom resources, has titles and a build order to learn from it.
  const topology = useQuery({
    ...q.requestTopology(request ?? ""),
    enabled: Boolean(request) && (byComponent.size > 1 || d.resources.some((r) => r.type === "custom")),
    retry: false,
  });
  const graph = useMemo(
    () => deploymentGraph(d, topology.data?.topology),
    // eslint-disable-next-line react-hooks/exhaustive-deps -- deploymentGraph writes its words in the language shown
    [d, topology.data, i18n.language],
  );
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
            {t("topology.showProperties", { name: vm.name })}
          </button>
        )}
        {grouped && (
          <section aria-label={t("topology.vmsOf", { title: n.title })} className="flex flex-col gap-1 text-secondary">
            <span className="text-muted">{t("topology.openVm")}</span>
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
    <Panel title={t("topology.title")} description={t("topology.lead")}>
      {graph.nodes.length === 0 ? (
        <p className="text-secondary text-muted">{t("topology.empty")}</p>
      ) : (
        <Canvas
          graph={graph}
          label={t("topology.label", { name: d.name })}
          selected={selected}
          onSelect={select}
          renderDetails={details}
        />
      )}
    </Panel>
  );
}
