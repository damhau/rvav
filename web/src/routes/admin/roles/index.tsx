import { createFileRoute } from "@tanstack/react-router";
import { RolesPage } from "@/features/admin/access/RolesPage";

export const Route = createFileRoute("/admin/roles/")({
  component: RolesPage,
});
