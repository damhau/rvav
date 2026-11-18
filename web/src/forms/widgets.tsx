// Widgets for the request form renderer, keyed by `ui:widget`. Each takes the
// same props so the renderer can pick one from the registry.
import { Eye, EyeOff, Info, LoaderCircle } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { IconButton } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Input, Textarea } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import type { Option, UiField, UiWidget } from "@/lib/api/types";
import { cn, localInput, withOffset, withUnits, zoneLabel } from "@/lib/utils";
import { DatagridWidget, KeyValueWidget, TagsWidget } from "./collections";
import { SourceFailed } from "./failed";
import { UserWidget } from "./people";
import { sourceFailed, useApplySuggestion, useFieldValues, type FormContext } from "./useFieldValues";

export interface WidgetProps {
  id: string;
  name: string;
  ui: UiField;
  schema: JsonSchemaProp;
  value: unknown;
  values: Record<string, unknown>;
  onChange: (value: unknown) => void;
  onBlur: () => void;
  ctx: FormContext;
  invalid: boolean;
  describedBy?: string;
  readOnly: boolean;
}

export interface JsonSchemaProp {
  type?: string;
  pattern?: string;
  minLength?: number;
  maxLength?: number;
  minimum?: number;
  maximum?: number;
  multipleOf?: number;
  format?: string;
  formatMinimum?: string;
  formatMaximum?: string;
  enum?: unknown[];
  readOnly?: boolean;
  items?: JsonSchemaProp;
  properties?: Record<string, JsonSchemaProp>;
  required?: string[];
  minItems?: number;
  maxItems?: number;
}

const str = (v: unknown) => (typeof v === "string" ? v : typeof v === "number" ? String(v) : "");

function TextWidget({ id, name, ui, schema, value, onChange, onBlur, invalid, describedBy, readOnly }: WidgetProps) {
  return (
    <Input
      id={id}
      name={name}
      value={str(value)}
      placeholder={ui.placeholder}
      maxLength={schema.maxLength}
      readOnly={readOnly}
      aria-invalid={invalid || undefined}
      aria-describedby={describedBy}
      className={cn(name === "role" && "font-mono text-secondary", invalid && "border-bad")}
      onChange={(e) => onChange(e.target.value)}
      onBlur={onBlur}
    />
  );
}

/** A secret typed in: hidden until its button shows it, and never offered to the browser's autofill. */
function SecretWidget({ id, name, ui, schema, value, onChange, onBlur, invalid, describedBy, readOnly }: WidgetProps) {
  const [shown, setShown] = useState(false);
  return (
    <div className="flex min-w-0 items-center gap-1">
      <Input
        id={id}
        name={name}
        type={shown ? "text" : "password"}
        autoComplete="new-password"
        spellCheck={false}
        value={str(value)}
        placeholder={ui.placeholder}
        maxLength={schema.maxLength}
        readOnly={readOnly}
        aria-invalid={invalid || undefined}
        aria-describedby={describedBy}
        className={cn("font-mono", invalid && "border-bad")}
        onChange={(e) => onChange(e.target.value)}
        onBlur={onBlur}
      />
      <IconButton
        label={`Show ${ui.label}`}
        aria-pressed={shown}
        aria-controls={id}
        onClick={() => setShown((s) => !s)}
      >
        {shown ? <EyeOff aria-hidden /> : <Eye aria-hidden />}
      </IconButton>
    </div>
  );
}

function TextareaWidget({
  id,
  name,
  ui,
  schema,
  value,
  onChange,
  onBlur,
  invalid,
  describedBy,
  readOnly,
}: WidgetProps) {
  return (
    <Textarea
      id={id}
      name={name}
      rows={3}
      value={str(value)}
      placeholder={ui.placeholder}
      maxLength={schema.maxLength}
      readOnly={readOnly}
      aria-invalid={invalid || undefined}
      aria-describedby={describedBy}
      className={cn(invalid && "border-bad")}
      onChange={(e) => onChange(e.target.value)}
      onBlur={onBlur}
    />
  );
}

function NumberWidget({ id, name, schema, value, onChange, onBlur, invalid, describedBy, readOnly }: WidgetProps) {
  const decimal = schema.type === "number";
  return (
    <Input
      id={id}
      name={name}
      type="number"
      inputMode={decimal ? "decimal" : "numeric"}
      min={schema.minimum}
      max={schema.maximum}
      step={schema.multipleOf ?? (decimal ? "any" : undefined)}
      value={str(value)}
      readOnly={readOnly}
      aria-invalid={invalid || undefined}
      aria-describedby={describedBy}
      className={cn("tnum w-40", invalid && "border-bad")}
      onChange={(e) => onChange(e.target.value === "" ? undefined : Number(e.target.value))}
      onBlur={onBlur}
    />
  );
}

function DateWidget({ id, name, schema, value, onChange, onBlur, invalid, describedBy, readOnly }: WidgetProps) {
  return (
    <Input
      id={id}
      name={name}
      type="date"
      min={schema.formatMinimum}
      max={schema.formatMaximum}
      value={str(value)}
      readOnly={readOnly}
      aria-invalid={invalid || undefined}
      aria-describedby={describedBy}
      className={cn("tnum w-44", invalid && "border-bad")}
      onChange={(e) => onChange(e.target.value || undefined)}
      onBlur={onBlur}
    />
  );
}

/** A local date and time, kept as ISO 8601 with the browser's offset so the server knows the moment meant. */
function DateTimeWidget({ id, name, schema, value, onChange, onBlur, invalid, describedBy, readOnly }: WidgetProps) {
  const local = localInput(str(value));
  return (
    <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
      <Input
        id={id}
        name={name}
        type="datetime-local"
        min={localInput(schema.formatMinimum)}
        max={localInput(schema.formatMaximum)}
        value={local}
        readOnly={readOnly}
        aria-invalid={invalid || undefined}
        aria-describedby={[describedBy, `${id}-zone`].filter(Boolean).join(" ")}
        className={cn("tnum w-56", invalid && "border-bad")}
        onChange={(e) => onChange(e.target.value ? withOffset(e.target.value) : undefined)}
        onBlur={onBlur}
      />
      <span id={`${id}-zone`} className="text-secondary text-muted">
        {zoneLabel(local ? new Date(local) : undefined)}
      </span>
    </div>
  );
}

function ToggleWidget({ id, ui, value, onChange, readOnly }: WidgetProps) {
  return (
    <label htmlFor={id} className="flex items-center gap-2 text-body">
      <Checkbox id={id} checked={value === true} onChange={(v) => !readOnly && onChange(v)} />
      {ui.help ?? ui.label}
    </label>
  );
}

function SegmentedWidget({ id, ui, value, onChange, readOnly }: WidgetProps) {
  const options = ui.options ?? [];
  return (
    <div
      role="radiogroup"
      aria-labelledby={`${id}-label`}
      className="flex overflow-hidden rounded-md border border-line-strong"
    >
      {options.map((o) => {
        const active = value === o.value;
        return (
          <button
            key={o.value}
            type="button"
            role="radio"
            aria-checked={active}
            disabled={readOnly || o.disabled}
            onClick={() => onChange(o.value)}
            className={cn(
              "h-8 flex-1 border-l border-line-strong px-3 font-mono text-secondary uppercase first:border-l-0",
              active ? "bg-fg font-medium text-bg" : "bg-surface text-muted hover:bg-sunken",
            )}
          >
            {o.label}
          </button>
        );
      })}
    </div>
  );
}

function RadioCardsWidget({ id, ui, value, onChange, readOnly }: WidgetProps) {
  const options = ui.options ?? [];
  return (
    <div
      role="radiogroup"
      aria-labelledby={`${id}-label`}
      className="grid grid-cols-[repeat(auto-fit,minmax(min(100%,150px),1fr))] gap-2"
    >
      {options.map((o) => {
        const active = value === o.value;
        return (
          <button
            key={o.value}
            type="button"
            role="radio"
            aria-checked={active}
            disabled={readOnly || o.disabled}
            onClick={() => onChange(o.value)}
            className={cn(
              "flex flex-col gap-1 rounded-md border px-3 py-2.5 text-left",
              active ? "border-accent bg-accent-soft" : "border-line bg-surface hover:border-line-strong",
            )}
          >
            <span className="font-semibold">{o.label}</span>
            {o.meta && <span className="font-mono text-secondary text-muted">{withUnits(o.meta)}</span>}
          </button>
        );
      })}
    </div>
  );
}

/**
 * Several options picked, from the field's options or its value source. When
 * a field it depends on changes, the picks the new options no longer offer
 * are dropped. While its source fails, it lists the picks it holds.
 */
function ChecklistWidget({ id, name, ui, value, values, onChange, ctx, describedBy, readOnly }: WidgetProps) {
  const sourced = useFieldValues(name, ui, values, ctx);
  const picked = Array.isArray(value) ? value.map(String) : [];
  const failed = Boolean(ui.value_source) && sourceFailed(sourced);
  const failId = `${id}-source`;
  const loaded = ui.value_source ? sourced.data?.options : (ui.options ?? []);
  const options: Option[] = loaded ?? (failed ? picked.map((v) => ({ value: v, label: v })) : []);
  const selected = new Set(picked);
  // Required options (disabled with a reason) are always part of the value.
  for (const o of options) if (o.disabled && o.disabled_reason) selected.add(o.value);
  const toggle = (o: Option) => {
    const next = new Set(selected);
    if (next.has(o.value)) next.delete(o.value);
    else next.add(o.value);
    onChange(options.map((x) => x.value).filter((v) => next.has(v)));
  };
  const offered = sourced.data?.options;
  const onChangeRef = useRef(onChange);
  onChangeRef.current = onChange;
  const pickedKey = JSON.stringify(picked);
  useEffect(() => {
    if (!offered) return;
    const before = JSON.parse(pickedKey) as string[];
    const kept = before.filter((v) => offered.some((o) => o.value === v));
    if (kept.length < before.length) onChangeRef.current(kept.length ? kept : undefined);
  }, [offered, pickedKey]);

  const failure = failed && (
    <SourceFailed
      id={failId}
      message="Couldn't load the options."
      onRetry={() => void sourced.refetch()}
      retrying={sourced.isFetching}
      focusTo={() => document.getElementById(`${id}-options`)?.querySelector<HTMLElement>("button")}
    />
  );
  if (ui.value_source && !sourced.data && !failed) {
    return (
      <p className="text-secondary text-muted" aria-describedby={describedBy}>
        Loading the options…
      </p>
    );
  }
  if (options.length === 0) {
    return (
      failure || (
        <p className="text-secondary text-muted" aria-describedby={describedBy}>
          Nothing available.
        </p>
      )
    );
  }
  return (
    <div className="flex flex-col gap-1">
      <ul
        id={`${id}-options`}
        className={cn("flex flex-col", options.length > 10 && "max-h-72 overflow-y-auto pr-1")}
        aria-labelledby={`${id}-label`}
        aria-describedby={[describedBy, failed && failId].filter(Boolean).join(" ") || undefined}
      >
        {options.map((o) => {
          const optionId = `${id}-${o.value}`;
          return (
            <li key={o.value} className="border-b border-line last:border-b-0">
              <label htmlFor={optionId} className="flex cursor-pointer items-start gap-2.5 py-2">
                <Checkbox
                  id={optionId}
                  className="mt-0.5"
                  checked={selected.has(o.value)}
                  onChange={() => !readOnly && !o.disabled && toggle(o)}
                />
                <span className="flex min-w-0 flex-1 flex-col">
                  <span className="font-medium">{o.label}</span>
                  {o.description && <span className="text-secondary text-muted">{o.description}</span>}
                  {o.meta && <span className="font-mono text-caption text-faint">{withUnits(o.meta)}</span>}
                </span>
                {o.disabled_reason && (
                  <span className="text-caption whitespace-nowrap text-faint">{o.disabled_reason}</span>
                )}
              </label>
            </li>
          );
        })}
      </ul>
      {failure}
    </div>
  );
}

/**
 * Select whose options come from static options or the server (vCenter,
 * EfficientIP, AWX). It shows the value the form holds, so it sends what it
 * shows: once its options load, a value they don't offer gives way to the
 * server's default, or to none; while they fail, the value held stays, shown.
 */
function SourcedSelectWidget({
  id,
  name,
  ui,
  value,
  values,
  onChange,
  ctx,
  invalid,
  describedBy,
  readOnly,
}: WidgetProps) {
  const sourced = useFieldValues(name, ui, values, ctx);
  const current = str(value);
  const loaded = ui.value_source ? sourced.data?.options : (ui.options ?? []);
  const failed = Boolean(ui.value_source) && sourceFailed(sourced);
  const options = loaded ?? (failed && current ? [{ value: current, label: current }] : []);
  const valid = options.some((o) => o.value === current && !o.disabled);
  const suggestion = sourced.data?.default;
  useApplySuggestion(value, suggestion, valid, onChange);
  const onChangeRef = useRef(onChange);
  onChangeRef.current = onChange;
  const stale = loaded !== undefined && !valid && current !== "" && suggestion === undefined && !readOnly;
  useEffect(() => {
    if (stale) onChangeRef.current(undefined);
  }, [stale]);
  const empty = ui.value_source && sourced.isSuccess && options.length === 0;
  const failId = `${id}-source`;
  return (
    <div className="flex flex-col gap-1">
      <Select
        id={id}
        value={valid ? current : ""}
        onChange={onChange}
        options={options.map((o) => ({ value: o.value, label: o.label, meta: o.meta }))}
        loading={sourced.isFetching && !sourced.data && !failed}
        disabled={readOnly || Boolean(empty) || (failed && options.length === 0)}
        placeholder={empty ? "Nothing available" : failed && options.length === 0 ? "Not loaded" : "Select…"}
        className={cn("font-mono text-secondary", invalid && "border-bad")}
        aria-invalid={invalid || undefined}
        aria-describedby={[describedBy, failed && failId].filter(Boolean).join(" ") || undefined}
      />
      {failed && (
        <SourceFailed
          id={failId}
          message="Couldn't load the options."
          onRetry={() => void sourced.refetch()}
          retrying={sourced.isFetching}
          focusTo={() => document.getElementById(id)}
        />
      )}
    </div>
  );
}

/** Read-only value computed by the server (VM name, next free IP) or a CEL `value` rule. */
function ComputedWidget({ id, name, ui, value, values, onChange, ctx, describedBy }: WidgetProps) {
  const sourced = useFieldValues(name, ui, values, ctx);
  const suggestion = typeof sourced.data?.value === "string" ? sourced.data.value : undefined;
  useApplySuggestion(value, suggestion, suggestion === undefined || suggestion === value, onChange);
  const shown = str(value);
  const failed = Boolean(ui.value_source) && sourceFailed(sourced);
  const failId = `${id}-source`;
  return (
    <div className="flex flex-col gap-1">
      {/* Focusable from code only: Try again gives the focus back here. */}
      <output
        id={id}
        tabIndex={-1}
        htmlFor={(ui.depends_on ?? []).map((d) => `f-${d}`).join(" ") || undefined}
        aria-describedby={[describedBy, failed && failId].filter(Boolean).join(" ") || undefined}
        className="flex h-8 items-center justify-between gap-2 rounded-md border border-dashed border-line-strong bg-sunken px-2.5 font-mono text-secondary"
      >
        <span className="truncate">{shown || <span className="text-faint">–</span>}</span>
        {sourced.isFetching ? (
          <LoaderCircle className="size-3.5 animate-spin text-faint" aria-label="Updating" />
        ) : (
          <span className="font-sans text-caption text-faint">{name === "ip_address" ? "next free" : "generated"}</span>
        )}
      </output>
      {failed && (
        <SourceFailed
          id={failId}
          message="Couldn't work out this value."
          onRetry={() => void sourced.refetch()}
          retrying={sourced.isFetching}
          focusTo={() => document.getElementById(id)}
        />
      )}
    </div>
  );
}

function InfoWidget({ ui }: WidgetProps) {
  return (
    <p className="flex items-start gap-2 rounded-md bg-warn-soft px-2.5 py-2 text-secondary text-warn" role="note">
      <Info className="mt-px size-3.5 shrink-0" aria-hidden />
      {ui.help ?? ui.label}
    </p>
  );
}

export const widgets: Partial<Record<UiWidget, React.ComponentType<WidgetProps>>> = {
  text: TextWidget,
  textarea: TextareaWidget,
  number: NumberWidget,
  slider: NumberWidget,
  date: DateWidget,
  datetime: DateTimeWidget,
  tags: TagsWidget,
  key_value: KeyValueWidget,
  datagrid: DatagridWidget,
  toggle: ToggleWidget,
  select: SourcedSelectWidget,
  segmented: SegmentedWidget,
  radio_cards: RadioCardsWidget,
  checklist: ChecklistWidget,
  multiselect: ChecklistWidget,
  computed: ComputedWidget,
  info: InfoWidget,
  secret: SecretWidget,
  vsphere_cluster: SourcedSelectWidget,
  vsphere_datastore: SourcedSelectWidget,
  vsphere_network: SourcedSelectWidget,
  vsphere_template: SourcedSelectWidget,
  vsphere_folder: SourcedSelectWidget,
  vsphere_vm: SourcedSelectWidget,
  ipam_subnet: SourcedSelectWidget,
  ipam_address: ComputedWidget,
  dns_zone: SourcedSelectWidget,
  awx_inventory: SourcedSelectWidget,
  awx_credential: SourcedSelectWidget,
  project: SourcedSelectWidget,
  user: UserWidget,
  deployment: SourcedSelectWidget,
};

/** The widget a field renders with: a text marked secret is masked like the secret widget. */
export function widgetFor(ui: UiField): React.ComponentType<WidgetProps> {
  if (ui.secret && (ui.widget === "text" || ui.widget === "textarea")) return SecretWidget;
  return widgets[ui.widget] ?? TextWidget;
}

/** Widgets that render their own label text (no separate label element). */
export const selfLabelled: UiWidget[] = ["info", "toggle"];
