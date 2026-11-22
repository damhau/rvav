import { ArrowDown, ArrowUp, ArrowUpDown, Ellipsis, Search } from "lucide-react";
import { useId, useLayoutEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { IconButton } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Menu, MenuContent, MenuTrigger } from "@/components/ui/dropdown-menu";
import { Input } from "@/components/ui/input";
import { rowStandIn } from "@/components/ui/opener";
import { ScrollRegion } from "@/components/ui/scroll-region";
import { useMediaQuery } from "@/lib/hooks";
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
  /** Clicking a row anywhere follows the first link in it; only such rows light up under the pointer. */
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
  /** The rows come in the sort's order (the server sorts the whole list): a header only changes the sort. */
  presorted?: boolean;
  /** The width under which the table scrolls sideways, such as 720. */
  minWidth?: number;
  /**
   * On a phone, each row shows as a card: its first column as the title,
   * the others as label and value. False keeps the columns side by side,
   * scrolling sideways, for rows that only read compared with each other.
   */
  stack?: boolean;
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

/** Below this width, rows are stacked as cards. */
export const PHONE = "(width < 40rem)";

// A card's value takes the column's classes but not its width, and wraps.
const asValue = (className?: string) =>
  cn(className, "w-auto max-w-none min-w-0 [overflow-wrap:anywhere] whitespace-normal");

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
  presorted,
  minWidth,
  stack = true,
  className,
}: DataTableProps<T>) {
  const { t } = useTranslation();
  const phone = useMediaQuery(PHONE);
  const stacked = stack && phone;
  const [ownSort, setOwnSort] = useState<Sort | null>(defaultSort ?? null);
  const sort = sortProp === undefined ? ownSort : sortProp;
  const setSort = (s: Sort | null) => {
    if (sortProp === undefined) setOwnSort(s);
    onSortChange?.(s);
  };
  const by = sort ? columns.find((c) => c.id === sort.id) : undefined;
  const sorted =
    rows && by?.sortValue && !presorted
      ? [...rows].sort((a, b) => compare(by.sortValue!(a), by.sortValue!(b)) * (sort?.desc ? -1 : 1))
      : rows;
  const menus = rowMenu && sorted ? new Map(sorted.map((r) => [rowKey(r), rowMenu(r)])) : undefined;
  const wrapper = useRef<HTMLDivElement>(null);
  // The row the focus was last in, for when its action removes it (given
  // back, deleted): the focus then goes to the row now at its place.
  const focusedRow = useRef<{ key: string; index: number } | null>(null);
  const keys = sorted?.map(rowKey);
  useLayoutEffect(() => {
    const was = focusedRow.current;
    if (!was || !keys || keys.includes(was.key)) return;
    focusedRow.current = null;
    const standIn = () => {
      const now = document.activeElement;
      if (!now || now === document.body) rowStandIn(wrapper.current, was.index)?.focus();
    };
    standIn();
    // A menu item that removed its row: the menu gives the focus back to its gone button as it closes.
    requestAnimationFrame(standIn);
  });
  const hasMenu = menus !== undefined && [...menus.values()].some((m) => m !== null && m !== undefined && m !== false);
  const width = columns.length + (selection ? 1 : 0) + (hasMenu ? 1 : 0);
  const allSelected = Boolean(sorted?.length) && sorted!.every((r) => selection?.selected.has(rowKey(r)));
  const firstSortable = columns.find((c) => c.sortValue)?.id;
  const allLabel = selection?.allLabel ?? t("table.selectAll");
  // Safari drops a table's semantics once its display changes: the roles keep them.
  const role = (r: string) => (stacked ? r : undefined);
  const cardColumns = [selection && "auto", "minmax(0,1fr)", hasMenu && "auto"].filter(Boolean).join(" ");
  const selectAll = selection && (
    <Checkbox
      aria-label={allLabel}
      checked={allSelected}
      disabled={!sorted?.length}
      onChange={(on) => {
        const shown = new Set((sorted ?? []).map(rowKey));
        // Rows selected but not shown stay selected either way.
        selection.onChange(
          new Set(on ? [...selection.selected, ...shown] : [...selection.selected].filter((k) => !shown.has(k))),
        );
      }}
    />
  );

  let body: React.ReactNode;
  if (error) body = error;
  else if (!loading && sorted && sorted.length === 0 && empty) body = empty;
  else {
    body = (
      <ScrollRegion label={label}>
        <table
          role={role("table")}
          className={cn("w-full text-body", stacked ? "block" : "border-collapse")}
          style={minWidth && !stacked ? { minWidth } : undefined}
          aria-label={label}
          data-stacked={stacked || undefined}
        >
          <thead
            role={role("rowgroup")}
            className={cn(stacked && (firstSortable || selection ? "block border-b border-line" : "sr-only"))}
          >
            <tr
              role={role("row")}
              className={cn("label text-left", stacked && "flex flex-wrap items-center gap-x-3 gap-y-1.5 px-4 py-2")}
            >
              {selection && (
                <th
                  role={role("columnheader")}
                  scope="col"
                  className={cn(stacked ? "mr-2 block" : "w-px border-b border-line py-2 pr-1 pl-4")}
                >
                  {stacked ? (
                    <label className="flex items-center gap-2 font-normal text-fg">
                      {selectAll}
                      <span aria-hidden>{allLabel}</span>
                    </label>
                  ) : (
                    selectAll
                  )}
                </th>
              )}
              {columns.map((c, i) => {
                const active = sort?.id === c.id;
                const Icon = !active ? ArrowUpDown : sort.desc ? ArrowDown : ArrowUp;
                return (
                  <th
                    key={c.id}
                    role={role("columnheader")}
                    scope="col"
                    aria-sort={active ? (sort.desc ? "descending" : "ascending") : undefined}
                    className={cn(
                      stacked
                        ? c.sortValue
                          ? "block font-medium whitespace-nowrap"
                          : "sr-only"
                        : [
                            "border-b border-line px-3 py-2 font-medium whitespace-nowrap",
                            i === 0 && !selection && "pl-4",
                            i === columns.length - 1 && !hasMenu && "pr-4",
                            c.align === "end" && "text-right",
                          ],
                    )}
                  >
                    {stacked && c.id === firstSortable && (
                      <span aria-hidden className="mr-3 font-normal text-faint">
                        {t("table.sortBy")}
                      </span>
                    )}
                    {c.sortValue ? (
                      <button
                        type="button"
                        onClick={() => setSort(active && sort.desc ? null : { id: c.id, desc: active })}
                        className={cn(
                          "-mx-1 inline-flex items-center gap-1 rounded-sm px-1 hover:text-fg",
                          active && "text-fg",
                          stacked && "min-h-6",
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
                <th
                  role={role("columnheader")}
                  scope="col"
                  className={stacked ? "sr-only" : "w-px border-b border-line py-2 pr-3 pl-1"}
                >
                  <span className="sr-only">{t("table.actions")}</span>
                </th>
              )}
            </tr>
          </thead>
          <tbody role={role("rowgroup")} className={cn(stacked && "block")}>
            {loading && !sorted?.length
              ? [0, 1, 2].map((r) => (
                  <tr
                    key={r}
                    aria-hidden
                    className={cn(stacked && "block border-b border-line px-4 py-3 last:border-b-0")}
                  >
                    {Array.from({ length: stacked ? 2 : width }, (_, c) => (
                      <td
                        key={c}
                        className={cn(stacked ? "block py-1" : "border-b border-line px-3 py-3 first:pl-4 last:pr-4")}
                      >
                        <span
                          className={cn("block h-3 animate-pulse rounded-sm bg-sunken", c === 0 ? "w-3/4" : "w-1/2")}
                        />
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
                      role={role("row")}
                      // The row's menu too: it is portalled out of the row, its focus bubbling here.
                      onFocus={(e) => {
                        const row = e.currentTarget;
                        focusedRow.current = { key, index: [...row.parentElement!.children].indexOf(row) };
                      }}
                      className={cn(
                        "border-b border-line last:border-b-0",
                        stacked && "grid gap-x-3 gap-y-1 px-4 py-3",
                        selection?.selected.has(key) && "bg-accent-soft/40",
                        rowLinks && "cursor-pointer hover:bg-sunken/60",
                      )}
                      style={stacked ? { gridTemplateColumns: cardColumns } : undefined}
                      onClick={
                        rowLinks
                          ? (e) => {
                              // The row menu is portalled out of the row, but React still bubbles its clicks here.
                              if (!e.currentTarget.contains(e.target as Node)) return;
                              if ((e.target as Element).closest(interactive)) return;
                              const link = e.currentTarget.querySelector<HTMLAnchorElement>("a[href]");
                              // What it opens gives the focus back to the link, not to the page.
                              link?.focus({ preventScroll: true });
                              link?.click();
                            }
                          : undefined
                      }
                    >
                      {selection && (
                        <td
                          role={role("cell")}
                          className={stacked ? "pt-0.5" : "w-px py-2.5 pr-1 pl-4 align-middle"}
                          style={stacked ? { gridRow: 1, gridColumn: 1 } : undefined}
                        >
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
                      {columns.map((c, i) =>
                        stacked ? (
                          <StackedCell key={c.id} column={c} first={i === 0} indent={Boolean(selection)}>
                            {c.cell(row)}
                          </StackedCell>
                        ) : (
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
                        ),
                      )}
                      {hasMenu && (
                        <td
                          role={role("cell")}
                          className={
                            stacked ? "-my-1 -mr-1.5 self-start" : "w-px py-1.5 pr-3 pl-1 text-right align-middle"
                          }
                          style={stacked ? { gridRow: 1, gridColumn: -2 } : undefined}
                        >
                          {menu ? (
                            <Menu>
                              <MenuTrigger asChild>
                                <IconButton label={rowMenuLabel?.(row) ?? t("table.actions")}>
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
            {t("loading")}
          </span>
        )}
      </ScrollRegion>
    );
  }

  return (
    <div
      ref={wrapper}
      data-table=""
      className={cn("min-w-0", className)}
      onBlur={(e) => {
        // Kept while the focus is in what a row opened (its menu, a dialog), or falls to the page as its row goes.
        const to = e.relatedTarget;
        if (to && !wrapper.current?.contains(to) && !to.closest('[role="menu"], [role="dialog"], [role="alertdialog"]'))
          focusedRow.current = null;
      }}
    >
      {toolbar && <div className="flex flex-wrap items-center gap-2 border-b border-line px-4 py-2.5">{toolbar}</div>}
      {body}
    </div>
  );
}

/** A cell of a row shown as a card: the first column is its title, the others its column's label and value. */
function StackedCell<T>({
  column,
  first,
  indent,
  children,
}: {
  column: Column<T>;
  first: boolean;
  indent: boolean;
  children: React.ReactNode;
}) {
  const labelled = !first && column.header !== "" && column.header !== null && column.header !== undefined;
  return (
    <td
      // eslint-disable-next-line jsx-a11y/no-interactive-element-to-noninteractive-role -- see role() in DataTable
      role="cell"
      className={
        first ? asValue(column.className) : "grid grid-cols-[minmax(0,2fr)_minmax(0,3fr)] items-baseline gap-x-3"
      }
      style={first ? { gridRow: 1 } : { gridColumn: indent ? "2 / -1" : "1 / -1" }}
    >
      {first ? (
        children
      ) : (
        <>
          {/* The column's header names the cell for screen readers already. */}
          {labelled && (
            <span aria-hidden className="label">
              {column.header}
            </span>
          )}
          <div className={cn(asValue(column.className), !labelled && "col-span-2")}>{children}</div>
        </>
      )}
    </td>
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
