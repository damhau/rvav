import { CircleCheck, CircleDashed, CircleMinus, CircleSlash, CircleX, Hourglass, LoaderCircle } from "lucide-react";
import { useTranslation } from "react-i18next";
import { i18n } from "@/lib/i18n";
import type { Step, StepStatus } from "@/lib/api/types";
import { cn } from "@/lib/utils";
import { clock, duration, fullDate } from "@/lib/format";
import { stepTitle } from "@/lib/i18n/names";

// The systems' own names read the same in every language.
const systemLabel: Record<NonNullable<Step["system"]>, () => string> = {
  rvav: () => "",
  policy: () => i18n.t("common:step.system.policy"),
  vcenter: () => "vCenter",
  efficientip: () => "EfficientIP",
  awx: () => "AWX",
  nsx: () => "NSX",
  ad: () => "Active Directory",
  rest: () => "REST",
  person: () => i18n.t("common:step.system.person"),
  wait: () => i18n.t("common:step.system.wait"),
  branch: () => i18n.t("common:step.system.branch"),
};

const systemTone: Record<NonNullable<Step["system"]>, string> = {
  rvav: "text-faint",
  policy: "text-accent",
  vcenter: "text-ok",
  efficientip: "text-warn",
  awx: "text-bad",
  nsx: "text-run",
  ad: "text-run",
  rest: "text-accent",
  person: "text-accent",
  wait: "text-accent",
  branch: "text-accent",
};

function StepIcon({ status, small, waits }: { status: StepStatus; small?: boolean; waits?: boolean }) {
  const { t } = useTranslation();
  const c = small ? "size-3.5" : "size-4";
  const label = t(`step.status.${status}`);
  if (waits && status === "running")
    return <Hourglass className={cn(c, "text-run")} aria-label={t("step.waitingIcon")} />;
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
  onOpenData,
  selectedDataStep,
}: {
  steps: Step[];
  onOpenLog?: (step: Step) => void;
  selectedLogStep?: string | null;
  /** Shows what a step was given and gave (ADR-0057). */
  onOpenData?: (step: Step) => void;
  selectedDataStep?: string | null;
}) {
  const { t } = useTranslation();
  return (
    <ol className="flex flex-col" aria-live="polite">
      {steps.map((s, i) => {
        const last = i === steps.length - 1;
        const system = s.system ? (systemLabel[s.system]?.() ?? s.system) : "";
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
              <StepIcon status={s.status} waits={s.system === "wait"} />
            </span>
            {/* Not started reads quieter, in colours that keep their contrast (no opacity). */}
            <div className="flex min-w-0 flex-col pb-4">
              <div className="flex flex-wrap items-baseline gap-x-2">
                <span
                  className={cn(
                    "text-body",
                    s.status === "pending" && "text-muted",
                    s.status === "running" && "font-semibold",
                    s.status === "failed" && "font-semibold text-bad",
                  )}
                >
                  {stepTitle(s)}
                </span>
                {system && s.system && (
                  <span
                    className={cn(
                      "font-mono text-badge tracking-wide uppercase",
                      s.status === "pending" ? "text-faint" : systemTone[s.system],
                    )}
                  >
                    {system}
                  </span>
                )}
                <span className="tnum ml-auto flex gap-3 text-caption text-faint">
                  {s.started_at && s.status !== "pending" && <span>{clock(s.started_at)}</span>}
                  {s.started_at && (s.status === "running" || s.finished_at) && (
                    <span className="w-14 text-right">
                      {s.status === "running"
                        ? t(s.system === "wait" ? "step.waiting" : "step.running")
                        : duration(s.started_at, s.finished_at)}
                    </span>
                  )}
                </span>
              </div>
              {s.detail && (
                <span lang="en" className="text-secondary text-muted">
                  {s.detail}
                </span>
              )}
              {/* A wait for a time, or a poll's deadline: in the viewer's own time. */}
              {s.due_at && (s.status === "running" || s.status === "done") && (
                <span className="text-secondary text-muted">{t("step.until", { date: fullDate(s.due_at) })}</span>
              )}
              {s.error && (
                <span lang="en" className="text-secondary text-bad">
                  {s.error}
                </span>
              )}
              {onOpenData && s.has_data && (
                <button
                  type="button"
                  onClick={() => onOpenData(s)}
                  aria-pressed={selectedDataStep === s.id}
                  className="self-start text-secondary font-medium text-accent hover:underline"
                >
                  {t("step.showData")}
                  <span className="sr-only">{t("step.ofStep", { step: stepTitle(s) })}</span>
                </button>
              )}
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
                              "font-mono text-secondary",
                              c.status === "pending" && "text-faint",
                              c.status === "running" && "font-medium",
                              c.status === "failed" && "text-bad",
                            )}
                          >
                            {c.title}
                          </span>
                          {c.error && <p className="font-mono text-caption text-bad">{c.error}</p>}
                        </div>
                      </li>
                    ))}
                  </ul>
                  {onOpenLog && s.has_log && (
                    <button
                      type="button"
                      onClick={() => onOpenLog(s)}
                      aria-pressed={selectedLogStep === s.id}
                      className="mt-2 text-secondary font-medium text-accent hover:underline"
                    >
                      {t("step.showOutput")}
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
