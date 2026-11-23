import { Check } from "lucide-react";
import { Checkbox as C } from "radix-ui";
import { cn } from "@/lib/utils";

export function Checkbox({
  id,
  checked,
  onChange,
  disabled,
  className,
  "aria-label": ariaLabel,
  "aria-describedby": describedBy,
  "aria-invalid": invalid,
}: {
  id?: string;
  /** The checkbox's name when no label element names it. */
  "aria-label"?: string;
  "aria-describedby"?: string;
  "aria-invalid"?: boolean;
  checked: boolean;
  onChange: (v: boolean) => void;
  disabled?: boolean;
  className?: string;
}) {
  return (
    <C.Root
      id={id}
      aria-label={ariaLabel}
      aria-describedby={describedBy}
      aria-invalid={invalid}
      checked={checked}
      disabled={disabled}
      onCheckedChange={(v) => onChange(v === true)}
      className={cn(
        "flex size-4 shrink-0 items-center justify-center rounded-sm border border-line-strong bg-surface disabled:opacity-60 data-[state=checked]:border-accent data-[state=checked]:bg-accent",
        className,
      )}
    >
      <C.Indicator>
        <Check className="size-3 text-accent-fg" strokeWidth={3} />
      </C.Indicator>
    </C.Root>
  );
}

/**
 * A checkbox with its label and a hint below: the label names it and the
 * hint describes it, so the hint isn't read as part of its name.
 */
export function CheckField({
  id,
  label,
  hint,
  checked,
  onChange,
  disabled,
  className,
}: {
  id: string;
  label: React.ReactNode;
  hint?: React.ReactNode;
  checked: boolean;
  onChange: (v: boolean) => void;
  disabled?: boolean;
  className?: string;
}) {
  return (
    <div className={cn("flex items-start gap-2 text-body", className)}>
      <Checkbox
        id={id}
        className="mt-0.5"
        checked={checked}
        onChange={onChange}
        disabled={disabled}
        aria-describedby={hint ? `${id}-hint` : undefined}
      />
      <span>
        <label htmlFor={id}>{label}</label>
        {hint && (
          <span id={`${id}-hint`} className="block text-secondary text-muted">
            {hint}
          </span>
        )}
      </span>
    </div>
  );
}
