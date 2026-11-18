import { ChevronRight } from "lucide-react";
import { DropdownMenu as D } from "radix-ui";
import * as React from "react";
import { cn } from "@/lib/utils";

// Not modal: a modal menu hides the rest of the page with aria-hidden while its
// buttons stay focusable, which axe reports (aria-hidden-focus). A menu button
// needs neither: Escape and Tab close it, and focus goes back to its button.
export function Menu(props: React.ComponentProps<typeof D.Root>) {
  return <D.Root modal={false} {...props} />;
}
export const MenuTrigger = D.Trigger;

export function MenuContent({ className, ...props }: React.ComponentProps<typeof D.Content>) {
  return (
    <D.Portal>
      <D.Content
        sideOffset={4}
        align="end"
        className={cn(
          "z-50 max-h-[var(--radix-dropdown-menu-content-available-height)] min-w-44 overflow-y-auto rounded-md border border-line bg-surface p-1 shadow-pop",
          className,
        )}
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
        "flex cursor-default items-center gap-2 rounded-sm px-2 py-1.5 text-body outline-none select-none data-[disabled]:opacity-40 data-[highlighted]:bg-sunken [&_svg]:size-3.5 [&_svg]:text-faint",
        danger && "text-bad [&_svg]:text-bad",
        className,
      )}
      {...props}
    >
      {children}
      {shortcut && <span className="ml-auto pl-4 font-mono text-caption text-faint">{shortcut}</span>}
    </D.Item>
  );
}

export function MenuLabel({ className, ...props }: React.ComponentProps<typeof D.Label>) {
  return <D.Label className={cn("label px-2 pt-2 pb-1", className)} {...props} />;
}

export function MenuSeparator() {
  return <D.Separator className="my-1 h-px bg-line" />;
}

export const MenuSub = D.Sub;

/** An item that opens a submenu, such as one VM's actions in a deployment's menu. */
export function MenuSubTrigger({ className, children, ...props }: React.ComponentProps<typeof D.SubTrigger>) {
  return (
    <D.SubTrigger
      className={cn(
        "flex cursor-default items-center gap-2 rounded-sm px-2 py-1.5 text-body outline-none select-none data-[highlighted]:bg-sunken data-[state=open]:bg-sunken",
        className,
      )}
      {...props}
    >
      {children}
      <ChevronRight className="ml-auto size-3.5 text-faint" aria-hidden />
    </D.SubTrigger>
  );
}

export function MenuSubContent({ className, ...props }: React.ComponentProps<typeof D.SubContent>) {
  return (
    <D.Portal>
      <D.SubContent
        sideOffset={4}
        className={cn(
          "z-50 max-h-[var(--radix-dropdown-menu-content-available-height)] min-w-44 overflow-y-auto rounded-md border border-line bg-surface p-1 shadow-pop",
          className,
        )}
        {...props}
      />
    </D.Portal>
  );
}
