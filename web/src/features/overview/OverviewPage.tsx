import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { CircleCheck, CircleX, Clock3, Hourglass, Inbox, ListChecks, Plus, Star } from "lucide-react";
import type { TFunction } from "i18next";
import { Trans, useTranslation } from "react-i18next";
import { ErrorState, Skeleton, SkeletonRows } from "@/components/common/feedback";
import { Meter } from "@/components/common/meter";
import { ItemIcon, itemTypeLabel, Time } from "@/components/common/misc";
import { Page } from "@/components/common/page";
import { EmptyState } from "@/components/ui/empty-state";
import { PageHeader } from "@/components/ui/page-header";
import { Panel } from "@/components/ui/panel";
import { EnvBadge, RequestStatusBadge } from "@/components/common/status";
import { Button } from "@/components/ui/button";
import { DataTable, type Column } from "@/components/ui/data-table";
import type { ServiceRequest } from "@/lib/api/types";
import { can } from "@/lib/can";
import { useEnvName } from "@/lib/environments";
import { useCurrentProject } from "@/lib/project";
import { adm, q } from "@/lib/query";
import { leaseLabel, roughDuration } from "@/lib/format";
import { versionLabel } from "@/features/catalog/facts";
import { ProgressSegments } from "@/features/requests/ProgressBar";
import { stepTitle } from "@/lib/i18n/names";

const quotaUnit = { vcpu: "", memory_gb: "GB", storage_gb: "GB", vm_count: "" } as const;

/** A week from the start of the next hour, so the query key stays stable. */
const weekAhead = () => new Date(Math.ceil(Date.now() / 3_600_000) * 3_600_000 + 7 * 86_400_000).toISOString();

function Attention() {
  const { t } = useTranslation("overview");
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
      title: r.error ? (
        <Trans
          t={t}
          i18nKey="attention.failedWith"
          values={{ number: r.number, message: r.error.message }}
          components={{ message: <span className="font-normal" /> }}
        />
      ) : (
        t("attention.failed", { number: r.number })
      ),
      body: r.title,
      to: "/requests/$requestId",
      params: { requestId: r.number },
      action: t("attention.open"),
    });
  }
  if (pending.length) {
    rows.push({
      key: "pending",
      icon: <Clock3 className="size-3.5" aria-hidden />,
      title: t("attention.waiting", { count: pending.length }),
      body: pending.map((r) => r.title).join(" · "),
      to: "/requests",
      search: { status: "pending_approval" },
      action: t("attention.view"),
    });
  }
  if (leases.length) {
    rows.push({
      key: "leases",
      icon: <Hourglass className="size-3.5" aria-hidden />,
      title: t("attention.leases", { count: leases.length }),
      body: t("attention.leasesBody", {
        list: leases.map((d) => `${d.name} ${leaseLabel(d.lease.expires_at).toLowerCase()}`).join(" · "),
      }),
      to: "/deployments",
      search: { expiring: true },
      action: t("attention.review"),
    });
  }
  if (rows.length === 0) {
    return <EmptyState icon={CircleCheck} title={t("attention.none")} body={t("attention.noneBody")} />;
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
              <span className="truncate text-secondary text-muted">{r.body}</span>
            </span>
            <span className="text-secondary font-medium whitespace-nowrap text-accent">{r.action}</span>
          </Link>
        </li>
      ))}
    </ul>
  );
}

type OpenRequest = ServiceRequest;

const openColumns = (t: TFunction<"overview">, envName: (key: string) => string): Column<OpenRequest>[] => [
  {
    id: "request",
    header: t("open.request"),
    cell: (r) => (
      <div className="flex flex-col">
        <Link
          to="/requests/$requestId"
          params={{ requestId: r.number }}
          className="font-medium text-fg hover:text-accent"
        >
          {r.title}
        </Link>
        <span className="text-caption text-muted">
          <span className="font-mono">{r.number}</span> · {envName(r.environment)} · {r.requester.name}
        </span>
      </div>
    ),
  },
  { id: "status", header: t("open.status"), cell: (r) => <RequestStatusBadge status={r.status} /> },
  {
    id: "step",
    header: t("open.step"),
    cell: (r) => (
      <div className="flex flex-col gap-1">
        <span className="text-muted">{r.current_step ? stepTitle(r.current_step) : "–"}</span>
        {r.status === "running" && <ProgressSegments request={r} />}
      </div>
    ),
  },
  {
    id: "submitted",
    header: t("open.submitted"),
    align: "end",
    sortValue: (r) => r.submitted_at,
    className: "whitespace-nowrap text-muted",
    cell: (r) => <Time iso={r.submitted_at} />,
  },
];

function OpenRequests() {
  const { t } = useTranslation("overview");
  const { projectId } = useCurrentProject();
  const envName = useEnvName();
  const open = useInfiniteQuery(
    q.requests({ projectId, status: ["running", "queued", "pending_approval", "scheduled", "cancelling"], limit: 8 }),
  );
  return (
    <DataTable
      label={t("open.title")}
      columns={openColumns(t, envName)}
      rows={open.data?.pages[0]?.items}
      rowKey={(r) => r.id}
      rowLinks
      minWidth={640}
      loading={open.isPending}
      error={open.isError && <ErrorState error={open.error} onRetry={() => void open.refetch()} />}
      empty={
        <EmptyState
          icon={Inbox}
          title={t("open.none")}
          body={t("open.noneBody")}
          action={
            <Button size="sm" asChild>
              <Link to="/catalog">{t("open.fromCatalog")}</Link>
            </Button>
          }
        />
      }
    />
  );
}

function Quota() {
  const { t } = useTranslation(["overview", "common"]);
  const { projectId, project } = useCurrentProject();
  const quota = useQuery(q.quota(projectId));
  return (
    <Panel title={t("quota.title")} description={t("quota.lead", { project: project.name })}>
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
              label={t(`common:quota.${l.resource}`)}
              unit={quotaUnit[l.resource]}
              used={l.used + l.reserved}
              total={l.limit}
            />
          ))}
        </div>
      )}
    </Panel>
  );
}

function Favourites() {
  const { t } = useTranslation(["overview", "shell"]);
  const { projectId, me } = useCurrentProject();
  const mayRequest = can(me, "request.submit", projectId);
  const favs = useQuery(q.catalog({ projectId, favourite: true }));
  return (
    <Panel
      title={t("favourites.title")}
      flush
      actions={
        <Button size="sm" variant="quiet" asChild>
          <Link to="/catalog">{t("shell:nav.main.catalog")}</Link>
        </Button>
      }
    >
      {favs.isPending ? (
        <SkeletonRows rows={3} cols={1} />
      ) : favs.isError ? (
        <ErrorState error={favs.error} />
      ) : favs.data.items.length === 0 ? (
        <EmptyState
          icon={Star}
          title={t("favourites.none")}
          body={t("favourites.noneBody")}
          action={
            <Button size="sm" asChild>
              <Link to="/catalog">{t("favourites.openCatalog")}</Link>
            </Button>
          }
        />
      ) : (
        <ul className="flex flex-col py-1">
          {favs.data.items.map((i) => (
            <li key={i.id}>
              <Link
                to={mayRequest ? "/catalog/$itemId/request" : "/catalog/$itemId"}
                params={{ itemId: i.slug }}
                className="flex items-center gap-2.5 px-4 py-2 text-fg hover:bg-sunken"
              >
                <ItemIcon name={i.icon} type={i.type} className="size-7" />
                <span className="min-w-0">
                  <span className="block font-medium">{i.name}</span>
                  <span className="text-caption text-muted">
                    {itemTypeLabel(i.type)} · {versionLabel(i)}
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

/** While the setup checklist is open (U1.5), the way back to it. */
function SetupNotice() {
  const { t } = useTranslation(["overview", "shell"]);
  const { me } = useCurrentProject();
  const setup = useQuery({ ...adm.setup(), enabled: can(me, "system.manage") });
  if (!setup.data?.open) return null;
  const done = setup.data.steps.filter((s) => s.done).length;
  return (
    <section
      aria-label={t("shell:nav.admin.setup")}
      className="flex flex-wrap items-center gap-3 rounded-lg border border-line bg-surface px-4 py-3 text-body"
    >
      <ListChecks className="size-4 text-accent" aria-hidden />
      <span className="flex-1">{t("setup.progress", { done, total: setup.data.steps.length })}</span>
      <Button size="sm" asChild>
        <Link to="/admin/setup">{t("setup.continue")}</Link>
      </Button>
    </section>
  );
}

export function OverviewPage() {
  const { t } = useTranslation(["overview", "shell"]);
  const { projectId, project, me } = useCurrentProject();
  const deployments = useInfiniteQuery(q.deployments({ projectId, limit: 200 }));
  const all = deployments.data?.pages.flatMap((p) => p.items) ?? [];
  const vms = all.reduce((n, d) => n + d.vm_count, 0);
  const envs = new Set(all.map((d) => d.environment));
  return (
    <Page>
      <PageHeader
        title={t("shell:nav.main.overview")}
        description={t("lead")}
        meta={
          deployments.data ? (
            <span className="flex items-center gap-1.5">
              {project.name} ·{" "}
              {t(deployments.hasNextPage ? "summaryFirst" : "summary", {
                vms: t("vmCount", { count: vms }),
                count: all.length,
              })}
              {[...envs].sort().map((e) => (
                <EnvBadge key={e} env={e} />
              ))}
            </span>
          ) : deployments.isPending ? (
            <Skeleton className="h-3 w-64" />
          ) : (
            project.name
          )
        }
        actions={
          can(me, "request.submit", projectId) && (
            <Button variant="primary" asChild>
              <Link to="/catalog">
                <Plus /> {t("newRequest")}
              </Link>
            </Button>
          )
        }
      />
      <SetupNotice />
      <div className="flex flex-wrap items-start gap-4">
        <div className="flex min-w-0 flex-[999_1_560px] flex-col gap-4">
          <Panel title={t("attention.title")} description={t("attention.lead")} flush>
            <Attention />
          </Panel>
          <Panel
            title={t("open.title")}
            description={t("open.lead")}
            flush
            actions={
              <Button size="sm" variant="quiet" asChild>
                <Link to="/requests">{t("open.all")}</Link>
              </Button>
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
