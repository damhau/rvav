import type { components } from "@/lib/api/schema";

type S = components["schemas"];

/** What each kind of notification is, as preferences list them (ADR-0023). */
export const kindLabel: Record<S["NotificationKind"], { label: string; hint: string }> = {
  request_submitted: { label: "Request submitted", hint: "Yours, or one made on your behalf" },
  approval_needed: {
    label: "Approval needed",
    hint: "A request waits for your decision, or was delegated or escalated to you",
  },
  request_approved: { label: "Request approved", hint: "Your request was approved" },
  request_rejected: { label: "Request rejected", hint: "Your request was rejected, with the comments" },
  request_succeeded: { label: "Request done", hint: "What you asked for is ready, or the action is done" },
  request_failed: { label: "Request failed", hint: "With what went wrong" },
  request_rolled_back: { label: "Request rolled back", hint: "What a failed request built was deleted" },
  lease_expiring: { label: "Lease ending", hint: "A week, then a day, before a deployment's lease ends" },
  lease_expired: { label: "Lease ended", hint: "A deployment's lease ended" },
  owner_changed: { label: "Deployment handed over", hint: "A deployment is now yours" },
};

/** The channels besides the app a kind goes on by default, as the server's (ADR-0023). */
export function defaultChannels(kind: S["NotificationKind"]): ("email" | "teams" | "slack")[] {
  if (kind === "approval_needed") return ["email", "teams", "slack"];
  if (["request_succeeded", "request_failed", "request_rejected", "lease_expiring", "lease_expired"].includes(kind))
    return ["email"];
  return [];
}

export const channelLabel: Record<"email" | "teams" | "slack", string> = {
  email: "Email",
  teams: "Teams",
  slack: "Slack",
};
