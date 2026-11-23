// A subscription's runs: each event delivered to it, with what was sent,
// every attempt and what came back; failed runs can be tried again.
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useSearch } from "@tanstack/react-router";
import { History } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { ErrorState, SkeletonRows } from "@/components/common/feedback";
import { KeyValues } from "@/components/common/page";
import { DataTable, type Column } from "@/components/ui/data-table";
import { EmptyState } from "@/components/ui/empty-state";
import { FilterBar, FilterEmpty, FilterSelect, useAddressFilters } from "@/components/ui/filter-bar";
import { Panel } from "@/components/ui/panel";
import { Mono, Time } from "@/components/common/misc";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent } from "@/components/ui/dialog";
import { api, call } from "@/lib/api/client";
import type { components } from "@/lib/api/schema";
import { i18n } from "@/lib/i18n";
import { adm } from "@/lib/query";
import { fullDate, relative } from "@/lib/format";
import { failed } from "../governance/common";

type S = components["schemas"];
type RunStatusKey = S["SubscriptionRunStatus"];

const tx = i18n.getFixedT(null, "admin-extensibility");

const statusTone: Record<S["SubscriptionRunStatus"], "ok" | "bad" | "warn" | "run" | "neutral"> = {
  queued: "neutral",
  delivering: "run",
  delivered: "ok",
  failed: "bad",
  blocked: "warn",
};

export const statusKeys = ["queued", "delivering", "delivered", "failed", "blocked"] as const satisfies RunStatusKey[];

/** The runs list's filter, in the subscription's address. */
export interface RunsSearch {
  status?: (typeof statusKeys)[number];
}

export function RunStatus({ status }: { status: RunStatusKey }) {
  const { t } = useTranslation("admin-extensibility");
  return <Badge tone={statusTone[status]}>{t(`run.status.${status}`)}</Badge>;
}

/** What a blocking answer changed on its VM, as rvav checked and applied it (ADR-0054). */
export function changesText(c: S["MachineChange"]): string {
  const parts: string[] = [];
  if (c.name) parts.push(tx("machineChange.renamed", { name: c.name }));
  const p = c.placement;
  if (p) {
    const where = [
      p.cluster && tx("machineChange.cluster", { name: p.cluster }),
      p.resource_pool && tx("machineChange.resourcePool", { name: p.resource_pool }),
      p.datastore && tx("machineChange.datastore", { name: p.datastore }),
      p.folder && tx("machineChange.folder", { name: p.folder }),
    ].filter(Boolean);
    if (where.length > 0) parts.push(tx("machineChange.moved", { where: where.join(", ") }));
  }
  for (const n of c.networks ?? []) {
    const what = [
      n.network && tx("machineChange.network", { name: n.network }),
      n.address && tx("machineChange.address", { address: n.address }),
    ].filter(Boolean);
    if (what.length > 0) parts.push(tx("machineChange.adapter", { index: n.index, what: what.join(", ") }));
  }
  return parts.join(tx("machineChange.join"));
}

function attemptText(a: S["SubscriptionRunAttempt"]): string {
  const parts: string[] = [];
  if (a.status_code) parts.push(tx("attempt.http", { code: a.status_code }));
  if (a.job_id) parts.push(tx("attempt.job", { id: a.job_id }));
  if (a.workflow_id) parts.push(tx("attempt.workflow", { id: a.workflow_id }));
  parts.push(tx("attempt.ms", { ms: a.duration_ms }));
  return parts.join(" · ");
}

export function RunDialog({
  runId,
  initial,
  title,
  canRetry,
  onClose,
}: {
  runId: string;
  initial?: S["SubscriptionRunDetail"];
  title?: string;
  canRetry?: boolean;
  onClose: () => void;
}) {
  const { t } = useTranslation("admin-extensibility");
  const qc = useQueryClient();
  const run = useQuery({ ...adm.subscriptionRun(runId), initialData: initial });
  const retry = useMutation({
    mutationFn: () => call(api.POST("/admin/subscription-runs/{id}/retry", { params: { path: { id: runId } } })),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["admin", "subscription-run", runId] });
      void qc.invalidateQueries({ queryKey: ["admin", "subscription-runs"] });
      toast.success(t("run.retried"));
    },
    onError: (e) => failed(e, t("run.notRetried")),
  });
  const r = run.data;
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent title={title ?? t("run.title")} className="w-[min(820px,calc(100vw-32px))]">
        <div className="flex max-h-[78vh] flex-col gap-4 overflow-y-auto p-4">
          {run.isPending && <SkeletonRows rows={4} cols={2} />}
          {run.isError && <ErrorState error={run.error} onRetry={() => void run.refetch()} />}
          {r && (
            <>
              <KeyValues
                items={[
                  { label: t("shared.status"), value: <RunStatus status={r.status} /> },
                  { label: t("shared.topic"), value: <Mono>{r.topic}</Mono> },
                  ...(r.recovery ? [{ label: t("run.runOf"), value: t("run.recoveryOf") }] : []),
                  ...(r.request
                    ? [
                        {
                          label: t("run.request"),
                          value: (
                            <Link
                              to="/requests/$requestId"
                              params={{ requestId: r.request.id }}
                              className="text-accent hover:underline"
                            >
                              {r.request.number}
                            </Link>
                          ),
                        },
                      ]
                    : []),
                  // A recovery that answered without an outcome left it to the subscription's setting (ADR-0054).
                  ...(r.outcome
                    ? [{ label: t("run.answer"), value: r.outcome }]
                    : r.recovery && r.status === "delivered"
                      ? [{ label: t("run.answer"), value: t("run.noAnswer") }]
                      : []),
                  ...(r.reason ? [{ label: t("run.why"), value: r.reason }] : []),
                  ...(r.next_attempt_at && r.status === "queued"
                    ? [{ label: t("run.nextAttempt"), value: <Time iso={r.next_attempt_at} /> }]
                    : []),
                  ...(r.properties && Object.keys(r.properties).length > 0
                    ? [
                        {
                          label: t("run.properties"),
                          value: (
                            <Mono>
                              {Object.entries(r.properties)
                                .map(([k, v]) => `${k}=${v}`)
                                .join(", ")}
                            </Mono>
                          ),
                        },
                      ]
                    : []),
                  ...(r.changes && changesText(r.changes)
                    ? [{ label: t("run.changed"), value: changesText(r.changes) }]
                    : []),
                ]}
              />
              <section className="flex flex-col gap-2">
                <h3 className="text-secondary font-medium">{t("run.attempts")}</h3>
                {r.history.length === 0 && <p className="text-secondary text-muted">{t("run.notTried")}</p>}
                <ol className="flex flex-col gap-2">
                  {r.history.map((a, i) => (
                    <li key={a.at + i} className="rounded-md border border-line p-2 text-secondary">
                      <div className="flex flex-wrap items-center gap-2">
                        <Badge tone={a.ok ? "ok" : "bad"}>{a.ok ? t("run.ok") : t("run.failed")}</Badge>
                        <Time iso={a.at} />
                        <span className="text-muted">{attemptText(a)}</span>
                      </div>
                      {a.error && <p className="mt-1 text-bad">{a.error}</p>}
                      {a.response && (
                        <pre className="mt-1 max-h-32 overflow-auto rounded bg-sunken p-2 font-mono text-caption">
                          {a.response}
                        </pre>
                      )}
                    </li>
                  ))}
                </ol>
              </section>
              <details className="text-secondary">
                <summary className="cursor-pointer text-muted">{t("run.sent")}</summary>
                <pre className="mt-1 max-h-80 overflow-auto rounded bg-sunken p-2 font-mono text-caption">
                  {JSON.stringify(r.payload, null, 2)}
                </pre>
              </details>
              {canRetry && r.status === "failed" && (
                <div className="flex justify-end">
                  <Button size="sm" variant="primary" disabled={retry.isPending} onClick={() => retry.mutate()}>
                    {t("run.retry")}
                  </Button>
                </div>
              )}
            </>
          )}
        </div>
      </DialogContent>
    </Dialog>
  );
}

export function RunsPanel({ subscriptionId, canRetry }: { subscriptionId: string; canRetry: boolean }) {
  const { t } = useTranslation(["admin-extensibility", "common"]);
  const search: RunsSearch = useSearch({ from: "/admin/subscriptions/$subscriptionId" });
  const filters = useAddressFilters(search, ["status"]);
  const [open, setOpen] = useState<string | null>(null);
  const runs = useInfiniteQuery(adm.subscriptionRuns(subscriptionId, search.status ?? "any"));
  const columns: Column<S["SubscriptionRun"]>[] = [
    {
      id: "when",
      header: t("runs.when"),
      sortValue: (r) => r.created_at,
      cell: (r) => (
        <>
          <button type="button" className="font-medium text-fg hover:text-accent" onClick={() => setOpen(r.id)}>
            <span className="sr-only">{t("runs.open")} </span>
            {relative(r.created_at)}
          </button>
          {r.test && (
            <Badge tone="outline" className="ml-2">
              {t("runs.test")}
            </Badge>
          )}
          {r.recovery && (
            <Badge tone="outline" className="ml-2">
              {t("runs.recovery")}
            </Badge>
          )}
          <div className="text-secondary text-faint">{fullDate(r.created_at)}</div>
        </>
      ),
    },
    {
      id: "status",
      header: t("shared.status"),
      cell: (r) => (
        <>
          <RunStatus status={r.status} />
          {r.reason && <div className="mt-0.5 max-w-sm truncate text-secondary text-muted">{r.reason}</div>}
        </>
      ),
    },
    {
      id: "request",
      header: t("run.request"),
      cell: (r) =>
        r.request ? (
          <Link
            to="/requests/$requestId"
            params={{ requestId: r.request.id }}
            className="font-mono text-secondary text-fg hover:text-accent"
          >
            {r.request.number}
          </Link>
        ) : (
          <span className="text-secondary text-faint">{t("shared.none")}</span>
        ),
    },
    {
      id: "attempts",
      header: t("run.attempts"),
      align: "end",
      sortValue: (r) => r.attempts,
      cell: (r) => r.attempts,
    },
  ];
  const statuses = statusKeys.map((value) => ({ value, label: t(`run.status.${value}`) }));
  const statusLabelOf = statuses.find((s) => s.value === search.status)?.label ?? "";
  return (
    <Panel
      title={t("runs.title")}
      description={t("runs.lead")}
      flush
      footer={
        runs.hasNextPage && (
          <Button size="sm" disabled={runs.isFetchingNextPage} onClick={() => void runs.fetchNextPage()}>
            {runs.isFetchingNextPage ? t("common:loadingEllipsis") : t("runs.loadMore")}
          </Button>
        )
      }
    >
      <DataTable
        label={t("runs.title")}
        columns={columns}
        rows={runs.data?.pages.flatMap((p) => p.items)}
        rowKey={(r) => r.id}
        minWidth={720}
        loading={runs.isPending}
        error={runs.isError && <ErrorState error={runs.error} onRetry={() => void runs.refetch()} />}
        toolbar={
          <FilterBar filters={filters}>
            <FilterSelect
              name="status"
              label={t("shared.status")}
              any={t("run.anyStatus")}
              options={statuses}
              className="w-48"
            />
          </FilterBar>
        }
        empty={
          filters.filtered ? (
            <FilterEmpty
              filters={filters}
              icon={History}
              title={t("runs.noneWith", { status: statusLabelOf })}
              body={t("runs.noneWithBody")}
            />
          ) : (
            <EmptyState icon={History} title={t("runs.none")} body={t("runs.noneBody")} />
          )
        }
      />
      {open && <RunDialog runId={open} canRetry={canRetry} onClose={() => setOpen(null)} />}
    </Panel>
  );
}
