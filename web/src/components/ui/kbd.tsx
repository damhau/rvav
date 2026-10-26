import { cn } from "@/lib/utils";

export function Kbd({ children, className }: { children: React.ReactNode; className?: string }) {
  return (
    <kbd
      className={cn(
        "inline-flex h-[18px] min-w-[18px] items-center justify-center rounded-sm border border-line bg-surface px-1 font-mono text-[10px] text-faint",
        className,
      )}
    >
      {children}
    </kbd>
  );
}
