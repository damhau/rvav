import { createFileRoute } from "@tanstack/react-router";
import { NotificationTemplatesPage } from "@/features/admin/notifications/NotificationTemplatesPage";

export const Route = createFileRoute("/admin/notification-templates/")({
  component: NotificationTemplatesPage,
});
