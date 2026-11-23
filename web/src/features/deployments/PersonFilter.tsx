import { useQuery } from "@tanstack/react-query";
import { X } from "lucide-react";
import { useId, useState } from "react";
import { useTranslation } from "react-i18next";
import { IconButton } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import type { UserRef } from "@/lib/api/types";
import { useDebounced } from "@/lib/hooks";
import { adm } from "@/lib/query";
import { cn } from "@/lib/utils";

/**
 * A list filter on a person, found by name or email: typing searches, the
 * arrow keys move in what it finds, Enter picks, Escape closes. The value is
 * the person's ID, which the address keeps; the filter names them.
 */
export function PersonFilter({
  label,
  value,
  onChange,
  className,
}: {
  /** Such as "Owner". */
  label: string;
  value: string | undefined;
  onChange: (id: string | undefined) => void;
  className?: string;
}) {
  const { t } = useTranslation(["deployments", "common"]);
  const id = useId();
  const [text, setText] = useState<string | null>(null);
  const [open, setOpen] = useState(false);
  const [active, setActive] = useState(0);
  const [chosen, setChosen] = useState<UserRef>();
  const search = useDebounced((text ?? "").trim(), 250);
  const searching = open && text !== null && search.length >= 2;
  const found = useQuery({ ...adm.users(search), enabled: searching });
  const known = chosen?.id === value ? chosen : undefined;
  const named = useQuery({ ...adm.users(value ?? ""), enabled: Boolean(value) && !known });
  const person = known ?? named.data?.items.find((u) => u.id === value);
  const people = searching ? (found.data?.items ?? []) : [];
  const listId = `${id}-people`;
  const expanded = people.length > 0;

  const pick = (u: UserRef) => {
    setChosen(u);
    onChange(u.id);
    setText(null);
    setOpen(false);
  };
  const status = !searching
    ? ""
    : !found.data || found.isFetching
      ? t("person.searching")
      : people.length === 0
        ? t("person.nobody")
        : t("person.found", { count: people.length });

  return (
    <span className="flex items-center gap-2">
      <label htmlFor={id} className="text-secondary text-muted">
        {label}
      </label>
      <div className={cn("relative w-48", className)}>
        <Input
          id={id}
          role="combobox"
          aria-expanded={expanded}
          aria-controls={listId}
          aria-autocomplete="list"
          aria-activedescendant={expanded ? `${listId}-${Math.min(active, people.length - 1)}` : undefined}
          aria-describedby={`${listId}-status`}
          value={text ?? person?.name ?? (value && named.isError ? value : "")}
          placeholder={value && !person && !named.isError ? t("common:loadingEllipsis") : t("person.anyone")}
          autoComplete="off"
          className={cn(value && "pr-8")}
          onChange={(e) => {
            setText(e.target.value);
            setOpen(true);
            setActive(0);
          }}
          onKeyDown={(e) => {
            if (e.key === "ArrowDown" || e.key === "ArrowUp") {
              e.preventDefault();
              setOpen(true);
              if (people.length > 0) {
                setActive((a) =>
                  e.key === "ArrowDown" ? (a + 1) % people.length : (a - 1 + people.length) % people.length,
                );
              }
            } else if (e.key === "Enter" && expanded) {
              e.preventDefault();
              const u = people[Math.min(active, people.length - 1)];
              if (u) pick(u);
            } else if (e.key === "Escape" && (open || text !== null)) {
              e.preventDefault();
              setOpen(false);
              setText(null);
            }
          }}
          onBlur={() => {
            setOpen(false);
            if (text?.trim() === "") {
              onChange(undefined);
            }
            setText(null);
          }}
        />
        {value && text === null && (
          <IconButton
            label={t("person.clear", { label: label.toLowerCase() })}
            className="absolute top-1/2 right-0.5 size-7 -translate-y-1/2"
            onClick={() => onChange(undefined)}
          >
            <X />
          </IconButton>
        )}
        <ul
          id={listId}
          role="listbox"
          aria-label={t("person.foundList", { label })}
          hidden={!expanded}
          className="absolute top-full z-10 mt-1 flex max-h-64 w-72 flex-col overflow-y-auto rounded-md border border-line bg-surface shadow-pop"
        >
          {people.map((u, i) => (
            // The search box takes the keys for the options (aria-activedescendant).
            // eslint-disable-next-line jsx-a11y/click-events-have-key-events
            <li
              key={u.id}
              id={`${listId}-${i}`}
              role="option"
              aria-selected={i === active}
              tabIndex={-1}
              className={cn("cursor-pointer px-3 py-1.5 text-body hover:bg-sunken", i === active && "bg-sunken")}
              onMouseDown={(e) => e.preventDefault()}
              onClick={() => pick(u)}
            >
              {u.name} <span className="text-secondary text-faint">{u.email}</span>
            </li>
          ))}
        </ul>
        <p id={`${listId}-status`} role="status" className="sr-only">
          {status}
        </p>
      </div>
    </span>
  );
}
