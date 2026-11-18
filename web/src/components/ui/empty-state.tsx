import { cn } from "@/lib/utils";

/** What a list or panel shows when it has nothing yet: what goes here, and the step that adds it. */
export function EmptyState({
  icon: Icon,
  title,
  body,
  action,
  className,
}: {
  icon?: React.ElementType;
  title: string;
  body: React.ReactNode;
  /** The one next step, usually a primary button. */
  action?: React.ReactNode;
  className?: string;
}) {
  return (
    <div className={cn("flex flex-col items-center gap-1 px-6 py-10 text-center", className)}>
      {Icon && (
        <span className="mb-2 flex size-10 items-center justify-center rounded-full bg-sunken text-muted" aria-hidden>
          <Icon className="size-5" strokeWidth={1.75} />
        </span>
      )}
      <p className="text-body font-medium">{title}</p>
      <p className="max-w-sm text-secondary text-muted">{body}</p>
      {action && <div className="mt-3 flex flex-wrap justify-center gap-2">{action}</div>}
    </div>
  );
}
