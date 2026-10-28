// Notifications (S11, ADR-0023): everything that concerned the caller, read
// or not, and the channels they want each kind on, in their language.
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useEffect, useState } from "react";
import { toast } from "sonner";
import { ErrorState, SkeletonRows } from "@/components/common/feedback";
import { EmptyState, Page, PageHeader, Panel } from "@/components/common/page";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Field } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { api, call } from "@/lib/api/client";
import type { components } from "@/lib/api/schema";
import { q } from "@/lib/query";
import { failed } from "../admin/governance/common";
import { channelLabel, kindLabel } from "./kinds";
import { NotificationLine, useMarkRead } from "./NotificationBell";

type S = components["schemas"];
type Channel = "email" | "teams" | "slack";
const channels: Channel[] = ["email", "teams", "slack"];

function List() {
  const [unread, setUnread] = useState(false);
  const list = useInfiniteQuery(q.notifications(unread));
  const markRead = useMarkRead();
  const items = list.data?.pages.flatMap((p) => p.items) ?? [];
  const count = list.data?.pages[0]?.unread ?? 0;
  return (
    <Panel
      title={count > 0 ? `${count} unread` : "All read"}
      flush
      actions={
        <div className="flex items-center gap-3">
          <label htmlFor="unread-only" className="flex items-center gap-1.5 text-xs">
            <Checkbox id="unread-only" checked={unread} onChange={setUnread} />
            Unread only
          </label>
          <Button size="sm" disabled={count === 0 || markRead.isPending} onClick={() => markRead.mutate({ all: true })}>
            Mark all read
          </Button>
        </div>
      }
    >
      {list.isPending && <SkeletonRows rows={4} cols={2} />}
      {list.isError && <ErrorState error={list.error} onRetry={() => void list.refetch()} />}
      {list.data && items.length === 0 && (
        <EmptyState
          title={unread ? "Nothing unread" : "No notification yet"}
          body="Requests, approvals and leases that concern you show here."
        />
      )}
      {items.length > 0 && (
        <ul aria-label="Notifications" className="divide-y divide-line">
          {items.map((n) => (
            <li key={n.id} className="flex items-start gap-3 px-4 py-2.5 text-[13px]">
              <div className="min-w-0 flex-1">
                {n.link ? (
                  <Link
                    to={n.link}
                    className="hover:text-accent"
                    onClick={() => !n.read_at && markRead.mutate({ ids: [n.id] })}
                  >
                    <NotificationLine n={n} />
                  </Link>
                ) : (
                  <NotificationLine n={n} />
                )}
              </div>
              {!n.read_at && (
                <Button
                  size="sm"
                  variant="ghost"
                  aria-label={`Mark read: ${n.title}`}
                  onClick={() => markRead.mutate({ ids: [n.id] })}
                >
                  Mark read
                </Button>
              )}
            </li>
          ))}
        </ul>
      )}
      {list.hasNextPage && (
        <div className="flex justify-center p-3">
          <Button size="sm" disabled={list.isFetchingNextPage} onClick={() => void list.fetchNextPage()}>
            Load more
          </Button>
        </div>
      )}
    </Panel>
  );
}

function Preferences() {
  const qc = useQueryClient();
  const prefs = useQuery(q.notificationPreferences());
  const [locale, setLocale] = useState<"en" | "fr">("en");
  const [rules, setRules] = useState<Record<string, Channel[]>>({});
  const [touched, setTouched] = useState<Set<string>>(new Set());
  useEffect(() => {
    if (!prefs.data) return;
    setLocale(prefs.data.locale);
    setRules(
      Object.fromEntries(prefs.data.rules.map((r) => [r.kind, r.channels.filter((c) => c !== "in_app") as Channel[]])),
    );
    setTouched(new Set(prefs.data.rules.filter((r) => !r.default).map((r) => r.kind)));
  }, [prefs.data]);
  const save = useMutation({
    mutationFn: () =>
      call(
        api.PUT("/me/notification-preferences", {
          body: {
            locale,
            rules: [...touched].map((kind) => ({ kind: kind as S["NotificationKind"], channels: rules[kind] ?? [] })),
          },
        }),
      ),
    onSuccess: (saved) => {
      qc.setQueryData(q.notificationPreferences().queryKey, saved);
      toast.success("Notification preferences saved");
    },
    onError: (e) => failed(e, "Not saved"),
  });
  const test = useMutation({
    mutationFn: (channel: Channel) => call(api.POST("/me/notification-preferences/test", { body: { channel } })),
    onSuccess: (r) => (r.ok ? toast.success(r.message) : toast.error("Not sent", { description: r.message })),
    onError: (e) => failed(e, "Not sent"),
  });
  if (prefs.isPending) return <SkeletonRows rows={6} cols={4} />;
  if (prefs.isError) return <ErrorState error={prefs.error} onRetry={() => void prefs.refetch()} />;
  const reach = Object.fromEntries(prefs.data.channels.map((c) => [c.channel, c]));
  const toggle = (kind: string, ch: Channel, on: boolean) => {
    setRules((r) => ({ ...r, [kind]: on ? [...(r[kind] ?? []), ch] : (r[kind] ?? []).filter((x) => x !== ch) }));
    setTouched((t) => new Set(t).add(kind));
  };
  return (
    <div className="flex flex-col gap-4">
      <Panel title="Where rvav can reach you">
        <ul className="flex flex-col gap-2 text-[13px]">
          <li className="flex items-center gap-2">
            <Badge tone="ok">On</Badge> <span className="font-medium">In the app</span>
            <span className="text-xs text-muted">Always: everything is here.</span>
          </li>
          {channels.map((ch) => {
            const c = reach[ch];
            return (
              <li key={ch} className="flex flex-wrap items-center gap-2">
                {c?.available ? <Badge tone="ok">Can reach you</Badge> : <Badge tone="neutral">Can't</Badge>}
                <span className="font-medium">{channelLabel[ch]}</span>
                <span className="text-xs text-muted">{c?.available ? c.address : c?.reason}</span>
                {c?.available && (
                  <Button
                    size="sm"
                    variant="ghost"
                    disabled={test.isPending}
                    aria-label={`Send me a test on ${channelLabel[ch]}`}
                    onClick={() => test.mutate(ch)}
                  >
                    Send me a test
                  </Button>
                )}
              </li>
            );
          })}
        </ul>
      </Panel>
      <Panel title="What comes where">
        <form
          className="flex flex-col gap-4"
          onSubmit={(e) => {
            e.preventDefault();
            save.mutate();
          }}
        >
          <Field label="Language of what is sent" htmlFor="notif-locale" className="max-w-xs">
            <Select
              id="notif-locale"
              value={locale}
              onChange={(v) => setLocale(v as "en" | "fr")}
              options={[
                { value: "en", label: "English" },
                { value: "fr", label: "Français" },
              ]}
            />
          </Field>
          <div className="overflow-x-auto">
            <table className="w-full min-w-[620px] border-collapse text-[13px]">
              <thead>
                <tr className="label text-left">
                  <th scope="col" className="border-b border-line py-2 pr-3 font-medium">
                    When
                  </th>
                  <th scope="col" className="border-b border-line px-3 py-2 font-medium">
                    App
                  </th>
                  {channels.map((ch) => (
                    <th key={ch} scope="col" className="border-b border-line px-3 py-2 font-medium">
                      {channelLabel[ch]}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {prefs.data.rules.map((r) => (
                  <tr key={r.kind}>
                    <th scope="row" className="border-b border-line py-2 pr-3 text-left font-normal">
                      <span className="font-medium">{kindLabel[r.kind].label}</span>
                      {!touched.has(r.kind) && <span className="ml-2 text-[11px] text-faint">default</span>}
                      <div className="text-xs text-muted">{kindLabel[r.kind].hint}</div>
                    </th>
                    <td className="border-b border-line px-3 py-2">
                      <Checkbox
                        checked
                        disabled
                        aria-label={`${kindLabel[r.kind].label} in the app`}
                        onChange={() => {}}
                      />
                    </td>
                    {channels.map((ch) => (
                      <td key={ch} className="border-b border-line px-3 py-2">
                        <Checkbox
                          aria-label={`${kindLabel[r.kind].label} by ${channelLabel[ch]}`}
                          disabled={!reach[ch]?.available}
                          checked={(rules[r.kind] ?? []).includes(ch)}
                          onChange={(on) => toggle(r.kind, ch, on)}
                        />
                      </td>
                    ))}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <div className="flex justify-end">
            <Button type="submit" variant="primary" disabled={save.isPending}>
              Save preferences
            </Button>
          </div>
        </form>
      </Panel>
    </div>
  );
}

export function NotificationsPage() {
  return (
    <Page>
      <PageHeader title="Notifications" meta="What concerns you, and where you want to hear about it." />
      <Tabs defaultValue="all">
        <TabsList>
          <TabsTrigger value="all">Notifications</TabsTrigger>
          <TabsTrigger value="preferences">Preferences</TabsTrigger>
        </TabsList>
        <TabsContent value="all">
          <List />
        </TabsContent>
        <TabsContent value="preferences">
          <Preferences />
        </TabsContent>
      </Tabs>
    </Page>
  );
}
