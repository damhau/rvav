import { createFileRoute } from "@tanstack/react-router";
import {
  NotificationsPage,
  notificationsTabs,
  type NotificationsTab,
} from "@/features/notifications/NotificationsPage";
import { optEnum } from "./-search";

export const Route = createFileRoute("/notifications")({
  validateSearch: (s: Record<string, unknown>): { tab?: NotificationsTab } => ({
    tab: optEnum(s.tab, notificationsTabs),
  }),
  component: function NotificationsRoute() {
    return <NotificationsPage tab={Route.useSearch().tab} />;
  },
});
