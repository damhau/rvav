import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { CircleX, Clock3, Hourglass, Plus } from "lucide-react";
import { ErrorState, Skeleton, SkeletonRows } from "@/components/common/feedback";
import { Meter } from "@/components/common/meter";
import { ItemIcon, itemTypeLabel, Time } from "@/components/common/misc";
import { EmptyState, Page, PageHeader, Panel } from "@/components/common/page";
import { EnvBadge, RequestStatusBadge } from "@/components/common/status";
import { Button } from "@/components/ui/button";
import type { QuotaResource } from "@/lib/api/types";
import { useCurrentProject } from "@/lib/project";
import { q } from "@/lib/query";
import { leaseLabel, roughDuration } from "@/lib/utils";
import { ProgressSegments } from "@/features/requests/ProgressBar";

const quotaLabel: Record<QuotaResource, [string, string]> = {
  vcpu: ["vCPU", ""],
  memory_gb: ["Memory", "GB"],
  storage_gb: ["Storage", "GB"],
  vm_count: ["VMs", ""],
};

/** A week from the start of the next hour, so the query key stays stable. */
const weekAhead = () => new Date(Math.ceil(Date.now() / 3_600_000) * 3_600_000 + 7 * 86_400_000).toISOString();

function Attention() {
  const { projectId } = useCurrentProject();
  const failed = useInfiniteQuery(q.requests({ projectId, status: ["failed"], requester: "me", limit: 5 }));
  const waiting = useInfiniteQuery(q.requests({ projectId, status: ["pending_approval"], limit: 20 }));
  const expiring = useInfiniteQuery(
    q.deployments({ projectId, leaseExpiresBefore: weekAhead(), sort: "lease_expires_at", limit: 10 }),
  );
  if (failed.isPending || waiting.isPending || expiring.isPending) return <SkeletonRows rows={3} cols={2} />;
  if (failed.isError) return <ErrorState error={failed.error} onRetry={() => void failed.refetch()} />;

  const failures = failed.data.pages[0]?.items ?? [];
  const pending = waiting.data?.pages[0]?.items ?? [];
  const leases = expiring.data?.pages[0]?.items ?? [];
  const rows: {
    key: string;
    icon: React.ReactNode;
    title: React.ReactNode;
    body: string;
    to: string;
    params?: Record<string, string>;
    search?: Record<string, unknown>;
    action: string;
  }[] = [];

  for (const r of failures) {
    rows.push({
      key: r.id,
      icon: <CircleX className="size-3.5" aria-hidden />,
      title: (
        <>
          {r.number} failed{r.error ? ":" : ""} <span className="font-normal">{r.error?.message}</span>
        </>
      ),
      body: r.title,
      to: "/requests/$requestId",
      params: { requestId: r.number },
      action: "Open",
    });
  }
  if (pending.length) {
    rows.push({
      key: "pending",
      icon: <Clock3 className="size-3.5" aria-hidden />,
      title: `${pending.length} request${pending.length === 1 ? " is" : "s are"} waiting for approval`,
      body: pending.map((r) => r.title).join(" · "),
      to: "/requests",
      search: { status: "pending_approval" },
      action: "View",
    });
  }
  if (leases.length) {
    rows.push({
      key: "leases",
      icon: <Hourglass className="size-3.5" aria-hidden />,
      title: `${leases.length} lease${leases.length === 1 ? " expires" : "s expire"} this week`,
      body:
        leases.map((d) => `${d.name} ${leaseLabel(d.lease.expires_at).toLowerCase()}`).join(" · ") +
        ". Expired VMs are powered off, then deleted.",
      to: "/deployments",
      search: { expiring: true },
      action: "Review",
    });
  }
  if (rows.length === 0) {
    return (
      <EmptyState
        title="Nothing needs you right now"
        body="Failed requests, approvals and expiring leases show up here."
      />
    );
  }
  const tone = (k: string) =>
    k === "pending" ? "bg-warn-soft text-warn" : k === "leases" ? "bg-sunken text-muted" : "bg-bad-soft text-bad";
  return (
    <ul className="flex flex-col">
      {rows.map((r) => (
        <li key={r.key} className="border-b border-line last:border-b-0">
          <Link
            to={r.to}
            params={r.params as never}
            search={r.search as never}
            className="flex items-start gap-3 px-4 py-3 text-fg hover:bg-sunken"
          >
            <span className={`mt-px flex size-[22px] shrink-0 items-center justify-center rounded-md ${tone(r.key)}`}>
              {r.icon}
            </span>
            <span className="flex min-w-0 flex-1 flex-col gap-px">
              <span className="font-medium">{r.title}</span>
              <span className="truncate text-xs text-muted">{r.body}</span>
            </span>
            <span className="text-xs font-medium whitespace-nowrap text-accent">{r.action}</span>
          </Link>
        </li>
      ))}
    </ul>
  );
}

function OpenRequests() {
  const { projectId } = useCurrentProject();
  const open = useInfiniteQuery(
    q.requests({ projectId, status: ["running", "queued", "pending_approval", "scheduled", "cancelling"], limit: 8 }),
  );
  if (open.isPending) return <SkeletonRows rows={3} />;
  if (open.isError) return <ErrorState error={open.error} onRetry={() => void open.refetch()} />;
  const items = open.data.pages[0]?.items ?? [];
  if (items.length === 0) {
    return (
      <EmptyState title="No open requests" body="Requests that are running or waiting for approval appear here." />
    );
  }
  return (
    <div className="overflow-x-auto">
      <table className="w-full min-w-[640px] border-collapse">
        <thead>
          <tr className="label text-left">
            <th scope="col" className="border-b border-line px-4 py-2 font-medium">
              Request
            </th>
            <th scope="col" className="border-b border-line px-3 py-2 font-medium">
              Status
            </th>
            <th scope="col" className="border-b border-line px-3 py-2 font-medium">
              Current step
            </th>
            <th scope="col" className="border-b border-line px-4 py-2 text-right font-medium">
              Submitted
            </th>
          </tr>
        </thead>
        <tbody>
          {items.map((r) => (
            <tr key={r.id} className="hover:bg-sunken">
              <td className="border-b border-line px-4 py-2 last:border-b-0">
                <Link
                  to="/requests/$requestId"
                  params={{ requestId: r.number }}
                  className="font-medium text-fg hover:text-accent"
                >
                  {r.title}
                </Link>
                <div className="font-mono text-[11px] text-faint">
                  {r.number} · {r.environment} · {r.requester.name}
                </div>
              </td>
              <td className="border-b border-line px-3 py-2">
                <RequestStatusBadge status={r.status} />
              </td>
              <td className="border-b border-line px-3 py-2">
                <div className="flex flex-col gap-1">
                  <span className="text-muted">{r.current_step?.title ?? "–"}</span>
                  {r.status === "running" && <ProgressSegments request={r} />}
                </div>
              </td>
              <td className="border-b border-line px-4 py-2 text-right text-muted">
                <Time iso={r.submitted_at} />
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function Quota() {
  const { projectId, project } = useCurrentProject();
  const quota = useQuery(q.quota(projectId));
  return (
    <Panel title="Quota" actions={<span className="text-[11px] text-faint">{project.name}</span>}>
      {quota.isPending ? (
        <div className="flex flex-col gap-3">
          {[0, 1, 2, 3].map((i) => (
            <Skeleton key={i} className="h-6" />
          ))}
        </div>
      ) : quota.isError ? (
        <ErrorState error={quota.error} className="p-0" />
      ) : (
        <div className="flex flex-col gap-3">
          {quota.data.lines.map((l) => (
            <Meter
              key={l.resource}
              label={quotaLabel[l.resource][0]}
              unit={quotaLabel[l.resource][1]}
              used={l.used + l.reserved}
              total={l.limit ?? Math.max(1, l.used)}
            />
          ))}
        </div>
      )}
    </Panel>
  );
}

function Favourites() {
  const { projectId } = useCurrentProject();
  const favs = useQuery(q.catalog({ projectId, favourite: true }));
  return (
    <Panel title="Your favourites" flush>
      {favs.isPending ? (
        <SkeletonRows rows={3} cols={1} />
      ) : favs.isError ? (
        <ErrorState error={favs.error} />
      ) : favs.data.items.length === 0 ? (
        <EmptyState title="No favourites yet" body="Star items in the catalog to request them again from here." />
      ) : (
        <ul className="flex flex-col py-1">
          {favs.data.items.map((i) => (
            <li key={i.id}>
              <Link
                to="/catalog/$itemId/request"
                params={{ itemId: i.slug }}
                className="flex items-center gap-2.5 px-4 py-2 text-fg hover:bg-sunken"
              >
                <ItemIcon name={i.icon} className="size-7" />
                <span className="min-w-0">
                  <span className="block font-medium">{i.name}</span>
                  <span className="text-[11px] text-faint">
                    {itemTypeLabel[i.type]} · {i.version.number}
                    {i.typical_duration_seconds ? ` · ${roughDuration(i.typical_duration_seconds)}` : ""}
                  </span>
                </span>
              </Link>
            </li>
          ))}
        </ul>
      )}
    </Panel>
  );
}

export function OverviewPage() {
  const { projectId, project } = useCurrentProject();
  const deployments = useInfiniteQuery(q.deployments({ projectId, limit: 200 }));
  const all = deployments.data?.pages.flatMap((p) => p.items) ?? [];
  const vms = all.reduce((n, d) => n + d.vm_count, 0);
  const envs = new Set(all.map((d) => d.environment));
  return (
    <Page>
      <PageHeader
        title="Overview"
        meta={
          deployments.data ? (
            <span className="flex items-center gap-1.5">
              {project.name} · {vms} VMs in {all.length} deployments
              {[...envs].sort().map((e) => (
                <EnvBadge key={e} env={e} />
              ))}
            </span>
          ) : (
            <Skeleton className="h-3 w-64" />
          )
        }
        actions={
          <Button variant="primary" asChild>
            <Link to="/catalog">
              <Plus /> New request
            </Link>
          </Button>
        }
      />
      <div className="flex flex-wrap items-start gap-4">
        <div className="flex min-w-0 flex-[999_1_560px] flex-col gap-4">
          <Panel title="Needs your attention" flush>
            <Attention />
          </Panel>
          <Panel
            title="Open requests"
            flush
            actions={
              <Link to="/requests" className="text-xs">
                All requests
              </Link>
            }
          >
            <OpenRequests />
          </Panel>
        </div>
        <aside className="flex min-w-0 flex-[1_1_300px] flex-col gap-4">
          <Quota />
          <Favourites />
        </aside>
      </div>
    </Page>
  );
}
