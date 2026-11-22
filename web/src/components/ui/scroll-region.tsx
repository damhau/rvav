import { useLayoutEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { cn } from "@/lib/utils";

interface Sideways {
  scrolls: boolean;
  /** There is more to see before the left edge. */
  start: boolean;
  /** There is more to see past the right edge. */
  end: boolean;
}

const still: Sideways = { scrolls: false, start: false, end: false };

/** Whether an element scrolls sideways, and which of its edges hide more. */
export function useSideways(el: HTMLElement | null): Sideways {
  const [state, setState] = useState(still);
  useLayoutEffect(() => {
    if (!el) return;
    const measure = () => {
      const max = el.scrollWidth - el.clientWidth;
      const next = { scrolls: max > 1, start: el.scrollLeft > 1, end: el.scrollLeft < max - 1 };
      setState((s) => (s.scrolls === next.scrolls && s.start === next.start && s.end === next.end ? s : next));
    };
    measure();
    el.addEventListener("scroll", measure, { passive: true });
    const resized = new ResizeObserver(measure);
    resized.observe(el);
    for (const child of el.children) resized.observe(child);
    return () => {
      el.removeEventListener("scroll", measure);
      resized.disconnect();
    };
  }, [el]);
  return state;
}

/**
 * Something wider than the page, such as a table, scrolling sideways within
 * itself, never the page. The edge with more behind it fades; while it
 * scrolls, it takes the focus so that the arrow keys scroll it, named by
 * `label`.
 */
export function ScrollRegion({
  label,
  children,
  className,
}: {
  label: string;
  children: React.ReactNode;
  className?: string;
}) {
  const { t } = useTranslation();
  const [el, setEl] = useState<HTMLDivElement | null>(null);
  const { scrolls, start, end } = useSideways(el);
  return (
    <div className={cn("relative", className)}>
      <div
        ref={setEl}
        className="overflow-x-auto focus-visible:outline-offset-[-2px]"
        tabIndex={scrolls ? 0 : undefined}
        role={scrolls ? "group" : undefined}
        aria-label={scrolls ? t("table.scrolls", { label }) : undefined}
      >
        {children}
      </div>
      <span
        aria-hidden
        className={cn(
          "pointer-events-none absolute inset-y-0 left-0 w-8 bg-linear-to-r from-surface to-transparent",
          !start && "hidden",
        )}
      />
      <span
        aria-hidden
        className={cn(
          "pointer-events-none absolute inset-y-0 right-0 w-8 bg-linear-to-l from-surface to-transparent",
          !end && "hidden",
        )}
      />
    </div>
  );
}
