import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { Braces, ExternalLink, SquareTerminal } from "lucide-react";
import { lazy, Suspense, useMemo, useState } from "react";
import { Trans, useTranslation } from "react-i18next";
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
import { toText } from "@/lib/utils";
import { duration } from "@/lib/format";
import { i18n } from "@/lib/i18n";
import { AskPanel } from "./AskPanel";
import { FailureActions, mayActOnFailure } from "./FailureActions";
import { LogViewer } from "./LogViewer";
import { StepDataView } from "./StepDataView";
import { failedNode, failedRollbackOf, flatten, pickLogStep, rollbackOf, runSteps } from "./run";

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
    return (
      v.map(formatItem).join(v.some((x) => typeof x === "object" && x !== null) ? "; " : ", ") ||
      i18n.t("requests:detail.none")
    );
  if (v === null || v === undefined || v === "") return "–";
  return toText(v);
}

function CancelButton({ request }: { request: ServiceRequest }) {
  const { t } = useTranslation("requests");
  const qc = useQueryClient();
  const [open, setOpen] = useState(false);
  const cancel = useMutation({
    mutationFn: () => call(api.POST("/requests/{requestId}/cancel", { params: { path: { requestId: request.id } } })),
    onSuccess: (r) => {
      qc.setQueryData(keys.requests.detail(r.number), r);
      setOpen(false);
      toast(t(r.status === "cancelled" ? "detail.cancelled" : "detail.cancelling", { number: r.number }), {
        description: r.status === "cancelled" ? undefined : t("detail.stopsAfter"),
      });
    },
    onError: (e) =>
      toast.error(isApiError(e) ? e.problem.title : t("detail.notCancelled"), {
        description: isApiError(e) ? e.problem.detail : undefined,
      }),
  });
  return (
    <>
      <Button variant="danger" onClick={() => setOpen(true)}>
        {t("detail.cancel")}
      </Button>
      <ConfirmDialog
        open={open}
        onOpenChange={setOpen}
        title={t("detail.cancelTitle", { number: request.number })}
        description={request.status === "running" ? t("detail.cancelRunning") : t("detail.cancelWaiting")}
        confirmLabel={t("detail.cancel")}
        cancelLabel={t("detail.keepRunning")}
        pending={cancel.isPending}
        onConfirm={() => cancel.mutate()}
      />
    </>
  );
}

export function RequestDetailPage({ requestKey }: { requestKey: string }) {
  const { t } = useTranslation("requests");
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
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [topology.data, i18n.language]);
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
  // A step's data holds its blueprint's literals: for catalog managers and auditors (ADR-0057).
  const readsData = can(me, "catalog.manage") || can(me, "extensibility.read");
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
            <Link to="/requests">{t("detail.breadcrumb")}</Link>
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
              <span>
                {r.schedule
                  ? t("detail.bySchedule", { schedule: r.schedule.name, name: r.requester.name })
                  : t("detail.requestedBy", { name: r.requester.name })}
              </span>
              {r.kind === "update" && r.deployment_id ? (
                <span>
                  <Trans
                    t={t}
                    i18nKey="detail.updateTo"
                    values={{
                      item: r.catalog_item.name,
                      version: r.version.number,
                      deployment: r.title.replace(/^Update · /, ""),
                    }}
                    components={{
                      target: (
                        <Link
                          to="/deployments/$deploymentId"
                          params={{ deploymentId: r.deployment_id }}
                          className="text-accent underline underline-offset-2"
                        />
                      ),
                    }}
                  />
                </span>
              ) : r.kind === "day2" && r.deployment_id ? (
                <span>
                  <Trans
                    t={t}
                    i18nKey="detail.actionOn"
                    values={{ action: r.action?.name ?? "", resource: r.resource?.name ?? t("detail.theDeployment") }}
                    components={{
                      target: (
                        <Link
                          to="/deployments/$deploymentId"
                          params={{ deploymentId: r.deployment_id }}
                          className="text-accent underline underline-offset-2"
                        />
                      ),
                    }}
                  />
                </span>
              ) : (
                <span>
                  {r.catalog_item.name} {r.version.number}
                </span>
              )}
              <span>
                <Trans t={t} i18nKey="detail.submitted" components={{ time: <Time iso={r.submitted_at} /> }} />
              </span>
              <span className="tnum">
                {r.status === "succeeded"
                  ? t("detail.allDone", { count: r.progress.total })
                  : running
                    ? t("progress.step", {
                        n: Math.min(r.progress.total, r.progress.done + 1),
                        total: r.progress.total,
                      })
                    : t("detail.doneOf", { done: r.progress.done, count: r.progress.total })}
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
                    {t("detail.requestAgain")}
                  </Link>
                </Button>
              )}
              {deploymentId && (
                <Button variant={r.status === "succeeded" ? "primary" : "secondary"} asChild>
                  <Link to="/deployments/$deploymentId" params={{ deploymentId }}>
                    {t("detail.openDeployment")}
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
          title={t("detail.steps")}
          actions={
            running ? (
              <span className="flex items-center gap-1.5 text-caption text-faint">
                <span className="size-1.5 rounded-full bg-ok" aria-hidden /> {t("detail.live")}
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
                mayAct={mayActOnFailure(me, r)}
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
                <FailureActions
                  request={r}
                  rolledBack={rollbackOf(steps.data.items)}
                  rollbackFailed={failedRollbackOf(steps.data.items)}
                  mayAct={mayActOnFailure(me, r)}
                />
              )}
            </>
          )}
        </Panel>

        <div className="flex min-w-0 flex-[1_1_380px] flex-col gap-4">
          <Panel
            title={t("detail.awx")}
            flush
            description={logStep && <span className="font-mono break-words">{logStep.title.replace(/^AWX /, "")}</span>}
            footer={
              logStep?.external_ref?.url && (
                <>
                  <span className="mr-auto">{t("detail.followsLive")}</span>
                  <a
                    href={logStep.external_ref.url}
                    target="_blank"
                    rel="noreferrer"
                    className="inline-flex items-center gap-1 text-accent hover:underline"
                  >
                    {t("detail.openInAwx")} <ExternalLink className="size-3" aria-hidden />
                  </a>
                </>
              )
            }
          >
            {id && logStep ? (
              <LogViewer requestId={id} step={logStep} />
            ) : (
              <EmptyState icon={SquareTerminal} title={t("detail.noAwx")} body={t("detail.noAwxBody")} />
            )}
          </Panel>

          {withData.length > 0 && (
            <Panel
              title={t("detail.stepData")}
              description={dataStep && <span className="font-mono break-words">{dataStep.title}</span>}
            >
              {id && dataStep ? (
                <StepDataView requestId={id} step={dataStep} />
              ) : (
                <EmptyState icon={Braces} title={t("detail.noStep")} body={t("detail.noStepBody")} />
              )}
            </Panel>
          )}

          {asked.length > 0 && <AskPanel rows={asked} />}

          {Boolean(r?.warnings?.length) && (
            <Panel title={t("detail.warnings")} description={t("detail.warningsBody")}>
              <PolicyWarnings warnings={r?.warnings ?? []} note="" />
            </Panel>
          )}

          {levels.length > 0 && (
            <Panel
              title={t("detail.approval")}
              actions={
                mine ? (
                  <Button size="sm" variant="primary" asChild>
                    <Link to="/approvals" search={{ id: mine.id }}>
                      {t("detail.decide")}
                    </Link>
                  </Button>
                ) : null
              }
            >
              <ApprovalHistory levels={levels} />
            </Panel>
          )}

          <Panel title={t("detail.asked")}>
            {r && Object.keys(r.inputs ?? {}).length === 0 && !r.justification ? (
              <p className="text-secondary text-muted">{t("detail.nothingAsked")}</p>
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
                    <dt className="text-muted">{t("detail.reason")}</dt>
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
