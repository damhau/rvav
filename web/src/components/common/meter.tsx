import { cn } from "@/lib/utils";

/**
 * Quota/usage bar. `adding` shows what a pending request would add. With no
 * `total` there is no limit, and so no bar: one drawn against the usage itself
 * would be full, in the colour of a quota nearly used up.
 */
export function Meter({
  label,
  used,
  total,
  unit,
  adding = 0,
}: {
  label: string;
  used: number;
  total?: number | null;
  unit?: string;
  adding?: number;
}) {
  const after = used + adding;
  const fmt = (v: number) => (Number.isInteger(v) ? v : v.toFixed(1));
  if (total === undefined || total === null) {
    return (
      <div className="flex items-baseline justify-between gap-2 text-xs">
        <span className="text-muted">{label}</span>
        <span className="tnum">
          {fmt(after)}
          {unit ? ` ${unit}` : ""} <span className="text-faint">· no limit</span>
        </span>
      </div>
    );
  }
  const pct = (v: number) => Math.min(100, (v / total) * 100);
  const tone = after > total ? "bg-bad" : after / total > 0.85 ? "bg-warn" : "bg-accent";
  return (
    <div className="flex flex-col gap-1">
      <div className="flex items-baseline justify-between gap-2 text-xs">
        <span className="text-muted">{label}</span>
        <span className="tnum">
          <span className={cn(after > total && "font-medium text-bad")}>{fmt(after)}</span>
          <span className="text-faint">
            {" "}
            / {fmt(total)}
            {unit ? ` ${unit}` : ""}
          </span>
        </span>
      </div>
      <div className="relative h-1.5 overflow-hidden rounded-full bg-sunken">
        <div className={cn("absolute inset-y-0 left-0", tone)} style={{ width: `${pct(used)}%` }} />
        {adding > 0 && (
          <div
            className={cn("absolute inset-y-0 opacity-45", tone)}
            style={{ left: `${pct(used)}%`, width: `${Math.max(0, pct(after) - pct(used))}%` }}
          />
        )}
      </div>
    </div>
  );
}
