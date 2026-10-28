import { createFileRoute } from "@tanstack/react-router";
import { PoliciesPage } from "@/features/admin/governance/PoliciesPage";

export const Route = createFileRoute("/admin/policies")({
  component: PoliciesPage,
});
