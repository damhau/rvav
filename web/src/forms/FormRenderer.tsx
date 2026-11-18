// Renders a request form from its JSON Schema and UI schema: sections, widgets,
// server-resolved values with dependencies, the server's rule results and
// field errors.
import type { ReactNode } from "react";
import { FieldNote } from "@/components/ui/input";
import { Panel } from "@/components/ui/panel";
import type { FieldStates, RequestForm, UiField } from "@/lib/api/types";
import { cn } from "@/lib/utils";
import type { FormContext } from "./useFieldValues";
import { selfLabelled, widgetFor, type JsonSchemaProp } from "./widgets";

export interface FormRendererProps {
  form: RequestForm;
  values: Record<string, unknown>;
  onChange: (name: string, value: unknown) => void;
  onBlur?: (name: string) => void;
  errors?: Record<string, string>;
  ctx: FormContext;
  /**
   * The server's rule results (the preview's `fields`): which fields show,
   * are required or read-only, and computed values. Until they come, the
   * schema's defaults.
   */
  fieldStates?: FieldStates;
  /** Rendered at the start of the first section (the environment picker). */
  leading?: ReactNode;
}

interface Schema {
  properties?: Record<string, JsonSchemaProp>;
  required?: string[];
}

const gridCols: Record<number, string> = {
  1: "md:grid-cols-1",
  2: "md:grid-cols-2",
  3: "md:grid-cols-3",
  4: "md:grid-cols-4",
};

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

export function FormRenderer({
  form,
  values,
  onChange,
  onBlur,
  errors = {},
  ctx,
  fieldStates,
  leading,
}: FormRendererProps) {
  const schema = form.schema as Schema;
  return (
    <div className="flex flex-col gap-4">
      {form.ui_schema.layout.sections.map((section, i) => {
        const fields = section.fields
          .map((name) => ({ name, ui: form.ui_schema.fields[name] }))
          .filter((f): f is { name: string; ui: UiField } => Boolean(f.ui))
          .map((f) => ({ ...f, state: fieldState(f.name, f.ui, schema, fieldStates) }))
          .filter((f) => f.state.visible);
        if (fields.length === 0 && !(i === 0 && leading)) return null;
        return (
          <Panel key={section.key} as="fieldset" title={section.title} description={section.description}>
            <div className={cn("grid grid-cols-1 gap-4", gridCols[section.columns ?? 2])}>
              {i === 0 && leading}
              {fields.map(({ name, ui, state }) => {
                const Widget = widgetFor(ui);
                const id = `f-${name}`;
                const error = errors[name];
                const help = selfLabelled.includes(ui.widget) ? undefined : ui.help;
                const noteId = error || help ? `${id}-note` : undefined;
                const wide = ["radio_cards", "checklist", "info", "multiselect", "key_value", "datagrid"].includes(
                  ui.widget,
                );
                const value = state.computed !== undefined ? state.computed : values[name];
                return (
                  <div key={name} className={cn("flex min-w-0 flex-col gap-1.5", wide && "md:col-span-full")}>
                    {!selfLabelled.includes(ui.widget) && (
                      <div className="flex items-baseline justify-between gap-2">
                        <label id={`${id}-label`} htmlFor={id} className="text-secondary font-medium">
                          {ui.label}
                        </label>
                        {!state.required && !state.readOnly && (
                          <span className="text-caption text-faint">Optional</span>
                        )}
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
          </Panel>
        );
      })}
    </div>
  );
}
