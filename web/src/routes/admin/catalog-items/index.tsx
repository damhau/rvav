import { createFileRoute } from "@tanstack/react-router";
import { CatalogItemsPage, catalogTabs, type CatalogTab } from "@/features/admin/CatalogItemsPage";
import { itemKinds, type ItemsSearch } from "@/features/admin/catalog/ItemsPanel";
import { optEnum, optString } from "../../-search";

export const Route = createFileRoute("/admin/catalog-items/")({
  validateSearch: (s: Record<string, unknown>): { tab?: CatalogTab } & ItemsSearch => ({
    tab: optEnum(s.tab, catalogTabs),
    q: optString(s.q),
    category: optString(s.category, 80),
    state: optEnum(s.state, ["published", "unpublished"] as const),
    kind: optEnum(s.kind, itemKinds),
  }),
  component: function CatalogItemsRoute() {
    return <CatalogItemsPage tab={Route.useSearch().tab} />;
  },
});
