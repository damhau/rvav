import { createFileRoute } from "@tanstack/react-router";
import { VraImportPage, type VraTab, vraTabs } from "@/features/admin/VraImportPage";
import { optEnum } from "../-search";

export const Route = createFileRoute("/admin/vra-import")({
  validateSearch: (s: Record<string, unknown>): { tab?: VraTab } => ({ tab: optEnum(s.tab, vraTabs) }),
  component: function VraImportRoute() {
    return <VraImportPage {...Route.useSearch()} />;
  },
});
