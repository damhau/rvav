import { createFileRoute } from "@tanstack/react-router";
import { ItemDetailPage } from "@/features/catalog/ItemDetailPage";

export const Route = createFileRoute("/catalog/$itemId/")({
  component: function ItemRoute() {
    const { itemId } = Route.useParams();
    return <ItemDetailPage itemKey={itemId} />;
  },
});
