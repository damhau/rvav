import { createFileRoute } from "@tanstack/react-router";
import { DeploymentsPage, type DeploymentsSearch } from "@/features/deployments/DeploymentsPage";
import { optBool, optEnum, optString } from "../-search";

export const Route = createFileRoute("/deployments/")({
  validateSearch: (s: Record<string, unknown>): DeploymentsSearch => ({
    status: optEnum(s.status, ["ready", "provisioning", "failed"] as const),
    env: optString(s.env, 40),
    q: optString(s.q),
    mine: optBool(s.mine),
    expiring: optBool(s.expiring),
    sort: optEnum(s.sort, [
      "name",
      "-name",
      "created_at",
      "-created_at",
      "lease_expires_at",
      "-lease_expires_at",
    ] as const),
  }),
  component: function DeploymentsRoute() {
    return <DeploymentsPage search={Route.useSearch()} />;
  },
});
