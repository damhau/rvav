import { useInfiniteQuery } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { useEffect, useState } from "react";
import { ErrorState, SkeletonRows } from "@/components/common/feedback";
import { Time } from "@/components/common/misc";
import { EmptyState, Page, PageHeader } from "@/components/common/page";
import { EnvBadge, RequestStatusBadge } from "@/components/common/status";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import type { RequestStatus } from "@/lib/api/types";
import { useCurrentProject } from "@/lib/project";
import { q } from "@/lib/query";
import { cn } from "@/lib/utils";
import { Drafts } from "./Drafts";
import { ProgressSegments } from "./ProgressBar";

export interface RequestsSearch {
  status?: string;
  mine?: boolean;
  q?: string;
}

const filters: { key: string; label: string; statuses?: RequestStatus[] }[] = [
  { key: "all", label: "All" },
  { key: "open", label: "Open", statuses: ["pending_approval", "scheduled", "queued", "running", "cancelling"] },
  { key: "pending_approval", label: "Awaiting approval", statuses: ["pending_approval"] },
  { key: "failed", label: "Failed", statuses: ["failed"] },
  { key: "succeeded", label: "Succeeded", statuses: ["succeeded"] },
];

export function RequestsPage({ search }: { search: RequestsSearch }) {
  const { projectId, project } = useCurrentProject();
  const navigate = useNavigate({ from: "/requests/" });
  const [text, setText] = useState(search.q ?? "");
  const active = filters.find((f) => f.key === (search.status ?? "all")) ?? filters[0]!;
  const list = useInfiniteQuery(
    q.requests({ projectId, status: active.statuses, requester: search.mine ? "me" : undefined, q: search.q }),
  );

  useEffect(() => {
    const t = setTimeout(() => {
      const next = text.trim() || undefined;
      if (next !== search.q) void navigate({ search: (s: RequestsSearch) => ({ ...s, q: next }), replace: true });
    }, 250);
    return () => clearTimeout(t);
  }, [text, search.q, navigate]);

  const items = list.data?.pages.flatMap((p) => p.items) ?? [];
  return (
    <Page>
      <PageHeader title="Requests" meta={`Everything requested in ${project.name}, newest first.`} />
      <Drafts />
      <div className="flex flex-wrap items-center gap-2">
        <label htmlFor="requests-search" className="sr-only">
          Filter requests
        </label>
        <Input
          id="requests-search"
          type="search"
          placeholder="Number, title or person"
          value={text}
          onChange={(e) => setText(e.target.value)}
          className="w-64 max-w-full"
        />
        <div role="group" aria-label="Status" className="flex flex-wrap gap-1.5">
          {filters.map((f) => (
            <Link
              key={f.key}
              to="/requests"
              search={(s: RequestsSearch) => ({ ...s, status: f.key === "all" ? undefined : f.key })}
              aria-current={f.key === active.key ? "true" : undefined}
              className={cn(
                "inline-flex h-7 items-center rounded-full border px-2.5",
                f.key === active.key
                  ? "border-fg bg-fg text-bg hover:text-bg"
                  : "border-line bg-surface text-muted hover:border-line-strong hover:text-fg",
              )}
            >
              {f.label}
            </Link>
          ))}
        </div>
        <label className="ml-auto flex items-center gap-2 text-xs text-muted">
          <input
            type="checkbox"
            checked={Boolean(search.mine)}
            onChange={(e) =>
              void navigate({ search: (s: RequestsSearch) => ({ ...s, mine: e.target.checked || undefined }) })
            }
            className="size-3.5 accent-[var(--accent)]"
          />
          Only mine
        </label>
      </div>

      <div className="overflow-x-auto rounded-lg border border-line bg-surface">
        {list.isPending ? (
          <SkeletonRows rows={6} cols={5} />
        ) : list.isError ? (
          <ErrorState error={list.error} onRetry={() => void list.refetch()} />
        ) : items.length === 0 ? (
          <EmptyState
            title="No requests match"
            body="Clear the search or choose “All” to see every request in this project."
          />
        ) : (
          <table className="w-full min-w-[880px] border-collapse">
            <thead>
              <tr className="label text-left">
                <th scope="col" className="border-b border-line px-4 py-2.5 font-medium">
                  Request
                </th>
                <th scope="col" className="border-b border-line px-3 py-2.5 font-medium">
                  Status
                </th>
                <th scope="col" className="border-b border-line px-3 py-2.5 font-medium">
                  Env
                </th>
                <th scope="col" className="border-b border-line px-3 py-2.5 font-medium">
                  Step
                </th>
                <th scope="col" className="border-b border-line px-3 py-2.5 font-medium">
                  Requested by
                </th>
                <th scope="col" className="border-b border-line px-4 py-2.5 text-right font-medium">
                  Submitted
                </th>
              </tr>
            </thead>
            <tbody>
              {items.map((r) => (
                <tr key={r.id} className="hover:bg-sunken">
                  <td className="border-b border-line px-4 py-2.5">
                    <Link
                      to="/requests/$requestId"
                      params={{ requestId: r.number }}
                      className="font-medium text-fg hover:text-accent"
                    >
                      {r.title}
                    </Link>
                    <div className="font-mono text-[11px] text-faint">{r.number}</div>
                  </td>
                  <td className="border-b border-line px-3 py-2.5">
                    <RequestStatusBadge status={r.status} />
                  </td>
                  <td className="border-b border-line px-3 py-2.5">
                    <EnvBadge env={r.environment} />
                  </td>
                  <td className="border-b border-line px-3 py-2.5">
                    <div className="flex flex-col gap-1">
                      <span className="text-muted">
                        {r.current_step?.title ??
                          (r.status === "failed"
                            ? r.error?.message
                            : `${r.progress.done} of ${r.progress.total} steps`)}
                      </span>
                      {(r.status === "running" || r.status === "cancelling") && <ProgressSegments request={r} />}
                    </div>
                  </td>
                  <td className="border-b border-line px-3 py-2.5 text-muted">{r.requester.name}</td>
                  <td className="border-b border-line px-4 py-2.5 text-right text-muted">
                    <Time iso={r.submitted_at} />
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
      {list.hasNextPage && (
        <Button className="self-start" onClick={() => void list.fetchNextPage()} disabled={list.isFetchingNextPage}>
          {list.isFetchingNextPage ? "Loading…" : "Load more"}
        </Button>
      )}
    </Page>
  );
}
