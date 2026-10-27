import { createFileRoute } from "@tanstack/react-router";
import { ConsolePage } from "@/features/deployments/ConsolePage";
import { q } from "@/lib/query";

export const Route = createFileRoute("/deployments/$deploymentId_/console/$resourceId")({
  loader: ({ context, params }) => {
    void context.queryClient.prefetchQuery(q.deployment(params.deploymentId));
  },
  component: function ConsoleRoute() {
    const { deploymentId, resourceId } = Route.useParams();
    return <ConsolePage deploymentId={deploymentId} resourceId={resourceId} />;
  },
});
