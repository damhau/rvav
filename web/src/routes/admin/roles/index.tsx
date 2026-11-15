import { createFileRoute } from "@tanstack/react-router";
import { RolesPage, type RolesTab } from "@/features/admin/access/RolesPage";
import { optEnum } from "../../-search";

export const Route = createFileRoute("/admin/roles/")({
  validateSearch: (s: Record<string, unknown>): { tab?: RolesTab } => ({
    tab: optEnum(s.tab, ["roles", "rvav"] as const),
  }),
  component: function RolesRoute() {
    return <RolesPage tab={Route.useSearch().tab} />;
  },
});
