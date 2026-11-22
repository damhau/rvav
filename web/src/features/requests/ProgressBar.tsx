import { useTranslation } from "react-i18next";
import type { ServiceRequest } from "@/lib/api/types";
import { cn } from "@/lib/utils";

/** One segment per top-level step: done, current, still to come. */
export function ProgressSegments({ request, className }: { request: ServiceRequest; className?: string }) {
  const { t } = useTranslation("requests");
  const { done, total } = request.progress;
  const failed = request.status === "failed";
  return (
    <span
      className={cn("flex gap-0.5", className)}
      role="img"
      aria-label={t("progress.step", { n: Math.min(total, done + 1), total })}
    >
      {Array.from({ length: total }, (_, i) => (
        <span
          key={i}
          className={cn(
            "h-[3px] w-3.5 rounded-[1px]",
            i < done ? "bg-ok" : i === done ? (failed ? "bg-bad" : "bg-run") : "bg-line",
          )}
        />
      ))}
    </span>
  );
}
