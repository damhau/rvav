import { createFileRoute } from "@tanstack/react-router";
import { RolesPage, type RolesSearch } from "@/features/admin/access/RolesPage";
import { optEnum, optString } from "../../-search";

export const Route = createFileRoute("/admin/roles/")({
  validateSearch: (s: Record<string, unknown>): RolesSearch => ({
    tab: optEnum(s.tab, ["roles", "rvav", "explain"] as const),
    user: optString(s.user, 64),
    project: optString(s.project, 64),
  }),
  component: function RolesRoute() {
    return <RolesPage {...Route.useSearch()} />;
  },
});
