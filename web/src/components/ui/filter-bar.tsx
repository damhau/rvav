import { useNavigate } from "@tanstack/react-router";
import { SearchX } from "lucide-react";
import { createContext, useContext, useId, useRef } from "react";
import { useTranslation } from "react-i18next";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { TableSearch } from "@/components/ui/data-table";
import { EmptyState } from "@/components/ui/empty-state";
import { Input } from "@/components/ui/input";
import { Select, type Option } from "@/components/ui/select";
import { useSearchBox } from "@/lib/hooks";
import { cn } from "@/lib/utils";

type Values = Record<string, unknown>;

/** A list's filters, kept in the address: what they are, and how to change or clear them. */
export interface Filters<S extends object = Values> {
  values: S;
  set: (patch: Partial<S>) => void;
  /** Some filter is set. */
  filtered: boolean;
  /** Removes every filter, leaves the rest of the address, and focuses the bar's first control. */
  clear: () => void;
  bar: React.RefObject<HTMLDivElement | null>;
}

const isSet = (v: unknown) => v !== undefined && v !== null && v !== "" && v !== false;

/**
 * The filters a list keeps in the address, under `keys`. The address's other
 * keys, such as a tab, the sort or the layout, stay as they are; changes
 * replace the history entry, so Back leaves the page rather than a filter.
 */
export function useAddressFilters<S extends object>(values: S, keys: readonly (keyof S & string)[]): Filters<S> {
  const navigate = useNavigate();
  const bar = useRef<HTMLDivElement>(null);
  const go = (next: (s: Values) => Values) => void navigate({ to: ".", search: next as never, replace: true });
  return {
    values,
    filtered: keys.some((k) => isSet(values[k])),
    set: (patch) => go((s) => ({ ...s, ...patch })),
    clear: () => {
      go((s) => Object.fromEntries(Object.entries(s).filter(([k]) => !(keys as readonly string[]).includes(k))));
      // Clear goes once nothing is filtered, so the focus moves to what stays.
      bar.current?.querySelector<HTMLElement>("input, button:not([disabled]):not([aria-disabled=true])")?.focus();
    },
    bar,
  };
}

const Ctx = createContext<Filters | null>(null);

function useFilters(): Filters {
  const f = useContext(Ctx);
  if (!f) throw new Error("A filter belongs in a FilterBar");
  return f;
}

/**
 * A list's toolbar: a search box, its filters, and Clear once one is set. A
 * control's name says what it filters; a select shows "All …" when it
 * filters nothing, and a box that holds a value of its own (a date, a path)
 * shows its label beside it.
 */
export function FilterBar<S extends object>({
  filters,
  search,
  label,
  children,
  end,
  className,
}: {
  filters: Filters<S>;
  /** The search box, kept under `q`. */
  search?: { label: string; placeholder?: string; className?: string };
  /** The group's name; "Filters" by default. */
  label?: string;
  /** FilterSelect, FilterCheck, FilterInput, or controls of the page's own that call `filters.set`. */
  children?: React.ReactNode;
  /** At the far end, such as a live count. */
  end?: React.ReactNode;
  className?: string;
}) {
  const { t } = useTranslation();
  return (
    <Ctx.Provider value={filters as unknown as Filters}>
      <div
        ref={filters.bar}
        role="group"
        aria-label={label ?? t("filters.label")}
        className={cn("flex w-full flex-wrap items-center gap-2", className)}
      >
        {search && <SearchBox {...search} />}
        {children}
        {filters.filtered && (
          <Button variant="quiet" onClick={filters.clear}>
            {t("filters.clear")}
          </Button>
        )}
        {end && (
          <span className="ml-auto text-secondary text-muted" aria-live="polite">
            {end}
          </span>
        )}
      </div>
    </Ctx.Provider>
  );
}

function SearchBox({ label, placeholder, className }: { label: string; placeholder?: string; className?: string }) {
  const f = useFilters();
  const [text, setText] = useSearchBox(typeof f.values.q === "string" ? f.values.q : undefined, (q) => f.set({ q }));
  return <TableSearch label={label} placeholder={placeholder} value={text} onChange={setText} className={className} />;
}

/** A filter on one value. "All …" (`any`) filters nothing; a value from the address that no option offers still shows. */
export function FilterSelect({
  name,
  label,
  any,
  options,
  loading,
  id,
  className,
  onChange,
}: {
  /** The address key. */
  name: string;
  /** What it filters, for screen readers: "Status". */
  label: string;
  /** The option that filters nothing: "All statuses". */
  any: string;
  options: Option[];
  loading?: boolean;
  id?: string;
  className?: string;
  /** Sets other keys with it, such as clearing a cluster when the vCenter changes. */
  onChange?: (value: string | undefined) => Values;
}) {
  const f = useFilters();
  const held = f.values[name];
  const value = typeof held === "string" && held !== "" ? held : "any";
  const offered =
    value === "any" || options.some((o) => o.value === value) ? options : [...options, { value, label: value }];
  return (
    <Select
      id={id}
      aria-label={label}
      value={value}
      loading={loading}
      onChange={(v) => {
        const next = v === "any" ? undefined : v;
        f.set({ [name]: next, ...onChange?.(next) });
      }}
      options={[{ value: "any", label: any }, ...offered]}
      className={className}
    />
  );
}

/** A filter that is on or off: on keeps `value` (true by default) under its key. */
export function FilterCheck({ name, label, value = true }: { name: string; label: string; value?: string | true }) {
  const f = useFilters();
  const id = useId();
  return (
    <span className="flex items-center gap-2 text-body">
      <Checkbox
        id={id}
        checked={f.values[name] === value}
        onChange={(on) => f.set({ [name]: on ? value : undefined })}
      />
      <label htmlFor={id}>{label}</label>
    </span>
  );
}

/** A box for a value of its own, such as a day or a folder, with its label beside it. Text is kept once typing pauses. */
export function FilterInput({
  name,
  label,
  type = "text",
  placeholder,
  className,
}: {
  name: string;
  label: string;
  type?: "text" | "date";
  placeholder?: string;
  /** The box's classes, such as a width or `font-mono`. */
  className?: string;
}) {
  const f = useFilters();
  const id = useId();
  const value = f.values[name];
  const held = typeof value === "string" ? value : undefined;
  const [text, setText] = useSearchBox(held, (v) => f.set({ [name]: v }));
  return (
    <span className="flex items-center gap-2">
      <label htmlFor={id} className="text-secondary text-muted">
        {label}
      </label>
      {type === "date" ? (
        <Input
          id={id}
          type="date"
          value={held ?? ""}
          onChange={(e) => f.set({ [name]: e.target.value || undefined })}
          className={cn("w-40", className)}
        />
      ) : (
        <Input
          id={id}
          value={text}
          placeholder={placeholder}
          onChange={(e) => setText(e.target.value)}
          className={cn("w-48", className)}
        />
      )}
    </span>
  );
}

/** A list with nothing that matches its filters: says so, and offers Clear. */
export function FilterEmpty({
  filters,
  title,
  body,
  icon = SearchX,
}: {
  filters: Pick<Filters<object>, "clear">;
  title: string;
  body: React.ReactNode;
  icon?: React.ElementType;
}) {
  const { t } = useTranslation();
  return (
    <EmptyState
      icon={icon}
      title={title}
      body={body}
      action={
        <Button size="sm" onClick={filters.clear}>
          {t("filters.clear")}
        </Button>
      }
    />
  );
}
