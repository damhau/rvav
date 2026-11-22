import { AlertDialog as A } from "radix-ui";
import { useTranslation } from "react-i18next";
import { Button } from "@/components/ui/button";
import { useOpener } from "@/components/ui/opener";

interface ConfirmProps {
  /** The question, naming the thing: "Remove RHEL 9.4?" */
  title: string;
  /** What will be removed or changed. */
  description: React.ReactNode;
  /** What depends on it and what happens to that. */
  consequences?: React.ReactNode;
  /** Says the action, as on the button that asked: "Remove template". */
  confirmLabel: string;
  /** The way out, when "Cancel" would read as the action: "Keep it running" next to "Cancel request". */
  cancelLabel?: string;
  onConfirm: () => void;
  /** While the change runs: the buttons wait, and the dialog stays (Escape too) until the page closes it. */
  pending?: boolean;
  /** "danger" for what can't be undone. */
  tone?: "danger" | "primary";
  /** Where the focus goes on closing, when not back to what opened it: its action removes it. */
  returnFocus?: () => HTMLElement | null | undefined;
}

/** Asks before a change that removes or breaks something, saying what depends on it. */
export function ConfirmDialog({
  open,
  onOpenChange,
  ...props
}: ConfirmProps & { open: boolean; onOpenChange: (open: boolean) => void }) {
  return (
    <A.Root open={open} onOpenChange={(o) => (o || !props.pending) && onOpenChange(o)}>
      <A.Portal>
        <A.Overlay className="fixed inset-0 z-40 bg-black/40" />
        <ConfirmContent {...props} />
      </A.Portal>
    </A.Root>
  );
}

function ConfirmContent({
  ref,
  title,
  description,
  consequences,
  confirmLabel,
  cancelLabel,
  onConfirm,
  pending,
  tone = "danger",
  returnFocus,
}: ConfirmProps & { ref?: React.Ref<HTMLDivElement> }) {
  const { t } = useTranslation();
  const giveFocusBack = useOpener(returnFocus);
  return (
    <A.Content
      ref={ref}
      onCloseAutoFocus={giveFocusBack}
      className="fixed top-[16vh] left-1/2 z-50 flex w-[min(460px,calc(100vw-32px))] -translate-x-1/2 flex-col gap-4 rounded-lg border border-line bg-surface p-5 shadow-pop"
    >
      <div className="flex flex-col gap-1.5">
        <A.Title className="text-section font-semibold">{title}</A.Title>
        <A.Description className="text-body text-muted">{description}</A.Description>
      </div>
      {consequences && (
        <div className="rounded-md border border-line bg-sunken/60 px-3 py-2.5 text-secondary">{consequences}</div>
      )}
      <div className="flex flex-wrap justify-end gap-2">
        <A.Cancel asChild>
          <Button variant="secondary" disabled={pending}>
            {cancelLabel ?? t("actions.cancel")}
          </Button>
        </A.Cancel>
        <Button variant={tone === "danger" ? "danger-solid" : "primary"} disabled={pending} onClick={onConfirm}>
          {confirmLabel}
        </Button>
      </div>
    </A.Content>
  );
}
