import { createFileRoute } from "@tanstack/react-router";
import { CatalogItemPage } from "@/features/admin/catalog/CatalogItemPage";

export const Route = createFileRoute("/admin/catalog-items/$itemId")({
  component: function CatalogItemRoute() {
    const { itemId } = Route.useParams();
    return <CatalogItemPage itemId={itemId} />;
  },
});
