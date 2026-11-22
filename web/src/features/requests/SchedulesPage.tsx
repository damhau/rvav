// Recurring schedules (ADR-0057): an item that makes no deployment, requested
// again and again as its owner. Each run is a request of its own, which
// policies, approvals and quota check as they would the owner's.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { CalendarClock } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { ErrorState } from "@/components/common/feedback";
import { Time } from "@/components/common/misc";
import { Page } from "@/components/common/page";
import { EnvBadge } from "@/components/common/status";
import { Badge } from "@/components/ui/badge";
import { Breadcrumbs } from "@/components/ui/breadcrumbs";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { DataTable, type Column } from "@/components/ui/data-table";
import { MenuItem, MenuSeparator } from "@/components/ui/dropdown-menu";
import { EmptyState } from "@/components/ui/empty-state";
import { Field, Textarea } from "@/components/ui/input";
import { PageHeader } from "@/components/ui/page-header";
import { Panel } from "@/components/ui/panel";
import { Sheet } from "@/components/ui/sheet";
import { api, call, isApiError } from "@/lib/api/client";
import type { Me, Schedule, ScheduleRun } from "@/lib/api/types";
import { can } from "@/lib/can";
import { useCurrentProject } from "@/lib/project";
import { q } from "@/lib/query";
import { fullDate } from "@/lib/format";

const units: [number, string][] = [
  [60 * 24 * 7, "week"],
  [60 * 24, "day"],
  [60, "hour"],
];

/** How often a schedule runs, in words. */
export function repeatText(s: Pick<Schedule, "cron" | "every_minutes" | "timezone">): string {
  if (s.cron) return `${s.cron} (${s.timezone})`;
  const m = s.every_minutes ?? 0;
  for (const [size, unit] of units)
    if (m % size === 0) return m === size ? `Every ${unit}` : `Every ${m / size} ${unit}s`;
  return `Every ${m} minutes`;
}

const outcomeTone = { started: "ok", refused: "bad", skipped: "neutral", failed: "bad" } as const;
const outcomeLabel = { started: "Started", refused: "Refused", skipped: "Skipped", failed: "Failed" } as const;

function RunLine({ run }: { run: ScheduleRun }) {
  return (
    <div className="flex min-w-0 flex-col gap-0.5">
      <span className="flex flex-wrap items-center gap-1.5">
        <Badge tone={outcomeTone[run.outcome]}>{outcomeLabel[run.outcome]}</Badge>
        {run.request && (
          <Link
            to="/requests/$requestId"
            params={{ requestId: run.request.number }}
            className="font-mono text-accent underline underline-offset-2"
          >
            {run.request.number}
          </Link>
        )}
        <Time iso={run.at} className="text-caption text-muted" />
      </span>
      {run.reason && <span className="text-caption text-muted">{run.reason}</span>}
    </div>
  );
}

const manages = (me: Me, s: Schedule) => s.owner.id === me.user.id || can(me, "request.manage", s.project.id);

const columns: Column<Schedule>[] = [
  {
    id: "name",
    header: "Schedule",
    sortValue: (s) => s.name,
    cell: (s) => (
      <div className="flex min-w-0 flex-col">
        <span className="font-medium">{s.name}</span>
        <Link
          to="/catalog/$itemId"
          params={{ itemId: s.catalog_item.slug }}
          className="text-caption text-muted underline-offset-2 hover:underline"
        >
          {s.catalog_item.name} {s.version.number}
        </Link>
      </div>
    ),
  },
  { id: "env", header: "Environment", cell: (s) => <EnvBadge env={s.environment} /> },
  {
    id: "repeat",
    header: "Repeats",
    cell: (s) => <span className={s.cron ? "font-mono text-secondary" : undefined}>{repeatText(s)}</span>,
  },
  {
    id: "next",
    header: "Next run",
    sortValue: (s) => (s.paused ? "~" : (s.next_runs[0] ?? "~")),
    cell: (s) =>
      s.paused ? (
        <div className="flex flex-col gap-0.5">
          <Badge tone="warn" className="self-start">
            Paused
          </Badge>
          {s.paused_note && <span className="text-caption text-muted">{s.paused_note}</span>}
        </div>
      ) : s.next_runs[0] ? (
        <Time iso={s.next_runs[0]} />
      ) : (
        <span className="text-muted">Not known: Temporal didn&apos;t answer</span>
      ),
  },
  {
    id: "last",
    header: "Last run",
    cell: (s) => (s.last_run ? <RunLine run={s.last_run} /> : <span className="text-muted">None yet</span>),
  },
  { id: "owner", header: "Runs as", className: "whitespace-nowrap text-muted", cell: (s) => s.owner.name },
];

function RunsSheet({ id, onClose }: { id: string; onClose: () => void }) {
  const schedule = useQuery(q.schedule(id));
  const s = schedule.data;
  return (
    <Sheet
      open
      onOpenChange={(o) => !o && onClose()}
      title={s ? s.name : "Schedule"}
      description="Its next times, and its last 20 runs, the newest first. Each run is a request of its own."
    >
      {schedule.isError ? (
        <ErrorState error={schedule.error} onRetry={() => void schedule.refetch()} />
      ) : (
        s && (
          <div className="flex flex-col gap-4">
            <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1.5 text-body">
              <dt className="text-muted">Requests</dt>
              <dd>
                {s.catalog_item.name} {s.version.number} in {s.project.name} <EnvBadge env={s.environment} />
              </dd>
              <dt className="text-muted">Repeats</dt>
              <dd>{repeatText(s)}</dd>
              <dt className="text-muted">Runs as</dt>
              <dd>{s.owner.name}</dd>
              {s.accept_warnings && (
                <>
                  <dt className="text-muted">Warnings</dt>
                  <dd>Each run goes on despite the policies&apos; warnings.</dd>
                </>
              )}
            </dl>
            <section aria-labelledby="next-runs" className="flex flex-col gap-1.5">
              <h3 id="next-runs" className="text-secondary font-medium">
                Next runs
              </h3>
              {s.paused ? (
                <p className="text-secondary text-muted">None while it is paused.</p>
              ) : s.next_runs.length ? (
                <ul className="flex flex-col gap-0.5 text-body">
                  {s.next_runs.map((t) => (
                    <li key={t} className="tnum">
                      {fullDate(t)}
                    </li>
                  ))}
                </ul>
              ) : (
                <p className="text-secondary text-muted">Not known: Temporal didn&apos;t answer.</p>
              )}
            </section>
            <section aria-labelledby="runs" className="flex flex-col gap-1.5">
              <h3 id="runs" className="text-secondary font-medium">
                Runs
              </h3>
              {s.runs?.length ? (
                <ul className="flex flex-col divide-y divide-line">
                  {s.runs.map((r) => (
                    <li key={`${r.at}-${r.outcome}`} className="py-2">
                      <RunLine run={r} />
                    </li>
                  ))}
                </ul>
              ) : (
                <p className="text-secondary text-muted">It hasn&apos;t run yet.</p>
              )}
            </section>
          </div>
        )
      )}
    </Sheet>
  );
}

function PauseSheet({ schedule, onClose }: { schedule: Schedule; onClose: () => void }) {
  const qc = useQueryClient();
  const [note, setNote] = useState("");
  const [error, setError] = useState<string>();
  const pause = useMutation({
    mutationFn: () =>
      call(
        api.POST("/schedules/{scheduleId}/pause", {
          params: { path: { scheduleId: schedule.id } },
          body: note.trim() ? { note: note.trim() } : {},
        }),
      ),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["schedules"] });
      toast.success(`${schedule.name} paused`);
      onClose();
    },
    onError: (e) => {
      const field = isApiError(e) ? e.fieldErrors.find((f) => f.field === "/note") : undefined;
      if (field) return setError(field.message);
      toast.error(isApiError(e) ? e.problem.title : "The schedule wasn't paused", {
        description: isApiError(e) ? e.problem.detail : undefined,
      });
    },
  });
  return (
    <Sheet
      open
      onOpenChange={(o) => !o && onClose()}
      pending={pause.isPending}
      title={`Pause ${schedule.name}`}
      description="It doesn't run until it is resumed; the times it misses meanwhile are skipped."
      submitLabel={pause.isPending ? "Pausing…" : "Pause"}
      onSubmit={() => pause.mutate()}
    >
      <Field label="Why" htmlFor="pause-note" hint="Optional: shown with the schedule." error={error}>
        <Textarea id="pause-note" rows={3} maxLength={500} value={note} onChange={(e) => setNote(e.target.value)} />
      </Field>
    </Sheet>
  );
}

export function SchedulesPage() {
  const qc = useQueryClient();
  const { projectId, project, me } = useCurrentProject();
  const schedules = useQuery({ ...q.schedules(projectId), enabled: projectId !== "" });
  const [showing, setShowing] = useState<string | null>(null);
  const [pausing, setPausing] = useState<Schedule | null>(null);
  const [deleting, setDeleting] = useState<Schedule | null>(null);
  const done = (what: string) => () => {
    void qc.invalidateQueries({ queryKey: ["schedules"] });
    toast.success(what);
  };
  const refused = (fallback: string) => (e: unknown) =>
    toast.error(isApiError(e) ? e.problem.title : fallback, {
      description: isApiError(e) ? e.problem.detail : undefined,
    });
  const resume = useMutation({
    mutationFn: (s: Schedule) =>
      call(api.POST("/schedules/{scheduleId}/resume", { params: { path: { scheduleId: s.id } } })),
    onSuccess: (s) => done(`${s.name} resumed`)(),
    onError: refused("The schedule wasn't resumed"),
  });
  const remove = useMutation({
    mutationFn: (s: Schedule) =>
      call(api.DELETE("/schedules/{scheduleId}", { params: { path: { scheduleId: s.id } } })),
    onSuccess: (_, s) => {
      done(`${s.name} deleted`)();
      setDeleting(null);
    },
    onError: (e) => {
      setDeleting(null);
      refused("The schedule wasn't deleted")(e);
    },
  });
  const newOne = (
    <Button variant="primary" asChild>
      <Link to="/catalog">Schedule a request</Link>
    </Button>
  );
  const list = schedules.data?.items;
  return (
    <Page>
      <PageHeader
        breadcrumbs={
          <Breadcrumbs>
            <Link to="/requests">Requests</Link>
          </Breadcrumbs>
        }
        title="Schedules"
        description={`Requests made again and again in ${project.name}, each as its owner: policies, approvals and quota check every run as they would a request of theirs.`}
        actions={list?.length ? newOne : undefined}
      />
      <Panel flush>
        <DataTable
          label="Schedules"
          columns={columns}
          rows={list}
          rowKey={(s) => s.id}
          minWidth={860}
          loading={schedules.isPending}
          error={schedules.isError && <ErrorState error={schedules.error} onRetry={() => void schedules.refetch()} />}
          empty={
            <EmptyState
              icon={CalendarClock}
              title="No schedules yet"
              body="Request an item that makes no deployment, such as a custom workflow, and pick On a schedule under Start."
              action={newOne}
            />
          }
          rowMenuLabel={(s) => `Actions for ${s.name}`}
          rowMenu={(s) => (
            <>
              <MenuItem onSelect={() => setShowing(s.id)}>Show its runs</MenuItem>
              {manages(me, s) && (
                <>
                  {s.paused ? (
                    <MenuItem onSelect={() => resume.mutate(s)}>Resume</MenuItem>
                  ) : (
                    <MenuItem onSelect={() => setPausing(s)}>Pause</MenuItem>
                  )}
                  <MenuSeparator />
                  <MenuItem danger onSelect={() => setDeleting(s)}>
                    Delete
                  </MenuItem>
                </>
              )}
            </>
          )}
        />
      </Panel>
      {showing && <RunsSheet id={showing} onClose={() => setShowing(null)} />}
      {pausing && <PauseSheet schedule={pausing} onClose={() => setPausing(null)} />}
      {deleting && (
        <ConfirmDialog
          open
          onOpenChange={(o) => !o && setDeleting(null)}
          title={`Delete ${deleting.name}?`}
          description="It doesn't run again. The requests it started stay, and still name it."
          confirmLabel="Delete schedule"
          pending={remove.isPending}
          onConfirm={() => remove.mutate(deleting)}
        />
      )}
    </Page>
  );
}
