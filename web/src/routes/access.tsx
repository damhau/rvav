import { createFileRoute } from "@tanstack/react-router";
import { MyAccessPage } from "@/features/admin/access/AccessExplained";
import { optString } from "./-search";

export const Route = createFileRoute("/access")({
  validateSearch: (s: Record<string, unknown>): { project?: string } => ({ project: optString(s.project, 64) }),
  component: function AccessRoute() {
    return <MyAccessPage project={Route.useSearch().project} />;
  },
});
