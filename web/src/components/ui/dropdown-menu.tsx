import { DropdownMenu as D } from "radix-ui";
import * as React from "react";
import { cn } from "@/lib/utils";

export const Menu = D.Root;
export const MenuTrigger = D.Trigger;

export function MenuContent({ className, ...props }: React.ComponentProps<typeof D.Content>) {
  return (
    <D.Portal>
      <D.Content
        sideOffset={4}
        align="end"
        className={cn("z-50 min-w-44 rounded-md border border-line bg-surface p-1 shadow-pop", className)}
        {...props}
      />
    </D.Portal>
  );
}

export function MenuItem({
  className,
  danger,
  shortcut,
  children,
  ...props
}: React.ComponentProps<typeof D.Item> & { danger?: boolean; shortcut?: string }) {
  return (
    <D.Item
      className={cn(
        "flex cursor-default select-none items-center gap-2 rounded-sm px-2 py-1.5 text-[13px] outline-none data-[disabled]:opacity-40 data-[highlighted]:bg-sunken [&_svg]:size-3.5 [&_svg]:text-faint",
        danger && "text-bad [&_svg]:text-bad",
        className,
      )}
      {...props}
    >
      {children}
      {shortcut && <span className="ml-auto pl-4 font-mono text-[11px] text-faint">{shortcut}</span>}
    </D.Item>
  );
}

export function MenuLabel({ className, ...props }: React.ComponentProps<typeof D.Label>) {
  return <D.Label className={cn("label px-2 pb-1 pt-2", className)} {...props} />;
}

export function MenuSeparator() {
  return <D.Separator className="my-1 h-px bg-line" />;
}
