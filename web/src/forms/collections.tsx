// Widgets whose value is a collection: tags (a list of strings), key and
// value pairs (an object of strings), tables (a list of rows) and objects
// (one row, its sub-fields labelled).
import { Plus, Trash2, X } from "lucide-react";
import { useEffect, useState } from "react";
import { Button, IconButton } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import type { UiColumn } from "@/lib/api/types";
import { cn } from "@/lib/utils";
import { SourceFailed } from "./failed";
import { sourceFailed, useFieldValues } from "./useFieldValues";
import type { JsonSchemaProp, WidgetProps } from "./widgets";

const str = (v: unknown) => (typeof v === "string" ? v : typeof v === "number" ? String(v) : "");
const isRecord = (v: unknown): v is Record<string, unknown> => typeof v === "object" && v !== null && !Array.isArray(v);

/** Moves the focus once the element exists: a row just added, or what is left after one is removed. */
function useFocusLater() {
  const [target, setTarget] = useState<string>();
  useEffect(() => {
    if (!target) return;
    document.getElementById(target)?.focus();
    setTarget(undefined);
  }, [target]);
  return setTarget;
}

/** Tags typed as chips, Enter or a comma ending each; suggested by the field's options or value source. */
export function TagsWidget({
  id,
  name,
  ui,
  schema,
  value,
  values,
  onChange,
  onBlur,
  ctx,
  invalid,
  describedBy,
  readOnly,
}: WidgetProps) {
  const tags = Array.isArray(value) ? value.filter((v): v is string => typeof v === "string") : [];
  const [text, setText] = useState("");
  const [note, setNote] = useState("");
  const sourced = useFieldValues(name, ui, values, ctx);
  const suggestions = (ui.value_source ? (sourced.data?.options ?? []) : (ui.options ?? [])).filter(
    (o) => !tags.includes(o.value),
  );
  const allowed = schema.items?.enum?.map(String);
  const full = schema.maxItems !== undefined && tags.length >= schema.maxItems;
  const focusLater = useFocusLater();
  const noteId = `${id}-tags-note`;

  const add = (raw: string) => {
    const tag = raw.trim();
    if (!tag) return setText("");
    if (tags.includes(tag)) return setNote(`${tag} is already there.`);
    if (allowed && !allowed.includes(tag)) return setNote(`${tag} isn't one this field takes: pick a suggestion.`);
    onChange([...tags, tag]);
    setText("");
    setNote("");
  };
  const remove = (tag: string) => {
    const next = tags.filter((t) => t !== tag);
    onChange(next.length ? next : undefined);
    setNote(`Removed ${tag}.`);
    focusLater(id);
  };

  return (
    <div className="flex flex-col gap-1">
      <div
        className={cn(
          "flex min-h-8 flex-wrap items-center gap-1 rounded-md border border-line-strong bg-surface px-1.5 py-1 focus-within:border-accent focus-within:ring-2 focus-within:ring-accent/25",
          invalid && "border-bad",
          readOnly && "opacity-60",
        )}
      >
        {tags.length > 0 && (
          <ul className="flex flex-wrap gap-1" aria-label={`${ui.label}, ${tags.length} added`}>
            {tags.map((t) => (
              <li
                key={t}
                className="inline-flex h-6 items-center gap-0.5 rounded-sm bg-sunken pr-0.5 pl-1.5 font-mono text-secondary"
              >
                {t}
                {!readOnly && (
                  <IconButton label={`Remove ${t}`} className="size-5" onClick={() => remove(t)}>
                    <X aria-hidden />
                  </IconButton>
                )}
              </li>
            ))}
          </ul>
        )}
        <input
          id={id}
          name={name}
          value={text}
          list={suggestions.length ? `${id}-suggestions` : undefined}
          disabled={readOnly || full}
          autoComplete="off"
          placeholder={full ? `${schema.maxItems} at most` : (ui.placeholder ?? "Type, then Enter")}
          aria-invalid={invalid || undefined}
          aria-describedby={
            [
              describedBy,
              note ? noteId : undefined,
              ui.value_source && sourceFailed(sourced) ? `${id}-source` : undefined,
            ]
              .filter(Boolean)
              .join(" ") || undefined
          }
          className="h-6 min-w-32 flex-1 bg-transparent px-1 text-body text-fg outline-none placeholder:text-faint"
          onChange={(e) => {
            const v = e.target.value;
            if (v.endsWith(",")) add(v.slice(0, -1));
            else setText(v);
          }}
          onKeyDown={(e) => {
            if (e.key === "Enter") {
              e.preventDefault();
              add(text);
            } else if (e.key === "Backspace" && text === "" && tags.length > 0) remove(tags.at(-1)!);
          }}
          onBlur={() => {
            if (text.trim()) add(text);
            onBlur();
          }}
        />
      </div>
      {suggestions.length > 0 && (
        <datalist id={`${id}-suggestions`}>
          {suggestions.map((o) => (
            <option key={o.value} value={o.value}>
              {o.label}
            </option>
          ))}
        </datalist>
      )}
      {ui.value_source && sourceFailed(sourced) && (
        <SourceFailed
          id={`${id}-source`}
          message="Couldn't load the suggestions."
          onRetry={() => void sourced.refetch()}
          retrying={sourced.isFetching}
          focusTo={() => document.getElementById(id)}
        />
      )}
      <p id={noteId} role="status" className="text-secondary text-muted empty:hidden">
        {note}
      </p>
    </div>
  );
}

interface Pair {
  key: string;
  value: string;
}

const pairsOf = (v: unknown): Pair[] =>
  isRecord(v) ? Object.entries(v).map(([key, value]) => ({ key, value: str(value) })) : [];

/** The object pairs make: each key once, the rows with a repeated key left out until it changes. */
function objectOf(rows: Pair[]): Record<string, string> {
  const out: Record<string, string> = {};
  for (const r of rows) {
    const key = r.key.trim();
    if (key && !(key in out)) out[key] = r.value;
  }
  return out;
}

/** Key and value pairs, as an object of strings: a key can't be given twice. */
export function KeyValueWidget({ id, value, onChange, onBlur, invalid, describedBy, readOnly }: WidgetProps) {
  const [rows, setRows] = useState<Pair[]>(() => pairsOf(value));
  const [sent, setSent] = useState(() => JSON.stringify(objectOf(pairsOf(value))));
  const focusLater = useFocusLater();
  // A value from outside (initial values, a reset) replaces the rows.
  const incoming = JSON.stringify(isRecord(value) ? value : {});
  if (incoming !== sent) {
    setSent(incoming);
    setRows(pairsOf(value));
  }
  const update = (next: Pair[]) => {
    setRows(next);
    const obj = objectOf(next);
    setSent(JSON.stringify(obj));
    onChange(Object.keys(obj).length ? obj : undefined);
  };
  const keyOf = (i: number) => rows[i]!.key.trim();
  const repeated = (i: number) => keyOf(i) !== "" && rows.findIndex((_, j) => keyOf(j) === keyOf(i)) < i;

  return (
    <div
      role="group"
      id={id}
      aria-labelledby={`${id}-label`}
      aria-describedby={describedBy}
      className="flex flex-col gap-2"
    >
      {rows.length > 0 && (
        <ul className="flex flex-col gap-1.5">
          {rows.map((r, i) => {
            const dup = repeated(i);
            const errId = `${id}-${i}-dup`;
            return (
              <li key={i} className="flex flex-col gap-0.5">
                <div className="grid grid-cols-[minmax(0,2fr)_minmax(0,3fr)_auto] items-center gap-1.5">
                  <Input
                    id={`${id}-${i}-key`}
                    aria-label={`Key ${i + 1}`}
                    value={r.key}
                    readOnly={readOnly}
                    placeholder="Key"
                    className={cn("font-mono text-secondary", (dup || invalid) && "border-bad")}
                    aria-invalid={dup || invalid || undefined}
                    aria-describedby={dup ? errId : undefined}
                    onChange={(e) => update(rows.map((x, j) => (j === i ? { ...x, key: e.target.value } : x)))}
                    onBlur={onBlur}
                  />
                  <Input
                    id={`${id}-${i}-value`}
                    aria-label={`Value ${i + 1}`}
                    value={r.value}
                    readOnly={readOnly}
                    placeholder="Value"
                    className={cn(invalid && "border-bad")}
                    onChange={(e) => update(rows.map((x, j) => (j === i ? { ...x, value: e.target.value } : x)))}
                    onBlur={onBlur}
                  />
                  <IconButton
                    id={`${id}-${i}-remove`}
                    label={`Remove pair ${i + 1}`}
                    disabled={readOnly}
                    onClick={() => {
                      update(rows.filter((_, j) => j !== i));
                      focusLater(rows.length > 1 ? `${id}-${Math.min(i, rows.length - 2)}-key` : `${id}-add`);
                    }}
                  >
                    <Trash2 aria-hidden />
                  </IconButton>
                </div>
                {dup && (
                  <p id={errId} className="text-secondary text-bad">
                    {keyOf(i)} is already a key above: change it or remove this pair.
                  </p>
                )}
              </li>
            );
          })}
        </ul>
      )}
      <div>
        <Button
          id={`${id}-add`}
          size="sm"
          disabled={readOnly}
          onClick={() => {
            setRows([...rows, { key: "", value: "" }]);
            focusLater(`${id}-${rows.length}-key`);
          }}
        >
          <Plus aria-hidden />
          Add a pair
        </Button>
      </div>
    </div>
  );
}

type Row = Record<string, unknown>;

function Cell({
  id,
  column,
  schema,
  row,
  label,
  readOnly,
  onChange,
}: {
  id: string;
  column: UiColumn;
  schema: JsonSchemaProp;
  row: Row;
  /** The control's name when no label element names it (a table's cells). */
  label?: string;
  readOnly: boolean;
  onChange: (v: unknown) => void;
}) {
  const v = row[column.key];
  switch (column.widget) {
    case "toggle":
      return (
        <Checkbox
          id={id}
          aria-label={label}
          checked={v === true}
          disabled={readOnly}
          onChange={(on) => onChange(on || undefined)}
        />
      );
    case "select":
      return (
        <Select
          id={id}
          aria-label={label}
          value={str(v)}
          disabled={readOnly}
          onChange={onChange}
          options={column.options ?? (schema.enum ?? []).map((e) => ({ value: String(e), label: String(e) }))}
        />
      );
    case "number":
      return (
        <Input
          id={id}
          type="number"
          aria-label={label}
          value={str(v)}
          readOnly={readOnly}
          min={schema.minimum}
          max={schema.maximum}
          step={schema.multipleOf ?? (schema.type === "number" ? "any" : undefined)}
          className="tnum"
          onChange={(e) => onChange(e.target.value === "" ? undefined : Number(e.target.value))}
        />
      );
    default:
      return (
        <Input
          id={id}
          aria-label={label}
          value={str(v)}
          readOnly={readOnly}
          maxLength={schema.maxLength}
          onChange={(e) => onChange(e.target.value || undefined)}
        />
      );
  }
}

/** One object, each sub-field (ui.columns) one of its properties. */
export function ObjectWidget({ id, ui, schema, value, onChange, invalid, describedBy, readOnly }: WidgetProps) {
  const obj = isRecord(value) ? value : {};
  const props = schema.properties ?? {};
  const required = schema.required ?? [];
  const set = (key: string, v: unknown) => {
    const next = { ...obj };
    if (v === undefined) delete next[key];
    else next[key] = v;
    onChange(Object.keys(next).length ? next : undefined);
  };
  return (
    <div
      role="group"
      id={id}
      aria-labelledby={`${id}-label`}
      aria-describedby={describedBy}
      className={cn("grid grid-cols-1 gap-3 rounded-md border border-line p-3 sm:grid-cols-2", invalid && "border-bad")}
    >
      {(ui.columns ?? []).map((c) => {
        const cellId = `${id}-${c.key}`;
        const cell = (
          <Cell
            id={cellId}
            column={c}
            schema={props[c.key] ?? {}}
            row={obj}
            readOnly={readOnly}
            onChange={(v) => set(c.key, v)}
          />
        );
        if (c.widget === "toggle") {
          return (
            <label key={c.key} htmlFor={cellId} className="flex items-center gap-2 self-end py-1.5 text-body">
              {cell}
              {c.label}
            </label>
          );
        }
        return (
          <div key={c.key} className="flex min-w-0 flex-col gap-1">
            <div className="flex items-baseline justify-between gap-2">
              <label htmlFor={cellId} className="text-secondary font-medium">
                {c.label}
              </label>
              {!required.includes(c.key) && <span className="text-caption text-faint">Optional</span>}
            </div>
            {cell}
          </div>
        );
      })}
    </div>
  );
}

/** A table of rows, each cell one of the row's properties: a list of objects, from minItems to maxItems rows. */
export function DatagridWidget({ id, ui, schema, value, onChange, describedBy, readOnly }: WidgetProps) {
  const rows = Array.isArray(value) ? value.filter(isRecord) : [];
  const columns = ui.columns ?? [];
  const cells = schema.items?.properties ?? {};
  const min = schema.minItems ?? 0;
  const max = schema.maxItems;
  const focusLater = useFocusLater();
  const set = (next: Row[]) => onChange(next.length ? next : undefined);
  const setCell = (i: number, key: string, v: unknown) =>
    set(
      rows.map((r, j) => {
        if (j !== i) return r;
        const next = { ...r };
        if (v === undefined) delete next[key];
        else next[key] = v;
        return next;
      }),
    );
  const cellId = (i: number, key: string) => `${id}-${i}-${key}`;
  const first = columns[0]?.key ?? "";

  return (
    <div
      role="group"
      id={id}
      aria-labelledby={`${id}-label`}
      aria-describedby={describedBy}
      className="flex flex-col gap-2"
    >
      {rows.length > 0 ? (
        <div className="overflow-x-auto rounded-md border border-line">
          <table className="w-full text-body">
            <thead className="bg-sunken text-left text-secondary text-muted">
              <tr>
                {columns.map((c) => (
                  <th key={c.key} scope="col" className="px-2 py-1.5 font-medium">
                    {c.label}
                  </th>
                ))}
                <th scope="col" className="w-9">
                  <span className="sr-only">Remove</span>
                </th>
              </tr>
            </thead>
            <tbody>
              {rows.map((r, i) => (
                <tr key={i} className="border-t border-line">
                  {columns.map((c) => (
                    <td key={c.key} className="min-w-28 px-1.5 py-1 align-middle">
                      <Cell
                        id={cellId(i, c.key)}
                        column={c}
                        schema={cells[c.key] ?? {}}
                        row={r}
                        label={`${c.label}, row ${i + 1}`}
                        readOnly={readOnly}
                        onChange={(v) => setCell(i, c.key, v)}
                      />
                    </td>
                  ))}
                  <td className="px-1">
                    <IconButton
                      label={`Remove row ${i + 1}`}
                      disabled={readOnly || rows.length <= min}
                      onClick={() => {
                        set(rows.filter((_, j) => j !== i));
                        focusLater(rows.length > 1 ? cellId(Math.min(i, rows.length - 2), first) : `${id}-add`);
                      }}
                    >
                      <Trash2 aria-hidden />
                    </IconButton>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : (
        <p className="text-secondary text-muted">No rows yet.</p>
      )}
      <div className="flex items-center gap-2">
        <Button
          id={`${id}-add`}
          size="sm"
          disabled={readOnly || (max !== undefined && rows.length >= max)}
          onClick={() => {
            set([...rows, {}]);
            focusLater(cellId(rows.length, first));
          }}
        >
          <Plus aria-hidden />
          Add a row
        </Button>
        {(min > 0 || max !== undefined) && (
          <span className="text-caption text-faint">
            {min > 0 && max !== undefined
              ? `${min} to ${max} rows`
              : min > 0
                ? `At least ${min} ${min === 1 ? "row" : "rows"}`
                : `At most ${max} rows`}
          </span>
        )}
      </div>
    </div>
  );
}
