import { createFileRoute } from "@tanstack/react-router";
import { CatalogPage, type CatalogSearch } from "@/features/catalog/CatalogPage";
import { catalogKinds } from "@/features/catalog/facts";
import { optEnum, optString } from "../-search";

export const Route = createFileRoute("/catalog/")({
  validateSearch: (s: Record<string, unknown>): CatalogSearch => ({
    category: optString(s.category, 40),
    q: optString(s.q),
    type: optEnum(s.type, catalogKinds),
  }),
  component: function CatalogRoute() {
    return <CatalogPage search={Route.useSearch()} />;
  },
});
