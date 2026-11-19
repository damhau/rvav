import { createFileRoute } from "@tanstack/react-router";
import { NotificationTemplatePage } from "@/features/admin/notifications/NotificationTemplatePage";
import { channels, locales } from "@/features/admin/notifications/templateLabels";
import type { NotificationKind, TemplateChannel, TemplateLocale } from "@/lib/api/types";
import { optEnum } from "../../-search";

export const Route = createFileRoute("/admin/notification-templates/$kind")({
  validateSearch: (s: Record<string, unknown>): { channel?: TemplateChannel; locale?: TemplateLocale } => ({
    channel: optEnum(s.channel, channels),
    locale: optEnum(s.locale, locales),
  }),
  component: function NotificationTemplateRoute() {
    const { kind } = Route.useParams();
    const { channel, locale } = Route.useSearch();
    return <NotificationTemplatePage key={kind} kind={kind as NotificationKind} channel={channel} locale={locale} />;
  },
});
