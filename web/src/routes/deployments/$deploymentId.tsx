import { createFileRoute } from "@tanstack/react-router";
import { DeploymentDetailPage, type DeploymentTab } from "@/features/deployments/DeploymentDetailPage";
import { q } from "@/lib/query";
import { optEnum } from "../-search";

export const Route = createFileRoute("/deployments/$deploymentId")({
  validateSearch: (s: Record<string, unknown>): { tab?: DeploymentTab } => ({
    tab: optEnum(s.tab, ["overview", "history", "inputs"] as const),
  }),
  loader: ({ context, params }) => {
    void context.queryClient.prefetchQuery(q.deployment(params.deploymentId));
  },
  component: function DeploymentRoute() {
    const { deploymentId } = Route.useParams();
    const { tab } = Route.useSearch();
    return <DeploymentDetailPage deploymentId={deploymentId} tab={tab ?? "overview"} />;
  },
});
