import { CircleAlert } from "lucide-react";
import * as React from "react";
import { cn } from "@/lib/utils";

const field =
  "w-full rounded-md border border-line-strong bg-surface px-2.5 text-body text-fg placeholder:text-faint focus-visible:border-accent focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent/25 disabled:opacity-60 aria-invalid:border-bad";

export function Input({ className, ...props }: React.ComponentProps<"input">) {
  return <input className={cn(field, "h-8", className)} {...props} />;
}

export function Textarea({ className, ...props }: React.ComponentProps<"textarea">) {
  return <textarea className={cn(field, "min-h-16 py-1.5", className)} {...props} />;
}

/** A control's label, with a line of help under it, or the problem with its value. */
export function Field({
  label,
  hint,
  error,
  htmlFor,
  children,
  className,
  aside,
}: {
  label: string;
  hint?: React.ReactNode;
  /** A problem with the value: shown instead of the hint and tied to the control. */
  error?: string;
  htmlFor?: string;
  children: React.ReactNode;
  className?: string;
  aside?: React.ReactNode;
}) {
  const noteId = htmlFor && (error || hint) ? `${htmlFor}-note` : undefined;
  const control =
    noteId && React.isValidElement<{ "aria-describedby"?: string; "aria-invalid"?: boolean }>(children)
      ? React.cloneElement(children, {
          "aria-describedby": children.props["aria-describedby"] ?? noteId,
          ...(error ? { "aria-invalid": true } : {}),
        })
      : children;
  return (
    <div className={cn("flex min-w-0 flex-col gap-1", className)}>
      <div className="flex items-baseline justify-between gap-2">
        <label htmlFor={htmlFor} className="text-secondary font-medium text-fg">
          {label}
        </label>
        {aside}
      </div>
      {control}
      {error ? (
        <p id={noteId} className="flex items-start gap-1.5 text-secondary text-bad">
          <CircleAlert className="mt-0.5 size-3.5 shrink-0" aria-hidden />
          {error}
        </p>
      ) : (
        hint && (
          <p id={noteId} className="text-secondary text-muted">
            {hint}
          </p>
        )
      )}
    </div>
  );
}
