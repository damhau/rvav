import { createFileRoute } from "@tanstack/react-router";
import { SubscriptionsPage } from "@/features/admin/extensibility/SubscriptionsPage";

export const Route = createFileRoute("/admin/subscriptions/")({
  component: SubscriptionsPage,
});
