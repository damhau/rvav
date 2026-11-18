import { cn } from "@/lib/utils";

/** The top of a page: where it sits, its title, what it is for, and its actions (one of them primary). */
export function PageHeader({
  title,
  description,
  meta,
  breadcrumbs,
  actions,
  className,
}: {
  title: React.ReactNode;
  /** One sentence on what the page is for. */
  description?: React.ReactNode;
  /** Facts about what the page shows, such as counts, badges or an owner. */
  meta?: React.ReactNode;
  breadcrumbs?: React.ReactNode;
  actions?: React.ReactNode;
  className?: string;
}) {
  return (
    <header className={cn("flex flex-col gap-1.5", className)}>
      {breadcrumbs && <div className="flex items-center gap-1 text-secondary text-muted">{breadcrumbs}</div>}
      <div className="flex flex-wrap items-start justify-between gap-x-6 gap-y-3">
        <div className="flex min-w-0 flex-[1_1_20rem] flex-col gap-1">
          <h1 className="min-w-0 text-title font-semibold">{title}</h1>
          {description && <p className="max-w-[72ch] text-secondary text-muted">{description}</p>}
          {meta && <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-secondary text-muted">{meta}</div>}
        </div>
        {actions && <div className="flex flex-wrap items-center gap-2">{actions}</div>}
      </div>
    </header>
  );
}
