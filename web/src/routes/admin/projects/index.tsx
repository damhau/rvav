import { createFileRoute } from "@tanstack/react-router";
import { ProjectsPage, projectsTabs, type ProjectsTab } from "@/features/admin/governance/ProjectsPage";
import { optEnum } from "../../-search";

export const Route = createFileRoute("/admin/projects/")({
  validateSearch: (s: Record<string, unknown>): { tab?: ProjectsTab } => ({ tab: optEnum(s.tab, projectsTabs) }),
  component: function ProjectsRoute() {
    const { tab } = Route.useSearch();
    return <ProjectsPage tab={tab} />;
  },
});
