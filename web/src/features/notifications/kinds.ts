import { i18n } from "@/lib/i18n";
import type { components } from "@/lib/api/schema";

type S = components["schemas"];

/** What each kind of notification is, as preferences list them (ADR-0023). */
export const kindLabel = (kind: S["NotificationKind"]) => ({
  label: i18n.t(`notifications:kinds.${kind}.label`),
  hint: i18n.t(`notifications:kinds.${kind}.hint`),
});

/** The channels besides the app a kind goes on by default, as the server's (ADR-0023). */
export function defaultChannels(kind: S["NotificationKind"]): ("email" | "teams" | "slack")[] {
  if (kind === "approval_needed" || kind === "step_message") return ["email", "teams", "slack"];
  if (["request_succeeded", "request_failed", "request_rejected", "lease_expiring", "lease_expired"].includes(kind))
    return ["email"];
  return [];
}

export const channelLabel = (channel: "email" | "teams" | "slack") => i18n.t(`notifications:channel.${channel}`);
