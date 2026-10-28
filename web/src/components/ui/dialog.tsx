import { X } from "lucide-react";
import { Dialog as D } from "radix-ui";
import * as React from "react";
import { cn } from "@/lib/utils";

export const Dialog = D.Root;
export const DialogTrigger = D.Trigger;
export const DialogClose = D.Close;

export function DialogContent({
  className,
  title,
  description,
  children,
  onOpenAutoFocus,
  onCloseAutoFocus,
  ...props
}: React.ComponentProps<typeof D.Content> & { title: string; description?: React.ReactNode }) {
  // Radix gives focus back to a DialogTrigger only; dialogs opened from state
  // give it back to whatever had focus when they opened.
  const opener = React.useRef<HTMLElement | null>(null);
  return (
    <D.Portal>
      <D.Overlay className="fixed inset-0 z-40 bg-black/40" />
      <D.Content
        className={cn(
          "fixed top-[12vh] left-1/2 z-50 w-[min(480px,calc(100vw-32px))] -translate-x-1/2 rounded-lg border border-line bg-surface shadow-pop",
          className,
        )}
        onOpenAutoFocus={(e) => {
          opener.current = document.activeElement as HTMLElement | null;
          onOpenAutoFocus?.(e);
        }}
        onCloseAutoFocus={(e) => {
          onCloseAutoFocus?.(e);
          const el = opener.current;
          if (!e.defaultPrevented && el?.isConnected && el !== document.body) {
            e.preventDefault();
            el.focus();
          }
        }}
        {...props}
      >
        <div className="flex items-start justify-between gap-4 border-b border-line px-4 py-3">
          <div>
            <D.Title className="text-sm font-semibold">{title}</D.Title>
            {description ? (
              <D.Description className="mt-0.5 text-xs text-muted">{description}</D.Description>
            ) : (
              <D.Description className="sr-only">{title}</D.Description>
            )}
          </div>
          <D.Close className="rounded-sm p-1 text-faint hover:bg-sunken hover:text-fg" aria-label="Close">
            <X className="size-4" />
          </D.Close>
        </div>
        {children}
      </D.Content>
    </D.Portal>
  );
}

export function SheetContent({
  className,
  title,
  description,
  side = "right",
  children,
}: {
  className?: string;
  title: string;
  description?: React.ReactNode;
  side?: "right" | "left";
  children: React.ReactNode;
}) {
  return (
    <D.Portal>
      <D.Overlay className="fixed inset-0 z-40 bg-black/30" />
      <D.Content
        className={cn(
          "fixed inset-y-0 z-50 flex w-[min(720px,100vw)] flex-col border-line bg-surface shadow-pop",
          side === "right" ? "right-0 border-l" : "left-0 w-[min(280px,85vw)] border-r",
          className,
        )}
        style={{ paddingTop: "env(safe-area-inset-top, 0px)", paddingBottom: "env(safe-area-inset-bottom, 0px)" }}
      >
        <div className="flex items-center justify-between gap-4 border-b border-line px-4 py-3">
          <div className="min-w-0">
            <D.Title className="truncate text-sm font-semibold">{title}</D.Title>
            {description ? (
              <D.Description className="text-xs text-muted">{description}</D.Description>
            ) : (
              <D.Description className="sr-only">{title}</D.Description>
            )}
          </div>
          <D.Close className="rounded-sm p-1 text-faint hover:bg-sunken hover:text-fg" aria-label="Close">
            <X className="size-4" />
          </D.Close>
        </div>
        <div className="min-h-0 flex-1 overflow-auto">{children}</div>
      </D.Content>
    </D.Portal>
  );
}
