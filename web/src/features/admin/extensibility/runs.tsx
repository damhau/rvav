// A subscription's runs: each event delivered to it, with what was sent,
// every attempt and what came back; failed runs can be tried again.
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useState } from "react";
import { toast } from "sonner";
import { ErrorState, SkeletonRows } from "@/components/common/feedback";
import { EmptyState, KeyValues, Panel } from "@/components/common/page";
import { Mono, Time } from "@/components/common/misc";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent } from "@/components/ui/dialog";
import { Select } from "@/components/ui/select";
import { api, call } from "@/lib/api/client";
import type { components } from "@/lib/api/schema";
import { adm } from "@/lib/query";
import { failed, td, th } from "../governance/common";

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
                ]}
              />
              <section className="flex flex-col gap-2">
                <h3 className="text-xs font-medium">Attempts</h3>
                {r.history.length === 0 && <p className="text-xs text-muted">Not tried yet.</p>}
                <ol className="flex flex-col gap-2">
                  {r.history.map((a, i) => (
                    <li key={a.at + i} className="rounded-md border border-line p-2 text-xs">
                      <div className="flex flex-wrap items-center gap-2">
                        <Badge tone={a.ok ? "ok" : "bad"}>{a.ok ? "OK" : "Failed"}</Badge>
                        <Time iso={a.at} />
                        <span className="text-muted">{attemptText(a)}</span>
                      </div>
                      {a.error && <p className="mt-1 text-bad">{a.error}</p>}
                      {a.response && (
                        <pre className="mt-1 max-h-32 overflow-auto rounded bg-sunken p-2 font-mono text-[11px]">
                          {a.response}
                        </pre>
                      )}
                    </li>
                  ))}
                </ol>
              </section>
              <details className="text-xs">
                <summary className="cursor-pointer text-muted">What was sent</summary>
                <pre className="mt-1 max-h-80 overflow-auto rounded bg-sunken p-2 font-mono text-[11px]">
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
  const list = runs.data?.pages.flatMap((p) => p.items) ?? [];
  return (
    <Panel
      title="Runs"
      flush
      actions={
        <Select
          id="runs-status"
          aria-label="Status"
          value={status}
          onChange={setStatus}
          options={statuses}
          className="w-44"
        />
      }
    >
      {runs.isPending && <SkeletonRows rows={4} cols={5} />}
      {runs.isError && <ErrorState error={runs.error} onRetry={() => void runs.refetch()} />}
      {runs.data && list.length === 0 && (
        <EmptyState
          title="No run"
          body="It runs for each event that matches once it exists; Test sends it the sample."
        />
      )}
      {list.length > 0 && (
        <div className="overflow-x-auto">
          <table className="w-full min-w-[720px] border-collapse text-[13px]">
            <thead>
              <tr className="label text-left">
                <th scope="col" className={`${th} pl-4`}>
                  When
                </th>
                <th scope="col" className={th}>
                  Status
                </th>
                <th scope="col" className={th}>
                  Request
                </th>
                <th scope="col" className={th}>
                  Attempts
                </th>
                <th scope="col" className={th}>
                  <span className="sr-only">Open</span>
                </th>
              </tr>
            </thead>
            <tbody>
              {list.map((r) => (
                <tr key={r.id}>
                  <td className={`${td} pl-4`}>
                    <Time iso={r.created_at} />
                    {r.test && (
                      <Badge tone="outline" className="ml-2">
                        Test
                      </Badge>
                    )}
                  </td>
                  <td className={td}>
                    <RunStatus status={r.status} />
                    {r.reason && <div className="mt-0.5 max-w-sm truncate text-xs text-muted">{r.reason}</div>}
                  </td>
                  <td className={`${td} font-mono text-xs`}>{r.request?.number ?? "—"}</td>
                  <td className={td}>{r.attempts}</td>
                  <td className={`${td} text-right`}>
                    <Button
                      size="sm"
                      variant="ghost"
                      aria-label={`Open the run of ${r.created_at}`}
                      onClick={() => setOpen(r.id)}
                    >
                      Open
                    </Button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {runs.hasNextPage && (
        <div className="flex justify-center p-3">
          <Button size="sm" disabled={runs.isFetchingNextPage} onClick={() => void runs.fetchNextPage()}>
            Load more
          </Button>
        </div>
      )}
      {open && <RunDialog runId={open} canRetry={canRetry} onClose={() => setOpen(null)} />}
    </Panel>
  );
}
