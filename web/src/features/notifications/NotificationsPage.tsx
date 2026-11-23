// Notifications (S11, ADR-0023): everything that concerned the caller, read
// or not, and the channels they want each kind on, in their language.
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { useEffect, useRef, useState } from "react";
import { toast } from "sonner";
import { useTranslation } from "react-i18next";
import { ErrorState, SkeletonRows } from "@/components/common/feedback";
import { Page } from "@/components/common/page";
import { EmptyState } from "@/components/ui/empty-state";
import { PageHeader } from "@/components/ui/page-header";
import { Panel } from "@/components/ui/panel";
import { DataTable, type Column } from "@/components/ui/data-table";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { useSaveFocus } from "@/components/ui/opener";
import { Checkbox } from "@/components/ui/checkbox";
import { Field, FieldNote } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { api, call, isApiError } from "@/lib/api/client";
import type { components } from "@/lib/api/schema";
import type { Me } from "@/lib/api/types";
import { applyLocale, currentLocale, isLocale, localeNames, locales, type Locale } from "@/lib/i18n";
import { useCurrentProject } from "@/lib/project";
import { keys, q } from "@/lib/query";
import { failed } from "../admin/governance/common";
import { channelLabel, defaultChannels, kindLabel } from "./kinds";
import { NotificationLine, useMarkRead } from "./NotificationBell";

type S = components["schemas"];
type Channel = "email" | "teams" | "slack";
const channels: Channel[] = ["email", "teams", "slack"];

/** Whether a kind's channels differ from its defaults. */
const differs = (kind: string, chs: Channel[]) => {
  const d = defaultChannels(kind as S["NotificationKind"]);
  return d.length !== chs.length || d.some((c) => !chs.includes(c));
};

type Rule = { kind: string; channels: Channel[] };

/**
 * A refusal's problems by what they are about: `locale`, a kind's row, and
 * `kind/channel` for the checkbox to fix (the row's first when the channel
 * named isn't one). Its pointers count the rules as sent.
 */
function problemsOf(e: unknown, sent: Rule[]): Record<string, string> {
  if (!isApiError(e)) return {};
  const out: Record<string, string> = {};
  for (const f of e.fieldErrors) {
    if (f.field === "/locale") out.locale ??= f.message;
    const at = /^\/rules\/(\d+)(?:\/channels\/(\d+))?/.exec(f.field);
    const rule = at ? sent[Number(at[1])] : undefined;
    if (!at || !rule) continue;
    const named = at[2] === undefined ? undefined : rule.channels[Number(at[2])];
    const channel = channels.find((c) => c === named) ?? channels[0];
    out[rule.kind] ??= f.message;
    out[`${rule.kind}/${channel}`] ??= f.message;
  }
  return out;
}

function List() {
  const { t } = useTranslation("notifications");
  const [unread, setUnread] = useState(false);
  const list = useInfiniteQuery(q.notifications(unread));
  const markRead = useMarkRead();
  const items = list.data?.pages.flatMap((p) => p.items) ?? [];
  const count = list.data?.pages[0]?.unread ?? 0;
  return (
    <Panel
      title={count > 0 ? t("unread", { count }) : t("allRead")}
      flush
      actions={
        <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
          <label htmlFor="unread-only" className="flex items-center gap-1.5 text-secondary">
            <Checkbox id="unread-only" checked={unread} onChange={setUnread} />
            {t("unreadOnly")}
          </label>
          <Button size="sm" disabled={count === 0 || markRead.isPending} onClick={() => markRead.mutate({ all: true })}>
            {t("markAllRead")}
          </Button>
        </div>
      }
    >
      {list.isPending && <SkeletonRows rows={4} cols={2} />}
      {list.isError && <ErrorState error={list.error} onRetry={() => void list.refetch()} />}
      {list.data && items.length === 0 && (
        <EmptyState title={t(unread ? "nothingUnread" : "none")} body={t("noneBody")} />
      )}
      {items.length > 0 && (
        <ul aria-label={t("title")} className="divide-y divide-line">
          {items.map((n) => (
            <li key={n.id} className="flex items-start gap-3 px-4 py-2.5 text-body">
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
                  variant="quiet"
                  aria-label={t("markReadOf", { title: n.title })}
                  onClick={() => markRead.mutate({ ids: [n.id] })}
                >
                  {t("markRead")}
                </Button>
              )}
            </li>
          ))}
        </ul>
      )}
      {list.hasNextPage && (
        <div className="flex justify-center p-3">
          <Button size="sm" disabled={list.isFetchingNextPage} onClick={() => void list.fetchNextPage()}>
            {t("loadMore")}
          </Button>
        </div>
      )}
    </Panel>
  );
}

function Preferences() {
  const { t } = useTranslation(["notifications", "shell"]);
  const qc = useQueryClient();
  const { me } = useCurrentProject();
  const prefs = useQuery(q.notificationPreferences());
  // The language shown until one is chosen: saving keeps what the person sees.
  const [locale, setLocale] = useState<Locale>(me.locale ?? currentLocale());
  // One chosen in the top bar meanwhile is the one this form saves.
  const [chosen, setChosen] = useState(me.locale);
  if (me.locale !== chosen) {
    setChosen(me.locale);
    if (me.locale) setLocale(me.locale);
  }
  const [rules, setRules] = useState<Record<string, Channel[]>>({});
  const [errors, setErrors] = useState<Record<string, string>>({});
  const sent = useRef<Rule[]>([]);
  useEffect(() => {
    if (!prefs.data) return;
    setRules(
      Object.fromEntries(prefs.data.rules.map((r) => [r.kind, r.channels.filter((c) => c !== "in_app") as Channel[]])),
    );
  }, [prefs.data]);
  const save = useMutation({
    mutationFn: () => {
      // Only kinds that differ from the defaults: the others follow them.
      sent.current = Object.entries(rules)
        .filter(([kind, chs]) => differs(kind, chs))
        .map(([kind, chs]) => ({ kind, channels: chs }));
      return call(
        api.PUT("/me/notification-preferences", {
          body: {
            locale,
            rules: sent.current.map((r) => ({ kind: r.kind as S["NotificationKind"], channels: r.channels })),
          },
        }),
      );
    },
    onSuccess: async (saved) => {
      qc.setQueryData(q.notificationPreferences().queryKey, saved);
      qc.setQueryData<Me>(keys.me, (m) => m && { ...m, locale: saved.locale });
      // Said in the language just saved.
      await applyLocale(saved.locale);
      toast.success(t("prefs.saved"));
    },
    onError: (e) => {
      setErrors(problemsOf(e, sent.current));
      failed(e, t("prefs.notSaved"));
    },
  });
  const test = useMutation({
    mutationFn: (channel: Channel) => call(api.POST("/me/notification-preferences/test", { body: { channel } })),
    onSuccess: (r) => (r.ok ? toast.success(r.message) : toast.error(t("prefs.notSent"), { description: r.message })),
    onError: (e) => failed(e, t("prefs.notSent")),
  });
  const form = useRef<HTMLFormElement>(null);
  const submit = useRef<HTMLButtonElement>(null);
  const saveFocus = useSaveFocus(form, save.isPending, () => submit.current);
  if (prefs.isPending) return <SkeletonRows rows={6} cols={4} />;
  if (prefs.isError) return <ErrorState error={prefs.error} onRetry={() => void prefs.refetch()} />;
  const reach = Object.fromEntries(prefs.data.channels.map((c) => [c.channel, c]));
  const toggle = (kind: string, ch: Channel, on: boolean) => {
    setRules((r) => ({ ...r, [kind]: on ? [...(r[kind] ?? []), ch] : (r[kind] ?? []).filter((x) => x !== ch) }));
  };
  const columns: Column<S["NotificationPreferences"]["rules"][number]>[] = [
    {
      id: "when",
      header: t("prefs.when"),
      cell: (r) => (
        <>
          <span className="font-medium">{kindLabel(r.kind).label}</span>
          {!differs(r.kind, rules[r.kind] ?? []) && (
            <span className="ml-2 text-caption text-faint">{t("prefs.default")}</span>
          )}
          <div className="text-secondary text-muted">{kindLabel(r.kind).hint}</div>
          <FieldNote id={`notif-${r.kind}-note`} error={errors[r.kind]} />
        </>
      ),
    },
    {
      id: "app",
      header: t("prefs.app"),
      cell: (r) => (
        <Checkbox
          checked
          disabled
          aria-label={t("prefs.inAppOf", { kind: kindLabel(r.kind).label })}
          onChange={() => {}}
        />
      ),
    },
    ...channels.map((ch): Column<S["NotificationPreferences"]["rules"][number]> => ({
      id: ch,
      header: channelLabel(ch),
      cell: (r) => (
        <Checkbox
          aria-label={t("prefs.by", { kind: kindLabel(r.kind).label, channel: channelLabel(ch) })}
          aria-invalid={errors[`${r.kind}/${ch}`] ? true : undefined}
          aria-describedby={errors[`${r.kind}/${ch}`] ? `notif-${r.kind}-note` : undefined}
          disabled={!reach[ch]?.available}
          checked={(rules[r.kind] ?? []).includes(ch)}
          onChange={(on) => toggle(r.kind, ch, on)}
        />
      ),
    })),
  ];
  return (
    <div className="flex flex-col gap-4">
      <Panel title={t("prefs.reach")}>
        <ul className="flex flex-col gap-2 text-body">
          <li className="flex items-center gap-2">
            <Badge tone="ok">{t("prefs.on")}</Badge> <span className="font-medium">{t("prefs.inApp")}</span>
            <span className="text-secondary text-muted">{t("prefs.always")}</span>
          </li>
          {channels.map((ch) => {
            const c = reach[ch];
            return (
              <li key={ch} className="flex flex-wrap items-center gap-2">
                {c?.available ? (
                  <Badge tone="ok">{t("prefs.canReach")}</Badge>
                ) : (
                  <Badge tone="neutral">{t("prefs.cannot")}</Badge>
                )}
                <span className="font-medium">{channelLabel(ch)}</span>
                <span className="text-secondary text-muted">{c?.available ? c.address : c?.reason}</span>
                {c?.available && (
                  <Button
                    size="sm"
                    variant="quiet"
                    disabled={test.isPending}
                    aria-label={t("prefs.testOn", { channel: channelLabel(ch) })}
                    onClick={() => test.mutate(ch)}
                  >
                    {t("prefs.test")}
                  </Button>
                )}
              </li>
            );
          })}
        </ul>
      </Panel>
      <Panel title={t("prefs.where")}>
        <form
          ref={form}
          className="flex flex-col gap-4"
          onKeyDownCapture={saveFocus.interacted}
          onPointerDownCapture={saveFocus.interacted}
          onSubmit={(e) => {
            e.preventDefault();
            if (save.isPending) return;
            setErrors({});
            saveFocus.submitted();
            save.mutate();
          }}
        >
          <Field
            label={t("shell:topBar.language")}
            hint={t("prefs.languageHint")}
            htmlFor="notif-locale"
            error={errors.locale}
            className="max-w-xs"
          >
            <Select
              id="notif-locale"
              value={locale}
              onChange={(v) => isLocale(v) && setLocale(v)}
              options={locales.map((l) => ({ value: l, label: localeNames[l] }))}
            />
          </Field>
          <DataTable
            label={t("prefs.where")}
            columns={columns}
            rows={prefs.data.rules}
            rowKey={(r) => r.kind}
            minWidth={620}
            className="-mx-4"
          />
          <div className="flex justify-end">
            <Button ref={submit} type="submit" variant="primary" disabled={save.isPending}>
              {t("prefs.save")}
            </Button>
          </div>
        </form>
      </Panel>
    </div>
  );
}

export const notificationsTabs = ["all", "preferences"] as const;
export type NotificationsTab = (typeof notificationsTabs)[number];

export function NotificationsPage({ tab = "all" }: { tab?: NotificationsTab }) {
  const { t } = useTranslation("notifications");
  const navigate = useNavigate();
  return (
    <Page>
      <PageHeader title={t("title")} description={t("lead")} />
      <Tabs
        value={tab}
        onValueChange={(next) =>
          void navigate({
            to: "/notifications",
            search: { tab: next === "all" ? undefined : (next as NotificationsTab) },
            replace: true,
          })
        }
      >
        <TabsList aria-label={t("sections")}>
          <TabsTrigger value="all">{t("tabs.all")}</TabsTrigger>
          <TabsTrigger value="preferences">{t("tabs.preferences")}</TabsTrigger>
        </TabsList>
        <TabsContent value="all" className="pt-4">
          <List />
        </TabsContent>
        <TabsContent value="preferences" className="pt-4">
          <Preferences />
        </TabsContent>
      </Tabs>
    </Page>
  );
}
