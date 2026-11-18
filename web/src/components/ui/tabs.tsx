import { Tabs as T } from "radix-ui";
import * as React from "react";
import { cn } from "@/lib/utils";

export const Tabs = T.Root;
export const TabsContent = T.Content;

// The strip's bottom line is a shadow the tabs' underline paints over, rather
// than a border they reach with a negative margin: that pixel overflows the
// strip, and overflow-x-auto then scrolls it vertically too (#92).
export const tabStrip =
  "flex gap-4 overflow-x-auto overflow-y-hidden shadow-[inset_0_-1px_0_var(--line)] [scrollbar-width:thin]";
export const tabItem =
  "flex items-center gap-1.5 border-b-2 border-transparent pt-1 pb-2 text-body whitespace-nowrap text-muted hover:text-fg focus-visible:-outline-offset-2";

export function TabsList({ className, ...props }: React.ComponentProps<typeof T.List>) {
  return <T.List className={cn(tabStrip, className)} {...props} />;
}

export function TabsTrigger({ className, ...props }: React.ComponentProps<typeof T.Trigger>) {
  return (
    <T.Trigger
      className={cn(
        tabItem,
        "data-[state=active]:border-accent data-[state=active]:font-medium data-[state=active]:text-fg",
        className,
      )}
      {...props}
    />
  );
}
