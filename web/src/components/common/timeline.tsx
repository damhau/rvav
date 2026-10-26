import { CircleCheck, CircleDashed, CircleMinus, CircleX, LoaderCircle } from "lucide-react";
import type { StepStatus, TimelineStep } from "@/lib/types";
import { clock, cn, duration } from "@/lib/utils";

function StepIcon({ status, small }: { status: StepStatus; small?: boolean }) {
  const c = small ? "size-3.5" : "size-4";
  switch (status) {
    case "done":
      return <CircleCheck className={cn(c, "text-ok")} />;
    case "running":
      return <LoaderCircle className={cn(c, "animate-spin text-run")} />;
    case "failed":
      return <CircleX className={cn(c, "text-bad")} />;
    case "skipped":
      return <CircleMinus className={cn(c, "text-faint")} />;
    default:
      return <CircleDashed className={cn(c, "text-line-strong")} />;
  }
}

const systemTone: Record<string, string> = {
  vCenter: "text-[#3b7a3a] dark:text-[#7fc27e]",
  EfficientIP: "text-warn",
  AWX: "text-bad",
  Policy: "text-accent",
};

export function Timeline({ steps, onOpenLog }: { steps: TimelineStep[]; onOpenLog?: () => void }) {
  return (
    <ol className="flex flex-col">
      {steps.map((s, i) => {
        const last = i === steps.length - 1;
        return (
          <li key={s.key} className="relative grid grid-cols-[16px_1fr] gap-x-3">
            {!last && (
              <span
                className={cn(
                  "absolute left-[7.5px] top-5 h-[calc(100%-12px)] w-px",
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
                <span className={cn("text-[13px]", s.status === "running" ? "font-medium" : "")}>{s.title}</span>
                {s.system && (
                  <span className={cn("font-mono text-[10px] uppercase tracking-wide", systemTone[s.system] ?? "text-faint")}>
                    {s.system}
                  </span>
                )}
                <span className="ml-auto flex gap-3 font-mono text-[11px] text-faint tnum">
                  {s.started && s.status !== "pending" && <span>{clock(s.started)}</span>}
                  {s.started && (s.status === "running" || s.finished) && (
                    <span className="w-14 text-right">{duration(s.started, s.finished)}</span>
                  )}
                </span>
              </div>
              {s.detail && (
                <span className={cn("text-xs", s.status === "failed" ? "text-bad" : "text-muted")}>{s.detail}</span>
              )}
              {s.children && s.status !== "pending" && (
                <div className="mt-2 rounded-md border border-line bg-sunken/60 px-3 py-2">
                  <ul className="flex flex-col gap-1">
                    {s.children.map((c) => (
                      <li key={c.key} className="flex min-w-0 items-start gap-2">
                        <span className="pt-0.5">
                          <StepIcon status={c.status} small />
                        </span>
                        <div className="min-w-0">
                          <span className={cn("font-mono text-[12px]", c.status === "pending" && "text-faint")}>
                            {c.title}
                          </span>
                          {c.detail && <p className="font-mono text-[11px] text-bad">{c.detail}</p>}
                        </div>
                      </li>
                    ))}
                  </ul>
                  {onOpenLog && (
                    <button type="button" onClick={onOpenLog} className="mt-2 text-xs font-medium text-accent hover:underline">
                      View job output
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
