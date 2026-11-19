// What soft policies warn about (ADR-0045): the requester sees each warning
// and may go on; the request keeps the warnings they went on despite.
import { TriangleAlert } from "lucide-react";
import { isApiError } from "@/lib/api/client";
import type { components } from "@/lib/api/schema";
import { cn } from "@/lib/utils";

export type PolicyWarning = components["schemas"]["PolicyWarning"];

/** The warnings, and what going on means. */
export function PolicyWarnings({
  warnings,
  note = "You may go on: the request keeps the warning.",
  className,
}: {
  warnings: PolicyWarning[];
  note?: string;
  className?: string;
}) {
  if (warnings.length === 0) return null;
  return (
    <div
      role="note"
      aria-label={warnings.length === 1 ? "Policy warning" : "Policy warnings"}
      className={cn("flex items-start gap-2 rounded-md bg-warn-soft px-3 py-2 text-secondary text-warn", className)}
    >
      <TriangleAlert className="mt-0.5 size-3.5 shrink-0" aria-hidden />
      <div className="flex min-w-0 flex-col gap-0.5">
        <ul className="flex flex-col gap-0.5">
          {warnings.map((w, i) => (
            <li key={i}>
              {w.message}
              {w.policy && <span className="text-caption"> ({w.policy})</span>}
            </li>
          ))}
        </ul>
        {note && <p>{note}</p>}
      </div>
    </div>
  );
}

/** The warnings a refusal asks to accept: a 422 at /accept_warnings with the code warning. */
export function refusedWarnings(e: unknown): PolicyWarning[] {
  if (!isApiError(e) || e.status !== 422) return [];
  return e.fieldErrors
    .filter((f) => f.field === "/accept_warnings" && f.code === "warning")
    .map((f) => ({ policy: "", message: f.message }));
}

/** The warnings shown, those of a refusal after: once each. */
export function allWarnings(...lists: (PolicyWarning[] | undefined)[]): PolicyWarning[] {
  const seen = new Set<string>();
  return lists
    .flatMap((l) => l ?? [])
    .filter((w) => {
      if (seen.has(w.message)) return false;
      seen.add(w.message);
      return true;
    });
}
