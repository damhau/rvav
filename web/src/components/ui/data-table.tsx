import { ArrowDown, ArrowUp, ArrowUpDown, Ellipsis, Search } from "lucide-react";
import { useId, useState } from "react";
import { IconButton } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Menu, MenuContent, MenuTrigger } from "@/components/ui/dropdown-menu";
import { Input } from "@/components/ui/input";
import { cn } from "@/lib/utils";

export interface Column<T> {
  id: string;
  header: React.ReactNode;
  cell: (row: T) => React.ReactNode;
  /** Makes the column sortable, by this value. */
  sortValue?: (row: T) => string | number | null | undefined;
  align?: "start" | "end";
  /** Classes for the column's cells, such as a width or whitespace-nowrap. */
  className?: string;
}

export interface Sort {
  id: string;
  desc: boolean;
}

export interface DataTableProps<T> {
  /** The table's accessible name. */
  label: string;
  columns: Column<T>[];
  rows: T[] | undefined;
  rowKey: (row: T) => string;
  /** The row's menu items (MenuItem); the row gets a ⋯ button when this returns any. */
  rowMenu?: (row: T) => React.ReactNode;
  /** The ⋯ button's name, such as "Actions for RHEL 9.4". */
  rowMenuLabel?: (row: T) => string;
  /** Clicking a row anywhere follows the first link in it. */
  rowLinks?: boolean;
  /** Search, filter selects and chips, above the table. */
  toolbar?: React.ReactNode;
  loading?: boolean;
  /** Shown instead of the rows when the list failed, such as an ErrorState. */
  error?: React.ReactNode;
  /** Shown instead of the table when there are no rows, such as an EmptyState. */
  empty?: React.ReactNode;
  selection?: {
    selected: ReadonlySet<string>;
    onChange: (next: Set<string>) => void;
    /** A row's checkbox name, such as "Select web-prd-front". */
    label: (row: T) => string;
    /** The header checkbox's name: "Select all shown" when filters or paging hide rows. */
    allLabel?: string;
  };
  defaultSort?: Sort;
  /** The sort, when the page keeps it (in the URL, say). */
  sort?: Sort | null;
  onSortChange?: (sort: Sort | null) => void;
  /** The width under which the table scrolls sideways, such as 720. */
  minWidth?: number;
  className?: string;
}

const compare = (a: string | number | null | undefined, b: string | number | null | undefined) => {
  if (a === b) return 0;
  if (a === null || a === undefined) return 1;
  if (b === null || b === undefined) return -1;
  if (typeof a === "number" && typeof b === "number") return a - b;
  return String(a).localeCompare(String(b), undefined, { numeric: true, sensitivity: "base" });
};

const interactive = "a, button, input, select, textarea, label, [role=checkbox], [role=menuitem], [role=combobox]";

/** A list of records: sortable columns, a row menu, a toolbar, and its loading, empty and failed states. */
export function DataTable<T>({
  label,
  columns,
  rows,
  rowKey,
  rowMenu,
  rowMenuLabel,
  rowLinks,
  toolbar,
  loading,
  error,
  empty,
  selection,
  defaultSort,
  sort: sortProp,
  onSortChange,
  minWidth,
  className,
}: DataTableProps<T>) {
  const [ownSort, setOwnSort] = useState<Sort | null>(defaultSort ?? null);
  const sort = sortProp === undefined ? ownSort : sortProp;
  const setSort = (s: Sort | null) => {
    if (sortProp === undefined) setOwnSort(s);
    onSortChange?.(s);
  };
  const by = sort ? columns.find((c) => c.id === sort.id) : undefined;
  const sorted =
    rows && by?.sortValue
      ? [...rows].sort((a, b) => compare(by.sortValue!(a), by.sortValue!(b)) * (sort?.desc ? -1 : 1))
      : rows;
  const menus = rowMenu && sorted ? new Map(sorted.map((r) => [rowKey(r), rowMenu(r)])) : undefined;
  const hasMenu = menus !== undefined && [...menus.values()].some((m) => m !== null && m !== undefined && m !== false);
  const width = columns.length + (selection ? 1 : 0) + (hasMenu ? 1 : 0);
  const allSelected = Boolean(sorted?.length) && sorted!.every((r) => selection?.selected.has(rowKey(r)));

  let body: React.ReactNode;
  if (error) body = error;
  else if (!loading && sorted && sorted.length === 0 && empty) body = empty;
  else {
    body = (
      <div className="overflow-x-auto">
        <table
          className="w-full border-collapse text-body"
          style={minWidth ? { minWidth } : undefined}
          aria-label={label}
        >
          <thead>
            <tr className="label text-left">
              {selection && (
                <th scope="col" className="w-px border-b border-line py-2 pr-1 pl-4">
                  <Checkbox
                    aria-label={selection.allLabel ?? "Select all"}
                    checked={allSelected}
                    disabled={!sorted?.length}
                    onChange={(on) => {
                      const shown = new Set((sorted ?? []).map(rowKey));
                      // Rows selected but not shown stay selected either way.
                      selection.onChange(
                        new Set(
                          on ? [...selection.selected, ...shown] : [...selection.selected].filter((k) => !shown.has(k)),
                        ),
                      );
                    }}
                  />
                </th>
              )}
              {columns.map((c, i) => {
                const active = sort?.id === c.id;
                const Icon = !active ? ArrowUpDown : sort.desc ? ArrowDown : ArrowUp;
                return (
                  <th
                    key={c.id}
                    scope="col"
                    aria-sort={active ? (sort.desc ? "descending" : "ascending") : undefined}
                    className={cn(
                      "border-b border-line px-3 py-2 font-medium whitespace-nowrap",
                      i === 0 && !selection && "pl-4",
                      i === columns.length - 1 && !hasMenu && "pr-4",
                      c.align === "end" && "text-right",
                    )}
                  >
                    {c.sortValue ? (
                      <button
                        type="button"
                        onClick={() => setSort(active && sort.desc ? null : { id: c.id, desc: active })}
                        className={cn(
                          "-mx-1 inline-flex items-center gap-1 rounded-sm px-1 hover:text-fg",
                          active && "text-fg",
                        )}
                      >
                        {c.header}
                        <Icon className={cn("size-3.5", !active && "text-faint")} aria-hidden />
                      </button>
                    ) : (
                      c.header
                    )}
                  </th>
                );
              })}
              {hasMenu && (
                <th scope="col" className="w-px border-b border-line py-2 pr-3 pl-1">
                  <span className="sr-only">Actions</span>
                </th>
              )}
            </tr>
          </thead>
          <tbody>
            {loading && !sorted?.length
              ? [0, 1, 2].map((r) => (
                  <tr key={r} aria-hidden>
                    {Array.from({ length: width }, (_, c) => (
                      <td key={c} className="border-b border-line px-3 py-3 first:pl-4 last:pr-4">
                        <span className="block h-3 w-3/4 animate-pulse rounded-sm bg-sunken" />
                      </td>
                    ))}
                  </tr>
                ))
              : sorted?.map((row) => {
                  const key = rowKey(row);
                  const menu = menus?.get(key);
                  return (
                    <tr
                      key={key}
                      className={cn(
                        "border-b border-line last:border-b-0 hover:bg-sunken/60",
                        selection?.selected.has(key) && "bg-accent-soft/40",
                        rowLinks && "cursor-pointer",
                      )}
                      onClick={
                        rowLinks
                          ? (e) => {
                              if ((e.target as Element).closest(interactive)) return;
                              e.currentTarget.querySelector<HTMLAnchorElement>("a[href]")?.click();
                            }
                          : undefined
                      }
                    >
                      {selection && (
                        <td className="w-px py-2.5 pr-1 pl-4 align-middle">
                          <Checkbox
                            aria-label={selection.label(row)}
                            checked={selection.selected.has(key)}
                            onChange={(on) => {
                              const next = new Set(selection.selected);
                              if (on) next.add(key);
                              else next.delete(key);
                              selection.onChange(next);
                            }}
                          />
                        </td>
                      )}
                      {columns.map((c, i) => (
                        <td
                          key={c.id}
                          className={cn(
                            "px-3 py-2.5 align-middle",
                            i === 0 && !selection && "pl-4",
                            i === columns.length - 1 && !hasMenu && "pr-4",
                            c.align === "end" && "text-right",
                            c.className,
                          )}
                        >
                          {c.cell(row)}
                        </td>
                      ))}
                      {hasMenu && (
                        <td className="w-px py-1.5 pr-3 pl-1 text-right align-middle">
                          {menu ? (
                            <Menu>
                              <MenuTrigger asChild>
                                <IconButton label={rowMenuLabel?.(row) ?? "Actions"}>
                                  <Ellipsis />
                                </IconButton>
                              </MenuTrigger>
                              <MenuContent>{menu}</MenuContent>
                            </Menu>
                          ) : null}
                        </td>
                      )}
                    </tr>
                  );
                })}
          </tbody>
        </table>
        {loading && !sorted?.length && (
          <span role="status" className="sr-only">
            Loading
          </span>
        )}
      </div>
    );
  }

  return (
    <div className={cn("min-w-0", className)}>
      {toolbar && <div className="flex flex-wrap items-center gap-2 border-b border-line px-4 py-2.5">{toolbar}</div>}
      {body}
    </div>
  );
}

/** A table's search box, named for screen readers by its label. */
export function TableSearch({
  label,
  value,
  onChange,
  placeholder,
  className,
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
  className?: string;
}) {
  const id = useId();
  return (
    <div className={cn("relative w-64 max-w-full", className)}>
      <label htmlFor={id} className="sr-only">
        {label}
      </label>
      <Search
        className="pointer-events-none absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-faint"
        aria-hidden
      />
      <Input
        id={id}
        type="search"
        value={value}
        placeholder={placeholder}
        onChange={(e) => onChange(e.target.value)}
        className="pl-8"
      />
    </div>
  );
}
