import { createFileRoute } from "@tanstack/react-router";
import { DeploymentsPage, deploymentSorts, type DeploymentsSearch } from "@/features/deployments/DeploymentsPage";
import { optBool, optEnum, optString } from "../-search";

export const Route = createFileRoute("/deployments/")({
  validateSearch: (s: Record<string, unknown>): DeploymentsSearch => ({
    status: optEnum(s.status, ["ready", "provisioning", "failed", "expired"] as const),
    env: optString(s.env, 40),
    item: optString(s.item, 80),
    template: optString(s.template, 200),
    // Only mine was `mine=true` before the owner filter: such links still work.
    owner: optString(s.owner, 80) ?? (optBool(s.mine) ? "me" : undefined),
    q: optString(s.q),
    expiring: optBool(s.expiring),
    sort: optEnum(s.sort, deploymentSorts),
    layout: optEnum(s.layout, ["table", "cards"] as const),
    project: optEnum(s.project, ["all"] as const),
  }),
  search: {
    // The next address drops `mine`: it would turn Only mine back on.
    middlewares: [
      ({ search, next }) => {
        const { mine: _, ...rest } = next(search) as DeploymentsSearch & { mine?: unknown };
        return rest;
      },
    ],
  },
  component: function DeploymentsRoute() {
    return <DeploymentsPage search={Route.useSearch()} />;
  },
});
