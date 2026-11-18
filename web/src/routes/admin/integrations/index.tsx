import { createFileRoute, redirect } from "@tanstack/react-router";
import { integrationKinds, integrationPage } from "@/features/admin/IntegrationSettings";
import { IntegrationsPage, integrationsTabs } from "@/features/admin/IntegrationsPage";
import { optEnum } from "../../-search";

const tabs = [...integrationsTabs, ...integrationKinds] as const;

export const Route = createFileRoute("/admin/integrations/")({
  validateSearch: (s: Record<string, unknown>): { tab?: (typeof tabs)[number] } => ({ tab: optEnum(s.tab, tabs) }),
  // Before V1.4 each integration was a tab here (?tab=vcenter): its page now.
  beforeLoad: ({ search }) => {
    const kind = integrationKinds.find((k) => k === search.tab);
    if (!kind) return;
    // TanStack Router's redirect is thrown, as its documentation does.
    // eslint-disable-next-line @typescript-eslint/only-throw-error
    throw redirect({ to: "/admin/integrations/$kind", params: { kind: integrationPage(kind) } });
  },
  component: function IntegrationsRoute() {
    const { tab } = Route.useSearch();
    return <IntegrationsPage tab={tab === "checks" ? "checks" : "connections"} />;
  },
});
