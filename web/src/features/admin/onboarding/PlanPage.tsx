// One onboarding plan (X1.5): what it matches, where the VMs go, and its
// runs, each with what it adopted, skipped and why, and what failed.
import type { TFunction } from "i18next";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { Ellipsis, History } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { Trans, useTranslation } from "react-i18next";
import { ErrorState, SkeletonRows } from "@/components/common/feedback";
import { Mono, Time } from "@/components/common/misc";
import { KeyValues, Page } from "@/components/common/page";
import { Badge } from "@/components/ui/badge";
import { Breadcrumbs } from "@/components/ui/breadcrumbs";
import { Button, IconButton } from "@/components/ui/button";
import { DataTable, type Column } from "@/components/ui/data-table";
import { Menu, MenuContent, MenuItem, MenuSeparator, MenuTrigger } from "@/components/ui/dropdown-menu";
import { EmptyState } from "@/components/ui/empty-state";
import { PageHeader } from "@/components/ui/page-header";
import { Panel } from "@/components/ui/panel";
import { Sheet } from "@/components/ui/sheet";
import type { components } from "@/lib/api/schema";
import { useEnvName } from "@/lib/environments";
import { adm } from "@/lib/query";
import { fullDate, relative } from "@/lib/format";
import {
  countsText,
  DeletePlan,
  filterText,
  PlanSheet,
  RunStatus,
  scheduleText,
  targetText,
  useProjectName,
  useRunNow,
  type Plan,
} from "./plans";

type S = components["schemas"];
type Run = S["OnboardingRun"];
type RunVm = S["OnboardingRunVm"];

const outcomeTone = { adopted: "ok", skipped: "neutral", failed: "bad" } as const;

const vmColumns = (t: TFunction<"admin-onboarding">): Column<RunVm>[] => [
  {
    id: "vm",
    header: t("run.columns.vm"),
    sortValue: (v) => v.name,
    cell: (v) => (
      <span className="flex flex-col">
        <span className="font-medium">{v.name}</span>
        <span className="font-mono text-caption text-faint">{v.moref}</span>
      </span>
    ),
  },
  {
    id: "outcome",
    header: t("run.columns.outcome"),
    sortValue: (v) => v.outcome,
    cell: (v) => <Badge tone={outcomeTone[v.outcome]}>{t(`run.outcome.${v.outcome}`)}</Badge>,
  },
  {
    id: "why",
    header: t("run.columns.why"),
    cell: (v) => <span className="text-secondary text-muted">{v.reason}</span>,
  },
  {
    id: "deployment",
    header: t("run.columns.deployment"),
    cell: (v) =>
      v.deployment ? (
        <Link
          to="/deployments/$deploymentId"
          params={{ deploymentId: v.deployment.id }}
          className="text-accent underline underline-offset-2"
        >
          {v.deployment.name}
        </Link>
      ) : null,
  },
];

function RunSheet({ runId, onClose }: { runId: string; onClose: () => void }) {
  const { t } = useTranslation("admin-onboarding");
  const run = useQuery({
    ...adm.onboardingRun(runId),
    refetchInterval: (q) => (q.state.data?.status === "running" ? 1500 : false),
  });
  const r = run.data;
  return (
    <Sheet
      open
      onOpenChange={(o) => !o && onClose()}
      title={r ? t("run.of", { date: fullDate(r.started_at) }) : t("run.title")}
      description={r ? countsText(r) : undefined}
      className="w-[min(820px,100vw)]"
    >
      {run.isPending && <SkeletonRows rows={4} cols={2} />}
      {run.isError && <ErrorState error={run.error} onRetry={() => void run.refetch()} />}
      {r && (
        <>
          <KeyValues
            items={[
              { label: t("run.status.label"), value: <RunStatus status={r.status} /> },
              {
                label: t("run.started"),
                value: r.started_by ? t("run.startedBy", { name: r.started_by.name }) : t("run.onSchedule"),
              },
              ...(r.finished_at ? [{ label: t("run.finished"), value: <Time iso={r.finished_at} /> }] : []),
              ...(r.error ? [{ label: t("run.whyFailed"), value: <span className="text-bad">{r.error}</span> }] : []),
            ]}
          />
          <DataTable
            label={t("run.vms")}
            columns={vmColumns(t)}
            rows={r.vms ?? []}
            rowKey={(v) => `${v.vcenter}/${v.moref}`}
            minWidth={640}
            empty={
              <EmptyState
                icon={History}
                title={r.status === "running" ? t("run.nothingYet") : t("run.noVm")}
                body={r.status === "running" ? t("run.nothingYetBody") : t("run.noVmBody")}
              />
            }
          />
        </>
      )}
    </Sheet>
  );
}

function Runs({ plan }: { plan: Plan }) {
  const { t } = useTranslation("admin-onboarding");
  const runs = useQuery({ ...adm.onboardingRuns(plan.id), refetchInterval: plan.running ? 1500 : false });
  const [open, setOpen] = useState<string | null>(null);
  const columns: Column<Run>[] = [
    {
      id: "when",
      header: t("run.started"),
      sortValue: (r) => r.started_at,
      cell: (r) => (
        <>
          <button type="button" className="font-medium text-fg hover:text-accent" onClick={() => setOpen(r.id)}>
            <span className="sr-only">{t("run.open")} </span>
            {relative(r.started_at)}
          </button>
          <div className="text-secondary text-faint">{fullDate(r.started_at)}</div>
        </>
      ),
    },
    {
      id: "by",
      header: t("run.columns.by"),
      cell: (r) => <span className="text-secondary">{r.started_by?.name ?? t("run.schedule")}</span>,
    },
    {
      id: "status",
      header: t("run.status.label"),
      sortValue: (r) => r.status,
      cell: (r) => <RunStatus status={r.status} />,
    },
    {
      id: "vms",
      header: t("run.columns.vms"),
      cell: (r) => (
        <span className="text-secondary">
          {countsText(r)}
          {r.error && <div className="text-bad">{r.error}</div>}
        </span>
      ),
    },
  ];
  return (
    <Panel title={t("runs.title")} description={t("runs.lead")} flush>
      <DataTable
        label={t("runs.title")}
        columns={columns}
        rows={runs.data?.items}
        rowKey={(r) => r.id}
        minWidth={640}
        loading={runs.isPending}
        error={runs.isError && <ErrorState error={runs.error} onRetry={() => void runs.refetch()} />}
        empty={
          <EmptyState
            icon={History}
            title={t("plans.neverRun")}
            body={plan.every_hours ? t("runs.neverScheduled") : t("runs.neverByHand")}
          />
        }
      />
      {open && <RunSheet runId={open} onClose={() => setOpen(null)} />}
    </Panel>
  );
}

export function PlanPage({ planId }: { planId: string }) {
  const { t } = useTranslation(["admin-onboarding", "shell"]);
  const qc = useQueryClient();
  const navigate = useNavigate();
  const plan = useQuery({
    ...adm.onboardingPlan(planId),
    refetchInterval: (q) => (q.state.data?.running ? 1500 : false),
  });
  const [editing, setEditing] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const runNow = useRunNow();
  const projectName = useProjectName();
  const envName = useEnvName();
  const p = plan.data;
  // A run that ended adopted VMs: the lists that show them change.
  const wasRunning = useRef(false);
  useEffect(() => {
    if (wasRunning.current && p && !p.running) {
      void qc.invalidateQueries({ queryKey: ["admin", "onboarding"] });
      void qc.invalidateQueries({ queryKey: ["deployments"] });
      void qc.invalidateQueries({ queryKey: ["vms"] });
    }
    wasRunning.current = p?.running ?? false;
  }, [p, qc]);
  return (
    <Page>
      <PageHeader
        breadcrumbs={
          <Breadcrumbs>
            <Link to="/admin/onboarding" search={{ tab: "plans" }}>
              {t("shell:nav.admin.onboarding")}
            </Link>
          </Breadcrumbs>
        }
        title={p?.name ?? t("plan.title")}
        description={p?.description || undefined}
        meta={p?.running && <Badge tone="run">{t("run.status.running")}</Badge>}
        actions={
          p && (
            <>
              <Menu>
                <MenuTrigger asChild>
                  <IconButton label={t("plan.moreActions", { name: p.name })} variant="secondary" className="size-8">
                    <Ellipsis />
                  </IconButton>
                </MenuTrigger>
                <MenuContent>
                  <MenuItem onSelect={() => setEditing(true)}>{t("plan.change")}</MenuItem>
                  <MenuSeparator />
                  <MenuItem danger disabled={p.running} onSelect={() => setDeleting(true)}>
                    <span>{t("delete.confirm")}</span>{" "}
                    {p.running && <span className="text-faint">{t("plans.goesOn")}</span>}
                  </MenuItem>
                </MenuContent>
              </Menu>
              {/* aria-disabled rather than disabled: a disabled button drops the focus to the page. */}
              <Button
                variant="primary"
                aria-disabled={p.running || runNow.isPending || undefined}
                className="aria-disabled:opacity-50"
                onClick={() => !p.running && !runNow.isPending && runNow.mutate(p)}
              >
                {p.running ? t("plan.running") : t("plans.runNow")}
              </Button>
            </>
          )
        }
      />
      {plan.isPending && <SkeletonRows rows={4} cols={2} />}
      {plan.isError && <ErrorState error={plan.error} onRetry={() => void plan.refetch()} />}
      {p && (
        <>
          <Panel title={t("plan.settings")}>
            <KeyValues
              items={[
                { label: t("plans.columns.vms"), value: filterText(p.filter) },
                { label: t("plan.now"), value: t("plan.matching", { count: p.matching }) },
                {
                  label: t("target.where"),
                  value: p.deployment ? (
                    <>
                      {targetText(p, projectName, envName)}{" "}
                      <Link
                        to="/deployments/$deploymentId"
                        params={{ deploymentId: p.deployment.id }}
                        className="text-accent underline underline-offset-2"
                      >
                        {t("plan.open", { name: p.deployment.name })}
                      </Link>
                    </>
                  ) : (
                    targetText(p, projectName, envName)
                  ),
                },
                ...(p.target.inputs && Object.keys(p.target.inputs).length > 0
                  ? [
                      {
                        label: t("target.inputs"),
                        value: (
                          <Mono>
                            {Object.entries(p.target.inputs)
                              .map(([k, v]) => `${k}=${typeof v === "string" ? v : JSON.stringify(v)}`)
                              .join(", ")}
                          </Mono>
                        ),
                      },
                    ]
                  : []),
                { label: t("plans.columns.runs"), value: scheduleText(p) },
                {
                  label: t("plan.changed"),
                  value: p.updated_by ? (
                    <Trans
                      t={t}
                      i18nKey="plan.changedBy"
                      values={{ name: p.updated_by.name }}
                      components={{ time: <Time iso={p.updated_at} /> }}
                    />
                  ) : (
                    <Time iso={p.updated_at} />
                  ),
                },
              ]}
            />
          </Panel>
          <Runs plan={p} />
          {editing && <PlanSheet plan={p} onClose={() => setEditing(false)} />}
          {deleting && (
            <DeletePlan
              plan={p}
              onClose={() => setDeleting(false)}
              onDeleted={() => void navigate({ to: "/admin/onboarding", search: { tab: "plans" } })}
            />
          )}
        </>
      )}
    </Page>
  );
}
