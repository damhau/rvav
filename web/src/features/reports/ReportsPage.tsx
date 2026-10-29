// Reports (S13.4, S13.5): how the platform is used, for the projects the
// caller sees. One row of filters scopes everything below it; each section
// exports as CSV. Chart colours are validated as a set (styles.css,
// --chart-*), and every value a chart shows is in its table too.
import { keepPreviousData, useMutation, useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { Download } from "lucide-react";
import { useEffect, useId, useRef, useState } from "react";
import { toast } from "sonner";
import { ErrorState, SkeletonRows } from "@/components/common/feedback";
import { Meter } from "@/components/common/meter";
import { EmptyState, Page, PageHeader, Panel } from "@/components/common/page";
import { Button } from "@/components/ui/button";
import { Field } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { api, call, isApiError } from "@/lib/api/client";
import type { components } from "@/lib/api/schema";
import { useCurrentProject } from "@/lib/project";
import { q } from "@/lib/query";
import { cn, fullDate } from "@/lib/utils";

type S = components["schemas"];
type Interval = S["Report"]["interval"];
type Section = "requests" | "approvals" | "quota" | "leases";

const ranges: { value: string; label: string; periods: number; interval: Interval }[] = [
  { value: "7", label: "Last 7 days", periods: 7, interval: "day" },
  { value: "30", label: "Last 30 days", periods: 30, interval: "day" },
  { value: "13w", label: "Last 13 weeks", periods: 13, interval: "week" },
  { value: "12m", label: "Last 12 months", periods: 12, interval: "month" },
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
  if (v === undefined || v === null) return "none";
  const s = Math.round(v);
  if (s < 90) return `${s} s`;
  const m = Math.round(s / 60);
  if (m < 120) return `${m} min`;
  const h = Math.floor(m / 60);
  if (h < 48) return m % 60 ? `${h} h ${m % 60} min` : `${h} h`;
  return `${Math.round(h / 24)} days`;
}

const percent = (r?: number | null) => (r === undefined || r === null ? "none" : `${Math.round(r * 1000) / 10}%`);

function periodLabel(iso: string, interval: Interval, long = false) {
  const d = new Date(`${iso}T00:00:00Z`);
  if (interval === "month")
    return d.toLocaleDateString("en-GB", { month: long ? "long" : "short", year: "numeric", timeZone: "UTC" });
  const day = d.toLocaleDateString("en-GB", { day: "numeric", month: "short", timeZone: "UTC" });
  return interval === "week" && long ? `Week of ${day}` : day;
}

function StatTile({ label, value, detail }: { label: string; value: string; detail?: string }) {
  return (
    <div className="flex min-w-0 flex-col gap-0.5 rounded-lg border border-line bg-surface px-4 py-3">
      <span className="text-xs text-muted">{label}</span>
      <span className="text-[22px] leading-tight font-semibold">{value}</span>
      {detail && <span className="text-[11px] text-faint">{detail}</span>}
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

const series = [
  { key: "succeeded", label: "Succeeded", fill: "var(--chart-succeeded)" },
  { key: "failed", label: "Failed", fill: "var(--chart-failed)" },
  { key: "other", label: "Rejected, cancelled or not finished", fill: "var(--chart-other)" },
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

/** Requests per period, stacked by how they ended. */
function RequestsChart({ report }: { report: S["Report"] }) {
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
      <ul className="flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted" aria-label="Legend">
        {series.map((s) => (
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
            aria-label={`Requests by ${report.interval}: ${report.requests.totals.submitted} in all. Use the arrow keys to read each ${report.interval}, or the table below.`}
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
            {ticks.map((t) => (
              <g key={t}>
                <line x1={left} x2={left + plotW} y1={8 + plotH - y(t)} y2={8 + plotH - y(t)} stroke="var(--line)" />
                <text
                  x={left - 6}
                  y={8 + plotH - y(t)}
                  dy="0.32em"
                  textAnchor="end"
                  className="tnum fill-faint text-[10px]"
                >
                  {t}
                </text>
              </g>
            ))}
            {data.map((d, i) => {
              const x = left + i * band + (band - barW) / 2;
              let base = 8 + plotH;
              const segs = series
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
                    <text x={x + barW / 2} y={H - 6} textAnchor="middle" className="fill-faint text-[10px]">
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
            ? `${periodLabel(a.period, report.interval, true)}: ${a.submitted} requests, ${series
                .map((s) => `${a[s.key]} ${s.label.toLowerCase()}`)
                .join(", ")}`
            : ""}
        </span>
        {a && active !== null && (
          <div
            id={tipId}
            role="tooltip"
            className="pointer-events-none absolute top-0 z-10 flex min-w-[170px] flex-col gap-0.5 rounded-md border border-line bg-surface px-3 py-2 text-xs shadow-[var(--shadow)]"
            style={{
              left: Math.min(Math.max(0, left + active * band + band / 2 - 85), Math.max(0, width - 180)),
            }}
          >
            <span className="text-muted">{periodLabel(a.period, report.interval, true)}</span>
            <span>
              <span className="font-semibold">{a.submitted}</span> <span className="text-muted">requests</span>
            </span>
            {series.map((s) => (
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
      <details className="text-xs">
        <summary className="cursor-pointer text-muted">Show as a table</summary>
        <div className="mt-2 max-h-64 overflow-y-auto">
          <table className="w-full border-collapse text-xs" aria-label="Requests by period">
            <thead>
              <tr className="label text-left">
                <th scope="col" className="py-1 pr-3 font-medium">
                  Period
                </th>
                <th scope="col" className="py-1 pr-3 text-right font-medium">
                  Requests
                </th>
                {series.map((s) => (
                  <th key={s.key} scope="col" className="py-1 pr-3 text-right font-medium">
                    {s.label}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {data.map((d) => (
                <tr key={d.period} className="border-t border-line">
                  <th scope="row" className="py-1 pr-3 text-left font-normal">
                    {periodLabel(d.period, report.interval, true)}
                  </th>
                  <td className="tnum py-1 pr-3 text-right">{d.submitted}</td>
                  {series.map((s) => (
                    <td key={s.key} className="tnum py-1 pr-3 text-right">
                      {d[s.key]}
                    </td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </details>
    </div>
  );
}

const resourceLabel: Record<S["QuotaResource"], [string, string?]> = {
  vcpu: ["vCPU"],
  memory_gb: ["Memory", "GB"],
  storage_gb: ["Storage", "GB"],
  vm_count: ["VMs"],
};

function Quota({ lines }: { lines: S["ReportQuotaLine"][] }) {
  if (lines.length === 0) return <EmptyState title="No projects" body="You see no project's quota here." />;
  // One block per project and scope.
  const groups = new Map<string, S["ReportQuotaLine"][]>();
  for (const l of lines) {
    const key = `${l.project.name}${l.environment ? ` · ${l.environment}` : ""}`;
    groups.set(key, [...(groups.get(key) ?? []), l]);
  }
  return (
    <div className="grid grid-cols-[repeat(auto-fill,minmax(240px,1fr))] gap-x-6 gap-y-4 p-4">
      {[...groups].map(([key, ls]) => (
        <section key={key} aria-label={`Quota of ${key}`} className="flex flex-col gap-2">
          <h3 className="text-[13px] font-medium">{key}</h3>
          {ls.map((l) => {
            const [name, unit] = resourceLabel[l.resource];
            return l.limit !== undefined && l.limit !== null ? (
              <Meter key={l.resource} label={name} used={l.used} total={l.limit} unit={unit} />
            ) : (
              <div key={l.resource} className="flex items-baseline justify-between gap-2 text-xs">
                <span className="text-muted">{name}</span>
                <span className="tnum">
                  {l.used}
                  {unit ? ` ${unit}` : ""} <span className="text-faint">· no limit</span>
                </span>
              </div>
            );
          })}
        </section>
      ))}
    </div>
  );
}

const th = "border-b border-line px-4 py-2 text-left font-medium";
const td = "border-b border-line px-4 py-2";

const spanDays: Record<Interval, number> = { day: 1, week: 7, month: 30.4 };

export function ReportsPage() {
  const { me } = useCurrentProject();
  const [range, setRange] = useState("30");
  const preset = ranges.find((r) => r.value === range)!;
  const [interval, setInterval] = useState<Interval>(preset.interval);
  const [project, setProject] = useState("any");
  const [environment, setEnvironment] = useState("any");
  const environments = useQuery(q.environments());
  const today = new Date();
  const filters = {
    // Whole periods of the chosen size, as many as the preset's span.
    from: isoDay(
      rangeStart(today, Math.ceil((preset.periods * spanDays[preset.interval]) / spanDays[interval]), interval),
    ),
    to: isoDay(today),
    project_id: project === "any" ? undefined : project,
    environment: environment === "any" ? undefined : environment,
  };
  const report = useQuery({
    queryKey: ["reports", filters, interval],
    queryFn: ({ signal }) => call(api.GET("/reports", { params: { query: { ...filters, interval } }, signal })),
    placeholderData: keepPreviousData,
  });
  const exportCsv = useMutation({
    mutationFn: async (section: Section) => {
      const text = await call(
        api.GET("/reports/export", { params: { query: { ...filters, section } }, parseAs: "text" }),
      );
      const url = URL.createObjectURL(new Blob([String(text)], { type: "text/csv" }));
      Object.assign(document.createElement("a"), { href: url, download: `rvav-${section}.csv` }).click();
      URL.revokeObjectURL(url);
    },
    onError: (e) => toast.error(isApiError(e) ? e.problem.title : "The export failed"),
  });
  const csv = (section: Section) => (
    <Button size="sm" variant="ghost" disabled={exportCsv.isPending} onClick={() => exportCsv.mutate(section)}>
      <Download /> CSV
    </Button>
  );
  const r = report.data;
  const seesAll = me.global_roles.includes("admin") || me.global_roles.includes("auditor");
  return (
    <Page>
      <PageHeader
        title="Reports"
        meta={
          seesAll
            ? "How the platform is used, across every project."
            : "How the platform is used, in the projects you're a member of."
        }
      />
      <div className="flex flex-wrap items-end gap-2" role="group" aria-label="Report filters">
        <Field label="Period" htmlFor="rp-range" className="w-40">
          <Select
            id="rp-range"
            value={range}
            onChange={(v) => {
              setRange(v);
              setInterval(ranges.find((x) => x.value === v)!.interval);
            }}
            options={ranges.map((x) => ({ value: x.value, label: x.label }))}
          />
        </Field>
        <Field label="By" htmlFor="rp-interval" className="w-28">
          <Select
            id="rp-interval"
            value={interval}
            onChange={(v) => setInterval(v as Interval)}
            options={[
              { value: "day", label: "Day" },
              { value: "week", label: "Week" },
              { value: "month", label: "Month" },
            ]}
          />
        </Field>
        <Field label="Project" htmlFor="rp-project" className="w-48">
          <Select
            id="rp-project"
            value={project}
            onChange={setProject}
            options={[
              { value: "any", label: "All" },
              ...me.projects.map((p) => ({ value: p.project.id, label: p.project.name })),
            ]}
          />
        </Field>
        <Field label="Environment" htmlFor="rp-env" className="w-36">
          <Select
            id="rp-env"
            value={environment}
            onChange={setEnvironment}
            options={[
              { value: "any", label: "All" },
              ...(environments.data?.items ?? []).map((e) => ({ value: e.key, label: e.name })),
            ]}
          />
        </Field>
      </div>
      {report.isPending && <SkeletonRows rows={6} cols={4} />}
      {report.isError && <ErrorState error={report.error} onRetry={() => void report.refetch()} />}
      {r && (
        <div className={cn("flex flex-col gap-4 transition-opacity", report.isPlaceholderData && "opacity-60")}>
          <div className="grid grid-cols-[repeat(auto-fit,minmax(170px,1fr))] gap-3">
            <StatTile label="Requests" value={r.requests.totals.submitted.toLocaleString("en-GB")} />
            <StatTile
              label="Success rate"
              value={percent(r.requests.totals.success_rate)}
              detail={`${r.requests.totals.succeeded} succeeded, ${r.requests.totals.failed} failed`}
            />
            <StatTile
              label="Provisioning time"
              value={seconds(r.provisioning.p50_seconds)}
              detail={
                r.provisioning.count ? `median; 90% within ${seconds(r.provisioning.p90_seconds)}` : "nothing built"
              }
            />
            <StatTile
              label="Approval lead time"
              value={seconds(r.approvals.lead_time.p50_seconds)}
              detail={r.approvals.decided ? `median of ${r.approvals.decided} decided` : "nothing decided"}
            />
            <StatTile
              label="Waiting for approval"
              value={String(r.approvals.pending)}
              detail={
                r.approvals.oldest_pending_since
                  ? `the oldest since ${fullDate(r.approvals.oldest_pending_since)}`
                  : undefined
              }
            />
          </div>
          <Panel title={`Requests by ${r.interval}`} actions={csv("requests")}>
            <RequestsChart report={r} />
          </Panel>
          <div className="flex flex-wrap items-start gap-4">
            <Panel title="Most requested" flush className="min-w-0 flex-[2_1_420px]">
              {r.requests.by_item.length === 0 ? (
                <EmptyState title="Nothing requested" body="No new deployments were requested in this period." />
              ) : (
                <div className="overflow-x-auto">
                  <table className="w-full min-w-[420px] border-collapse text-[13px]">
                    <thead>
                      <tr className="label">
                        <th scope="col" className={th}>
                          Item
                        </th>
                        <th scope="col" className={th + " text-right"}>
                          Requests
                        </th>
                        <th scope="col" className={th + " text-right"}>
                          Success rate
                        </th>
                        <th scope="col" className={th + " text-right"}>
                          Build time (median)
                        </th>
                      </tr>
                    </thead>
                    <tbody>
                      {r.requests.by_item.map((it) => (
                        <tr key={it.item.id}>
                          <th scope="row" className={td + " text-left font-medium"}>
                            {it.item.name}
                          </th>
                          <td className={td + " tnum text-right"}>{it.submitted}</td>
                          <td className={td + " tnum text-right"}>{percent(it.success_rate)}</td>
                          <td className={td + " tnum text-right"}>{seconds(it.provisioning.p50_seconds)}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )}
            </Panel>
            <Panel title="Approvals" actions={csv("approvals")} className="min-w-0 flex-[1_1_260px]">
              <dl className="grid grid-cols-[1fr_auto] gap-x-4 gap-y-1.5 text-[13px]">
                <dt className="text-muted">Decided</dt>
                <dd className="tnum text-right">{r.approvals.decided}</dd>
                <dt className="text-muted">Approved</dt>
                <dd className="tnum text-right">{r.approvals.approved}</dd>
                <dt className="text-muted">Rejected</dt>
                <dd className="tnum text-right">{r.approvals.rejected}</dd>
                <dt className="text-muted">Lead time, median</dt>
                <dd className="tnum text-right">{seconds(r.approvals.lead_time.p50_seconds)}</dd>
                <dt className="text-muted">Lead time, 90%</dt>
                <dd className="tnum text-right">{seconds(r.approvals.lead_time.p90_seconds)}</dd>
                <dt className="text-muted">Waiting now</dt>
                <dd className="tnum text-right">{r.approvals.pending}</dd>
              </dl>
            </Panel>
          </div>
          <Panel title="Usage against quota" flush actions={csv("quota")}>
            <Quota lines={r.quota} />
          </Panel>
          <Panel title="Leases ending in the next 30 days" flush actions={csv("leases")}>
            {r.leases.length === 0 ? (
              <EmptyState title="None" body="No lease ends in the next 30 days." />
            ) : (
              <div className="overflow-x-auto">
                <table className="w-full min-w-[560px] border-collapse text-[13px]">
                  <thead>
                    <tr className="label">
                      <th scope="col" className={th}>
                        Deployment
                      </th>
                      <th scope="col" className={th}>
                        Project
                      </th>
                      <th scope="col" className={th}>
                        Owner
                      </th>
                      <th scope="col" className={th}>
                        Ends
                      </th>
                    </tr>
                  </thead>
                  <tbody>
                    {r.leases.map((l) => (
                      <tr key={l.deployment.id}>
                        <th scope="row" className={td + " text-left font-medium"}>
                          <Link to="/deployments/$deploymentId" params={{ deploymentId: l.deployment.id }}>
                            {l.deployment.name}
                          </Link>
                        </th>
                        <td className={td + " text-muted"}>
                          {l.project.name} · {l.environment}
                        </td>
                        <td className={td}>{l.owner.name}</td>
                        <td className={td + " tnum"}>{fullDate(l.expires_at)}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </Panel>
        </div>
      )}
    </Page>
  );
}
