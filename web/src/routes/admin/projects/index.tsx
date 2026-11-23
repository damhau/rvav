import { createFileRoute } from "@tanstack/react-router";
import {
  ProjectsPage,
  projectsTabs,
  type ProjectsSearch,
  type ProjectsTab,
} from "@/features/admin/governance/ProjectsPage";
import { optBool, optEnum, optString } from "../../-search";

export const Route = createFileRoute("/admin/projects/")({
  validateSearch: (s: Record<string, unknown>): { tab?: ProjectsTab } & ProjectsSearch => ({
    tab: optEnum(s.tab, projectsTabs),
    q: optString(s.q),
    archived: optBool(s.archived),
  }),
  component: function ProjectsRoute() {
    const { tab } = Route.useSearch();
    return <ProjectsPage tab={tab} />;
  },
});
