import { cn } from "@/lib/utils";

export function Page({ children, className }: { children: React.ReactNode; className?: string }) {
  return (
    <div className={cn("mx-auto flex w-full max-w-[1280px] flex-col gap-5 px-4 py-5 md:px-6", className)}>
      {children}
    </div>
  );
}

export function KeyValues({
  items,
  className,
}: {
  items: { label: string; value: React.ReactNode }[];
  className?: string;
}) {
  return (
    <dl className={cn("grid grid-cols-[minmax(96px,auto)_1fr] gap-x-4 gap-y-1.5 text-body", className)}>
      {items.map((i) => (
        <div key={i.label} className="contents">
          <dt className="text-muted">{i.label}</dt>
          <dd className="min-w-0 break-words">{i.value}</dd>
        </div>
      ))}
    </dl>
  );
}
