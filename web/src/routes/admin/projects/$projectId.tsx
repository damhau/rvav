import { createFileRoute } from "@tanstack/react-router";
import { ProjectPage, projectTabs, type ProjectTab } from "@/features/admin/governance/ProjectPage";
import { optEnum } from "../../-search";

export const Route = createFileRoute("/admin/projects/$projectId")({
  validateSearch: (s: Record<string, unknown>): { tab?: ProjectTab } => ({ tab: optEnum(s.tab, projectTabs) }),
  component: function ProjectRoute() {
    const { projectId } = Route.useParams();
    const { tab } = Route.useSearch();
    return <ProjectPage projectId={projectId} tab={tab} />;
  },
});
