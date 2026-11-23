import { createFileRoute } from "@tanstack/react-router";
import {
  NotificationsPage,
  notificationsTabs,
  type NotificationsSearch,
} from "@/features/notifications/NotificationsPage";
import { optBool, optEnum } from "./-search";

export const Route = createFileRoute("/notifications")({
  validateSearch: (s: Record<string, unknown>): NotificationsSearch => ({
    tab: optEnum(s.tab, notificationsTabs),
    unread: optBool(s.unread),
  }),
  component: function NotificationsRoute() {
    return <NotificationsPage search={Route.useSearch()} />;
  },
});
