import { createFileRoute } from "@tanstack/react-router";
import { UpdatePage } from "@/features/deployments/UpdatePage";
import { q } from "@/lib/query";

export const Route = createFileRoute("/deployments/$deploymentId_/update")({
  loader: ({ context, params }) => {
    void context.queryClient.prefetchQuery(q.deployment(params.deploymentId));
  },
  component: function UpdateRoute() {
    const { deploymentId } = Route.useParams();
    return <UpdatePage deploymentId={deploymentId} />;
  },
});
