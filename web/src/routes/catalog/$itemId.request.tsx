import { createFileRoute } from "@tanstack/react-router";
import { RequestFormPage } from "@/features/requests/RequestFormPage";

export const Route = createFileRoute("/catalog/$itemId/request")({
  component: function RequestRoute() {
    const { itemId } = Route.useParams();
    return <RequestFormPage itemKey={itemId} />;
  },
});
