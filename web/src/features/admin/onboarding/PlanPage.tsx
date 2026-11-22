// One onboarding plan (X1.5): what it matches, where the VMs go, and its
// runs, each with what it adopted, skipped and why, and what failed.
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { Ellipsis, History } from "lucide-react";
import { useEffect, useRef, useState } from "react";
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
const outcomeLabel = { adopted: "Adopted", skipped: "Skipped", failed: "Failed" } as const;

const vmColumns: Column<RunVm>[] = [
  {
    id: "vm",
    header: "VM",
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
    header: "Outcome",
    sortValue: (v) => v.outcome,
    cell: (v) => <Badge tone={outcomeTone[v.outcome]}>{outcomeLabel[v.outcome]}</Badge>,
  },
  {
    id: "why",
    header: "Why",
    cell: (v) => <span className="text-secondary text-muted">{v.reason}</span>,
  },
  {
    id: "deployment",
    header: "Deployment",
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
  const run = useQuery({
    ...adm.onboardingRun(runId),
    refetchInterval: (q) => (q.state.data?.status === "running" ? 1500 : false),
  });
  const r = run.data;
  return (
    <Sheet
      open
      onOpenChange={(o) => !o && onClose()}
      title={r ? `Run of ${fullDate(r.started_at)}` : "Run"}
      description={r ? countsText(r) : undefined}
      className="w-[min(820px,100vw)]"
    >
      {run.isPending && <SkeletonRows rows={4} cols={2} />}
      {run.isError && <ErrorState error={run.error} onRetry={() => void run.refetch()} />}
      {r && (
        <>
          <KeyValues
            items={[
              { label: "Status", value: <RunStatus status={r.status} /> },
              { label: "Started", value: r.started_by ? `By ${r.started_by.name}` : "On its schedule" },
              ...(r.finished_at ? [{ label: "Finished", value: <Time iso={r.finished_at} /> }] : []),
              ...(r.error ? [{ label: "Why it failed", value: <span className="text-bad">{r.error}</span> }] : []),
            ]}
          />
          <DataTable
            label="VMs of the run"
            columns={vmColumns}
            rows={r.vms ?? []}
            rowKey={(v) => `${v.vcenter}/${v.moref}`}
            minWidth={640}
            empty={
              <EmptyState
                icon={History}
                title={r.status === "running" ? "Nothing yet" : "No VM matched"}
                body={
                  r.status === "running"
                    ? "The VMs show as the run takes them."
                    : "The plan's filter matched no VM in the inventory then."
                }
              />
            }
          />
        </>
      )}
    </Sheet>
  );
}

function Runs({ plan }: { plan: Plan }) {
  const runs = useQuery({ ...adm.onboardingRuns(plan.id), refetchInterval: plan.running ? 1500 : false });
  const [open, setOpen] = useState<string | null>(null);
  const columns: Column<Run>[] = [
    {
      id: "when",
      header: "Started",
      sortValue: (r) => r.started_at,
      cell: (r) => (
        <>
          <button type="button" className="font-medium text-fg hover:text-accent" onClick={() => setOpen(r.id)}>
            <span className="sr-only">Open the run from </span>
            {relative(r.started_at)}
          </button>
          <div className="text-secondary text-faint">{fullDate(r.started_at)}</div>
        </>
      ),
    },
    {
      id: "by",
      header: "By",
      cell: (r) => <span className="text-secondary">{r.started_by?.name ?? "Its schedule"}</span>,
    },
    { id: "status", header: "Status", sortValue: (r) => r.status, cell: (r) => <RunStatus status={r.status} /> },
    {
      id: "vms",
      header: "VMs",
      cell: (r) => (
        <span className="text-secondary">
          {countsText(r)}
          {r.error && <div className="text-bad">{r.error}</div>}
        </span>
      ),
    },
  ];
  return (
    <Panel title="Runs" description="The last 20, latest first." flush>
      <DataTable
        label="Runs"
        columns={columns}
        rows={runs.data?.items}
        rowKey={(r) => r.id}
        minWidth={640}
        loading={runs.isPending}
        error={runs.isError && <ErrorState error={runs.error} onRetry={() => void runs.refetch()} />}
        empty={
          <EmptyState
            icon={History}
            title="Never run"
            body={
              plan.every_hours ? "It runs on its schedule, or now with Run now." : "It runs when you select Run now."
            }
          />
        }
      />
      {open && <RunSheet runId={open} onClose={() => setOpen(null)} />}
    </Panel>
  );
}

export function PlanPage({ planId }: { planId: string }) {
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
              Onboarding
            </Link>
          </Breadcrumbs>
        }
        title={p?.name ?? "Plan"}
        description={p?.description || undefined}
        meta={p?.running && <Badge tone="run">Running</Badge>}
        actions={
          p && (
            <>
              <Menu>
                <MenuTrigger asChild>
                  <IconButton label={`More actions for ${p.name}`} variant="secondary" className="size-8">
                    <Ellipsis />
                  </IconButton>
                </MenuTrigger>
                <MenuContent>
                  <MenuItem onSelect={() => setEditing(true)}>Change plan</MenuItem>
                  <MenuSeparator />
                  <MenuItem danger disabled={p.running} onSelect={() => setDeleting(true)}>
                    <span>Delete plan</span> {p.running && <span className="text-faint">a run goes on</span>}
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
                {p.running ? "Running…" : "Run now"}
              </Button>
            </>
          )
        }
      />
      {plan.isPending && <SkeletonRows rows={4} cols={2} />}
      {plan.isError && <ErrorState error={plan.error} onRetry={() => void plan.refetch()} />}
      {p && (
        <>
          <Panel title="Settings">
            <KeyValues
              items={[
                { label: "VMs it adopts", value: filterText(p.filter) },
                { label: "Now", value: `${p.matching} VM${p.matching === 1 ? "" : "s"} it matches no deployment has` },
                {
                  label: "Where they go",
                  value: p.deployment ? (
                    <>
                      {targetText(p, projectName)}{" "}
                      <Link
                        to="/deployments/$deploymentId"
                        params={{ deploymentId: p.deployment.id }}
                        className="text-accent underline underline-offset-2"
                      >
                        Open {p.deployment.name}
                      </Link>
                    </>
                  ) : (
                    targetText(p, projectName)
                  ),
                },
                ...(p.target.inputs && Object.keys(p.target.inputs).length > 0
                  ? [
                      {
                        label: "Inputs",
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
                { label: "Runs", value: scheduleText(p) },
                {
                  label: "Changed",
                  value: (
                    <>
                      <Time iso={p.updated_at} />
                      {p.updated_by ? ` by ${p.updated_by.name}` : ""}
                    </>
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
