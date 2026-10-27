import { createFileRoute } from "@tanstack/react-router";
import { CatalogItemsPage } from "@/features/admin/CatalogItemsPage";

export const Route = createFileRoute("/admin/catalog-items")({
  component: CatalogItemsPage,
});
