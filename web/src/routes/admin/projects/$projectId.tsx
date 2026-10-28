import { createFileRoute } from "@tanstack/react-router";
import { ProjectPage } from "@/features/admin/governance/ProjectPage";

export const Route = createFileRoute("/admin/projects/$projectId")({
  component: function ProjectRoute() {
    const { projectId } = Route.useParams();
    return <ProjectPage projectId={projectId} />;
  },
});
