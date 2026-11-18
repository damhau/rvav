import { createFileRoute } from "@tanstack/react-router";
import {
  InfrastructurePage,
  infrastructureTabs,
  type InfrastructureTab,
} from "@/features/admin/governance/InfrastructurePage";
import { optEnum } from "../-search";

export const Route = createFileRoute("/admin/infrastructure")({
  validateSearch: (s: Record<string, unknown>): { tab?: InfrastructureTab } => ({
    tab: optEnum(s.tab, infrastructureTabs),
  }),
  component: function InfrastructureRoute() {
    return <InfrastructurePage tab={Route.useSearch().tab} />;
  },
});
