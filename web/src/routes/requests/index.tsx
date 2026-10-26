import { createFileRoute } from "@tanstack/react-router";
import { RequestsPage, type RequestsSearch } from "@/features/requests/RequestsPage";
import { optBool, optEnum, optString } from "../-search";

export const Route = createFileRoute("/requests/")({
  validateSearch: (s: Record<string, unknown>): RequestsSearch => ({
    status: optEnum(s.status, ["open", "pending_approval", "failed", "succeeded"] as const),
    mine: optBool(s.mine),
    q: optString(s.q),
  }),
  component: function RequestsRoute() {
    return <RequestsPage search={Route.useSearch()} />;
  },
});
