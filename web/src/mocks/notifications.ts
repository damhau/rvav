// Notifications and their preferences in the mock backend (S11, ADR-0023):
// the centre's list, read or not, and the channels per kind. Email is set
// up; Teams finds nobody; Slack isn't set up.
import { http, HttpResponse } from "msw";
import type { components } from "@/lib/api/schema";
import { seesDeployment } from "./listing";
import { MOCK_LOCALE, type Db } from "./seed";

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
  "step_message",
];

/** What is wrong with preferences to save, as the server's SetNotificationPreferences. */
export function preferenceProblems(b: S["NotificationPreferencesWrite"]): S["FieldError"][] {
  const errors: S["FieldError"][] = [];
  if (b.locale !== "en" && b.locale !== "fr") errors.push({ field: "/locale", code: "invalid", message: "en or fr." });
  const seen = new Set<string>();
  b.rules.forEach((r, i) => {
    const at = `/rules/${i}`;
    if (!kinds.includes(r.kind))
      return void errors.push({
        field: `${at}/kind`,
        code: "invalid",
        message: `${r.kind} is not a kind of notification.`,
      });
    if (seen.has(r.kind))
      return void errors.push({ field: `${at}/kind`, code: "duplicate", message: "One rule per kind." });
    seen.add(r.kind);
    r.channels.forEach((ch, j) => {
      const field = `${at}/channels/${j}`;
      if (ch === "in_app") errors.push({ field, code: "invalid", message: "In the app is always on." });
      else if (!["email", "teams", "slack"].includes(ch))
        errors.push({ field, code: "invalid", message: `${ch} is not a channel.` });
    });
  });
  return errors;
}

function defaults(kind: Kind): Channel[] {
  if (kind === "approval_needed" || kind === "step_message") return ["email", "teams", "slack"];
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
      link: `/approvals/${db.approvals[0]?.id ?? "apr_mock"}`,
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
  d.notifications = { items, locale: db.me.locale ?? "en", rules: {}, seq: 4 };
  return d.notifications;
}

/** One language for the app and what is sent, as on the server (ADR-0061). */
function choose(db: Db, locale: "en" | "fr") {
  db.me.locale = locale;
  state(db).locale = locale;
  try {
    localStorage.setItem(MOCK_LOCALE, locale);
  } catch {
    // Kept until the page reloads.
  }
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

/**
 * The seeded notifications are everyone's; the server notifies only those
 * concerned. Kept: those about a deployment or request the viewer sees,
 * which owners only takes away (ADR-0045).
 */
function concerns(db: Db, n: S["Notification"]): boolean {
  const [, kind, key] = n.link?.split("/") ?? [];
  const me = db.me.user.id;
  if (kind === "deployments") {
    const d = db.deployments.find((x) => x.id === key);
    return !d || seesDeployment(db, d);
  }
  if (kind === "requests") {
    const r = db.requests.find((x) => x.request.number === key || x.request.id === key)?.request;
    return (
      !r ||
      seesDeployment(db, {
        project: r.project,
        owner: { id: r.requester.id === me || r.on_behalf_of?.id === me ? me : r.requester.id },
      })
    );
  }
  return true;
}

export function notificationHandlers(db: Db, base: string, wait: () => Promise<void>) {
  const mine = () => state(db).items.filter((n) => concerns(db, n));
  const unread = () => mine().filter((n) => !n.read_at).length;
  return [
    http.get(`${base}/me/notifications`, async ({ request }) => {
      await wait();
      const url = new URL(request.url);
      const onlyUnread = url.searchParams.get("unread") === "true";
      const limit = Number(url.searchParams.get("limit") ?? 50);
      const start = Number(url.searchParams.get("cursor") ?? 0);
      const all = mine().filter((n) => !onlyUnread || !n.read_at);
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
      for (const n of mine()) if (!n.read_at && (b.all || b.ids?.includes(n.id))) n.read_at = now;
      return HttpResponse.json({ unread: unread() });
    }),
    http.get(`${base}/me/notification-preferences`, async () => {
      await wait();
      return HttpResponse.json(preferences(db));
    }),
    http.put(`${base}/me/notification-preferences`, async ({ request }) => {
      await wait();
      const b = (await request.json()) as S["NotificationPreferencesWrite"];
      const errors = preferenceProblems(b);
      if (errors.length) return problem(422, "Some inputs need attention", errors);
      const st = state(db);
      choose(db, b.locale);
      st.rules = Object.fromEntries(b.rules.map((r) => [r.kind, r.channels as Channel[]]));
      return HttpResponse.json(preferences(db));
    }),
    http.put(`${base}/me/locale`, async ({ request }) => {
      await wait();
      const b = (await request.json()) as S["MyLocale"];
      if (b.locale !== "en" && b.locale !== "fr")
        return problem(422, "Some inputs need attention", [
          { field: "/locale", code: "invalid", message: "en or fr." },
        ]);
      choose(db, b.locale);
      return HttpResponse.json<S["MyLocale"]>({ locale: b.locale });
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
