import { createFileRoute } from "@tanstack/react-router";
import { CatalogItemsPage, catalogTabs, type CatalogTab } from "@/features/admin/CatalogItemsPage";
import { optEnum } from "../../-search";

export const Route = createFileRoute("/admin/catalog-items/")({
  validateSearch: (s: Record<string, unknown>): { tab?: CatalogTab } => ({ tab: optEnum(s.tab, catalogTabs) }),
  component: function CatalogItemsRoute() {
    return <CatalogItemsPage {...Route.useSearch()} />;
  },
});
