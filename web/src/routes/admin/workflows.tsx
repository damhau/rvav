import { createFileRoute } from "@tanstack/react-router";
import { WorkflowsPage } from "@/features/admin/extensibility/WorkflowsPage";

export const Route = createFileRoute("/admin/workflows")({
  component: WorkflowsPage,
});
