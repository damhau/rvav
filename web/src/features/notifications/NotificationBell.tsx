// The notification centre in the top bar (S11, ADR-0023): the unread
// count, the latest notifications, marked read when opened.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { Bell } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Menu, MenuContent, MenuItem, MenuLabel, MenuSeparator, MenuTrigger } from "@/components/ui/dropdown-menu";
import { api, call } from "@/lib/api/client";
import type { components } from "@/lib/api/schema";
import { q } from "@/lib/query";
import { cn, relative } from "@/lib/utils";

type Notification = components["schemas"]["Notification"];

const dot: Record<Notification["level"], string> = {
  info: "bg-accent",
  success: "bg-ok",
  warning: "bg-warn",
  error: "bg-bad",
};

export function useMarkRead() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: components["schemas"]["NotificationsRead"]) =>
      call(api.POST("/me/notifications/read", { body })),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ["me", "notifications"] }),
  });
}

export function NotificationLine({ n }: { n: Notification }) {
  return (
    <span className="flex min-w-0 items-start gap-2">
      <span
        aria-hidden
        className={cn("mt-1.5 size-1.5 shrink-0 rounded-full", n.read_at ? "bg-transparent" : dot[n.level])}
      />
      <span className="flex min-w-0 flex-col">
        <span className={cn("truncate", !n.read_at && "font-medium")}>{n.title}</span>
        {n.body && <span className="line-clamp-2 text-xs text-muted">{n.body}</span>}
        <span className="text-[11px] text-faint">{relative(n.created_at)}</span>
      </span>
    </span>
  );
}

export function NotificationBell() {
  const navigate = useNavigate();
  const latest = useQuery(q.latestNotifications());
  const markRead = useMarkRead();
  const unread = latest.data?.unread ?? 0;
  const open = (n: Notification) => {
    if (!n.read_at) markRead.mutate({ ids: [n.id] });
    if (n.link) void navigate({ to: n.link });
  };
  return (
    <Menu>
      <MenuTrigger asChild>
        <Button
          variant="secondary"
          size="icon"
          className="relative size-[30px] border-line"
          aria-label={unread > 0 ? `Notifications, ${unread} unread` : "Notifications"}
        >
          <Bell />
          {unread > 0 && (
            <span
              aria-hidden
              className="absolute -top-1 -right-1 min-w-4 rounded-full bg-bad px-1 text-[10px] leading-4 font-semibold text-white"
            >
              {unread > 99 ? "99+" : unread}
            </span>
          )}
        </Button>
      </MenuTrigger>
      <MenuContent className="w-[min(380px,calc(100vw-32px))]">
        <div className="flex items-center justify-between">
          <MenuLabel>Notifications</MenuLabel>
          {unread > 0 && (
            <button
              type="button"
              className="px-2 text-xs text-accent hover:underline"
              onClick={() => markRead.mutate({ all: true })}
            >
              Mark all read
            </button>
          )}
        </div>
        {latest.data?.items.length === 0 && <p className="px-2 py-3 text-xs text-muted">Nothing yet.</p>}
        {latest.data?.items.map((n) => (
          <MenuItem key={n.id} onSelect={() => open(n)}>
            <NotificationLine n={n} />
          </MenuItem>
        ))}
        <MenuSeparator />
        <MenuItem onSelect={() => void navigate({ to: "/notifications" })}>All notifications and preferences</MenuItem>
      </MenuContent>
    </Menu>
  );
}
