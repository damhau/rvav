// A person picked by searching their name or email (rvav.users).
import { useState } from "react";
import { Input } from "@/components/ui/input";
import type { Option } from "@/lib/api/types";
import { useDebounced } from "@/lib/hooks";
import { cn } from "@/lib/utils";
import { useFieldValues } from "./useFieldValues";
import type { WidgetProps } from "./widgets";

/** The fewest letters a search needs. */
const least = 2;

/**
 * A combobox: typing searches people, the arrow keys move in what it finds,
 * Enter picks, Escape closes. The value is the person's ID.
 */
export function UserWidget({
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
}: WidgetProps) {
  const picked = typeof value === "string" && value !== "" ? value : undefined;
  // null: the box shows the person picked; otherwise what is typed.
  const [text, setText] = useState<string | null>(null);
  const [open, setOpen] = useState(false);
  const [active, setActive] = useState(0);
  const [chosen, setChosen] = useState<Option>();
  const search = useDebounced((text ?? "").trim(), 250);
  const searching = open && text !== null && search.length >= least;
  const found = useFieldValues(name, ui, values, ctx, { text: search, enabled: searching });
  // Names a person given another way (a draft, Request again): the source finds an ID too.
  const known = chosen?.value === picked ? chosen : undefined;
  const named = useFieldValues(name, ui, values, ctx, { text: picked ?? "", enabled: Boolean(picked) && !known });
  const person = known ?? named.data?.options?.find((o) => o.value === picked);
  const options = searching ? (found.data?.options ?? []) : [];
  const listId = `${id}-people`;
  const statusId = `${id}-people-status`;
  const expanded = options.length > 0;

  const pick = (o: Option) => {
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
    : found.isError
      ? "Couldn't search. Try again in a moment."
      : !found.data || found.isFetching
        ? "Searching…"
        : options.length === 0
          ? "Nobody found."
          : options.length === 1
            ? "1 person found: press Enter to pick them."
            : `${options.length} people found: use the arrow keys, then Enter.`;

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
        aria-describedby={[describedBy, person && `${id}-person`, statusId].filter(Boolean).join(" ")}
        value={text ?? person?.label ?? (picked && named.isFetching ? "" : (picked ?? ""))}
        placeholder={picked && !person ? "Loading…" : "Name or email"}
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
      {person?.meta && text === null && (
        <span id={`${id}-person`} className="text-secondary text-muted">
          {person.meta}
        </span>
      )}
      <ul
        id={listId}
        role="listbox"
        aria-label={`${ui.label}: people found`}
        hidden={!expanded}
        className="absolute top-full z-10 mt-1 flex max-h-64 w-full flex-col overflow-y-auto rounded-md border border-line bg-surface shadow-pop"
      >
        {options.map((o, i) => (
          <li
            key={o.value}
            id={`${listId}-${i}`}
            role="option"
            aria-selected={i === active}
            tabIndex={-1}
            className={cn("cursor-pointer px-3 py-1.5 text-body hover:bg-sunken", i === active && "bg-sunken")}
            onMouseDown={(e) => e.preventDefault()}
            onClick={() => pick(o)}
            onKeyDown={(e) => e.key === "Enter" && pick(o)}
          >
            {o.label} {o.meta && <span className="text-secondary text-faint">{o.meta}</span>}
          </li>
        ))}
      </ul>
      <p id={statusId} role="status" className={cn("text-caption text-faint", !status && "sr-only")}>
        {status}
      </p>
    </div>
  );
}
