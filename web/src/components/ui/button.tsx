import { cva, type VariantProps } from "class-variance-authority";
import { Slot } from "radix-ui";
import * as React from "react";
import { cn } from "@/lib/utils";

// One primary action per page or panel; secondary for the others; quiet for
// actions in rows, toolbars and menus' triggers; danger for what removes or
// breaks something, and danger-solid to confirm it.
const buttonVariants = cva(
  "inline-flex shrink-0 items-center justify-center gap-1.5 rounded-md font-medium whitespace-nowrap transition-colors disabled:pointer-events-none disabled:opacity-50 aria-disabled:opacity-50 [&_svg]:size-4 [&_svg]:shrink-0",
  {
    variants: {
      variant: {
        primary: "bg-accent text-accent-fg hover:bg-accent/90",
        secondary: "border border-line-strong bg-surface text-fg hover:bg-sunken",
        quiet: "text-muted hover:bg-sunken hover:text-fg",
        danger: "border border-bad/40 bg-surface text-bad hover:bg-bad-soft",
        "danger-solid": "bg-bad text-bad-fg hover:bg-bad/90",
      },
      size: {
        sm: "h-7 px-2.5 text-secondary [&_svg]:size-3.5",
        md: "h-8 px-3 text-body",
        icon: "size-7",
      },
    },
    defaultVariants: { variant: "secondary", size: "md" },
  },
);

export interface ButtonProps extends React.ComponentProps<"button">, VariantProps<typeof buttonVariants> {
  asChild?: boolean;
}

/**
 * A button. One that turns disabled while it has the focus, such as a submit
 * button while its save runs, keeps the focus rather than dropping it to the
 * page: it says it's unavailable and ignores presses until the focus leaves.
 */
export function Button({
  className,
  variant,
  size,
  asChild,
  disabled,
  "aria-disabled": ariaDisabled,
  onFocus,
  onBlur,
  onClick,
  ...props
}: ButtonProps) {
  const Comp = asChild ? Slot.Root : "button";
  const [focused, setFocused] = React.useState(false);
  const holding = Boolean(disabled) && focused;
  return (
    <Comp
      {...props}
      className={cn(buttonVariants({ variant, size }), className)}
      disabled={holding ? undefined : disabled}
      aria-disabled={holding || ariaDisabled}
      onFocus={(e) => {
        setFocused(true);
        onFocus?.(e);
      }}
      onBlur={(e) => {
        setFocused(false);
        onBlur?.(e);
      }}
      onClick={(e) => {
        if (holding) e.preventDefault();
        else onClick?.(e);
      }}
    />
  );
}

/** A button that shows only an icon: the label names it for screen readers and shows on hover. */
export function IconButton({
  label,
  variant = "quiet",
  size = "icon",
  ...props
}: Omit<ButtonProps, "aria-label" | "title"> & { label: string }) {
  return <Button variant={variant} size={size} aria-label={label} title={label} {...props} />;
}
