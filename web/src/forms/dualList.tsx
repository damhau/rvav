// A dual list: options picked from one list into another, in an order.
import { ArrowDown, ArrowUp, ChevronLeft, ChevronRight } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { Button } from "@/components/ui/button";
import type { Option } from "@/lib/api/types";
import { cn, withUnits } from "@/lib/utils";
import { SourceFailed } from "./failed";
import { sourceFailed, useFieldValues } from "./useFieldValues";
import type { WidgetProps } from "./widgets";

function Listbox({
  id,
  labelledBy,
  options,
  active,
  onActive,
  onMove,
  onReorder,
  empty,
}: {
  id: string;
  labelledBy: string;
  options: Option[];
  active: number;
  onActive: (i: number) => void;
  onMove: (i: number) => void;
  onReorder?: (i: number, delta: -1 | 1) => void;
  empty: string;
}) {
  if (options.length === 0) {
    return (
      <p
        id={id}
        className="flex h-40 items-center justify-center rounded-md border border-dashed border-line px-3 text-secondary text-muted"
      >
        {empty}
      </p>
    );
  }
  const at = Math.min(active, options.length - 1);
  const onKeyDown = (e: React.KeyboardEvent) => {
    const last = options.length - 1;
    if (onReorder && e.altKey && (e.key === "ArrowUp" || e.key === "ArrowDown")) {
      e.preventDefault();
      onReorder(at, e.key === "ArrowUp" ? -1 : 1);
      return;
    }
    const next = { ArrowDown: Math.min(at + 1, last), ArrowUp: Math.max(at - 1, 0), Home: 0, End: last }[e.key];
    if (next !== undefined) {
      e.preventDefault();
      onActive(next);
    } else if (e.key === "Enter" || e.key === " ") {
      e.preventDefault();
      onMove(at);
    }
  };
  return (
    <ul
      id={id}
      role="listbox"
      tabIndex={0}
      aria-labelledby={labelledBy}
      aria-activedescendant={`${id}-${at}`}
      onKeyDown={onKeyDown}
      className="flex h-40 flex-col overflow-y-auto rounded-md border border-line-strong bg-surface py-1 focus-visible:border-accent focus-visible:ring-2 focus-visible:ring-accent/25 focus-visible:outline-none"
    >
      {options.map((o, i) => (
        <li
          key={o.value}
          id={`${id}-${i}`}
          role="option"
          aria-selected={i === at}
          className={cn(
            "flex cursor-pointer items-baseline justify-between gap-2 px-2.5 py-1 text-body",
            i === at ? "bg-accent-soft" : "hover:bg-sunken",
          )}
          onMouseDown={() => onActive(i)}
          onDoubleClick={() => onMove(i)}
        >
          <span className="truncate">{o.label}</span>
          {o.meta && <span className="shrink-0 font-mono text-caption text-faint">{withUnits(o.meta)}</span>}
        </li>
      ))}
    </ul>
  );
}

/**
 * Two lists: what is available and what is chosen, in order. Enter (or a
 * double click) moves the active option to the other list, Alt and the arrow
 * keys move a chosen one up or down; the buttons do the same. The value is
 * the chosen values, in their order.
 */
export function DualListWidget({ id, name, ui, value, values, onChange, ctx, describedBy, readOnly }: WidgetProps) {
  const { t } = useTranslation("forms");
  const sourced = useFieldValues(name, ui, values, ctx);
  const chosen = Array.isArray(value) ? value.map(String) : [];
  const failed = Boolean(ui.value_source) && sourceFailed(sourced);
  const loaded = ui.value_source ? sourced.data?.options : (ui.options ?? []);
  const options: Option[] = loaded ?? [];
  const known = (v: string) => options.find((o) => o.value === v) ?? { value: v, label: v };
  const available = options.filter((o) => !chosen.includes(o.value) && !o.disabled);
  const picked = chosen.map(known);
  const [activeAvailable, setActiveAvailable] = useState(0);
  const [activeChosen, setActiveChosen] = useState(0);
  const [note, setNote] = useState("");
  const set = (next: string[]) => onChange(next.length ? next : undefined);

  const offered = sourced.data?.options;
  const onChangeRef = useRef(onChange);
  onChangeRef.current = onChange;
  const chosenKey = JSON.stringify(chosen);
  useEffect(() => {
    if (!offered) return;
    const before = JSON.parse(chosenKey) as string[];
    const kept = before.filter((v) => offered.some((o) => o.value === v));
    if (kept.length < before.length) onChangeRef.current(kept.length ? kept : undefined);
  }, [offered, chosenKey]);

  const add = (i: number) => {
    const o = available[i];
    if (!o || readOnly) return;
    set([...chosen, o.value]);
    setNote(t("dual.added", { name: o.label, at: chosen.length + 1, total: chosen.length + 1 }));
  };
  const remove = (i: number) => {
    const o = picked[i];
    if (!o || readOnly) return;
    set(chosen.filter((v) => v !== o.value));
    setNote(t("removed", { name: o.label }));
  };
  const reorder = (i: number, delta: -1 | 1) => {
    const j = i + delta;
    if (readOnly || j < 0 || j >= chosen.length) return;
    const next = chosen.slice();
    [next[i], next[j]] = [next[j]!, next[i]!];
    set(next);
    setActiveChosen(j);
    setNote(t("dual.moved", { name: picked[i]!.label, at: j + 1, total: chosen.length }));
  };

  if (ui.value_source && !sourced.data && !failed) {
    return (
      <p className="text-secondary text-muted" aria-describedby={describedBy}>
        {t("source.loading")}
      </p>
    );
  }
  const failId = `${id}-source`;
  const availableId = `${id}-available`;
  const chosenId = `${id}-chosen`;
  const at = Math.min(activeChosen, Math.max(picked.length - 1, 0));
  return (
    <div
      role="group"
      id={id}
      aria-labelledby={`${id}-label`}
      aria-describedby={[describedBy, failed && failId].filter(Boolean).join(" ") || undefined}
      className="flex flex-col gap-1.5"
    >
      <div className="grid grid-cols-1 gap-2 sm:grid-cols-[minmax(0,1fr)_auto_minmax(0,1fr)] sm:items-center">
        <div className="flex min-w-0 flex-col gap-1">
          <span id={`${availableId}-label`} className="text-caption text-muted">
            {t("dual.available")}
          </span>
          <Listbox
            id={availableId}
            labelledBy={`${id}-label ${availableId}-label`}
            options={available}
            active={activeAvailable}
            onActive={setActiveAvailable}
            onMove={add}
            empty={failed ? t("source.notLoaded") : options.length === 0 ? t("source.empty") : t("dual.allChosen")}
          />
        </div>
        <div className="flex gap-1.5 sm:flex-col">
          <Button
            size="sm"
            disabled={readOnly || available.length === 0}
            onClick={() => add(Math.min(activeAvailable, available.length - 1))}
          >
            {t("dual.add")}
            <ChevronRight aria-hidden />
          </Button>
          <Button size="sm" disabled={readOnly || picked.length === 0} onClick={() => remove(at)}>
            <ChevronLeft aria-hidden />
            {t("dual.remove")}
          </Button>
        </div>
        <div className="flex min-w-0 flex-col gap-1">
          <span id={`${chosenId}-label`} className="text-caption text-muted">
            {t("dual.chosen")}
          </span>
          <Listbox
            id={chosenId}
            labelledBy={`${id}-label ${chosenId}-label`}
            options={picked}
            active={activeChosen}
            onActive={setActiveChosen}
            onMove={remove}
            onReorder={reorder}
            empty={t("dual.noneChosen")}
          />
        </div>
      </div>
      <div className="flex flex-wrap items-center gap-1.5 sm:justify-end">
        <Button size="sm" disabled={readOnly || at <= 0} onClick={() => reorder(at, -1)}>
          <ArrowUp aria-hidden />
          {t("dual.moveUp")}
        </Button>
        <Button size="sm" disabled={readOnly || at >= picked.length - 1} onClick={() => reorder(at, 1)}>
          <ArrowDown aria-hidden />
          {t("dual.moveDown")}
        </Button>
      </div>
      {failed && (
        <SourceFailed
          id={failId}
          message={t("source.failed")}
          onRetry={() => void sourced.refetch()}
          retrying={sourced.isFetching}
          focusTo={() => document.getElementById(availableId)}
        />
      )}
      <p role="status" className="sr-only">
        {note}
      </p>
    </div>
  );
}
