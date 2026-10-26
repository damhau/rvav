import { Check, ChevronDown, LoaderCircle } from "lucide-react";
import { Select as S } from "radix-ui";
import { cn } from "@/lib/utils";

export interface Option {
  value: string;
  label: string;
  meta?: string;
}

export function Select({
  id,
  value,
  onChange,
  options,
  placeholder = "Select…",
  loading,
  disabled,
  className,
  ...aria
}: {
  "aria-invalid"?: boolean;
  "aria-describedby"?: string;
  id?: string;
  value: string;
  onChange: (v: string) => void;
  options: Option[];
  placeholder?: string;
  loading?: boolean;
  disabled?: boolean;
  className?: string;
}) {
  return (
    <S.Root value={value} onValueChange={onChange} disabled={disabled || loading}>
      <S.Trigger
        id={id}
        {...aria}
        className={cn(
          "flex h-8 w-full min-w-0 items-center justify-between gap-2 rounded-md border border-line-strong bg-surface px-2.5 text-left text-[13px] text-fg focus-visible:border-accent focus-visible:ring-2 focus-visible:ring-accent/25 focus-visible:outline-none disabled:opacity-60 data-[placeholder]:text-faint",
          className,
        )}
      >
        <span className="truncate">
          {loading ? <span className="text-faint">Loading…</span> : <S.Value placeholder={placeholder} />}
        </span>
        {loading ? (
          <LoaderCircle className="size-3.5 animate-spin text-faint" />
        ) : (
          <ChevronDown className="size-3.5 text-faint" />
        )}
      </S.Trigger>
      <S.Portal>
        <S.Content
          position="popper"
          sideOffset={4}
          className="z-50 max-h-72 min-w-[var(--radix-select-trigger-width)] overflow-hidden rounded-md border border-line bg-surface shadow-pop"
        >
          <S.Viewport className="p-1">
            {options.map((o) => (
              <S.Item
                key={o.value}
                value={o.value}
                className="relative flex cursor-default items-center gap-3 rounded-sm py-1.5 pr-2 pl-6 text-[13px] outline-none select-none data-[highlighted]:bg-sunken"
              >
                <S.ItemIndicator className="absolute left-1.5">
                  <Check className="size-3.5 text-accent" />
                </S.ItemIndicator>
                <S.ItemText>{o.label}</S.ItemText>
                {o.meta && <span className="ml-auto font-mono text-[11px] text-faint">{o.meta}</span>}
              </S.Item>
            ))}
          </S.Viewport>
        </S.Content>
      </S.Portal>
    </S.Root>
  );
}
