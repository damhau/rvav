import { createFileRoute } from "@tanstack/react-router";
import { SubscriptionPage } from "@/features/admin/extensibility/SubscriptionPage";

export const Route = createFileRoute("/admin/subscriptions/$subscriptionId")({
  component: function SubscriptionRoute() {
    const { subscriptionId } = Route.useParams();
    return <SubscriptionPage subscriptionId={subscriptionId} />;
  },
});
