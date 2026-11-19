// One subscription: its settings and every run, with what was sent and
// what came back.
import { useQuery } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { Ellipsis } from "lucide-react";
import { useState } from "react";
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
import { useCurrentProject } from "@/lib/project";
import { adm } from "@/lib/query";
import { RunsPanel } from "./runs";
import {
  changeText,
  listText,
  RemoveSubscription,
  runsText,
  SubscriptionSheet,
  targetText,
  useSubscriptionSwitch,
  useSubscriptionTest,
} from "./SubscriptionsPage";

type Subscription = components["schemas"]["Subscription"];

function Settings({ s }: { s: Subscription }) {
  const topics = useQuery(adm.eventTopics());
  const changes = (topics.data?.items.find((t) => t.key === s.topic)?.may_change ?? []).map((c) => changeText[c]);
  return (
    <Panel title="Settings">
      <KeyValues
        items={[
          { label: "Topic", value: <Mono>{s.topic}</Mono> },
          { label: "Only when", value: s.condition ? <Mono>{s.condition}</Mono> : "Every event" },
          { label: "Target", value: targetText(s.target) },
          {
            label: "Blocking",
            value: s.blocking
              ? `Yes: ${s.on_failure === "continue_request" ? "the request goes on" : "the request stops"} when it fails, doesn't answer or asks for a change rvav refuses`
              : "No",
          },
          ...(s.blocking && changes.length > 0 ? [{ label: "Its answer may change", value: listText(changes) }] : []),
          ...(s.recovery ? [{ label: "Recovery target", value: targetText(s.recovery) }] : []),
          { label: "Priority", value: s.priority },
          { label: "Timeout", value: `${s.timeout_seconds} s` },
          { label: "Created", value: <Time iso={s.created_at} /> },
          { label: "Last 30 days", value: runsText(s) },
        ]}
      />
    </Panel>
  );
}

export function SubscriptionPage({ subscriptionId }: { subscriptionId: string }) {
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
            <Link to="/admin/subscriptions">Subscriptions</Link>
          </Breadcrumbs>
        }
        title={s?.name ?? "Subscription"}
        description={s?.description || undefined}
        meta={
          s && (
            <>
              {s.enabled ? <Badge tone="ok">On</Badge> : <Badge tone="neutral">Off</Badge>}
              {s.blocking && <Badge tone="warn">Blocking</Badge>}
            </>
          )
        }
        actions={
          s &&
          canEdit && (
            <>
              <Menu>
                <MenuTrigger asChild>
                  <IconButton label={`More actions for ${s.name}`} variant="secondary" className="size-8">
                    <Ellipsis />
                  </IconButton>
                </MenuTrigger>
                <MenuContent>
                  <MenuItem disabled={test.isPending} onSelect={() => test.mutate(s.id)}>
                    Send a test event
                  </MenuItem>
                  <MenuItem disabled={toggle.isPending} onSelect={() => toggle.mutate(s)}>
                    {s.enabled ? "Turn off" : "Turn on"}
                  </MenuItem>
                  <MenuSeparator />
                  <MenuItem danger onSelect={() => setRemoving(true)}>
                    Remove subscription
                  </MenuItem>
                </MenuContent>
              </Menu>
              <Button variant="primary" onClick={() => setEditing(true)}>
                Change subscription
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
