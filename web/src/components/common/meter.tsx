import { cn } from "@/lib/utils";

/** Quota/usage bar. `adding` shows what a pending request would add. */
export function Meter({
  label,
  used,
  total,
  unit,
  adding = 0,
}: {
  label: string;
  used: number;
  total: number;
  unit?: string;
  adding?: number;
}) {
  const pct = (v: number) => Math.min(100, (v / total) * 100);
  const after = used + adding;
  const tone = after > total ? "bg-bad" : after / total > 0.85 ? "bg-warn" : "bg-accent";
  const fmt = (v: number) => (Number.isInteger(v) ? v : v.toFixed(1));
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
