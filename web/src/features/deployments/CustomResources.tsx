import { useTranslation } from "react-i18next";
import { KeyValues } from "@/components/common/page";
import { Badge } from "@/components/ui/badge";
import { Panel } from "@/components/ui/panel";
import type { CustomResource, Day2Action } from "@/lib/api/types";
import { ActionsMenu } from "./actions";
import { labelledProperties } from "./properties";

/** A deployment's custom resources (ADR-0056): what their types' workflows or AWX templates made, with their actions. */
export function CustomResourcesPanel({
  resources,
  actions,
  onPick,
}: {
  resources: CustomResource[];
  actions: Day2Action[];
  onPick: (a: Day2Action) => void;
}) {
  const { t } = useTranslation("deployments");
  if (resources.length === 0) return null;
  return (
    <Panel title={t("custom.title")} description={t("custom.lead")} flush>
      <ul className="divide-y divide-line">
        {resources.map((r) => (
          <li key={r.id} aria-labelledby={`res-${r.id}`} className="flex items-start gap-3 px-4 py-3">
            <div className="flex min-w-0 flex-1 flex-col gap-2">
              <div>
                <span className="flex flex-wrap items-center gap-2">
                  <span id={`res-${r.id}`} className="font-medium">
                    {r.name}
                  </span>
                  <Badge>{r.resource_type.title}</Badge>
                </span>
                <span className="font-mono text-caption text-faint">{r.key}</span>
              </div>
              {Object.keys(r.properties).length > 0 && (
                <KeyValues
                  className="text-secondary"
                  items={labelledProperties(r).map((p) => ({
                    label: p.label,
                    value: <span className="font-mono">{p.text}</span>,
                  }))}
                />
              )}
            </div>
            <ActionsMenu
              actions={actions.filter((a) => a.resource_id === r.id)}
              label={t("moreActionsFor", { name: r.name })}
              onPick={onPick}
            />
          </li>
        ))}
      </ul>
    </Panel>
  );
}
