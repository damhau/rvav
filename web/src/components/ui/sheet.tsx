import { X } from "lucide-react";
import { Dialog as D } from "radix-ui";
import { Button, IconButton } from "@/components/ui/button";
import { useOpener } from "@/components/ui/opener";
import { cn } from "@/lib/utils";

interface PanelProps {
  title: string;
  description?: React.ReactNode;
  children: React.ReactNode;
  /** Makes the sheet a form: its footer has Cancel and this submit button. */
  onSubmit?: () => void;
  submitLabel?: string;
  /** Disables the submit button: a required value is missing, or the save is running. */
  submitDisabled?: boolean;
  /** A footer of its own, for a sheet that isn't a form. */
  footer?: React.ReactNode;
  className?: string;
}

/**
 * A side panel for creating and editing: the page stays in view behind it.
 * Focus stays inside while it is open, Escape closes it, and the focus goes
 * back to what opened it.
 */
export function Sheet({
  open,
  onOpenChange,
  ...panel
}: PanelProps & { open: boolean; onOpenChange: (open: boolean) => void }) {
  return (
    <D.Root open={open} onOpenChange={onOpenChange}>
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
  submitLabel = "Save",
  submitDisabled,
  footer,
  className,
}: PanelProps & { ref?: React.Ref<HTMLDivElement> }) {
  const giveFocusBack = useOpener();
  const body = <div className="flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto px-5 py-4">{children}</div>;
  const bar = "flex shrink-0 flex-wrap items-center justify-end gap-2 border-t border-line px-5 py-3";
  return (
    <D.Content
      ref={ref}
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
          <IconButton label="Close" className="-mt-1 -mr-2">
            <X />
          </IconButton>
        </D.Close>
      </div>
      {onSubmit ? (
        <form
          className="flex min-h-0 flex-1 flex-col"
          onSubmit={(e) => {
            e.preventDefault();
            onSubmit();
          }}
        >
          {body}
          <div className={bar}>
            <D.Close asChild>
              <Button type="button" variant="secondary">
                Cancel
              </Button>
            </D.Close>
            <Button type="submit" variant="primary" disabled={submitDisabled}>
              {submitLabel}
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
