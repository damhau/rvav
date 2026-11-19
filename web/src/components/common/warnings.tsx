// What soft policies warn about (ADR-0045): the requester sees each warning
// and may go on; the request keeps the warnings they went on despite.
import { TriangleAlert } from "lucide-react";
import { useEffect, useId, useRef } from "react";
import { isApiError } from "@/lib/api/client";
import type { components } from "@/lib/api/schema";
import { cn } from "@/lib/utils";

export type PolicyWarning = components["schemas"]["PolicyWarning"];

/**
 * The warnings, and what going on means. When `refusal` changes, as on a
 * refusal that only asks to accept them, the note takes the focus.
 */
export function PolicyWarnings({
  warnings,
  note = "You may go on: the request keeps the warning.",
  className,
  refusal,
}: {
  warnings: PolicyWarning[];
  note?: string;
  className?: string;
  refusal?: number;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const listId = useId();
  useEffect(() => {
    if (refusal) ref.current?.focus();
  }, [refusal]);
  if (warnings.length === 0) return null;
  return (
    <div
      ref={ref}
      role="note"
      tabIndex={-1}
      aria-live="polite"
      aria-label={warnings.length === 1 ? "Policy warning" : "Policy warnings"}
      aria-describedby={listId}
      className={cn(
        "flex items-start gap-2 rounded-md bg-warn-soft px-3 py-2 text-secondary text-warn outline-none focus-visible:ring-2 focus-visible:ring-accent",
        className,
      )}
    >
      <TriangleAlert className="mt-0.5 size-3.5 shrink-0" aria-hidden />
      <div className="flex min-w-0 flex-col gap-0.5">
        <ul id={listId} className="flex flex-col gap-0.5">
          {warnings.map((w) => (
            <li key={w.id || w.message}>
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
    .map((f) => f.warning ?? { id: "", policy: "", message: f.message });
}

/** Whether a refusal only asks to accept warnings: nothing to fix in the inputs. */
export function onlyWarned(e: unknown): boolean {
  return (
    isApiError(e) &&
    e.status === 422 &&
    e.fieldErrors.length > 0 &&
    e.fieldErrors.every((f) => f.field === "/accept_warnings" && f.code === "warning")
  );
}

/** The warnings shown, those of a refusal after: once each. */
export function allWarnings(...lists: (PolicyWarning[] | undefined)[]): PolicyWarning[] {
  const seen = new Set<string>();
  return lists
    .flatMap((l) => l ?? [])
    .filter((w) => {
      const key = w.id || w.message;
      if (seen.has(key)) return false;
      seen.add(key);
      return true;
    });
}

/** What a request sends to go on despite the warnings shown: their IDs, or none to be told of any. */
export function acceptedWarnings(warnings: PolicyWarning[]): string[] {
  return warnings.map((w) => w.id).filter(Boolean);
}
