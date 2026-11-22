// One subscription: its settings and every run, with what was sent and
// what came back.
import { useQuery } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { Ellipsis } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { ErrorState, SkeletonRows } from "@/components/common/feedback";
import { Mono, Time } from "@/components/common/misc";
import { KeyValues, Page } from "@/components/common/page";
import { Badge } from "@/components/ui/badge";
import { Breadcrumbs } from "@/components/ui/breadcrumbs";
import { Button, IconButton } from "@/components/ui/button";
import { Menu, MenuContent, MenuItem, MenuSeparator, MenuTrigger } from "@/components/ui/dropdown-menu";
import { PageHeader } from "@/components/ui/page-header";
import { Panel } from "@/components/ui/panel";
import type { components } from "@/lib/api/schema";
import { can } from "@/lib/can";
import { formatList } from "@/lib/format";
import { useCurrentProject } from "@/lib/project";
import { adm } from "@/lib/query";
import { RunsPanel } from "./runs";
import {
  changeText,
  RemoveSubscription,
  runsText,
  SubscriptionSheet,
  targetText,
  useSubscriptionSwitch,
  useSubscriptionTest,
} from "./SubscriptionsPage";

type Subscription = components["schemas"]["Subscription"];

function Settings({ s }: { s: Subscription }) {
  const { t } = useTranslation("admin-extensibility");
  const topics = useQuery(adm.eventTopics());
  const changes = (topics.data?.items.find((e) => e.key === s.topic)?.may_change ?? []).map(changeText);
  return (
    <Panel title={t("shared.settings")}>
      <KeyValues
        items={[
          { label: t("shared.topic"), value: <Mono>{s.topic}</Mono> },
          {
            label: t("subscription.onlyWhen"),
            value: s.condition ? <Mono>{s.condition}</Mono> : t("subscription.everyEvent"),
          },
          { label: t("shared.target"), value: targetText(s.target) },
          {
            label: t("shared.blocking"),
            value: s.blocking
              ? s.on_failure === "continue_request"
                ? t("subscription.blockingGoesOn")
                : t("subscription.blockingStops")
              : t("subscription.no"),
          },
          ...(s.blocking && changes.length > 0
            ? [{ label: t("subscription.mayChange"), value: formatList(changes) }]
            : []),
          ...(s.recovery ? [{ label: t("subscriptions.sheet.recovery"), value: targetText(s.recovery) }] : []),
          { label: t("shared.priority"), value: s.priority },
          { label: t("subscription.timeout"), value: t("subscription.seconds", { seconds: s.timeout_seconds }) },
          { label: t("subscription.created"), value: <Time iso={s.created_at} /> },
          { label: t("shared.last30"), value: runsText(s) },
        ]}
      />
    </Panel>
  );
}

export function SubscriptionPage({ subscriptionId }: { subscriptionId: string }) {
  const { t } = useTranslation(["admin-extensibility", "shell"]);
  const navigate = useNavigate();
  const { me } = useCurrentProject();
  const canEdit = can(me, "extensibility.manage");
  const sub = useQuery(adm.subscription(subscriptionId));
  const [editing, setEditing] = useState(false);
  const [removing, setRemoving] = useState(false);
  const { test, dialog } = useSubscriptionTest();
  const toggle = useSubscriptionSwitch();
  const s = sub.data;
  return (
    <Page>
      <PageHeader
        breadcrumbs={
          <Breadcrumbs>
            <Link to="/admin/subscriptions">{t("shell:nav.admin.subscriptions")}</Link>
          </Breadcrumbs>
        }
        title={s?.name ?? t("subscription.title")}
        description={s?.description || undefined}
        meta={
          s && (
            <>
              {s.enabled ? <Badge tone="ok">{t("shared.on")}</Badge> : <Badge tone="neutral">{t("shared.off")}</Badge>}
              {s.blocking && <Badge tone="warn">{t("shared.blocking")}</Badge>}
            </>
          )
        }
        actions={
          s &&
          canEdit && (
            <>
              <Menu>
                <MenuTrigger asChild>
                  <IconButton label={t("shared.moreActions", { name: s.name })} variant="secondary" className="size-8">
                    <Ellipsis />
                  </IconButton>
                </MenuTrigger>
                <MenuContent>
                  <MenuItem disabled={test.isPending} onSelect={() => test.mutate(s.id)}>
                    {t("subscriptions.sendTest")}
                  </MenuItem>
                  <MenuItem disabled={toggle.isPending} onSelect={() => toggle.mutate(s)}>
                    {s.enabled ? t("shared.turnOff") : t("shared.turnOn")}
                  </MenuItem>
                  <MenuSeparator />
                  <MenuItem danger onSelect={() => setRemoving(true)}>
                    {t("subscription.remove")}
                  </MenuItem>
                </MenuContent>
              </Menu>
              <Button variant="primary" onClick={() => setEditing(true)}>
                {t("subscription.change")}
              </Button>
            </>
          )
        }
      />
      {sub.isPending && <SkeletonRows rows={4} cols={2} />}
      {sub.isError && <ErrorState error={sub.error} onRetry={() => void sub.refetch()} />}
      {s && (
        <>
          <Settings s={s} />
          <RunsPanel subscriptionId={s.id} canRetry={canEdit} />
          {editing && <SubscriptionSheet sub={s} onClose={() => setEditing(false)} />}
          {removing && (
            <RemoveSubscription
              sub={s}
              onClose={() => setRemoving(false)}
              onRemoved={() => void navigate({ to: "/admin/subscriptions" })}
            />
          )}
        </>
      )}
      {dialog}
    </Page>
  );
}
