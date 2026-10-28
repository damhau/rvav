import { createFileRoute } from "@tanstack/react-router";
import { InfrastructurePage } from "@/features/admin/governance/InfrastructurePage";

export const Route = createFileRoute("/admin/infrastructure")({
  component: InfrastructurePage,
});
