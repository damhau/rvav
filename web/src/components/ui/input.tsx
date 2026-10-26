import * as React from "react";
import { cn } from "@/lib/utils";

const field =
  "w-full rounded-md border border-line-strong bg-surface px-2.5 text-[13px] text-fg placeholder:text-faint focus-visible:border-accent focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent/25 disabled:opacity-60";

export function Input({ className, ...props }: React.InputHTMLAttributes<HTMLInputElement>) {
  return <input className={cn(field, "h-8", className)} {...props} />;
}

export function Textarea({ className, ...props }: React.TextareaHTMLAttributes<HTMLTextAreaElement>) {
  return <textarea className={cn(field, "min-h-16 py-1.5", className)} {...props} />;
}

export function Field({
  label,
  hint,
  htmlFor,
  children,
  className,
  aside,
}: {
  label: string;
  hint?: React.ReactNode;
  htmlFor?: string;
  children: React.ReactNode;
  className?: string;
  aside?: React.ReactNode;
}) {
  return (
    <div className={cn("flex min-w-0 flex-col gap-1", className)}>
      <div className="flex items-baseline justify-between gap-2">
        <label htmlFor={htmlFor} className="text-xs font-medium text-fg">
          {label}
        </label>
        {aside}
      </div>
      {children}
      {hint && <p className="text-xs text-faint">{hint}</p>}
    </div>
  );
}
