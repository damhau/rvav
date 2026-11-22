import { Tooltip as T } from "radix-ui";
import { useLayoutEffect, useState } from "react";
import { cn } from "@/lib/utils";

function useCut(el: HTMLElement | null, text: string) {
  const [cut, setCut] = useState(false);
  useLayoutEffect(() => {
    if (!el) return;
    const measure = () => setCut(el.scrollWidth > el.clientWidth);
    measure();
    const resized = new ResizeObserver(measure);
    resized.observe(el);
    return () => resized.disconnect();
  }, [el, text]);
  return cut;
}

/**
 * A long name on one line, such as an FQDN or a path, cut with an ellipsis
 * where it doesn't fit; the whole name shows in a tooltip on hover and on
 * keyboard focus. Only a name that is cut takes the focus. In a table cell,
 * give it a maximum width (`max-w-56`), or the column grows to fit it.
 */
export function Truncate({ children, mono, className }: { children: string; mono?: boolean; className?: string }) {
  const [el, setEl] = useState<HTMLSpanElement | null>(null);
  const cut = useCut(el, children);
  const [open, setOpen] = useState(false);
  return (
    <T.Root open={cut && open} onOpenChange={setOpen}>
      <T.Trigger asChild>
        <span
          ref={setEl}
          // eslint-disable-next-line jsx-a11y/no-noninteractive-tabindex -- the keyboard reaches the tooltip that shows the rest of the name (WCAG 1.4.13)
          tabIndex={cut ? 0 : undefined}
          className={cn("block min-w-0 truncate", mono && "font-mono", className)}
        >
          {children}
        </span>
      </T.Trigger>
      <T.Portal>
        <T.Content
          sideOffset={4}
          collisionPadding={8}
          className={cn(
            "z-50 max-w-[min(40rem,calc(100vw-16px))] rounded-sm bg-fg px-2 py-1 text-caption [overflow-wrap:anywhere] text-bg",
            mono && "font-mono",
          )}
        >
          {children}
        </T.Content>
      </T.Portal>
    </T.Root>
  );
}
