import type { TemplateChannel, TemplateLocale } from "@/lib/api/types";
import { i18n } from "@/lib/i18n";

export const channelLabel = (c: TemplateChannel) => i18n.t(`admin-notifications:channel.${c}`);

/** A template's language, as a button names it. */
export const localeLabel = (l: TemplateLocale) => i18n.t(`admin-notifications:locale.${l}`);

/** A template's language, inside a sentence ("in English", "en anglais"). */
export const localeInText = (l: TemplateLocale) => i18n.t(`admin-notifications:localeInText.${l}`);

export const channels: TemplateChannel[] = ["email", "teams", "slack"];
export const locales: TemplateLocale[] = ["en", "fr"];
