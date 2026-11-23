import { createFileRoute } from "@tanstack/react-router";
import {
  InfrastructurePage,
  infrastructureTabs,
  inventoryKinds,
  type InfrastructureTab,
  type InventorySearch,
} from "@/features/admin/governance/InfrastructurePage";
import { optEnum, optString } from "../-search";

export const Route = createFileRoute("/admin/infrastructure")({
  validateSearch: (s: Record<string, unknown>): { tab?: InfrastructureTab } & InventorySearch => ({
    tab: optEnum(s.tab, infrastructureTabs),
    q: optString(s.q),
    kind: optEnum(s.kind, inventoryKinds),
  }),
  component: function InfrastructureRoute() {
    return <InfrastructurePage tab={Route.useSearch().tab} />;
  },
});
