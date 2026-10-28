// Notifications and their preferences in the mock backend (S11, ADR-0023):
// the centre's list, read or not, and the channels per kind. Email is set
// up; Teams finds nobody; Slack isn't set up.
import { http, HttpResponse } from "msw";
import type { components } from "@/lib/api/schema";
import type { Db } from "./seed";

type S = components["schemas"];
type Kind = S["NotificationKind"];
type Channel = "email" | "teams" | "slack";

interface NotifState {
  items: S["Notification"][];
  locale: "en" | "fr";
  rules: Partial<Record<Kind, Channel[]>>;
  seq: number;
}

const kinds: Kind[] = [
  "request_submitted",
  "approval_needed",
  "request_approved",
  "request_rejected",
  "request_succeeded",
  "request_failed",
  "request_rolled_back",
  "lease_expiring",
  "lease_expired",
  "owner_changed",
];

function defaults(kind: Kind): Channel[] {
  if (kind === "approval_needed") return ["email", "teams", "slack"];
  if (["request_succeeded", "request_failed", "request_rejected", "lease_expiring", "lease_expired"].includes(kind))
    return ["email"];
  return [];
}

const ago = (minutes: number) => new Date(Date.now() - minutes * 60_000).toISOString();

function state(db: Db): NotifState {
  const d = db as Db & { notifications?: NotifState };
  if (d.notifications) return d.notifications;
  const req = db.requests[0]?.request;
  const dep = db.deployments[0];
  const items: S["Notification"][] = [
    {
      id: "ntf_4",
      kind: "approval_needed",
      title: "Approval needed: RHEL 9 server web-prd-front04",
      body: "Production VM above 32 GB RAM",
      level: "warning",
      link: "/approvals",
      created_at: ago(5),
    },
    {
      id: "ntf_3",
      kind: "request_succeeded",
      title: `Ready: ${req?.title ?? "RHEL 9 server"}`,
      level: "success",
      link: req ? `/requests/${req.number}` : "/requests",
      created_at: ago(40),
    },
    {
      id: "ntf_2",
      kind: "lease_expiring",
      title: `The lease of ${dep?.name ?? "web-dev-app001"} ends in 6 days`,
      body: "Extend it if you still need it.",
      level: "warning",
      link: dep ? `/deployments/${dep.id}` : "/deployments",
      created_at: ago(60 * 26),
    },
    {
      id: "ntf_1",
      kind: "request_submitted",
      title: `Submitted: ${req?.title ?? "RHEL 9 server"}`,
      level: "info",
      link: req ? `/requests/${req.number}` : "/requests",
      created_at: ago(60 * 27),
      read_at: ago(60 * 26),
    },
  ];
  d.notifications = { items, locale: "en", rules: {}, seq: 4 };
  return d.notifications;
}

function problem(status: number, title: string, errors?: S["FieldError"][]) {
  return HttpResponse.json(
    { type: "https://rvav.dev/problems/x", title, status, request_id: "req_mock", ...(errors ? { errors } : {}) },
    { status, headers: { "Content-Type": "application/problem+json" } },
  );
}

function preferences(db: Db): S["NotificationPreferences"] {
  const st = state(db);
  return {
    locale: st.locale,
    rules: kinds.map((kind) => ({
      kind,
      channels: st.rules[kind] ?? defaults(kind),
      default: !(kind in st.rules),
    })),
    channels: [
      { channel: "email", available: true, address: db.me.user.email },
      { channel: "teams", available: false, reason: `${db.me.user.email} is not in the tenant's directory` },
      { channel: "slack", available: false, reason: "Slack is not set up in rvav: ask an administrator." },
    ],
  };
}

export function notificationHandlers(db: Db, base: string, wait: () => Promise<void>) {
  const unread = () => state(db).items.filter((n) => !n.read_at).length;
  return [
    http.get(`${base}/me/notifications`, async ({ request }) => {
      await wait();
      const url = new URL(request.url);
      const onlyUnread = url.searchParams.get("unread") === "true";
      const limit = Number(url.searchParams.get("limit") ?? 50);
      const start = Number(url.searchParams.get("cursor") ?? 0);
      const all = state(db).items.filter((n) => !onlyUnread || !n.read_at);
      const items = all.slice(start, start + limit);
      return HttpResponse.json({
        items,
        unread: unread(),
        next_cursor: start + limit < all.length ? String(start + limit) : null,
      });
    }),
    http.post(`${base}/me/notifications/read`, async ({ request }) => {
      await wait();
      const b = (await request.json()) as S["NotificationsRead"];
      if (!b.all && !(b.ids?.length ?? 0))
        return problem(422, "Some inputs need attention", [
          { field: "/ids", code: "required", message: "Say which, or all." },
        ]);
      const now = new Date().toISOString();
      for (const n of state(db).items) if (!n.read_at && (b.all || b.ids?.includes(n.id))) n.read_at = now;
      return HttpResponse.json({ unread: unread() });
    }),
    http.get(`${base}/me/notification-preferences`, async () => {
      await wait();
      return HttpResponse.json(preferences(db));
    }),
    http.put(`${base}/me/notification-preferences`, async ({ request }) => {
      await wait();
      const b = (await request.json()) as S["NotificationPreferencesWrite"];
      const st = state(db);
      st.locale = b.locale;
      st.rules = Object.fromEntries(b.rules.map((r) => [r.kind, r.channels as Channel[]]));
      return HttpResponse.json(preferences(db));
    }),
    http.post(`${base}/me/notification-preferences/test`, async ({ request }) => {
      await wait();
      const { channel } = (await request.json()) as { channel: Channel };
      if (channel !== "email")
        return HttpResponse.json({ name: channel, ok: false, message: `${channel} can't reach you.` });
      return HttpResponse.json({ name: channel, ok: true, message: `Sent to ${db.me.user.email}` });
    }),
  ];
}
