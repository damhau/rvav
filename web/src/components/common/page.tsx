import { cn } from "@/lib/utils";

export function Page({ children, className }: { children: React.ReactNode; className?: string }) {
  return <div className={cn("mx-auto flex w-full max-w-[1280px] flex-col gap-5 px-4 py-5 md:px-6", className)}>{children}</div>;
}

export function PageHeader({
  title,
  meta,
  actions,
  crumbs,
}: {
  title: React.ReactNode;
  meta?: React.ReactNode;
  actions?: React.ReactNode;
  crumbs?: React.ReactNode;
}) {
  return (
    <header className="flex flex-col gap-1">
      {crumbs && <div className="flex items-center gap-1 text-xs text-faint">{crumbs}</div>}
      <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-2">
        <h1 className="min-w-0 text-lg font-semibold tracking-[-0.01em]">{title}</h1>
        {actions && <div className="flex flex-wrap items-center gap-2">{actions}</div>}
      </div>
      {meta && <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted">{meta}</div>}
    </header>
  );
}

export function Panel({
  title,
  actions,
  children,
  className,
  flush,
}: {
  title?: React.ReactNode;
  actions?: React.ReactNode;
  children: React.ReactNode;
  className?: string;
  flush?: boolean;
}) {
  return (
    <section className={cn("min-w-0 rounded-lg border border-line bg-surface", className)}>
      {title && (
        <div className="flex items-center justify-between gap-3 border-b border-line px-4 py-2.5">
          <h2 className="text-[13px] font-semibold">{title}</h2>
          {actions}
        </div>
      )}
      <div className={flush ? "" : "p-4"}>{children}</div>
    </section>
  );
}

export function EmptyState({ title, body, action }: { title: string; body: string; action?: React.ReactNode }) {
  return (
    <div className="flex flex-col items-start gap-2 px-4 py-8">
      <p className="text-[13px] font-medium">{title}</p>
      <p className="max-w-md text-xs text-muted">{body}</p>
      {action}
    </div>
  );
}

export function KeyValues({ items, className }: { items: { label: string; value: React.ReactNode }[]; className?: string }) {
  return (
    <dl className={cn("grid grid-cols-[minmax(96px,auto)_1fr] gap-x-4 gap-y-1.5 text-[13px]", className)}>
      {items.map((i) => (
        <div key={i.label} className="contents">
          <dt className="text-muted">{i.label}</dt>
          <dd className="min-w-0 break-words">{i.value}</dd>
        </div>
      ))}
    </dl>
  );
}
