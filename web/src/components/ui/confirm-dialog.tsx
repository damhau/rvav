import { AlertDialog as A } from "radix-ui";
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
  onConfirm: () => void;
  /** While the change runs: the buttons wait, and the dialog stays until the page closes it. */
  pending?: boolean;
  /** "danger" for what can't be undone. */
  tone?: "danger" | "primary";
}

/** Asks before a change that removes or breaks something, saying what depends on it. */
export function ConfirmDialog({
  open,
  onOpenChange,
  ...props
}: ConfirmProps & { open: boolean; onOpenChange: (open: boolean) => void }) {
  return (
    <A.Root open={open} onOpenChange={onOpenChange}>
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
  onConfirm,
  pending,
  tone = "danger",
}: ConfirmProps & { ref?: React.Ref<HTMLDivElement> }) {
  const giveFocusBack = useOpener();
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
            Cancel
          </Button>
        </A.Cancel>
        <Button variant={tone === "danger" ? "danger-solid" : "primary"} disabled={pending} onClick={onConfirm}>
          {confirmLabel}
        </Button>
      </div>
    </A.Content>
  );
}
