import { createFileRoute } from "@tanstack/react-router";
import { PropertyGroupsPage } from "@/features/admin/catalog/PropertyGroupsPage";

export const Route = createFileRoute("/admin/property-groups")({
  component: PropertyGroupsPage,
});
