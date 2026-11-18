import { createFileRoute } from "@tanstack/react-router";
import { PoliciesPage, policiesTabs, type PoliciesTab } from "@/features/admin/governance/PoliciesPage";
import { optEnum } from "../-search";

export const Route = createFileRoute("/admin/policies")({
  validateSearch: (s: Record<string, unknown>): { tab?: PoliciesTab } => ({ tab: optEnum(s.tab, policiesTabs) }),
  component: function PoliciesRoute() {
    return <PoliciesPage tab={Route.useSearch().tab} />;
  },
});
