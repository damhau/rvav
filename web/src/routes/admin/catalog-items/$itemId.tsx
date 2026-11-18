import { createFileRoute } from "@tanstack/react-router";
import { CatalogItemPage, itemTabs, type ItemTab } from "@/features/admin/catalog/CatalogItemPage";
import { optEnum } from "../../-search";

export const Route = createFileRoute("/admin/catalog-items/$itemId")({
  validateSearch: (s: Record<string, unknown>): { tab?: ItemTab } => ({ tab: optEnum(s.tab, itemTabs) }),
  component: function CatalogItemRoute() {
    const { itemId } = Route.useParams();
    const { tab } = Route.useSearch();
    return <CatalogItemPage itemId={itemId} tab={tab} />;
  },
});
