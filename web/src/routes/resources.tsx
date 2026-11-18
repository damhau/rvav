import { createFileRoute } from "@tanstack/react-router";
import { ResourcesPage, vmSorts, type ResourcesSearch } from "@/features/resources/ResourcesPage";
import { optEnum, optString } from "./-search";

export const Route = createFileRoute("/resources")({
  validateSearch: (s: Record<string, unknown>): ResourcesSearch => ({
    env: optString(s.env, 40),
    power: optEnum(s.power, ["on", "off", "suspended"] as const),
    vcenter: optString(s.vcenter, 200),
    cluster: optString(s.cluster, 200),
    network: optString(s.network, 200),
    q: optString(s.q),
    sort: optEnum(s.sort, vmSorts),
    project: optEnum(s.project, ["all"] as const),
  }),
  component: function ResourcesRoute() {
    return <ResourcesPage search={Route.useSearch()} />;
  },
});
