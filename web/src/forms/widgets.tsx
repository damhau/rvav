// Widgets for the request form renderer, keyed by `ui:widget`. Each takes the
// same props so the renderer can pick one from the registry.
import { Info, LoaderCircle } from "lucide-react";
import { Checkbox } from "@/components/ui/checkbox";
import { Input, Textarea } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import type { Option, UiField, UiWidget } from "@/lib/api/types";
import { cn } from "@/lib/utils";
import { useApplySuggestion, useFieldValues, type FormContext } from "./useFieldValues";

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
  enum?: unknown[];
  readOnly?: boolean;
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
      className={cn(name === "role" && "font-mono text-[12px]", invalid && "border-bad")}
      onChange={(e) => onChange(e.target.value)}
      onBlur={onBlur}
    />
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
  return (
    <Input
      id={id}
      name={name}
      type="number"
      inputMode="numeric"
      min={schema.minimum}
      max={schema.maximum}
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

function ToggleWidget({ id, ui, value, onChange, readOnly }: WidgetProps) {
  return (
    <label htmlFor={id} className="flex items-center gap-2 text-[13px]">
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
              "h-8 flex-1 border-l border-line-strong px-3 font-mono text-[12px] uppercase first:border-l-0",
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
            {o.meta && <span className="font-mono text-[12px] text-muted">{o.meta}</span>}
          </button>
        );
      })}
    </div>
  );
}

function ChecklistWidget({ id, ui, value, onChange, readOnly }: WidgetProps) {
  const options = ui.options ?? [];
  const selected = new Set(Array.isArray(value) ? (value as string[]) : []);
  // Required options (disabled with a reason) are always part of the value.
  for (const o of options) if (o.disabled && o.disabled_reason) selected.add(o.value);
  const toggle = (o: Option) => {
    const next = new Set(selected);
    if (next.has(o.value)) next.delete(o.value);
    else next.add(o.value);
    onChange(options.map((x) => x.value).filter((v) => next.has(v)));
  };
  return (
    <ul className="flex flex-col" aria-labelledby={`${id}-label`}>
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
                {o.meta && <span className="font-mono text-[11px] text-faint">{o.meta}</span>}
              </span>
              {o.disabled_reason && (
                <span className="text-[11px] whitespace-nowrap text-faint">{o.disabled_reason}</span>
              )}
            </label>
          </li>
        );
      })}
    </ul>
  );
}

/** Select whose options come from static options or the server (vCenter, EfficientIP, AWX). */
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
  const options = ui.value_source ? (sourced.data?.options ?? []) : (ui.options ?? []);
  const current = str(value);
  const valid = options.some((o) => o.value === current && !o.disabled);
  useApplySuggestion(value, sourced.data?.default, valid, onChange);
  const empty = ui.value_source && sourced.isSuccess && options.length === 0;
  return (
    <div className="flex flex-col gap-1">
      <Select
        id={id}
        value={valid ? current : ""}
        onChange={onChange}
        options={options.map((o) => ({ value: o.value, label: o.label, meta: o.meta }))}
        loading={sourced.isFetching && !sourced.data}
        disabled={readOnly || Boolean(empty)}
        placeholder={empty ? "Nothing available" : "Select…"}
        className={cn("font-mono text-[12px]", invalid && "border-bad")}
        aria-invalid={invalid || undefined}
        aria-describedby={describedBy}
      />
      {sourced.isError && (
        <span className="text-xs text-bad">
          Couldn't load the options. They'll be retried when you change a related field.
        </span>
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
  return (
    <output
      id={id}
      htmlFor={(ui.depends_on ?? []).map((d) => `f-${d}`).join(" ") || undefined}
      aria-describedby={describedBy}
      className="flex h-8 items-center justify-between gap-2 rounded-md border border-dashed border-line-strong bg-sunken px-2.5 font-mono text-[12px]"
    >
      <span className="truncate">{shown || <span className="text-faint">–</span>}</span>
      {sourced.isFetching ? (
        <LoaderCircle className="size-3.5 animate-spin text-faint" aria-label="Updating" />
      ) : (
        <span className="font-sans text-[11px] text-faint">{name === "ip_address" ? "next free" : "generated"}</span>
      )}
    </output>
  );
}

function InfoWidget({ ui }: WidgetProps) {
  return (
    <p className="flex items-start gap-2 rounded-md bg-warn-soft px-2.5 py-2 text-xs text-warn" role="note">
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
  toggle: ToggleWidget,
  select: SourcedSelectWidget,
  segmented: SegmentedWidget,
  radio_cards: RadioCardsWidget,
  checklist: ChecklistWidget,
  multiselect: ChecklistWidget,
  computed: ComputedWidget,
  info: InfoWidget,
  secret: TextWidget,
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
  user: SourcedSelectWidget,
  deployment: SourcedSelectWidget,
};

/** Widgets that render their own label text (no separate label element). */
export const selfLabelled: UiWidget[] = ["info", "toggle"];
