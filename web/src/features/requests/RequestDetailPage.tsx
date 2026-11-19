import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { Braces, ExternalLink, SquareTerminal } from "lucide-react";
import { lazy, Suspense, useMemo, useState } from "react";
import { toast } from "sonner";
import { blueprintGraph } from "@/components/canvas/blueprint";
import { ApprovalHistory } from "@/components/common/approvals";
import { PolicyWarnings } from "@/components/common/warnings";
import { ErrorState, Skeleton, SkeletonRows } from "@/components/common/feedback";
import { Time } from "@/components/common/misc";
import { Page } from "@/components/common/page";
import { Breadcrumbs } from "@/components/ui/breadcrumbs";
import { EmptyState } from "@/components/ui/empty-state";
import { PageHeader } from "@/components/ui/page-header";
import { Panel } from "@/components/ui/panel";
import { EnvBadge, RequestStatusBadge } from "@/components/common/status";
import { Timeline } from "@/components/common/timeline";
import { Button } from "@/components/ui/button";
import { FileDownload, isFileRef } from "@/forms/file";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { api, call, isApiError } from "@/lib/api/client";
import type { RequestStatus, ServiceRequest, Step } from "@/lib/api/types";
import { useNow } from "@/lib/hooks";
import { useRequestStream } from "@/lib/live/LiveProvider";
import { can } from "@/lib/can";
import { useCurrentProject } from "@/lib/project";
import { keys, q } from "@/lib/query";
import { duration, toText } from "@/lib/utils";
import { AskPanel } from "./AskPanel";
import { FailureActions } from "./FailureActions";
import { LogViewer } from "./LogViewer";
import { StepDataView } from "./StepDataView";
import { failedNode, flatten, pickLogStep, rollbackOf, runSteps } from "./run";

// A composite's run on its graph: React Flow loads with it, on its pages only.
const RequestRun = lazy(() => import("./RequestRun"));

const cancellable: RequestStatus[] = ["pending_approval", "scheduled", "queued", "running"];

/** A row of a table, or an object's fields, as `key: value` pairs. */
function formatItem(v: unknown): string {
  if (typeof v !== "object" || v === null || Array.isArray(v)) return toText(v);
  return Object.entries(v)
    .map(([k, x]) => `${k}: ${toText(x)}`)
    .join(", ");
}

function formatValue(v: unknown): string {
  if (Array.isArray(v))
    return v.map(formatItem).join(v.some((x) => typeof x === "object" && x !== null) ? "; " : ", ") || "none";
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
    <>
      <Button variant="danger" onClick={() => setOpen(true)}>
        Cancel request
      </Button>
      <ConfirmDialog
        open={open}
        onOpenChange={setOpen}
        title={`Cancel ${request.number}?`}
        description={
          request.status === "running"
            ? "The request stops after the step that is running now. The VM, IP address and DNS records it created are removed."
            : "The request is withdrawn before anything is created."
        }
        confirmLabel="Cancel request"
        cancelLabel="Keep it running"
        pending={cancel.isPending}
        onConfirm={() => cancel.mutate()}
      />
    </>
  );
}

export function RequestDetailPage({ requestKey }: { requestKey: string }) {
  useNow(1000);
  const request = useQuery(q.request(requestKey));
  const id = request.data?.id;
  const { me, projectId } = useCurrentProject();
  useRequestStream(id, Boolean(request.data && request.data.project.id !== projectId));
  // Request again: for those who may request in its project.
  const canRequestIn = (pid: string) => can(me, "request.submit", pid);
  const steps = useQuery({ ...q.steps(id ?? ""), enabled: Boolean(id) });
  const approvals = useQuery({ ...q.requestApprovals(id ?? ""), enabled: Boolean(id) });
  const levels = (approvals.data?.items ?? []).filter((a) => !a.step_key);
  // A composite step's wait for a person (ADR-0033) has its own panel.
  const asked = (approvals.data?.items ?? []).filter((a) => a.step_key);
  const mine = levels.find((a) => a.can_decide);
  const [chosenLog, setChosenLog] = useState<string | null>(null);
  const [chosenData, setChosenData] = useState<string | null>(null);
  // A composite's run is drawn on what its version builds (ADR-0032).
  const composite = request.data?.kind === "provision" && request.data.catalog_item.type === "composite";
  const topology = useQuery({ ...q.requestTopology(id ?? ""), enabled: Boolean(id) && composite });
  const graph = useMemo(() => {
    const g = topology.data?.topology ? blueprintGraph(topology.data.topology, "/topology") : null;
    return g?.kind === "composite" ? g : null;
  }, [topology.data]);
  // The node selected: at first, the one the request failed at.
  const [chosenNode, setChosenNode] = useState<string | null | undefined>(undefined);

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
  const all = steps.data?.items ?? [];
  const byNode = graph ? runSteps(graph, all) : undefined;
  const selectedNode = chosenNode !== undefined ? chosenNode : r && byNode ? (failedNode(r, byNode) ?? null) : null;
  // The job chosen, else the selected node's, else the request's.
  const logStep =
    (chosenLog ? flatten(all).find((s) => s.id === chosenLog && s.has_log) : undefined) ??
    pickLogStep((selectedNode && byNode?.get(selectedNode)) || [], null) ??
    pickLogStep(all, null);
  // The step chosen, else the selected node's own: its ID is its step's key.
  // A step's data holds its blueprint's literals: for catalog managers (ADR-0057).
  const readsData = can(me, "catalog.manage");
  const withData = readsData ? flatten(all).filter((s) => s.has_data) : [];
  const openData = readsData ? (s: Step) => setChosenData(s.id) : undefined;
  const dataStep =
    (chosenData ? withData.find((s) => s.id === chosenData) : undefined) ??
    (selectedNode ? withData.find((s) => s.key === selectedNode) : undefined);
  const deploymentId = r?.deployment_ids?.[0];
  const running = r && ["running", "queued", "cancelling"].includes(r.status);
  const elapsed = r?.started_at ? duration(r.started_at, r.finished_at) : null;

  return (
    <Page>
      <PageHeader
        breadcrumbs={
          <Breadcrumbs>
            <Link to="/requests">Requests</Link>
          </Breadcrumbs>
        }
        title={r ? r.title : <Skeleton className="h-5 w-72" />}
        meta={
          r && (
            <>
              <RequestStatusBadge status={r.status} />
              <span className="font-mono">{r.number}</span>
              <span className="flex items-center gap-1.5">
                {r.project.name} <EnvBadge env={r.environment} />
              </span>
              <span>Requested by {r.requester.name}</span>
              {r.kind === "update" && r.deployment_id ? (
                <span>
                  To {r.catalog_item.name} {r.version.number} on{" "}
                  <Link
                    to="/deployments/$deploymentId"
                    params={{ deploymentId: r.deployment_id }}
                    className="text-accent underline underline-offset-2"
                  >
                    {r.title.replace(/^Update · /, "")}
                  </Link>
                </span>
              ) : r.kind === "day2" && r.deployment_id ? (
                <span>
                  {r.action?.name} on{" "}
                  <Link
                    to="/deployments/$deploymentId"
                    params={{ deploymentId: r.deployment_id }}
                    className="text-accent underline underline-offset-2"
                  >
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
            </>
          )
        }
        actions={
          r && (
            <>
              {cancellable.includes(r.status) && <CancelButton request={r} />}
              {r.kind === "provision" && canRequestIn(r.project.id) && (
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
            </>
          )
        }
      />

      <div className="flex flex-wrap items-start gap-4">
        <Panel
          className="flex-[999_1_520px]"
          title="Steps"
          actions={
            running ? (
              <span className="flex items-center gap-1.5 text-caption text-faint">
                <span className="size-1.5 rounded-full bg-ok" aria-hidden /> Live
              </span>
            ) : null
          }
        >
          {steps.isPending || (composite && topology.isPending) ? (
            <SkeletonRows rows={6} cols={2} />
          ) : steps.isError ? (
            <ErrorState error={steps.error} onRetry={() => void steps.refetch()} className="p-0" />
          ) : r && graph ? (
            <Suspense fallback={<SkeletonRows rows={6} cols={2} />}>
              <RequestRun
                request={r}
                graph={graph}
                steps={steps.data.items}
                selected={selectedNode}
                onSelect={(node) => {
                  setChosenNode(node);
                  setChosenLog(null);
                  setChosenData(null);
                }}
                logStep={logStep?.id ?? null}
                onOpenLog={(s) => setChosenLog(s.id)}
                dataStep={dataStep?.id ?? null}
                onOpenData={openData}
              />
            </Suspense>
          ) : (
            <>
              <Timeline
                steps={steps.data.items}
                onOpenLog={(s) => setChosenLog(s.id)}
                selectedLogStep={logStep?.id ?? null}
                onOpenData={openData}
                selectedDataStep={dataStep?.id ?? null}
              />
              {r?.status === "failed" && r.error && (
                <FailureActions request={r} rolledBack={rollbackOf(steps.data.items)} />
              )}
            </>
          )}
        </Panel>

        <div className="flex min-w-0 flex-[1_1_380px] flex-col gap-4">
          <Panel
            title="AWX job output"
            flush
            description={logStep && <span className="font-mono break-words">{logStep.title.replace(/^AWX /, "")}</span>}
            footer={
              logStep?.external_ref?.url && (
                <>
                  <span className="mr-auto">Follows the job live.</span>
                  <a
                    href={logStep.external_ref.url}
                    target="_blank"
                    rel="noreferrer"
                    className="inline-flex items-center gap-1 text-accent hover:underline"
                  >
                    Open in AWX <ExternalLink className="size-3" aria-hidden />
                  </a>
                </>
              )
            }
          >
            {id && logStep ? (
              <LogViewer requestId={id} step={logStep} />
            ) : (
              <EmptyState
                icon={SquareTerminal}
                title="No AWX job"
                body="This request has no AWX job, or it hasn't been planned yet."
              />
            )}
          </Panel>

          {withData.length > 0 && (
            <Panel
              title="Step data"
              description={dataStep && <span className="font-mono break-words">{dataStep.title}</span>}
            >
              {id && dataStep ? (
                <StepDataView requestId={id} step={dataStep} />
              ) : (
                <EmptyState
                  icon={Braces}
                  title="No step chosen"
                  body="Choose Show data on a step to read what it was given and what it gave."
                />
              )}
            </Panel>
          )}

          {asked.length > 0 && <AskPanel rows={asked} />}

          {Boolean(r?.warnings?.length) && (
            <Panel title="Policy warnings" description="The requester went on despite them.">
              <PolicyWarnings warnings={r?.warnings ?? []} note="" />
            </Panel>
          )}

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
            {r && Object.keys(r.inputs ?? {}).length === 0 && !r.justification ? (
              <p className="text-secondary text-muted">Nothing: the item asks no questions.</p>
            ) : r ? (
              <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-3.5 gap-y-1.5">
                {Object.entries(r.inputs ?? {}).map(([k, v]) => (
                  <div key={k} className="contents">
                    <dt className="font-mono text-secondary text-muted">{k}</dt>
                    {isFileRef(v) ? (
                      <dd className="m-0 min-w-0 text-secondary">
                        <FileDownload file={v} />
                      </dd>
                    ) : (
                      <dd className="m-0 font-mono text-secondary break-words">{formatValue(v)}</dd>
                    )}
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
