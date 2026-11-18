import { TriangleAlert } from "lucide-react";
import type { Ref } from "react";

/** A problem with an input the page has no control for. */
export interface Unplaced {
  key: string;
  /** The input's name, or the request option's label. */
  label: string;
  /** Set for an input's name, shown as code. */
  code?: boolean;
  message: string;
}

/**
 * What a refused submit leaves to fix, at the top of the form: how many
 * inputs need attention, and the problems of those it shows no field for.
 * The other problems are under their fields.
 */
export function Attention({
  count,
  unplaced,
  ref,
}: {
  count: number;
  unplaced: Unplaced[];
  ref?: Ref<HTMLDivElement>;
}) {
  return (
    <div
      ref={ref}
      role="alert"
      tabIndex={-1}
      className="flex items-start gap-2 rounded-lg border border-bad/40 bg-bad-soft px-4 py-3 text-bad outline-none focus-visible:ring-2 focus-visible:ring-bad/40"
    >
      <TriangleAlert className="mt-0.5 size-4 shrink-0" aria-hidden />
      <div className="flex min-w-0 flex-col gap-1">
        <p className="font-medium">{count === 1 ? "1 input needs attention." : `${count} inputs need attention.`}</p>
        {unplaced.length > 0 && (
          <ul className="flex flex-col gap-0.5 text-secondary">
            {unplaced.map((u) => (
              <li key={u.key} className="break-words">
                {u.code ? <span className="font-mono">{u.label}</span> : u.label}, not on this form: {u.message}
              </li>
            ))}
          </ul>
        )}
      </div>
    </div>
  );
}

const focusable = [
  "input:not([type=hidden]):not([disabled])",
  "textarea:not([disabled])",
  "select:not([disabled])",
  "button:not([disabled])",
  '[tabindex]:not([tabindex="-1"])',
].join(", ");

/** The control a person fixes a form field with: the first one in the field that takes the focus. */
export function fieldControl(name: string): HTMLElement | null {
  return document.querySelector(`[data-field="${name}"]`)?.querySelector<HTMLElement>(focusable) ?? null;
}
