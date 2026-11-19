import { createFileRoute } from "@tanstack/react-router";
import { ResourceTypesPage } from "@/features/admin/extensibility/ResourceTypesPage";

export const Route = createFileRoute("/admin/resource-types")({
  component: ResourceTypesPage,
});
