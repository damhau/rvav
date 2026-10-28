import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { ExternalLink } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { ApprovalHistory } from "@/components/common/approvals";
import { ErrorState, Skeleton, SkeletonRows } from "@/components/common/feedback";
import { Time } from "@/components/common/misc";
import { Page, Panel } from "@/components/common/page";
import { EnvBadge, RequestStatusBadge } from "@/components/common/status";
import { Timeline } from "@/components/common/timeline";
import { Button } from "@/components/ui/button";
import { Dialog, DialogClose, DialogContent } from "@/components/ui/dialog";
import { api, call, isApiError } from "@/lib/api/client";
import type { RequestStatus, ServiceRequest, Step } from "@/lib/api/types";
import { useNow } from "@/lib/hooks";
import { useRequestStream } from "@/lib/live/LiveProvider";
import { useCurrentProject } from "@/lib/project";
import { keys, q } from "@/lib/query";
import { duration, toText } from "@/lib/utils";
import { LogViewer } from "./LogViewer";

const cancellable: RequestStatus[] = ["pending_approval", "scheduled", "queued", "running"];

function formatValue(v: unknown): string {
  if (Array.isArray(v)) return v.join(", ") || "none";
  if (v === null || v === undefined || v === "") return "–";
  return toText(v);
}

function CancelButton({ request }: { request: ServiceRequest }) {
  const qc = useQueryClient();
  const [open, setOpen] = useState(false);
  const cancel = useMutation({
    mutationFn: () => call(api.POST("/requests/{requestId}/cancel", { params: { path: { requestId: request.id } } })),
    onSuccess: (r) => {
      qc.setQueryData(keys.requests.detail(r.number), r);
      setOpen(false);
      toast(r.status === "cancelled" ? `${r.number} cancelled` : `Cancelling ${r.number}`, {
        description:
          r.status === "cancelled" ? undefined : "It stops after the current step and rolls back what it created.",
      });
    },
    onError: (e) =>
      toast.error(isApiError(e) ? e.problem.title : "Couldn't cancel the request", {
        description: isApiError(e) ? e.problem.detail : undefined,
      }),
  });
  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <Button variant="danger" onClick={() => setOpen(true)}>
        Cancel request
      </Button>
      <DialogContent
        title={`Cancel ${request.number}?`}
        description={
          request.status === "running"
            ? "The request stops after the step that is running now. The VM, IP address and DNS records it created are removed."
            : "The request is withdrawn before anything is created."
        }
      >
        <div className="flex justify-end gap-2 px-4 py-3">
          <DialogClose asChild>
            <Button>Keep it running</Button>
          </DialogClose>
          <Button variant="danger-solid" onClick={() => cancel.mutate()} disabled={cancel.isPending}>
            Cancel request
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}

/** Retry from the failed step, or roll back what a failed provisioning request built. */
function FailureActions({ request }: { request: ServiceRequest }) {
  const qc = useQueryClient();
  const [confirmRollback, setConfirmRollback] = useState(false);
  const done = (r: ServiceRequest, what: string) => {
    qc.setQueryData(keys.requests.detail(r.number), r);
    void qc.invalidateQueries({ queryKey: keys.requests.steps(r.id) });
    if (r.deployment_ids?.[0]) void qc.invalidateQueries({ queryKey: keys.deployments.detail(r.deployment_ids[0]) });
    toast.success(`${r.number}: ${what}`);
  };
  const failed = (e: unknown) =>
    toast.error(isApiError(e) ? e.problem.title : "That didn't work", {
      description: isApiError(e) ? e.problem.detail : undefined,
    });
  const params = { params: { path: { requestId: request.id } } };
  const retry = useMutation({
    mutationFn: () => call(api.POST("/requests/{requestId}/retry", params)),
    onSuccess: (r) => done(r, "running again from the failed step"),
    onError: failed,
  });
  const rollback = useMutation({
    mutationFn: () => call(api.POST("/requests/{requestId}/rollback", params)),
    onSuccess: (r) => {
      setConfirmRollback(false);
      done(r, "rolling back");
    },
    onError: failed,
  });
  return (
    <div role="alert" className="mt-1 flex flex-col gap-2 rounded-lg border border-bad bg-bad-soft px-3.5 py-3">
      <span className="font-semibold text-bad">{request.error?.message}</span>
      <span className="text-xs">
        {request.kind === "day2"
          ? "Retrying runs the failed step and the ones after it; the steps that were done are kept."
          : "What was created is kept so you can look at it. Retry runs the failed step and the ones after it; Roll back deletes the VM, its IP address and DNS records."}
      </span>
      <div className="flex flex-wrap gap-2">
        <Button size="sm" variant="primary" onClick={() => retry.mutate()} disabled={retry.isPending}>
          Retry from the failed step
        </Button>
        {request.kind !== "day2" && (
          <Dialog open={confirmRollback} onOpenChange={setConfirmRollback}>
            <Button size="sm" variant="danger" onClick={() => setConfirmRollback(true)}>
              Roll back
            </Button>
            <DialogContent
              title={`Roll back ${request.number}?`}
              description="Deletes what it created: the VM, its IP address and DNS records. You can retry it afterwards."
            >
              <div className="flex justify-end gap-2 px-4 py-3">
                <DialogClose asChild>
                  <Button>Keep it</Button>
                </DialogClose>
                <Button variant="danger-solid" onClick={() => rollback.mutate()} disabled={rollback.isPending}>
                  Roll back
                </Button>
              </div>
            </DialogContent>
          </Dialog>
        )}
      </div>
    </div>
  );
}

function pickLogStep(steps: Step[], chosen: string | null): Step | undefined {
  const awx = steps.filter((s) => s.has_log);
  return (
    awx.find((s) => s.id === chosen) ??
    awx.find((s) => s.status === "running") ??
    awx.findLast((s) => s.status !== "pending") ??
    awx[0]
  );
}

export function RequestDetailPage({ requestKey }: { requestKey: string }) {
  useNow(1000);
  const request = useQuery(q.request(requestKey));
  const id = request.data?.id;
  const { projectId } = useCurrentProject();
  useRequestStream(id, Boolean(request.data && request.data.project.id !== projectId));
  const steps = useQuery({ ...q.steps(id ?? ""), enabled: Boolean(id) });
  const approvals = useQuery({ ...q.requestApprovals(id ?? ""), enabled: Boolean(id) });
  const levels = approvals.data?.items ?? [];
  const mine = levels.find((a) => a.can_decide);
  const [chosenLog, setChosenLog] = useState<string | null>(null);

  if (request.isError) {
    return (
      <Page>
        <ErrorState
          error={request.error}
          onRetry={() => void request.refetch()}
          className="rounded-lg border border-line bg-surface"
        />
      </Page>
    );
  }
  const r = request.data;
  const logStep = steps.data ? pickLogStep(steps.data.items, chosenLog) : undefined;
  const deploymentId = r?.deployment_ids?.[0];
  const running = r && ["running", "queued", "cancelling"].includes(r.status);
  const elapsed = r?.started_at ? duration(r.started_at, r.finished_at) : null;

  return (
    <Page>
      <div className="flex flex-col gap-1.5">
        <span className="text-xs text-faint">
          <Link to="/requests">Requests</Link> / {r?.number ?? requestKey}
        </span>
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div className="flex min-w-0 flex-wrap items-center gap-2.5">
            <h1 className="text-lg font-semibold tracking-[-0.01em]">
              {r ? r.title : <Skeleton className="h-5 w-72" />}
            </h1>
            {r && <RequestStatusBadge status={r.status} />}
          </div>
          {r && (
            <div className="flex flex-wrap gap-2">
              {cancellable.includes(r.status) && <CancelButton request={r} />}
              {r.kind === "provision" && (
                <Button variant="secondary" asChild>
                  <Link
                    to="/catalog/$itemId/request"
                    params={{ itemId: r.catalog_item.slug }}
                    search={{ from: r.number }}
                  >
                    Request again
                  </Link>
                </Button>
              )}
              {deploymentId && (
                <Button variant={r.status === "succeeded" ? "primary" : "secondary"} asChild>
                  <Link to="/deployments/$deploymentId" params={{ deploymentId }}>
                    Open deployment
                  </Link>
                </Button>
              )}
            </div>
          )}
        </div>
        {r && (
          <div className="flex flex-wrap items-center gap-x-3.5 gap-y-1 text-xs text-muted">
            <span>Requested by {r.requester.name}</span>
            <span className="flex items-center gap-1.5">
              {r.project.name} <EnvBadge env={r.environment} />
            </span>
            {r.kind === "day2" && r.deployment_id ? (
              <span>
                {r.action?.name} on{" "}
                <Link to="/deployments/$deploymentId" params={{ deploymentId: r.deployment_id }}>
                  {r.resource?.name ?? "the deployment"}
                </Link>
              </span>
            ) : (
              <span>
                {r.catalog_item.name} {r.version.number}
              </span>
            )}
            <span>
              Submitted <Time iso={r.submitted_at} />
            </span>
            <span className="tnum">
              {r.status === "succeeded"
                ? `All ${r.progress.total} steps done`
                : running
                  ? `Step ${Math.min(r.progress.total, r.progress.done + 1)} of ${r.progress.total}`
                  : `${r.progress.done} of ${r.progress.total} steps done`}
              {elapsed ? ` · ${elapsed}` : ""}
            </span>
          </div>
        )}
      </div>

      <div className="flex flex-wrap items-start gap-4">
        <Panel
          className="flex-[999_1_520px]"
          title="Steps"
          actions={
            running ? (
              <span className="flex items-center gap-1.5 text-[11px] text-faint">
                <span className="size-1.5 rounded-full bg-ok" aria-hidden /> Live
              </span>
            ) : null
          }
        >
          {steps.isPending ? (
            <SkeletonRows rows={6} cols={2} />
          ) : steps.isError ? (
            <ErrorState error={steps.error} onRetry={() => void steps.refetch()} className="p-0" />
          ) : (
            <Timeline
              steps={steps.data.items}
              onOpenLog={(s) => setChosenLog(s.id)}
              selectedLogStep={logStep?.id ?? null}
            />
          )}
          {r?.status === "failed" && r.error && <FailureActions request={r} />}
        </Panel>

        <div className="flex min-w-0 flex-[1_1_380px] flex-col gap-4">
          <section className="overflow-hidden rounded-lg border border-line bg-surface">
            <div className="flex items-center justify-between gap-2 border-b border-line px-4 py-2.5">
              <h2 className="text-[13px] font-semibold">AWX job output</h2>
              <span className="truncate font-mono text-[11px] text-faint">
                {logStep?.title.replace(/^AWX /, "") ?? "no AWX job"}
              </span>
            </div>
            {id && logStep ? (
              <LogViewer requestId={id} step={logStep} />
            ) : (
              <p className="px-4 py-6 text-xs text-muted">
                This request has no AWX job, or it hasn't been planned yet.
              </p>
            )}
            {logStep?.external_ref?.url && (
              <div className="flex justify-between border-t border-line px-4 py-2 text-xs">
                <span className="text-faint">Follows the job live.</span>
                <a
                  href={logStep.external_ref.url}
                  target="_blank"
                  rel="noreferrer"
                  className="inline-flex items-center gap-1"
                >
                  Open in AWX <ExternalLink className="size-3" aria-hidden />
                </a>
              </div>
            )}
          </section>

          {levels.length > 0 && (
            <Panel
              title="Approval"
              actions={
                mine ? (
                  <Button size="sm" variant="primary" asChild>
                    <Link to="/approvals" search={{ id: mine.id }}>
                      Decide
                    </Link>
                  </Button>
                ) : null
              }
            >
              <ApprovalHistory levels={levels} />
            </Panel>
          )}

          <Panel title="What was asked for">
            {r ? (
              <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-3.5 gap-y-1.5">
                {Object.entries(r.inputs ?? {}).map(([k, v]) => (
                  <div key={k} className="contents">
                    <dt className="font-mono text-[12px] text-muted">{k}</dt>
                    <dd className="m-0 font-mono text-[12px] break-words">{formatValue(v)}</dd>
                  </div>
                ))}
                {r.justification && (
                  <div className="contents">
                    <dt className="text-muted">Reason</dt>
                    <dd className="m-0">{r.justification}</dd>
                  </div>
                )}
              </dl>
            ) : (
              <Skeleton className="h-24" />
            )}
          </Panel>
        </div>
      </div>
    </Page>
  );
}
