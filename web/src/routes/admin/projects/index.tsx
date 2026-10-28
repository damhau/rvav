import { createFileRoute } from "@tanstack/react-router";
import { ProjectsPage } from "@/features/admin/governance/ProjectsPage";

export const Route = createFileRoute("/admin/projects/")({
  component: ProjectsPage,
});
