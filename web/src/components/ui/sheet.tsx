import { X } from "lucide-react";
import { Dialog as D } from "radix-ui";
import { useEffect, useRef } from "react";
import { useTranslation } from "react-i18next";
import { Button, IconButton } from "@/components/ui/button";
import { focusInvalid, useOpener } from "@/components/ui/opener";
import { cn } from "@/lib/utils";

const firstField = "input:not([disabled]), select:not([disabled]), textarea:not([disabled]), button:not([disabled])";

interface PanelProps {
  title: string;
  description?: React.ReactNode;
  children: React.ReactNode;
  /** Makes the sheet a form: its footer has Cancel and this submit button. */
  onSubmit?: () => void;
  submitLabel?: string;
  /** Disables the submit button: a required value is missing. */
  submitDisabled?: boolean;
  /** While its change runs: the sheet stays open, and Cancel, Close and the submit button wait. */
  pending?: boolean;
  /** A footer of its own, for a sheet that isn't a form. */
  footer?: React.ReactNode;
  /** Where the focus goes on closing, when not back to what opened the sheet: its action removed it. */
  returnFocus?: () => HTMLElement | null | undefined;
  className?: string;
}

/**
 * A side panel for creating and editing: the page stays in view behind it.
 * It opens on its first field, keeps the focus inside, closes with Escape, and
 * gives the focus back to what opened it. While `pending`, it can't be closed;
 * a save refused once it ends moves the focus to the first invalid field.
 */
export function Sheet({
  open,
  onOpenChange,
  ...panel
}: PanelProps & { open: boolean; onOpenChange: (open: boolean) => void }) {
  return (
    <D.Root open={open} onOpenChange={(o) => (o || !panel.pending) && onOpenChange(o)}>
      <D.Portal>
        <D.Overlay className="fixed inset-0 z-40 bg-black/30" />
        <SheetPanel {...panel} />
      </D.Portal>
    </D.Root>
  );
}

function SheetPanel({
  ref,
  title,
  description,
  children,
  onSubmit,
  submitLabel,
  submitDisabled,
  pending,
  footer,
  returnFocus,
  className,
}: PanelProps & { ref?: React.Ref<HTMLDivElement> }) {
  const { t } = useTranslation();
  const giveFocusBack = useOpener(returnFocus);
  const bodyRef = useRef<HTMLDivElement>(null);
  const submitRef = useRef<HTMLButtonElement>(null);
  // From a submit to the render that shows how it went. A quick answer can come with no render
  // while pending, so a field marked invalid ends the wait too; the person's next key or click
  // ends it as well, so a field that turns invalid as they type keeps their focus.
  const awaiting = useRef<"submitted" | "saving" | null>(null);
  useEffect(() => {
    if (!awaiting.current) return;
    if (pending) {
      awaiting.current = "saving";
      return;
    }
    if (focusInvalid(bodyRef.current)) awaiting.current = null;
    else if (awaiting.current === "saving") {
      awaiting.current = null;
      // The submit button, disabled while saving, lost the focus.
      const active = document.activeElement;
      if (!active || active === document.body || active === bodyRef.current?.closest('[role="dialog"]'))
        submitRef.current?.focus();
    }
  });
  const interacted = () => {
    awaiting.current = null;
  };
  const body = (
    <div ref={bodyRef} className="flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto px-5 py-4">
      {children}
    </div>
  );
  const bar = "flex shrink-0 flex-wrap items-center justify-end gap-2 border-t border-line px-5 py-3";
  return (
    <D.Content
      ref={ref}
      onOpenAutoFocus={(e) => {
        const first = bodyRef.current?.querySelector<HTMLElement>(firstField);
        if (first) {
          e.preventDefault();
          first.focus();
        }
      }}
      onCloseAutoFocus={giveFocusBack}
      className={cn(
        "fixed inset-y-0 right-0 z-50 flex w-[min(560px,100vw)] flex-col border-l border-line bg-surface shadow-pop",
        className,
      )}
      style={{ paddingTop: "env(safe-area-inset-top, 0px)", paddingBottom: "env(safe-area-inset-bottom, 0px)" }}
    >
      <div className="flex shrink-0 items-start justify-between gap-4 border-b border-line px-5 py-4">
        <div className="flex min-w-0 flex-col gap-0.5">
          <D.Title className="text-section font-semibold">{title}</D.Title>
          {description ? (
            <D.Description className="text-secondary text-muted">{description}</D.Description>
          ) : (
            <D.Description className="sr-only">{title}</D.Description>
          )}
        </div>
        <D.Close asChild>
          <IconButton label={t("actions.close")} className="-mt-1 -mr-2" disabled={pending}>
            <X />
          </IconButton>
        </D.Close>
      </div>
      {onSubmit ? (
        <form
          className="flex min-h-0 flex-1 flex-col"
          onKeyDownCapture={interacted}
          onPointerDownCapture={interacted}
          onSubmit={(e) => {
            e.preventDefault();
            if (pending) return;
            awaiting.current = "submitted";
            onSubmit();
          }}
        >
          {body}
          <div className={bar}>
            <D.Close asChild>
              <Button type="button" variant="secondary" disabled={pending}>
                {t("actions.cancel")}
              </Button>
            </D.Close>
            <Button ref={submitRef} type="submit" variant="primary" disabled={submitDisabled || pending}>
              {submitLabel ?? t("actions.save")}
            </Button>
          </div>
        </form>
      ) : (
        <>
          {body}
          {footer && <div className={bar}>{footer}</div>}
        </>
      )}
    </D.Content>
  );
}
