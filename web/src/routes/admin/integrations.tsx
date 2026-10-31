import { createFileRoute } from "@tanstack/react-router";
import { IntegrationsPage } from "@/features/admin/IntegrationsPage";
import { optString } from "../-search";

export const Route = createFileRoute("/admin/integrations")({
  // tab: the tab open first (the setup checklist's links).
  validateSearch: (s: Record<string, unknown>): { tab?: string } => ({ tab: optString(s.tab) }),
  component: function IntegrationsRoute() {
    return <IntegrationsPage tab={Route.useSearch().tab} />;
  },
});
