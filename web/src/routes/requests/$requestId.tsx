import { createFileRoute } from "@tanstack/react-router";
import { RequestDetailPage } from "@/features/requests/RequestDetailPage";
import { q } from "@/lib/query";

export const Route = createFileRoute("/requests/$requestId")({
  loader: ({ context, params }) => {
    // Start loading before the component renders; the page shows skeletons until then.
    void context.queryClient.prefetchQuery(q.request(params.requestId));
  },
  component: function RequestRoute() {
    const { requestId } = Route.useParams();
    return <RequestDetailPage requestKey={requestId} />;
  },
});
