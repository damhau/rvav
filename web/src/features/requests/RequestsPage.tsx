import { useInfiniteQuery } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { Inbox, Plus, SearchX } from "lucide-react";
import { ErrorState } from "@/components/common/feedback";
import { Time } from "@/components/common/misc";
import { Page } from "@/components/common/page";
import { EnvBadge, RequestStatusBadge } from "@/components/common/status";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { DataTable, TableSearch, type Column } from "@/components/ui/data-table";
import { EmptyState } from "@/components/ui/empty-state";
import { PageHeader } from "@/components/ui/page-header";
import { Panel } from "@/components/ui/panel";
import { Select } from "@/components/ui/select";
import type { RequestStatus, ServiceRequest } from "@/lib/api/types";
import { useSearchBox } from "@/lib/hooks";
import { useCurrentProject } from "@/lib/project";
import { q } from "@/lib/query";
import { Drafts } from "./Drafts";
import { ProgressSegments } from "./ProgressBar";

export const requestFilters = ["open", "pending_approval", "failed", "succeeded"] as const;

export interface RequestsSearch {
  status?: (typeof requestFilters)[number];
  mine?: boolean;
  q?: string;
}

const statuses: Record<(typeof requestFilters)[number], { label: string; statuses: RequestStatus[] }> = {
  open: { label: "Open", statuses: ["pending_approval", "scheduled", "queued", "running", "cancelling"] },
  pending_approval: { label: "Awaiting approval", statuses: ["pending_approval"] },
  failed: { label: "Failed", statuses: ["failed"] },
  succeeded: { label: "Succeeded", statuses: ["succeeded"] },
};

// No sortable column: the list loads page by page, newest first, and the API doesn't sort it otherwise.
const columns: Column<ServiceRequest>[] = [
  {
    id: "request",
    header: "Request",
    cell: (r) => (
      <div className="flex min-w-0 flex-col">
        <Link to="/requests/$requestId" params={{ requestId: r.number }} className="font-medium text-fg">
          {r.title}
        </Link>
        <span className="font-mono text-caption text-muted">{r.number}</span>
      </div>
    ),
  },
  { id: "status", header: "Status", cell: (r) => <RequestStatusBadge status={r.status} /> },
  { id: "env", header: "Environment", cell: (r) => <EnvBadge env={r.environment} /> },
  {
    id: "step",
    header: "Step",
    cell: (r) => (
      <div className="flex flex-col gap-1">
        <span className="text-muted">
          {r.current_step?.title ??
            (r.status === "failed" ? r.error?.message : `${r.progress.done} of ${r.progress.total} steps`)}
        </span>
        {(r.status === "running" || r.status === "cancelling") && <ProgressSegments request={r} />}
      </div>
    ),
  },
  {
    id: "requester",
    header: "Requested by",
    className: "whitespace-nowrap text-muted",
    cell: (r) => r.requester.name,
  },
  {
    id: "submitted",
    header: "Submitted",
    align: "end",
    className: "whitespace-nowrap text-muted",
    cell: (r) => <Time iso={r.submitted_at} />,
  },
];

export function RequestsPage({ search }: { search: RequestsSearch }) {
  const { projectId, project } = useCurrentProject();
  const navigate = useNavigate({ from: "/requests/" });
  // The address keeps the search, without a history entry per keystroke.
  const [text, setText] = useSearchBox(
    search.q,
    (next) => void navigate({ search: (s: RequestsSearch) => ({ ...s, q: next }), replace: true }),
  );
  const list = useInfiniteQuery(
    q.requests({
      projectId,
      status: search.status ? statuses[search.status].statuses : undefined,
      requester: search.mine ? "me" : undefined,
      q: search.q,
    }),
  );
  const filtered = Boolean(search.status || search.mine || search.q);

  const set = (patch: Partial<RequestsSearch>) =>
    void navigate({ search: (s: RequestsSearch) => ({ ...s, ...patch }), replace: true });
  const clear = () => {
    void navigate({ search: {}, replace: true });
  };

  return (
    <Page>
      <PageHeader
        title="Requests"
        description={`Everything requested in ${project.name}, newest first.`}
        actions={
          <Button variant="primary" asChild>
            <Link to="/catalog">
              <Plus /> New request
            </Link>
          </Button>
        }
      />
      <Drafts />
      <Panel
        flush
        footer={
          list.hasNextPage && (
            <Button size="sm" onClick={() => void list.fetchNextPage()} disabled={list.isFetchingNextPage}>
              {list.isFetchingNextPage ? "Loading…" : "Load more"}
            </Button>
          )
        }
      >
        <DataTable
          label="Requests"
          columns={columns}
          rows={list.data?.pages.flatMap((p) => p.items)}
          rowKey={(r) => r.id}
          rowLinks
          minWidth={880}
          loading={list.isPending}
          error={list.isError && <ErrorState error={list.error} onRetry={() => void list.refetch()} />}
          toolbar={
            <>
              <TableSearch
                label="Search requests"
                placeholder="Number, title or person"
                value={text}
                onChange={setText}
              />
              <Select
                aria-label="Status"
                value={search.status ?? "any"}
                onChange={(v) => set({ status: v === "any" ? undefined : (v as RequestsSearch["status"]) })}
                options={[
                  { value: "any", label: "All statuses" },
                  ...requestFilters.map((k) => ({ value: k, label: statuses[k].label })),
                ]}
                className="w-48"
              />
              <span className="flex items-center gap-2 text-body">
                <Checkbox
                  id="requests-mine"
                  checked={Boolean(search.mine)}
                  onChange={(v) => set({ mine: v || undefined })}
                />
                <label htmlFor="requests-mine">Only mine</label>
              </span>
              {filtered && (
                <Button variant="quiet" onClick={clear}>
                  Clear filters
                </Button>
              )}
            </>
          }
          empty={
            filtered ? (
              <EmptyState
                icon={SearchX}
                title="No requests match"
                body="Try other words, or another status."
                action={
                  <Button size="sm" onClick={clear}>
                    Clear filters
                  </Button>
                }
              />
            ) : (
              <EmptyState
                icon={Inbox}
                title={`Nothing requested in ${project.name} yet`}
                body="Requests show here from the moment they are submitted, with each step as it runs."
                action={
                  <Button size="sm" asChild>
                    <Link to="/catalog">Request something from the catalog</Link>
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
