import { CircleCheck, CircleDashed, CircleMinus, CircleSlash, CircleX, LoaderCircle } from "lucide-react";
import type { Step, StepStatus } from "@/lib/api/types";
import { clock, cn, duration } from "@/lib/utils";

const systemLabel: Record<NonNullable<Step["system"]>, string> = {
  rvav: "",
  policy: "Policy",
  vcenter: "vCenter",
  efficientip: "EfficientIP",
  awx: "AWX",
};

const systemTone: Record<NonNullable<Step["system"]>, string> = {
  rvav: "text-faint",
  policy: "text-accent",
  vcenter: "text-ok",
  efficientip: "text-warn",
  awx: "text-bad",
};

const statusText: Record<StepStatus, string> = {
  done: "Done",
  running: "Running",
  failed: "Failed",
  skipped: "Skipped",
  cancelled: "Cancelled",
  pending: "Not started",
};

function StepIcon({ status, small }: { status: StepStatus; small?: boolean }) {
  const c = small ? "size-3.5" : "size-4";
  const label = statusText[status];
  switch (status) {
    case "done":
      return <CircleCheck className={cn(c, "text-ok")} aria-label={label} />;
    case "running":
      return <LoaderCircle className={cn(c, "animate-spin text-run")} aria-label={label} />;
    case "failed":
      return <CircleX className={cn(c, "text-bad")} aria-label={label} />;
    case "skipped":
      return <CircleMinus className={cn(c, "text-faint")} aria-label={label} />;
    case "cancelled":
      return <CircleSlash className={cn(c, "text-faint")} aria-label={label} />;
    default:
      return <CircleDashed className={cn(c, "text-line-strong")} aria-label={label} />;
  }
}

export function Timeline({
  steps,
  onOpenLog,
  selectedLogStep,
}: {
  steps: Step[];
  onOpenLog?: (step: Step) => void;
  selectedLogStep?: string | null;
}) {
  return (
    <ol className="flex flex-col" aria-live="polite">
      {steps.map((s, i) => {
        const last = i === steps.length - 1;
        const system = s.system ? systemLabel[s.system] : "";
        return (
          <li key={s.id} className="relative grid grid-cols-[16px_minmax(0,1fr)] gap-x-3">
            {!last && (
              <span
                className={cn(
                  "absolute top-5 left-[7.5px] h-[calc(100%-12px)] w-px",
                  s.status === "done" ? "bg-ok/40" : "bg-line",
                )}
                aria-hidden
              />
            )}
            <span className="pt-0.5">
              <StepIcon status={s.status} />
            </span>
            <div className={cn("flex min-w-0 flex-col pb-4", s.status === "pending" && "opacity-55")}>
              <div className="flex flex-wrap items-baseline gap-x-2">
                <span
                  className={cn(
                    "text-[13px]",
                    s.status === "running" && "font-semibold",
                    s.status === "failed" && "font-semibold text-bad",
                  )}
                >
                  {s.title}
                </span>
                {system && s.system && (
                  <span className={cn("font-mono text-[10px] tracking-wide uppercase", systemTone[s.system])}>
                    {system}
                  </span>
                )}
                <span className="tnum ml-auto flex gap-3 font-mono text-[11px] text-faint">
                  {s.started_at && s.status !== "pending" && <span>{clock(s.started_at)}</span>}
                  {s.started_at && (s.status === "running" || s.finished_at) && (
                    <span className="w-14 text-right">
                      {s.status === "running" ? "running" : duration(s.started_at, s.finished_at)}
                    </span>
                  )}
                </span>
              </div>
              {s.detail && <span className="text-xs text-muted">{s.detail}</span>}
              {s.error && <span className="text-xs text-bad">{s.error}</span>}
              {s.children.length > 0 && s.status !== "pending" && (
                <div className="mt-2 rounded-md border border-line bg-sunken/60 px-3 py-2">
                  <ul className="flex flex-col gap-1">
                    {s.children.map((c) => (
                      <li key={c.id} className="flex min-w-0 items-start gap-2">
                        <span className="pt-0.5">
                          <StepIcon status={c.status} small />
                        </span>
                        <div className="min-w-0">
                          <span
                            className={cn(
                              "font-mono text-[12px]",
                              c.status === "pending" && "text-faint",
                              c.status === "running" && "font-medium",
                              c.status === "failed" && "text-bad",
                            )}
                          >
                            {c.title}
                          </span>
                          {c.error && <p className="font-mono text-[11px] text-bad">{c.error}</p>}
                        </div>
                      </li>
                    ))}
                  </ul>
                  {onOpenLog && s.has_log && (
                    <button
                      type="button"
                      onClick={() => onOpenLog(s)}
                      aria-pressed={selectedLogStep === s.id}
                      className="mt-2 text-xs font-medium text-accent hover:underline"
                    >
                      {selectedLogStep === s.id ? "Showing job output" : "Show job output"}
                    </button>
                  )}
                </div>
              )}
            </div>
          </li>
        );
      })}
    </ol>
  );
}
