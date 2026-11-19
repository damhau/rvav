import type { TemplateChannel, TemplateLocale } from "@/lib/api/types";

export const channelLabel: Record<TemplateChannel, string> = { email: "Email", teams: "Teams", slack: "Slack" };

export const localeLabel: Record<TemplateLocale, string> = { en: "English", fr: "French" };

export const channels: TemplateChannel[] = ["email", "teams", "slack"];
export const locales: TemplateLocale[] = ["en", "fr"];
