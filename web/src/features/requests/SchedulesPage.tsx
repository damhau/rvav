// Recurring schedules (ADR-0057): an item that makes no deployment, requested
// again and again as its owner. Each run is a request of its own, which
// policies, approvals and quota check as they would the owner's.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import type { TFunction } from "i18next";
import { CalendarClock } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
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
import { i18n } from "@/lib/i18n";

const units = [
  [60 * 24 * 7, "week"],
  [60 * 24, "day"],
  [60, "hour"],
] as const;

/** How often a schedule runs, in words. */
export function repeatText(s: Pick<Schedule, "cron" | "every_minutes" | "timezone">): string {
  if (s.cron) return `${s.cron} (${s.timezone})`;
  const t = i18n.getFixedT(null, "requests");
  const m = s.every_minutes ?? 0;
  for (const [size, unit] of units) if (m % size === 0) return t(`schedules.repeat.${unit}`, { count: m / size });
  return t("schedules.repeat.minute", { count: m });
}

const outcomeTone = { started: "ok", refused: "bad", skipped: "neutral", failed: "bad" } as const;
function RunLine({ run }: { run: ScheduleRun }) {
  const { t } = useTranslation("requests");
  return (
    <div className="flex min-w-0 flex-col gap-0.5">
      <span className="flex flex-wrap items-center gap-1.5">
        <Badge tone={outcomeTone[run.outcome]}>{t(`schedules.outcome.${run.outcome}`)}</Badge>
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

const columnsFor = (t: TFunction<"requests">): Column<Schedule>[] => [
  {
    id: "name",
    header: t("schedules.columns.name"),
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
  { id: "env", header: t("schedules.columns.environment"), cell: (s) => <EnvBadge env={s.environment} /> },
  {
    id: "repeat",
    header: t("schedules.columns.repeats"),
    cell: (s) => <span className={s.cron ? "font-mono text-secondary" : undefined}>{repeatText(s)}</span>,
  },
  {
    id: "next",
    header: t("schedules.columns.next"),
    sortValue: (s) => (s.paused ? "~" : (s.next_runs[0] ?? "~")),
    cell: (s) =>
      s.paused ? (
        <div className="flex flex-col gap-0.5">
          <Badge tone="warn" className="self-start">
            {t("schedules.paused")}
          </Badge>
          {s.paused_note && <span className="text-caption text-muted">{s.paused_note}</span>}
        </div>
      ) : s.next_runs[0] ? (
        <Time iso={s.next_runs[0]} />
      ) : (
        <span className="text-muted">{t("schedules.notKnown")}</span>
      ),
  },
  {
    id: "last",
    header: t("schedules.columns.last"),
    cell: (s) =>
      s.last_run ? <RunLine run={s.last_run} /> : <span className="text-muted">{t("schedules.noneYet")}</span>,
  },
  {
    id: "owner",
    header: t("schedules.columns.owner"),
    className: "whitespace-nowrap text-muted",
    cell: (s) => s.owner.name,
  },
];

function RunsSheet({ id, onClose }: { id: string; onClose: () => void }) {
  const { t } = useTranslation("requests");
  const schedule = useQuery(q.schedule(id));
  const s = schedule.data;
  return (
    <Sheet
      open
      onOpenChange={(o) => !o && onClose()}
      title={s ? s.name : t("schedules.schedule")}
      description={t("schedules.sheetBody")}
    >
      {schedule.isError ? (
        <ErrorState error={schedule.error} onRetry={() => void schedule.refetch()} />
      ) : (
        s && (
          <div className="flex flex-col gap-4">
            <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1.5 text-body">
              <dt className="text-muted">{t("schedules.requests")}</dt>
              <dd>
                {t("schedules.requestsWhat", {
                  item: s.catalog_item.name,
                  version: s.version.number,
                  project: s.project.name,
                })}{" "}
                <EnvBadge env={s.environment} />
              </dd>
              <dt className="text-muted">{t("schedules.repeats")}</dt>
              <dd>{repeatText(s)}</dd>
              <dt className="text-muted">{t("schedules.runsAs")}</dt>
              <dd>{s.owner.name}</dd>
              {s.accept_warnings && (
                <>
                  <dt className="text-muted">{t("schedules.warnings")}</dt>
                  <dd>{t("schedules.despiteWarnings")}</dd>
                </>
              )}
            </dl>
            <section aria-labelledby="next-runs" className="flex flex-col gap-1.5">
              <h3 id="next-runs" className="text-secondary font-medium">
                {t("schedules.nextRuns")}
              </h3>
              {s.paused ? (
                <p className="text-secondary text-muted">{t("schedules.noneWhilePaused")}</p>
              ) : s.next_runs.length ? (
                <ul className="flex flex-col gap-0.5 text-body">
                  {s.next_runs.map((at) => (
                    <li key={at} className="tnum">
                      {fullDate(at)}
                    </li>
                  ))}
                </ul>
              ) : (
                <p className="text-secondary text-muted">{t("schedules.notKnownPeriod")}</p>
              )}
            </section>
            <section aria-labelledby="runs" className="flex flex-col gap-1.5">
              <h3 id="runs" className="text-secondary font-medium">
                {t("schedules.runs")}
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
                <p className="text-secondary text-muted">{t("schedules.notRunYet")}</p>
              )}
            </section>
          </div>
        )
      )}
    </Sheet>
  );
}

function PauseSheet({ schedule, onClose }: { schedule: Schedule; onClose: () => void }) {
  const { t } = useTranslation("requests");
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
      toast.success(t("schedules.pausedToast", { name: schedule.name }));
      onClose();
    },
    onError: (e) => {
      const field = isApiError(e) ? e.fieldErrors.find((f) => f.field === "/note") : undefined;
      if (field) return setError(field.message);
      toast.error(isApiError(e) ? e.problem.title : t("schedules.notPaused"), {
        description: isApiError(e) ? e.problem.detail : undefined,
      });
    },
  });
  return (
    <Sheet
      open
      onOpenChange={(o) => !o && onClose()}
      pending={pause.isPending}
      title={t("schedules.pauseTitle", { name: schedule.name })}
      description={t("schedules.pauseBody")}
      submitLabel={pause.isPending ? t("schedules.pausing") : t("schedules.pause")}
      onSubmit={() => pause.mutate()}
    >
      <Field label={t("schedules.why")} htmlFor="pause-note" hint={t("schedules.whyHint")} error={error}>
        <Textarea id="pause-note" rows={3} maxLength={500} value={note} onChange={(e) => setNote(e.target.value)} />
      </Field>
    </Sheet>
  );
}

export function SchedulesPage() {
  const { t } = useTranslation("requests");
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
    onSuccess: (s) => done(t("schedules.resumed", { name: s.name }))(),
    onError: refused(t("schedules.notResumed")),
  });
  const remove = useMutation({
    mutationFn: (s: Schedule) =>
      call(api.DELETE("/schedules/{scheduleId}", { params: { path: { scheduleId: s.id } } })),
    onSuccess: (_, s) => {
      done(t("schedules.deleted", { name: s.name }))();
      setDeleting(null);
    },
    onError: (e) => {
      setDeleting(null);
      refused(t("schedules.notDeleted"))(e);
    },
  });
  const newOne = (
    <Button variant="primary" asChild>
      <Link to="/catalog">{t("schedules.new")}</Link>
    </Button>
  );
  const list = schedules.data?.items;
  return (
    <Page>
      <PageHeader
        breadcrumbs={
          <Breadcrumbs>
            <Link to="/requests">{t("schedules.breadcrumb")}</Link>
          </Breadcrumbs>
        }
        title={t("schedules.title")}
        description={t("schedules.lead", { project: project.name })}
        actions={list?.length ? newOne : undefined}
      />
      <Panel flush>
        <DataTable
          label={t("schedules.table")}
          columns={columnsFor(t)}
          rows={list}
          rowKey={(s) => s.id}
          minWidth={860}
          loading={schedules.isPending}
          error={schedules.isError && <ErrorState error={schedules.error} onRetry={() => void schedules.refetch()} />}
          empty={
            <EmptyState
              icon={CalendarClock}
              title={t("schedules.none")}
              body={t("schedules.noneBody")}
              action={newOne}
            />
          }
          rowMenuLabel={(s) => t("schedules.actionsFor", { name: s.name })}
          rowMenu={(s) => (
            <>
              <MenuItem onSelect={() => setShowing(s.id)}>{t("schedules.showRuns")}</MenuItem>
              {manages(me, s) && (
                <>
                  {s.paused ? (
                    <MenuItem onSelect={() => resume.mutate(s)}>{t("schedules.resume")}</MenuItem>
                  ) : (
                    <MenuItem onSelect={() => setPausing(s)}>{t("schedules.pause")}</MenuItem>
                  )}
                  <MenuSeparator />
                  <MenuItem danger onSelect={() => setDeleting(s)}>
                    {t("schedules.delete")}
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
          title={t("schedules.deleteTitle", { name: deleting.name })}
          description={t("schedules.deleteBody")}
          confirmLabel={t("schedules.deleteConfirm")}
          pending={remove.isPending}
          onConfirm={() => remove.mutate(deleting)}
        />
      )}
    </Page>
  );
}
