// Reports (S13.4, S13.5): how the platform is used, for the projects the
// caller sees. One row of filters scopes everything below it; each section
// exports as CSV. Chart colours are validated as a set (styles.css,
// --chart-*), and every value a chart shows is in its table too.
import { keepPreviousData, useMutation, useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { Download } from "lucide-react";
import { useEffect, useId, useRef, useState } from "react";
import type { TFunction } from "i18next";
import { Trans, useTranslation } from "react-i18next";
import { toast } from "sonner";
import { ErrorState, SkeletonRows } from "@/components/common/feedback";
import { Meter } from "@/components/common/meter";
import { Page } from "@/components/common/page";
import { EmptyState } from "@/components/ui/empty-state";
import { PageHeader } from "@/components/ui/page-header";
import { Panel } from "@/components/ui/panel";
import { DataTable, type Column } from "@/components/ui/data-table";
import { Button } from "@/components/ui/button";
import { FilterBar, FilterSelect, useAddressFilters } from "@/components/ui/filter-bar";
import { Select } from "@/components/ui/select";
import { api, call, isApiError } from "@/lib/api/client";
import type { components } from "@/lib/api/schema";
import { can } from "@/lib/can";
import { useEnvName } from "@/lib/environments";
import { useCurrentProject } from "@/lib/project";
import { q } from "@/lib/query";
import { cn } from "@/lib/utils";
import { formatDate, formatNumber, formatPercent, fullDate } from "@/lib/format";
import { i18n } from "@/lib/i18n";

type S = components["schemas"];
type Interval = S["Report"]["interval"];
type Section = "requests" | "approvals" | "quota" | "leases";

export const reportPeriods = ["7d", "30d", "13w", "12m"] as const;
export const reportIntervals = ["day", "week", "month"] as const;

/** The report's filters in the address; each left out is its default: 30 days, by day, every project and environment. */
export interface ReportsSearch {
  period?: (typeof reportPeriods)[number];
  by?: Interval;
  project?: string;
  env?: string;
}

const filterKeys = ["period", "by", "project", "env"] as const;

const ranges: { value: (typeof reportPeriods)[number]; periods: number; interval: Interval }[] = [
  { value: "7d", periods: 7, interval: "day" },
  { value: "30d", periods: 30, interval: "day" },
  { value: "13w", periods: 13, interval: "week" },
  { value: "12m", periods: 12, interval: "month" },
];

const isoDay = (d: Date) => d.toISOString().slice(0, 10);

/** The first day (UTC) of the period n periods back from today's, so that
 * the first column counts a whole day, week (from Monday) or month. */
export function rangeStart(today: Date, periods: number, interval: Interval): Date {
  const d = new Date(Date.UTC(today.getUTCFullYear(), today.getUTCMonth(), today.getUTCDate()));
  if (interval === "month") return new Date(Date.UTC(d.getUTCFullYear(), d.getUTCMonth() - (periods - 1), 1));
  if (interval === "week") {
    const monday = d.getTime() - ((d.getUTCDay() + 6) % 7) * 86_400_000;
    return new Date(monday - (periods - 1) * 7 * 86_400_000);
  }
  return new Date(d.getTime() - (periods - 1) * 86_400_000);
}

/** Seconds as people say them: 45 s, 7 min, 2 h 10 min, 3 days. */
export function seconds(v?: number | null): string {
  const t = i18n.getFixedT(null, "reports");
  if (v === undefined || v === null) return t("duration.none");
  const s = Math.round(v);
  if (s < 90) return t("duration.seconds", { s });
  const m = Math.round(s / 60);
  if (m < 120) return t("duration.minutes", { m });
  const h = Math.floor(m / 60);
  if (h < 48) return m % 60 ? t("duration.hoursMinutes", { h, m: m % 60 }) : t("duration.hours", { h });
  return t("duration.days", { count: Math.round(h / 24) });
}

const percent = (r?: number | null) =>
  r === undefined || r === null ? i18n.t("reports:duration.none") : formatPercent(r);

function periodLabel(iso: string, interval: Interval, long = false) {
  const d = new Date(`${iso}T00:00:00Z`);
  if (interval === "month") return formatDate(d, { month: long ? "long" : "short", year: "numeric", timeZone: "UTC" });
  const day = formatDate(d, { day: "numeric", month: "short", timeZone: "UTC" });
  return interval === "week" && long ? i18n.t("reports:weekOf", { day }) : day;
}

function StatTile({ label, value, detail }: { label: string; value: string; detail?: string }) {
  return (
    <div className="flex min-w-0 flex-col gap-0.5 rounded-lg border border-line bg-surface px-4 py-3">
      <span className="text-secondary text-muted">{label}</span>
      <span className="text-title leading-tight font-semibold">{value}</span>
      {detail && <span className="text-caption text-faint">{detail}</span>}
    </div>
  );
}

function useWidth<T extends HTMLElement>() {
  const ref = useRef<T>(null);
  const [width, setWidth] = useState(0);
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    const ro = new ResizeObserver(([e]) => setWidth(e!.contentRect.width));
    ro.observe(el);
    return () => ro.disconnect();
  }, []);
  return [ref, width] as const;
}

/** Axis ticks for counts: whole numbers, five steps at most, 1, 2 or 5
 * times a power of ten apart, the last at or above max. */
export function countTicks(max: number): number[] {
  const mag = 10 ** Math.floor(Math.log10(Math.max(max, 1)));
  let step = mag * 10;
  for (const m of [0.1, 0.2, 0.5, 1, 2, 5, 10]) {
    const s = Math.max(1, m * mag);
    if (Math.ceil(max / s) <= 5) {
      step = s;
      break;
    }
  }
  const top = Math.max(1, Math.ceil(max / step)) * step;
  return Array.from({ length: top / step + 1 }, (_, i) => i * step);
}

const series = (t: TFunction<"reports">) =>
  [
    { key: "succeeded", label: t("series.succeeded"), fill: "var(--chart-succeeded)" },
    { key: "failed", label: t("series.failed"), fill: "var(--chart-failed)" },
    { key: "other", label: t("series.other"), fill: "var(--chart-other)" },
  ] as const;

type Bar = { period: string; succeeded: number; failed: number; other: number; submitted: number };

function bars(r: S["Report"]): Bar[] {
  return r.requests.over_time.map((p) => ({
    period: p.period,
    succeeded: p.succeeded,
    failed: p.failed,
    other: p.submitted - p.succeeded - p.failed,
    submitted: p.submitted,
  }));
}

/** The chart's figures as a table: a row per period. */
const chartColumns = (t: TFunction<"reports">, interval: Interval): Column<Bar>[] => [
  {
    id: "period",
    header: t("chart.period"),
    sortValue: (d) => d.period,
    cell: (d) => periodLabel(d.period, interval, true),
  },
  {
    id: "requests",
    header: t("chart.requests"),
    align: "end",
    className: "tnum",
    sortValue: (d) => d.submitted,
    cell: (d) => d.submitted,
  },
  ...series(t).map((s): Column<Bar> => ({
    id: s.key,
    header: s.label,
    align: "end",
    className: "tnum",
    sortValue: (d) => d[s.key],
    cell: (d) => d[s.key],
  })),
];

/** Requests per period, stacked by how they ended. */
function RequestsChart({ report }: { report: S["Report"] }) {
  const { t } = useTranslation("reports");
  const lines = series(t);
  const [wrap, width] = useWidth<HTMLDivElement>();
  const [active, setActive] = useState<number | null>(null);
  const tipId = useId();
  const data = bars(report);
  const H = 200;
  const left = 32;
  const bottom = 22;
  const plotW = Math.max(0, width - left - 8);
  const plotH = H - bottom - 8;
  const ticks = countTicks(Math.max(0, ...data.map((d) => d.submitted)));
  const top = ticks[ticks.length - 1]!;
  const y = (v: number) => (v / top) * plotH;
  const band = data.length ? plotW / data.length : 0;
  const barW = Math.max(2, Math.min(24, band * 0.7));
  const every = Math.max(1, Math.ceil((data.length * 46) / Math.max(plotW, 1)));
  const a = active === null ? null : data[active];
  return (
    <div className="flex flex-col gap-2">
      <ul className="flex flex-wrap gap-x-4 gap-y-1 text-secondary text-muted" aria-label={t("chart.legend")}>
        {lines.map((s) => (
          <li key={s.key} className="flex items-center gap-1.5">
            <svg width="10" height="10" aria-hidden>
              <rect width="10" height="10" rx="2" fill={s.fill} />
            </svg>
            {s.label}
          </li>
        ))}
      </ul>
      <div ref={wrap} className="relative">
        {width > 0 && (
          <svg
            width={width}
            height={H}
            role="img"
            tabIndex={0}
            aria-label={t(`chart.label.${report.interval}`, { total: report.requests.totals.submitted })}
            className="block outline-none focus-visible:ring-2 focus-visible:ring-accent/40"
            onKeyDown={(e) => {
              if (e.key === "ArrowRight") setActive((i) => Math.min(data.length - 1, (i ?? -1) + 1));
              else if (e.key === "ArrowLeft") setActive((i) => Math.max(0, (i ?? data.length) - 1));
              else if (e.key === "Escape") setActive(null);
              else return;
              e.preventDefault();
            }}
            onBlur={() => setActive(null)}
            onPointerLeave={() => setActive(null)}
          >
            {ticks.map((tick) => (
              <g key={tick}>
                <line
                  x1={left}
                  x2={left + plotW}
                  y1={8 + plotH - y(tick)}
                  y2={8 + plotH - y(tick)}
                  stroke="var(--line)"
                />
                <text
                  x={left - 6}
                  y={8 + plotH - y(tick)}
                  dy="0.32em"
                  textAnchor="end"
                  className="tnum fill-faint text-caption"
                >
                  {tick}
                </text>
              </g>
            ))}
            {data.map((d, i) => {
              const x = left + i * band + (band - barW) / 2;
              let base = 8 + plotH;
              const segs = lines
                .map((s) => ({ s, v: d[s.key] }))
                .filter((g) => g.v > 0)
                .map((g, j, all) => {
                  const h = y(g.v);
                  const gap = j < all.length - 1 ? 2 : 0; // the surface gap between segments
                  const seg = { ...g, y: base - h, h: Math.max(0, h - gap), last: j === all.length - 1 };
                  base -= h;
                  return seg;
                });
              const dim = active !== null && active !== i;
              return (
                <g key={d.period} opacity={dim ? 0.6 : 1}>
                  {segs.map((g) => {
                    const r = g.last ? Math.min(4, g.h, barW / 2) : 0;
                    const x2 = x + barW;
                    const y2 = g.y + g.h;
                    return (
                      <path
                        key={g.s.key}
                        fill={g.s.fill}
                        d={`M${x},${y2}V${g.y + r}Q${x},${g.y} ${x + r},${g.y}H${x2 - r}Q${x2},${g.y} ${x2},${g.y + r}V${y2}Z`}
                      />
                    );
                  })}
                  {/* The hit target is the whole band, taller than the bar. */}
                  <rect
                    x={left + i * band}
                    y={8}
                    width={band}
                    height={plotH}
                    fill="transparent"
                    onPointerEnter={() => setActive(i)}
                  />
                  {i % every === 0 && (
                    <text x={x + barW / 2} y={H - 6} textAnchor="middle" className="fill-faint text-caption">
                      {periodLabel(d.period, report.interval)}
                    </text>
                  )}
                </g>
              );
            })}
          </svg>
        )}
        {/* What the arrow keys move to, said by screen readers. */}
        <span className="sr-only" aria-live="polite">
          {a
            ? t("chart.live", {
                period: periodLabel(a.period, report.interval, true),
                count: a.submitted,
                parts: lines.map((s) => t("chart.part", { n: a[s.key], label: s.label.toLowerCase() })).join(", "),
              })
            : ""}
        </span>
        {a && active !== null && (
          <div
            id={tipId}
            role="tooltip"
            className="pointer-events-none absolute top-0 z-10 flex min-w-[170px] flex-col gap-0.5 rounded-md border border-line bg-surface px-3 py-2 text-secondary shadow-[var(--shadow)]"
            style={{
              left: Math.min(Math.max(0, left + active * band + band / 2 - 85), Math.max(0, width - 180)),
            }}
          >
            <span className="text-muted">{periodLabel(a.period, report.interval, true)}</span>
            <span>
              <Trans
                t={t}
                i18nKey="chart.tipRequests"
                count={a.submitted}
                components={{ n: <span className="font-semibold" />, label: <span className="text-muted" /> }}
              />
            </span>
            {lines.map((s) => (
              <span key={s.key} className="flex items-center gap-1.5">
                <svg width="10" height="2" aria-hidden>
                  <rect width="10" height="2" fill={s.fill} />
                </svg>
                <span className="tnum font-medium">{a[s.key]}</span>
                <span className="text-muted">{s.label.toLowerCase()}</span>
              </span>
            ))}
          </div>
        )}
      </div>
      <details className="text-secondary">
        <summary className="cursor-pointer text-muted">{t("chart.showTable")}</summary>
        <div className="-mx-4 mt-2 max-h-64 overflow-y-auto">
          <DataTable
            label={t("chart.table")}
            columns={chartColumns(t, report.interval)}
            rows={data}
            rowKey={(d) => d.period}
            stack={false}
          />
        </div>
      </details>
    </div>
  );
}

const resourceUnit: Record<S["QuotaResource"], "GB" | undefined> = {
  vcpu: undefined,
  memory_gb: "GB",
  storage_gb: "GB",
  vm_count: undefined,
};

function Quota({ lines }: { lines: S["ReportQuotaLine"][] }) {
  const { t } = useTranslation(["reports", "common"]);
  const envName = useEnvName();
  if (lines.length === 0) return <EmptyState title={t("quota.none")} body={t("quota.noneBody")} />;
  // One block per project and scope.
  const groups = new Map<string, S["ReportQuotaLine"][]>();
  for (const l of lines) {
    const key = `${l.project.name}${l.environment ? ` · ${envName(l.environment)}` : ""}`;
    groups.set(key, [...(groups.get(key) ?? []), l]);
  }
  return (
    <div className="grid grid-cols-[repeat(auto-fill,minmax(240px,1fr))] gap-x-6 gap-y-4 p-4">
      {[...groups].map(([key, ls]) => (
        <section key={key} aria-label={t("quota.of", { scope: key })} className="flex flex-col gap-2">
          <h3 className="text-body font-medium">{key}</h3>
          {ls.map((l) => {
            return (
              <Meter
                key={l.resource}
                label={t(`common:quota.${l.resource}`)}
                used={l.used}
                total={l.limit}
                unit={resourceUnit[l.resource]}
              />
            );
          })}
        </section>
      ))}
    </div>
  );
}

const itemColumns = (t: TFunction<"reports">): Column<S["ReportItem"]>[] => [
  {
    id: "item",
    header: t("panels.item"),
    sortValue: (it) => it.item.name,
    cell: (it) => <span className="font-medium">{it.item.name}</span>,
  },
  {
    id: "requests",
    header: t("panels.requests"),
    align: "end",
    className: "tnum",
    sortValue: (it) => it.submitted,
    cell: (it) => it.submitted,
  },
  {
    id: "success",
    header: t("panels.successRate"),
    align: "end",
    className: "tnum",
    sortValue: (it) => it.success_rate,
    cell: (it) => percent(it.success_rate),
  },
  {
    id: "build",
    header: t("panels.buildTime"),
    align: "end",
    className: "tnum",
    sortValue: (it) => it.provisioning.p50_seconds,
    cell: (it) => seconds(it.provisioning.p50_seconds),
  },
];

const leaseColumns = (t: TFunction<"reports">, envName: (key: string) => string): Column<S["ReportLease"]>[] => [
  {
    id: "deployment",
    header: t("panels.deployment"),
    sortValue: (l) => l.deployment.name,
    cell: (l) => (
      <Link to="/deployments/$deploymentId" params={{ deploymentId: l.deployment.id }} className="font-medium">
        {l.deployment.name}
      </Link>
    ),
  },
  {
    id: "project",
    header: t("panels.project"),
    className: "text-muted",
    sortValue: (l) => `${l.project.name} ${envName(l.environment)}`,
    cell: (l) => (
      <>
        {l.project.name} · {envName(l.environment)}
      </>
    ),
  },
  {
    id: "owner",
    header: t("panels.owner"),
    sortValue: (l) => l.owner.name,
    cell: (l) => l.owner.name,
  },
  {
    id: "ends",
    header: t("panels.ends"),
    className: "tnum",
    sortValue: (l) => Date.parse(l.expires_at),
    cell: (l) => fullDate(l.expires_at),
  },
];

const spanDays: Record<Interval, number> = { day: 1, week: 7, month: 30.4 };

export function ReportsPage({ search }: { search: ReportsSearch }) {
  const { t } = useTranslation("reports");
  const { me } = useCurrentProject();
  const filters = useAddressFilters(search, filterKeys);
  const preset = ranges.find((r) => r.value === (search.period ?? "30d"))!;
  const interval = search.by ?? preset.interval;
  const environments = useQuery(q.environments());
  const envName = useEnvName();
  const today = new Date();
  const scope = {
    // Whole periods of the chosen size, as many as the preset's span.
    from: isoDay(
      rangeStart(today, Math.ceil((preset.periods * spanDays[preset.interval]) / spanDays[interval]), interval),
    ),
    to: isoDay(today),
    project_id: search.project,
    environment: search.env,
  };
  const report = useQuery({
    queryKey: ["reports", scope, interval],
    queryFn: ({ signal }) => call(api.GET("/reports", { params: { query: { ...scope, interval } }, signal })),
    placeholderData: keepPreviousData,
  });
  const exportCsv = useMutation({
    mutationFn: async (section: Section) => {
      const text = await call(
        api.GET("/reports/export", { params: { query: { ...scope, section } }, parseAs: "text" }),
      );
      const url = URL.createObjectURL(new Blob([String(text)], { type: "text/csv" }));
      Object.assign(document.createElement("a"), { href: url, download: `rvav-${section}.csv` }).click();
      URL.revokeObjectURL(url);
    },
    onError: (e) => toast.error(isApiError(e) ? e.problem.title : i18n.t("reports:page.exportFailed")),
  });
  const csv = (section: Section) => (
    <Button size="sm" variant="quiet" disabled={exportCsv.isPending} onClick={() => exportCsv.mutate(section)}>
      <Download /> CSV
    </Button>
  );
  const r = report.data;
  const seesAll = can(me, "project.see");
  return (
    <Page>
      <PageHeader title={t("page.title")} meta={t(seesAll ? "page.metaAll" : "page.metaMember")} />
      <FilterBar filters={filters} label={t("page.filters")}>
        <Select
          aria-label={t("page.period")}
          value={preset.value}
          onChange={(v) =>
            filters.set({ period: v === "30d" ? undefined : (v as ReportsSearch["period"]), by: undefined })
          }
          options={ranges.map((x) => ({ value: x.value, label: t(`ranges.${x.value}`) }))}
          className="w-44"
        />
        <Select
          aria-label={t("page.interval")}
          value={interval}
          onChange={(v) => filters.set({ by: v === preset.interval ? undefined : (v as Interval) })}
          options={reportIntervals.map((x) => ({ value: x, label: t(`page.by.${x}`) }))}
          className="w-36"
        />
        <FilterSelect
          name="project"
          label={t("page.project")}
          any={t("page.allProjects")}
          options={me.projects.map((p) => ({ value: p.project.id, label: p.project.name }))}
          className="w-48"
        />
        <FilterSelect
          name="env"
          label={t("page.environment")}
          any={t("page.allEnvironments")}
          loading={environments.isPending}
          options={(environments.data?.items ?? []).map((e) => ({ value: e.key, label: e.name }))}
          className="w-48"
        />
      </FilterBar>
      {report.isPending && <SkeletonRows rows={6} cols={4} />}
      {report.isError && <ErrorState error={report.error} onRetry={() => void report.refetch()} />}
      {r && (
        <div className={cn("flex flex-col gap-4 transition-opacity", report.isPlaceholderData && "opacity-60")}>
          <div className="grid grid-cols-[repeat(auto-fit,minmax(170px,1fr))] gap-3">
            <StatTile label={t("stats.requests")} value={formatNumber(r.requests.totals.submitted)} />
            <StatTile
              label={t("stats.successRate")}
              value={percent(r.requests.totals.success_rate)}
              detail={t("stats.outcomes", { succeeded: r.requests.totals.succeeded, failed: r.requests.totals.failed })}
            />
            <StatTile
              label={t("stats.provisioningTime")}
              value={seconds(r.provisioning.p50_seconds)}
              detail={
                r.provisioning.count
                  ? t("stats.provisioningDetail", { time: seconds(r.provisioning.p90_seconds) })
                  : t("stats.nothingBuilt")
              }
            />
            <StatTile
              label={t("stats.approvalLeadTime")}
              value={seconds(r.approvals.lead_time.p50_seconds)}
              detail={
                r.approvals.decided ? t("stats.medianOf", { count: r.approvals.decided }) : t("stats.nothingDecided")
              }
            />
            <StatTile
              label={t("stats.waiting")}
              value={String(r.approvals.pending)}
              detail={
                r.approvals.oldest_pending_since
                  ? t("stats.oldestSince", { date: fullDate(r.approvals.oldest_pending_since) })
                  : undefined
              }
            />
          </div>
          <Panel title={t(`panels.requestsBy.${r.interval}`)} actions={csv("requests")}>
            <RequestsChart report={r} />
          </Panel>
          <div className="flex flex-wrap items-start gap-4">
            <Panel title={t("panels.mostRequested")} flush className="min-w-0 flex-[2_1_420px]">
              {r.requests.by_item.length === 0 ? (
                <EmptyState title={t("panels.nothingRequested")} body={t("panels.nothingRequestedBody")} />
              ) : (
                <DataTable
                  label={t("panels.mostRequested")}
                  columns={itemColumns(t)}
                  rows={r.requests.by_item}
                  rowKey={(it) => it.item.id}
                  minWidth={420}
                />
              )}
            </Panel>
            <Panel title={t("panels.approvals")} actions={csv("approvals")} className="min-w-0 flex-[1_1_260px]">
              <dl className="grid grid-cols-[1fr_auto] gap-x-4 gap-y-1.5 text-body">
                <dt className="text-muted">{t("panels.decided")}</dt>
                <dd className="tnum text-right">{r.approvals.decided}</dd>
                <dt className="text-muted">{t("panels.approved")}</dt>
                <dd className="tnum text-right">{r.approvals.approved}</dd>
                <dt className="text-muted">{t("panels.rejected")}</dt>
                <dd className="tnum text-right">{r.approvals.rejected}</dd>
                <dt className="text-muted">{t("panels.leadMedian")}</dt>
                <dd className="tnum text-right">{seconds(r.approvals.lead_time.p50_seconds)}</dd>
                <dt className="text-muted">{t("panels.lead90")}</dt>
                <dd className="tnum text-right">{seconds(r.approvals.lead_time.p90_seconds)}</dd>
                <dt className="text-muted">{t("panels.waitingNow")}</dt>
                <dd className="tnum text-right">{r.approvals.pending}</dd>
              </dl>
            </Panel>
          </div>
          <Panel title={t("panels.quota")} flush actions={csv("quota")}>
            <Quota lines={r.quota} />
          </Panel>
          <Panel title={t("panels.leases")} flush actions={csv("leases")}>
            {r.leases.length === 0 ? (
              <EmptyState title={t("panels.noLeases")} body={t("panels.noLeasesBody")} />
            ) : (
              <DataTable
                label={t("panels.leases")}
                columns={leaseColumns(t, envName)}
                rows={r.leases}
                rowKey={(l) => l.deployment.id}
                rowLinks
                minWidth={560}
              />
            )}
          </Panel>
        </div>
      )}
    </Page>
  );
}
