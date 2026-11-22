// What every layout of a request form shares: each field's state from the
// server's rule results, the fields a section shows, and the grid they show in.
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { FieldNote } from "@/components/ui/input";
import type { FieldStates, RequestForm, UiField, UiSection, UiWidget } from "@/lib/api/types";
import { cn } from "@/lib/utils";
import { withOwnSource, type FormContext } from "./useFieldValues";
import { selfLabelled, widgetFor, type JsonSchemaProp } from "./widgets";

export interface Schema {
  properties?: Record<string, JsonSchemaProp>;
  required?: string[];
}

export interface FieldState {
  visible: boolean;
  required: boolean;
  readOnly: boolean;
  computed?: unknown;
}

/**
 * A field's state: the server's rule results, else the schema's defaults. A
 * field with a visible rule stays hidden until the server has said, rather
 * than show and then vanish.
 */
export function fieldState(name: string, ui: UiField, schema: Schema, states?: FieldStates): FieldState {
  const server = states?.[name];
  if (server) {
    return { visible: server.visible, required: server.required, readOnly: server.read_only, computed: server.value };
  }
  return {
    visible: !ui.rules?.visible,
    required: schema.required?.includes(name) ?? false,
    readOnly: Boolean(schema.properties?.[name]?.readOnly) || Boolean(ui.rules?.value),
  };
}

export interface ShownField {
  name: string;
  ui: UiField;
  state: FieldState;
}

/** A section's fields that show, with their state. */
export function sectionFields(form: RequestForm, names: string[], states?: FieldStates): ShownField[] {
  const schema = form.schema as Schema;
  return names
    .map((name) => ({ name, ui: form.ui_schema.fields[name] }))
    .filter((f): f is { name: string; ui: UiField } => Boolean(f.ui))
    .map((f) => ({ ...f, ui: withOwnSource(f.ui) }))
    .map((f) => ({ ...f, state: fieldState(f.name, f.ui, schema, states) }))
    .filter((f) => f.state.visible);
}

/** A section with the fields it shows: a wizard's page, or a tab. */
export interface Part {
  section: UiSection;
  index: number;
  fields: ShownField[];
}

/** Every section with the fields it shows, in order. */
export function formParts(form: RequestForm, states?: FieldStates): Part[] {
  return form.ui_schema.layout.sections.map((section, index) => ({
    section,
    index,
    fields: sectionFields(form, section.fields, states),
  }));
}

/** Whether a part shows: one of its fields does, or it holds what leads the form. */
export function partShows(p: Part, leading: boolean): boolean {
  return p.fields.length > 0 || (p.index === 0 && leading);
}

/** The inputs a part asks for: its fields, and those of what leads the form in the first. */
export function partInputs(p: Part, leadingFields: string[]): string[] {
  return [...(p.index === 0 ? leadingFields : []), ...p.fields.map((f) => f.name)];
}

const gridCols: Record<number, string> = {
  1: "md:grid-cols-1",
  2: "md:grid-cols-2",
  3: "md:grid-cols-3",
  4: "md:grid-cols-4",
};

/** Widgets that take the whole width of a two-column section. */
const wideWidgets: UiWidget[] = [
  "radio_cards",
  "checklist",
  "info",
  "image",
  "multiselect",
  "key_value",
  "datagrid",
  "dual_list",
  "tree",
  "object",
];

export interface GridProps {
  form: RequestForm;
  values: Record<string, unknown>;
  onChange: (name: string, value: unknown) => void;
  onBlur?: (name: string) => void;
  errors: Record<string, string>;
  ctx: FormContext;
}

/** A section's fields, in its columns, after what leads the form when it is the first. */
export function FieldGrid({
  form,
  section,
  fields,
  leading,
  values,
  onChange,
  onBlur,
  errors,
  ctx,
}: GridProps & { section: UiSection; fields: ShownField[]; leading?: ReactNode }) {
  const { t } = useTranslation("forms");
  const schema = form.schema as Schema;
  return (
    <div className={cn("grid grid-cols-1 gap-4", gridCols[section.columns ?? 2])}>
      {leading}
      {fields.map(({ name, ui, state }) => {
        const Widget = widgetFor(ui);
        const id = `f-${name}`;
        const error = errors[name];
        const help = selfLabelled.includes(ui.widget) ? undefined : ui.help;
        const noteId = error || help ? `${id}-note` : undefined;
        const wide = wideWidgets.includes(ui.widget);
        const value = state.computed !== undefined ? state.computed : values[name];
        return (
          <div key={name} data-field={name} className={cn("flex min-w-0 flex-col gap-1.5", wide && "md:col-span-full")}>
            {!selfLabelled.includes(ui.widget) && (
              <div className="flex items-baseline justify-between gap-2">
                <label id={`${id}-label`} htmlFor={id} className="text-secondary font-medium">
                  {ui.label}
                </label>
                {!state.required && !state.readOnly && <span className="text-caption text-faint">{t("optional")}</span>}
              </div>
            )}
            <Widget
              id={id}
              name={name}
              ui={ui}
              schema={schema.properties?.[name] ?? {}}
              value={value}
              values={values}
              onChange={(v) => onChange(name, v)}
              onBlur={() => onBlur?.(name)}
              ctx={ctx}
              invalid={Boolean(error)}
              describedBy={noteId}
              readOnly={state.readOnly}
            />
            <FieldNote id={noteId} error={error} hint={help} />
          </div>
        );
      })}
    </div>
  );
}

/** How many of a part's inputs have a problem shown. */
export function problemsIn(p: Part, leadingFields: string[], errors: Record<string, string>): number {
  return partInputs(p, leadingFields).filter((name) => errors[name]).length;
}
