import { X } from "lucide-react";
import { Dialog as D } from "radix-ui";
import * as React from "react";
import { IconButton } from "@/components/ui/button";
import { useOpener } from "@/components/ui/opener";
import { cn } from "@/lib/utils";

export const Dialog = D.Root;
export const DialogTrigger = D.Trigger;
export const DialogClose = D.Close;

type ContentProps = React.ComponentProps<typeof D.Content> & { title: string; description?: React.ReactNode };

export function DialogContent(props: ContentProps) {
  return (
    <D.Portal>
      <D.Overlay className="fixed inset-0 z-40 bg-black/40" />
      <Content {...props} />
    </D.Portal>
  );
}

// Mounted as the dialog opens, so that it reads what had the focus then.
function Content({ className, title, description, children, onCloseAutoFocus, ...props }: ContentProps) {
  const giveFocusBack = useOpener();
  return (
    <D.Content
      className={cn(
        "fixed top-[12vh] left-1/2 z-50 w-[min(480px,calc(100vw-32px))] -translate-x-1/2 rounded-lg border border-line bg-surface shadow-pop",
        className,
      )}
      onCloseAutoFocus={(e) => {
        onCloseAutoFocus?.(e);
        giveFocusBack(e);
      }}
      {...props}
    >
      <div className="flex items-start justify-between gap-4 border-b border-line px-4 py-3">
        <div className="flex min-w-0 flex-col gap-0.5">
          <D.Title className="text-section font-semibold">{title}</D.Title>
          {description ? (
            <D.Description className="text-secondary text-muted">{description}</D.Description>
          ) : (
            <D.Description className="sr-only">{title}</D.Description>
          )}
        </div>
        <D.Close asChild>
          <IconButton label="Close" className="-mt-0.5 -mr-1.5">
            <X />
          </IconButton>
        </D.Close>
      </div>
      {children}
    </D.Content>
  );
}
