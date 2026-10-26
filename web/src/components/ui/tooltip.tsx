import { Tooltip as T } from "radix-ui";
import type * as React from "react";

export const TooltipProvider = T.Provider;

export function Tip({ content, children }: { content: React.ReactNode; children: React.ReactNode }) {
  return (
    <T.Root>
      <T.Trigger asChild>{children}</T.Trigger>
      <T.Portal>
        <T.Content
          sideOffset={4}
          className="z-50 rounded-sm bg-fg px-2 py-1 text-[11px] text-bg"
        >
          {content}
        </T.Content>
      </T.Portal>
    </T.Root>
  );
}
