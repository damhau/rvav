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

type ControlProps = { "aria-describedby"?: string; "aria-invalid"?: boolean };

/**
 * A control's label, with a line of help under it, or the problem with its
 * value. Its first child is the control: Field adds the note to the ids that
 * describe it, and marks it invalid when there is a problem.
 */
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
  let tied = false;
  // The control is the first element: a list of the value's problems can follow it.
  const control = React.Children.map(children, (child) => {
    if (tied || !noteId || !React.isValidElement<ControlProps>(child)) return child;
    tied = true;
    const ids = new Set([...(child.props["aria-describedby"]?.split(" ") ?? []), noteId].filter(Boolean));
    return React.cloneElement(child, {
      "aria-describedby": [...ids].join(" "),
      ...(error ? { "aria-invalid": true } : {}),
    });
  });
  return (
    <div className={cn("flex min-w-0 flex-col gap-1", className)}>
      <div className="flex items-baseline justify-between gap-2">
        <label htmlFor={htmlFor} className="text-secondary font-medium text-fg">
          {label}
        </label>
        {aside}
      </div>
      {control}
      <FieldNote id={noteId} error={error} hint={hint} />
    </div>
  );
}

/** What goes under a control: the problem with its value, or else its help. */
export function FieldNote({ id, error, hint }: { id?: string; error?: string; hint?: React.ReactNode }) {
  if (error)
    return (
      <p id={id} className="flex items-start gap-1.5 text-secondary text-bad">
        <CircleAlert className="mt-0.5 size-3.5 shrink-0" aria-hidden />
        {error}
      </p>
    );
  return hint ? (
    <p id={id} className="text-secondary break-words text-muted">
      {hint}
    </p>
  ) : null;
}
