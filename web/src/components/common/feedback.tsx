import { TriangleAlert } from "lucide-react";
import { Button } from "@/components/ui/button";
import { isApiError } from "@/lib/api/client";
import { cn } from "@/lib/utils";

export function Skeleton({ className }: { className?: string }) {
  return <span className={cn("block animate-pulse rounded-sm bg-sunken", className)} aria-hidden />;
}

/** Placeholder rows for a table that is loading. */
export function SkeletonRows({ rows = 5, cols = 4 }: { rows?: number; cols?: number }) {
  return (
    <div className="flex flex-col" role="status" aria-label="Loading">
      {Array.from({ length: rows }, (_, r) => (
        <div key={r} className="flex items-center gap-4 border-b border-line px-4 py-3 last:border-b-0">
          {Array.from({ length: cols }, (_, c) => (
            <Skeleton key={c} className={cn("h-3", c === 0 ? "w-48" : "w-20", c === cols - 1 && "ml-auto")} />
          ))}
        </div>
      ))}
    </div>
  );
}

/** A failed load, with what went wrong and the request ID to quote to support. */
export function ErrorState({
  error,
  onRetry,
  className,
}: {
  error: unknown;
  onRetry?: () => void;
  className?: string;
}) {
  const api = isApiError(error) ? error : null;
  const title = api?.problem.title ?? "Something went wrong";
  const detail = api?.problem.detail ?? (error instanceof Error ? error.message : undefined);
  return (
    <div role="alert" className={cn("flex items-start gap-3 px-4 py-6", className)}>
      <TriangleAlert className="mt-0.5 size-4 shrink-0 text-bad" aria-hidden />
      <div className="flex min-w-0 flex-col gap-1">
        <p className="text-[13px] font-medium">{title}</p>
        {detail && <p className="max-w-prose text-xs text-muted">{detail}</p>}
        {api && api.requestId !== "none" && (
          <p className="text-xs text-faint">
            Request ID <span className="font-mono select-all">{api.requestId}</span>
          </p>
        )}
        {onRetry && (
          <Button size="sm" className="mt-1 self-start" onClick={onRetry}>
            Try again
          </Button>
        )}
      </div>
    </div>
  );
}
