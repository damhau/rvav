import { createFileRoute } from "@tanstack/react-router";
import { ApprovalsPage, type ApprovalsSearch } from "@/features/approvals/ApprovalsPage";
import { q } from "@/lib/query";
import { optEnum, optString } from "./-search";

export const Route = createFileRoute("/approvals")({
  validateSearch: (s: Record<string, unknown>): ApprovalsSearch => ({
    id: optString(s.id, 64),
    tab: optEnum(s.tab, ["decided"] as const),
  }),
  loader: ({ context }) => {
    void context.queryClient.prefetchQuery(q.approvals(["pending"]));
  },
  component: function ApprovalsRoute() {
    return <ApprovalsPage search={Route.useSearch()} />;
  },
});
