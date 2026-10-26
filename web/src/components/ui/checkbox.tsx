import { Check } from "lucide-react";
import { Checkbox as C } from "radix-ui";
import { cn } from "@/lib/utils";

export function Checkbox({
  id,
  checked,
  onChange,
  className,
}: {
  id?: string;
  checked: boolean;
  onChange: (v: boolean) => void;
  className?: string;
}) {
  return (
    <C.Root
      id={id}
      checked={checked}
      onCheckedChange={(v) => onChange(v === true)}
      className={cn(
        "flex size-4 shrink-0 items-center justify-center rounded-sm border border-line-strong bg-surface data-[state=checked]:border-accent data-[state=checked]:bg-accent",
        className,
      )}
    >
      <C.Indicator>
        <Check className="size-3 text-accent-fg" strokeWidth={3} />
      </C.Indicator>
    </C.Root>
  );
}
