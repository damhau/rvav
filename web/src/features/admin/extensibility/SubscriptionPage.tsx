// One subscription: its settings and every run, with what was sent and
// what came back.
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { ErrorState, SkeletonRows } from "@/components/common/feedback";
import { KeyValues, Page, PageHeader, Panel } from "@/components/common/page";
import { Mono, Time } from "@/components/common/misc";
import { Badge } from "@/components/ui/badge";
import { adm } from "@/lib/query";
import { useCurrentProject } from "@/lib/project";
import { RunsPanel } from "./runs";
import { targetText } from "./SubscriptionsPage";

export function SubscriptionPage({ subscriptionId }: { subscriptionId: string }) {
  const { me } = useCurrentProject();
  const sub = useQuery(adm.subscription(subscriptionId));
  const s = sub.data;
  return (
    <Page>
      <PageHeader
        crumbs={
          <Link to="/admin/subscriptions" className="hover:text-fg">
            Subscriptions
          </Link>
        }
        title={s?.name ?? "Subscription"}
        meta={s?.description}
      />
      {sub.isPending && <SkeletonRows rows={4} cols={2} />}
      {sub.isError && <ErrorState error={sub.error} onRetry={() => void sub.refetch()} />}
      {s && (
        <>
          <Panel title="Settings">
            <KeyValues
              items={[
                { label: "Topic", value: <Mono>{s.topic}</Mono> },
                { label: "Only when", value: s.condition ? <Mono>{s.condition}</Mono> : "Every event" },
                { label: "Target", value: targetText(s.target) },
                {
                  label: "Blocking",
                  value: s.blocking
                    ? `Yes: ${s.on_failure === "continue_request" ? "the request goes on" : "the request stops"} when it fails or doesn't answer`
                    : "No",
                },
                { label: "Priority", value: s.priority },
                { label: "Timeout", value: `${s.timeout_seconds} s` },
                { label: "State", value: s.enabled ? <Badge tone="ok">On</Badge> : <Badge tone="neutral">Off</Badge> },
                { label: "Created", value: <Time iso={s.created_at} /> },
                {
                  label: "Last 30 days",
                  value: `${s.runs.delivered} delivered, ${s.runs.failed} failed, ${s.runs.pending} waiting`,
                },
              ]}
            />
          </Panel>
          <RunsPanel subscriptionId={s.id} canRetry={me.global_roles.includes("admin")} />
        </>
      )}
    </Page>
  );
}
