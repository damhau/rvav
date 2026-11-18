import { createFileRoute, redirect } from "@tanstack/react-router";
import { IntegrationPage } from "@/features/admin/IntegrationPage";

export const Route = createFileRoute("/admin/integrations/$kind")({
  // The console's settings are on vCenter's page.
  beforeLoad: ({ params }) => {
    if (params.kind !== "console") return;
    // TanStack Router's redirect is thrown, as its documentation does.
    // eslint-disable-next-line @typescript-eslint/only-throw-error
    throw redirect({ to: "/admin/integrations/$kind", params: { kind: "vcenter" } });
  },
  component: function IntegrationRoute() {
    const { kind } = Route.useParams();
    return <IntegrationPage kind={kind} />;
  },
});
