import { createFileRoute } from "@tanstack/react-router";
import { SubscriptionPage } from "@/features/admin/extensibility/SubscriptionPage";
import { statusKeys, type RunsSearch } from "@/features/admin/extensibility/runs";
import { optEnum } from "../../-search";

export const Route = createFileRoute("/admin/subscriptions/$subscriptionId")({
  validateSearch: (s: Record<string, unknown>): RunsSearch => ({ status: optEnum(s.status, statusKeys) }),
  component: function SubscriptionRoute() {
    const { subscriptionId } = Route.useParams();
    return <SubscriptionPage subscriptionId={subscriptionId} />;
  },
});
