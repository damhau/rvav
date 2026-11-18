import { cn } from "@/lib/utils";

export interface Segment<T extends string> {
  value: T;
  label: string;
  icon?: React.ElementType;
  /** Shows only the icon; the label names the button. */
  iconOnly?: boolean;
}

/** A few ways to show one thing, as pressed buttons in a group: one is pressed at a time. */
export function Segmented<T extends string>({
  label,
  value,
  options,
  onChange,
  className,
}: {
  /** The group's name, such as "Show as". */
  label: string;
  value: T;
  options: Segment<T>[];
  onChange: (v: T) => void;
  className?: string;
}) {
  return (
    <div
      role="group"
      aria-label={label}
      className={cn("inline-flex shrink-0 rounded-md border border-line bg-surface p-0.5", className)}
    >
      {options.map((o) => (
        <button
          key={o.value}
          type="button"
          aria-pressed={value === o.value}
          aria-label={o.iconOnly ? o.label : undefined}
          title={o.iconOnly ? o.label : undefined}
          onClick={() => onChange(o.value)}
          className={cn(
            "inline-flex h-6 items-center gap-1.5 rounded-sm px-2 text-secondary font-medium [&_svg]:size-3.5",
            value === o.value ? "bg-accent-soft text-accent" : "text-muted hover:text-fg",
          )}
        >
          {o.icon && <o.icon aria-hidden />}
          {!o.iconOnly && o.label}
        </button>
      ))}
    </div>
  );
}
