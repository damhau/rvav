// A subscription's runs: each event delivered to it, with what was sent,
// every attempt and what came back; failed runs can be tried again.
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { History } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { ErrorState, SkeletonRows } from "@/components/common/feedback";
import { KeyValues } from "@/components/common/page";
import { DataTable, type Column } from "@/components/ui/data-table";
import { EmptyState } from "@/components/ui/empty-state";
import { Panel } from "@/components/ui/panel";
import { Mono, Time } from "@/components/common/misc";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent } from "@/components/ui/dialog";
import { Select } from "@/components/ui/select";
import { api, call } from "@/lib/api/client";
import type { components } from "@/lib/api/schema";
import { adm } from "@/lib/query";
import { fullDate, relative } from "@/lib/format";
import { failed } from "../governance/common";

type S = components["schemas"];

const statusTone: Record<S["SubscriptionRunStatus"], "ok" | "bad" | "warn" | "run" | "neutral"> = {
  queued: "neutral",
  delivering: "run",
  delivered: "ok",
  failed: "bad",
  blocked: "warn",
};

const statusLabel: Record<S["SubscriptionRunStatus"], string> = {
  queued: "Waiting",
  delivering: "Delivering",
  delivered: "Delivered",
  failed: "Failed",
  blocked: "Stopped the request",
};

export function RunStatus({ status }: { status: S["SubscriptionRunStatus"] }) {
  return <Badge tone={statusTone[status]}>{statusLabel[status]}</Badge>;
}

/** What a blocking answer changed on its VM, as rvav checked and applied it (ADR-0054). */
export function changesText(c: S["MachineChange"]): string {
  const parts: string[] = [];
  if (c.name) parts.push(`renamed ${c.name}`);
  const p = c.placement;
  if (p) {
    const where = [
      p.cluster && `cluster ${p.cluster}`,
      p.resource_pool && `resource pool ${p.resource_pool}`,
      p.datastore && `datastore ${p.datastore}`,
      p.folder && `folder ${p.folder}`,
    ].filter(Boolean);
    if (where.length > 0) parts.push(`moved to ${where.join(", ")}`);
  }
  for (const n of c.networks ?? []) {
    const what = [n.network && `network ${n.network}`, n.address && `address ${n.address}`].filter(Boolean);
    if (what.length > 0) parts.push(`adapter ${n.index}: ${what.join(", ")}`);
  }
  return parts.join("; ");
}

function attemptText(a: S["SubscriptionRunAttempt"]): string {
  const parts: string[] = [];
  if (a.status_code) parts.push(`HTTP ${a.status_code}`);
  if (a.job_id) parts.push(`AWX job ${a.job_id}`);
  if (a.workflow_id) parts.push(`workflow ${a.workflow_id}`);
  parts.push(`${a.duration_ms} ms`);
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
  const qc = useQueryClient();
  const run = useQuery({ ...adm.subscriptionRun(runId), initialData: initial });
  const retry = useMutation({
    mutationFn: () => call(api.POST("/admin/subscription-runs/{id}/retry", { params: { path: { id: runId } } })),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["admin", "subscription-run", runId] });
      void qc.invalidateQueries({ queryKey: ["admin", "subscription-runs"] });
      toast.success("It will be delivered again");
    },
    onError: (e) => failed(e, "Not retried"),
  });
  const r = run.data;
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent title={title ?? "Run"} className="w-[min(820px,calc(100vw-32px))]">
        <div className="flex max-h-[78vh] flex-col gap-4 overflow-y-auto p-4">
          {run.isPending && <SkeletonRows rows={4} cols={2} />}
          {run.isError && <ErrorState error={run.error} onRetry={() => void run.refetch()} />}
          {r && (
            <>
              <KeyValues
                items={[
                  { label: "Status", value: <RunStatus status={r.status} /> },
                  { label: "Topic", value: <Mono>{r.topic}</Mono> },
                  ...(r.recovery
                    ? [{ label: "Run of", value: "Its recovery target, after the subscription failed" }]
                    : []),
                  ...(r.request
                    ? [
                        {
                          label: "Request",
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
                  ...(r.outcome ? [{ label: "Answer", value: r.outcome }] : []),
                  ...(r.reason ? [{ label: "Why", value: r.reason }] : []),
                  ...(r.next_attempt_at && r.status === "queued"
                    ? [{ label: "Next attempt", value: <Time iso={r.next_attempt_at} /> }]
                    : []),
                  ...(r.properties && Object.keys(r.properties).length > 0
                    ? [
                        {
                          label: "Properties set",
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
                    ? [{ label: "Changed on the VM", value: changesText(r.changes) }]
                    : []),
                ]}
              />
              <section className="flex flex-col gap-2">
                <h3 className="text-secondary font-medium">Attempts</h3>
                {r.history.length === 0 && <p className="text-secondary text-muted">Not tried yet.</p>}
                <ol className="flex flex-col gap-2">
                  {r.history.map((a, i) => (
                    <li key={a.at + i} className="rounded-md border border-line p-2 text-secondary">
                      <div className="flex flex-wrap items-center gap-2">
                        <Badge tone={a.ok ? "ok" : "bad"}>{a.ok ? "OK" : "Failed"}</Badge>
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
                <summary className="cursor-pointer text-muted">What was sent</summary>
                <pre className="mt-1 max-h-80 overflow-auto rounded bg-sunken p-2 font-mono text-caption">
                  {JSON.stringify(r.payload, null, 2)}
                </pre>
              </details>
              {canRetry && r.status === "failed" && (
                <div className="flex justify-end">
                  <Button size="sm" variant="primary" disabled={retry.isPending} onClick={() => retry.mutate()}>
                    Try again
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

const statuses = [
  { value: "any", label: "Any status" },
  { value: "queued", label: "Waiting" },
  { value: "delivering", label: "Delivering" },
  { value: "delivered", label: "Delivered" },
  { value: "failed", label: "Failed" },
  { value: "blocked", label: "Stopped the request" },
];

export function RunsPanel({ subscriptionId, canRetry }: { subscriptionId: string; canRetry: boolean }) {
  const [status, setStatus] = useState("any");
  const [open, setOpen] = useState<string | null>(null);
  const runs = useInfiniteQuery(adm.subscriptionRuns(subscriptionId, status));
  const columns: Column<S["SubscriptionRun"]>[] = [
    {
      id: "when",
      header: "When",
      sortValue: (r) => r.created_at,
      cell: (r) => (
        <>
          <button type="button" className="font-medium text-fg hover:text-accent" onClick={() => setOpen(r.id)}>
            <span className="sr-only">Open the run from </span>
            {relative(r.created_at)}
          </button>
          {r.test && (
            <Badge tone="outline" className="ml-2">
              Test
            </Badge>
          )}
          {r.recovery && (
            <Badge tone="outline" className="ml-2">
              Recovery
            </Badge>
          )}
          <div className="text-secondary text-faint">{fullDate(r.created_at)}</div>
        </>
      ),
    },
    {
      id: "status",
      header: "Status",
      cell: (r) => (
        <>
          <RunStatus status={r.status} />
          {r.reason && <div className="mt-0.5 max-w-sm truncate text-secondary text-muted">{r.reason}</div>}
        </>
      ),
    },
    {
      id: "request",
      header: "Request",
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
          <span className="text-secondary text-faint">None</span>
        ),
    },
    { id: "attempts", header: "Attempts", align: "end", sortValue: (r) => r.attempts, cell: (r) => r.attempts },
  ];
  const statusLabelOf = statuses.find((s) => s.value === status)?.label ?? "";
  return (
    <Panel
      title="Runs"
      description="One for each event it heard about. Open one to see what was sent and what came back."
      flush
      footer={
        runs.hasNextPage && (
          <Button size="sm" disabled={runs.isFetchingNextPage} onClick={() => void runs.fetchNextPage()}>
            {runs.isFetchingNextPage ? "Loading…" : "Load more"}
          </Button>
        )
      }
    >
      <DataTable
        label="Runs"
        columns={columns}
        rows={runs.data?.pages.flatMap((p) => p.items)}
        rowKey={(r) => r.id}
        minWidth={720}
        loading={runs.isPending}
        error={runs.isError && <ErrorState error={runs.error} onRetry={() => void runs.refetch()} />}
        toolbar={
          <Select
            id="runs-status"
            aria-label="Status"
            value={status}
            onChange={setStatus}
            options={statuses}
            className="w-48"
          />
        }
        empty={
          status === "any" ? (
            <EmptyState
              icon={History}
              title="No run yet"
              body="It runs for each event that matches. To see one now, send it a test event from the menu above."
            />
          ) : (
            <EmptyState
              icon={History}
              title={`No run with the status ${statusLabelOf}`}
              body="Other runs may have another status."
              action={
                <Button size="sm" onClick={() => setStatus("any")}>
                  Show every run
                </Button>
              }
            />
          )
        }
      />
      {open && <RunDialog runId={open} canRetry={canRetry} onClose={() => setOpen(null)} />}
    </Panel>
  );
}
