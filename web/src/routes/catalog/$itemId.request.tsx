import { createFileRoute } from "@tanstack/react-router";
import { RequestFormPage } from "@/features/requests/RequestFormPage";

// ?draft= resumes a saved draft; ?from= asks again for a past request.
export const Route = createFileRoute("/catalog/$itemId/request")({
  validateSearch: (s: Record<string, unknown>): { draft?: string; from?: string } => ({
    draft: typeof s.draft === "string" ? s.draft : undefined,
    from: typeof s.from === "string" ? s.from : undefined,
  }),
  component: function RequestRoute() {
    const { itemId } = Route.useParams();
    const { draft, from } = Route.useSearch();
    return <RequestFormPage itemKey={itemId} draftId={draft} from={from} />;
  },
});
