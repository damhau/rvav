import { Check } from "lucide-react";
import { cn } from "@/lib/utils";

export type StepState = "done" | "now" | "later";

/** A step's number in a sequence, a tick once done; the step's own text says its state to screen readers. */
export function StepMark({ n, state, className }: { n: number; state: StepState; className?: string }) {
  return (
    <span
      aria-hidden
      className={cn(
        "tnum flex size-6 shrink-0 items-center justify-center rounded-full text-caption font-semibold",
        state === "done" && "bg-ok-soft text-ok",
        state === "now" && "bg-accent text-accent-fg",
        state === "later" && "bg-sunken text-muted",
        className,
      )}
    >
      {state === "done" ? <Check className="size-3.5" /> : n}
    </span>
  );
}

/** The classes that mark the step to do now, in a list of steps. */
export const stepNow = "shadow-[inset_3px_0_0_var(--color-accent)]";
