import { createFileRoute } from "@tanstack/react-router";
import { VariablesPage } from "@/features/admin/catalog/VariablesPage";

export const Route = createFileRoute("/admin/variables")({
  component: VariablesPage,
});
