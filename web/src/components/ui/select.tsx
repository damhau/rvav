import { Check, ChevronDown, LoaderCircle } from "lucide-react";
import { Select as S } from "radix-ui";
import { cn } from "@/lib/utils";

export interface Option {
  value: string;
  label: string;
  meta?: string;
  /** Options with a group are listed under its heading, the groups in the order they first come. */
  group?: string;
}

/** Options in groups: those without one first, under no heading. */
function grouped(options: Option[]): [string | undefined, Option[]][] {
  const out = new Map<string | undefined, Option[]>();
  for (const o of options) out.set(o.group, [...(out.get(o.group) ?? []), o]);
  return [...out.entries()].sort(([a], [b]) => Number(a !== undefined) - Number(b !== undefined));
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
  onCloseAutoFocus,
  ...aria
}: {
  "aria-invalid"?: boolean;
  "aria-describedby"?: string;
  "aria-label"?: string;
  id?: string;
  value: string;
  onChange: (v: string) => void;
  options: Option[];
  placeholder?: string;
  loading?: boolean;
  disabled?: boolean;
  className?: string;
  /** When the list closes and gives the focus back to the select: prevent it to put the focus elsewhere. */
  onCloseAutoFocus?: (e: Event) => void;
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
          onCloseAutoFocus={onCloseAutoFocus}
          className="z-50 max-h-72 max-w-[var(--radix-select-content-available-width)] min-w-[var(--radix-select-trigger-width)] overflow-hidden rounded-md border border-line bg-surface shadow-pop"
        >
          <S.Viewport className="p-1">
            {grouped(options).map(([group, list]) =>
              group === undefined ? (
                list.map((o) => <Item key={o.value} option={o} />)
              ) : (
                <S.Group key={group}>
                  <S.Label className="px-2 pt-2 pb-1 text-[11px] font-medium text-muted">{group}</S.Label>
                  {list.map((o) => (
                    <Item key={o.value} option={o} />
                  ))}
                </S.Group>
              ),
            )}
          </S.Viewport>
        </S.Content>
      </S.Portal>
    </S.Root>
  );
}

function Item({ option: o }: { option: Option }) {
  return (
    <S.Item
      value={o.value}
      className="relative flex cursor-default items-center gap-3 rounded-sm py-1.5 pr-2 pl-6 text-[13px] outline-none select-none data-[highlighted]:bg-sunken"
    >
      <S.ItemIndicator className="absolute left-1.5">
        <Check className="size-3.5 text-accent" />
      </S.ItemIndicator>
      <S.ItemText>{o.label}</S.ItemText>
      {o.meta && <span className="ml-auto font-mono text-[11px] text-faint">{o.meta}</span>}
    </S.Item>
  );
}
