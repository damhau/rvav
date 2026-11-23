import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import type { TFunction } from "i18next";
import { CalendarClock, Inbox, Plus } from "lucide-react";
import { useTranslation } from "react-i18next";
import { ErrorState } from "@/components/common/feedback";
import { Time } from "@/components/common/misc";
import { Page } from "@/components/common/page";
import { EnvBadge, RequestStatusBadge } from "@/components/common/status";
import { Button } from "@/components/ui/button";
import { DataTable, type Column } from "@/components/ui/data-table";
import { EmptyState } from "@/components/ui/empty-state";
import { FilterBar, FilterCheck, FilterEmpty, FilterSelect, useAddressFilters } from "@/components/ui/filter-bar";
import { PageHeader } from "@/components/ui/page-header";
import { Panel } from "@/components/ui/panel";
import type { RequestStatus, ServiceRequest } from "@/lib/api/types";
import { can } from "@/lib/can";
import { useCurrentProject } from "@/lib/project";
import { q } from "@/lib/query";
import { Drafts } from "./Drafts";
import { ProgressSegments } from "./ProgressBar";
import { stepTitle } from "@/lib/i18n/names";

export const requestFilters = ["open", "pending_approval", "failed", "succeeded"] as const;

export interface RequestsSearch {
  status?: (typeof requestFilters)[number];
  mine?: boolean;
  q?: string;
}

const statuses: Record<(typeof requestFilters)[number], RequestStatus[]> = {
  open: ["pending_approval", "scheduled", "queued", "running", "cancelling"],
  pending_approval: ["pending_approval"],
  failed: ["failed"],
  succeeded: ["succeeded"],
};

// No sortable column: the list loads page by page, newest first, and the API doesn't sort it otherwise.
const columnsFor = (t: TFunction<"requests">): Column<ServiceRequest>[] => [
  {
    id: "request",
    header: t("list.columns.request"),
    cell: (r) => (
      <div className="flex min-w-0 flex-col">
        <Link to="/requests/$requestId" params={{ requestId: r.number }} className="font-medium text-fg">
          {r.title}
        </Link>
        <span className="font-mono text-caption text-muted">{r.number}</span>
      </div>
    ),
  },
  { id: "status", header: t("list.columns.status"), cell: (r) => <RequestStatusBadge status={r.status} /> },
  { id: "env", header: t("list.columns.environment"), cell: (r) => <EnvBadge env={r.environment} /> },
  {
    id: "step",
    header: t("list.columns.step"),
    cell: (r) => (
      <div className="flex flex-col gap-1">
        <span className="text-muted">
          {(r.current_step && stepTitle(r.current_step)) ??
            (r.status === "failed"
              ? r.error?.message
              : t("list.stepsOf", { done: r.progress.done, count: r.progress.total }))}
        </span>
        {(r.status === "running" || r.status === "cancelling") && <ProgressSegments request={r} />}
      </div>
    ),
  },
  {
    id: "requester",
    header: t("list.columns.requester"),
    className: "whitespace-nowrap text-muted",
    cell: (r) => r.requester.name,
  },
  {
    id: "submitted",
    header: t("list.columns.submitted"),
    align: "end",
    className: "whitespace-nowrap text-muted",
    cell: (r) => <Time iso={r.submitted_at} />,
  },
];

export function RequestsPage({ search }: { search: RequestsSearch }) {
  const { t } = useTranslation(["requests", "common"]);
  const { projectId, project, me } = useCurrentProject();
  const settings = useQuery({ ...q.project(projectId), enabled: projectId !== "" });
  // Under Owners only, a member sees the requests they made or own, nothing else to narrow to (ADR-0045).
  const ownOnly = settings.data?.deployment_sharing === "owner_only" && !can(me, "deployments.read_all", projectId);
  const filters = useAddressFilters(search, ["status", "mine", "q"]);
  const list = useInfiniteQuery(
    q.requests({
      projectId,
      status: search.status ? statuses[search.status] : undefined,
      requester: search.mine ? "me" : undefined,
      q: search.q,
    }),
  );

  return (
    <Page>
      <PageHeader
        title={t("list.title")}
        description={ownOnly ? t("list.leadOwn", { project: project.name }) : t("list.lead", { project: project.name })}
        actions={
          <>
            <Button asChild>
              <Link to="/schedules">
                <CalendarClock /> {t("list.schedules")}
              </Link>
            </Button>
            <Button variant="primary" asChild>
              <Link to="/catalog">
                <Plus /> {t("list.new")}
              </Link>
            </Button>
          </>
        }
      />
      <Drafts />
      <Panel
        flush
        footer={
          list.hasNextPage && (
            <Button size="sm" onClick={() => void list.fetchNextPage()} disabled={list.isFetchingNextPage}>
              {list.isFetchingNextPage ? t("common:loadingEllipsis") : t("list.loadMore")}
            </Button>
          )
        }
      >
        <DataTable
          label={t("list.table")}
          columns={columnsFor(t)}
          rows={list.data?.pages.flatMap((p) => p.items)}
          rowKey={(r) => r.id}
          rowLinks
          minWidth={880}
          loading={list.isPending}
          error={list.isError && <ErrorState error={list.error} onRetry={() => void list.refetch()} />}
          toolbar={
            <FilterBar filters={filters} search={{ label: t("list.search"), placeholder: t("list.searchPlaceholder") }}>
              <FilterSelect
                name="status"
                label={t("list.status")}
                any={t("list.allStatuses")}
                options={requestFilters.map((k) => ({ value: k, label: t(`list.filter.${k}`) }))}
                className="w-48"
              />
              {!ownOnly && <FilterCheck name="mine" label={t("list.onlyMine")} />}
            </FilterBar>
          }
          empty={
            filters.filtered ? (
              <FilterEmpty filters={filters} title={t("list.noMatch")} body={t("list.noMatchBody")} />
            ) : (
              <EmptyState
                icon={Inbox}
                title={t("list.none", { project: project.name })}
                body={t("list.noneBody")}
                action={
                  <Button size="sm" asChild>
                    <Link to="/catalog">{t("list.fromCatalog")}</Link>
                  </Button>
                }
              />
            )
          }
        />
      </Panel>
    </Page>
  );
}
