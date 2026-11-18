// A choice found by typing: the field's value source is searched with what
// is typed (F1.2); people are found the same way in rvav.users.
import { useState } from "react";
import { Input } from "@/components/ui/input";
import type { Option } from "@/lib/api/types";
import { useDebounced } from "@/lib/hooks";
import { cn } from "@/lib/utils";
import { SourceFailed } from "./failed";
import { sourceFailed, useFieldValues } from "./useFieldValues";
import type { WidgetProps } from "./widgets";

/** The fewest letters a search needs. */
const least = 2;
/** The most matches listed: past that, the requester types more. */
const listed = 20;

/** How a search box speaks of what it finds. */
export interface SearchWords {
  /** The end of the list's ID. */
  list: string;
  /** The list's name, after the field's label. */
  listName: string;
  placeholder: string;
  none: string;
  found: (n: number) => string;
  lookupFailed: string;
}

const matchWords: SearchWords = {
  list: "matches",
  listName: "matches",
  placeholder: "Type to search",
  none: "No matches.",
  found: (n) => (n === 1 ? "1 match: press Enter to pick it." : `${n} matches: use the arrow keys, then Enter.`),
  lookupFailed: "Couldn't look up this choice.",
};

/**
 * A combobox: typing searches the field's source, the arrow keys move in
 * what it finds, Enter picks, Escape closes. The value is the option's.
 */
export function SearchBox({
  id,
  name,
  ui,
  value,
  values,
  onChange,
  onBlur,
  ctx,
  invalid,
  describedBy,
  readOnly,
  words,
}: WidgetProps & { words: SearchWords }) {
  const picked = typeof value === "string" && value !== "" ? value : undefined;
  // null: the box shows the option picked; otherwise what is typed.
  const [text, setText] = useState<string | null>(null);
  const [open, setOpen] = useState(false);
  const [active, setActive] = useState(0);
  const [chosen, setChosen] = useState<Option>();
  const search = useDebounced((text ?? "").trim(), 250);
  const typed = text !== null && search.length >= least;
  const searching = open && typed;
  const found = useFieldValues(name, ui, values, ctx, { text: search, enabled: searching });
  // An option given another way (a draft, Request again): searched for by its value.
  const known = chosen?.value === picked ? chosen : undefined;
  const named = useFieldValues(name, ui, values, ctx, { text: picked ?? "", enabled: Boolean(picked) && !known });
  const option = known ?? named.data?.options?.find((o) => o.value === picked);
  const all = searching ? (found.data?.options ?? []) : [];
  const options = all.slice(0, listed);
  const listId = `${id}-${words.list}`;
  const statusId = `${listId}-status`;
  // A value given another way that the source couldn't name: the box shows the value it holds.
  const unnamed = Boolean(picked) && !option && sourceFailed(named) && text === null;
  const failId = `${id}-source`;
  // Said while the text stays, focus elsewhere or not: Try again takes the focus.
  const searchFailed = typed && sourceFailed(found);
  const searchFailId = `${id}-search-failed`;
  const expanded = options.length > 0;

  const pick = (o: Option) => {
    if (o.disabled) return;
    setChosen(o);
    onChange(o.value);
    setText(null);
    setOpen(false);
  };
  const onKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (e.key === "ArrowDown" || e.key === "ArrowUp") {
      e.preventDefault();
      setOpen(true);
      if (options.length > 0) {
        setActive((a) =>
          e.key === "ArrowDown" ? (a + 1) % options.length : (a - 1 + options.length) % options.length,
        );
      }
    } else if (e.key === "Enter" && expanded) {
      e.preventDefault();
      const o = options[Math.min(active, options.length - 1)];
      if (o) pick(o);
    } else if (e.key === "Escape" && (open || text !== null)) {
      e.preventDefault();
      setOpen(false);
      if (picked) setText(null);
    }
  };

  const status = !searching
    ? ""
    : searchFailed
      ? "Couldn't search."
      : !found.data || found.isFetching
        ? "Searching…"
        : all.length === 0
          ? words.none
          : all.length > listed
            ? `${all.length} found, the first ${listed} listed: type more to narrow them down.`
            : words.found(all.length);

  return (
    <div className="relative flex flex-col gap-1">
      <Input
        id={id}
        role="combobox"
        aria-expanded={expanded}
        aria-controls={listId}
        aria-autocomplete="list"
        aria-activedescendant={expanded ? `${listId}-${Math.min(active, options.length - 1)}` : undefined}
        aria-invalid={invalid || undefined}
        aria-describedby={[
          describedBy,
          option?.meta && text === null && `${id}-meta`,
          unnamed && failId,
          searchFailed && searchFailId,
          statusId,
        ]
          .filter(Boolean)
          .join(" ")}
        value={text ?? option?.label ?? (picked && named.isFetching && !unnamed ? "" : (picked ?? ""))}
        placeholder={picked && !option ? "Loading…" : words.placeholder}
        autoComplete="off"
        readOnly={readOnly}
        onChange={(e) => {
          setText(e.target.value);
          setOpen(true);
          setActive(0);
          if (picked) onChange(undefined);
        }}
        onKeyDown={onKeyDown}
        onBlur={() => {
          setOpen(false);
          if (text?.trim() === "") setText(null);
          onBlur();
        }}
      />
      {unnamed && (
        <SourceFailed
          id={failId}
          message={words.lookupFailed}
          onRetry={() => void named.refetch()}
          retrying={named.isFetching}
          focusTo={() => document.getElementById(id)}
        />
      )}
      {searchFailed && (
        <SourceFailed
          id={searchFailId}
          message="Couldn't search."
          onRetry={() => {
            setOpen(true);
            void found.refetch();
          }}
          retrying={found.isFetching}
          focusTo={() => document.getElementById(id)}
        />
      )}
      {option?.meta && text === null && (
        <span id={`${id}-meta`} className="text-secondary text-muted">
          {option.meta}
        </span>
      )}
      <ul
        id={listId}
        role="listbox"
        aria-label={`${ui.label}: ${words.listName}`}
        hidden={!expanded}
        className="absolute top-full z-10 mt-1 flex max-h-64 w-full flex-col overflow-y-auto rounded-md border border-line bg-surface shadow-pop"
      >
        {options.map((o, i) => (
          <li
            key={o.value}
            id={`${listId}-${i}`}
            role="option"
            aria-selected={i === active}
            aria-disabled={o.disabled || undefined}
            tabIndex={-1}
            className={cn(
              "px-3 py-1.5 text-body",
              o.disabled ? "text-faint" : "cursor-pointer hover:bg-sunken",
              i === active && "bg-sunken",
            )}
            onMouseDown={(e) => e.preventDefault()}
            onClick={() => pick(o)}
            onKeyDown={(e) => e.key === "Enter" && pick(o)}
          >
            {o.label}{" "}
            {(o.disabled ? o.disabled_reason : o.meta) && (
              <span className="text-secondary text-faint">{o.disabled ? o.disabled_reason : o.meta}</span>
            )}
          </li>
        ))}
      </ul>
      <p id={statusId} role="status" className={cn("text-caption text-faint", (!status || searchFailed) && "sr-only")}>
        {status}
      </p>
    </div>
  );
}

/** A search field: its value source is searched with what is typed. */
export function SearchWidget(props: WidgetProps) {
  return (
    <SearchBox {...props} words={{ ...matchWords, placeholder: props.ui.placeholder ?? matchWords.placeholder }} />
  );
}
