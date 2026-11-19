// Defaults (F1.4): a default rule's, or a value source's for a field that
// takes one value. The server works them out with the rules; the form shows
// a field's default while the requester hasn't given it a value, and once
// they have, keeps theirs.
import type { FieldStates, RequestForm, UiField, UiWidget } from "@/lib/api/types";

const valueWidgets: UiWidget[] = ["text", "textarea", "number", "slider", "date", "datetime", "toggle"];

/** A field whose `rest`, `cel` or `library` source gives it one value: a computed field's, another's default. */
export function singleSource(ui: UiField): boolean {
  const kind = ui.value_source?.kind;
  if ((kind !== "rest" && kind !== "cel" && kind !== "library") || ui.secret) return false;
  return ui.widget === "computed" || valueWidgets.includes(ui.widget);
}

/** A field whose value is worked out rather than given: by a value rule, or a computed field's source. */
export function computedBy(ui: UiField): boolean {
  return Boolean(ui.rules?.value) || (ui.widget === "computed" && singleSource(ui));
}

/** A field that takes a default while it has no value. */
export function defaulted(ui: UiField): boolean {
  return !computedBy(ui) && (Boolean(ui.rules?.default) || singleSource(ui));
}

/**
 * What the form shows: the values with the defaults of the fields the
 * requester hasn't given a value. A read-only field shows what the server
 * gives it, as `applyStates` does: its default, else its initial value;
 * one whose value is worked out (a computed field, a value rule) keeps it.
 */
export function withDefaults(
  values: Record<string, unknown>,
  states?: FieldStates,
  form?: Pick<RequestForm, "ui_schema" | "initial_values">,
): Record<string, unknown> {
  const initial = form?.initial_values;
  let out = values;
  const set = (name: string, v: unknown) => {
    if (out === values) out = { ...values };
    if (v === undefined) delete out[name];
    else out[name] = v;
  };
  for (const [name, st] of Object.entries(states ?? {})) {
    const def = st.default ?? undefined;
    const ui = form?.ui_schema.fields[name];
    if (ui && (ui.widget === "computed" || computedBy(ui))) continue;
    if (st.read_only) {
      const v = def !== undefined ? def : initial?.[name];
      if (values[name] !== v || Object.hasOwn(values, name) !== (v !== undefined)) set(name, v);
    } else if (def !== undefined && !Object.hasOwn(values, name)) set(name, def);
  }
  return out;
}

/**
 * The inputs to send: those given, and as null a field the requester
 * emptied that has a default or a value the form starts with (`initial`),
 * so the server gives it none. The server fills the others in with those
 * (ADR-0055).
 */
export function sentInputs(
  fields: Record<string, UiField>,
  values: Record<string, unknown>,
  initial: Record<string, unknown> = {},
): Record<string, unknown> {
  let out = values;
  for (const [name, v] of Object.entries(values)) {
    const ui = fields[name];
    if (v !== undefined || !(Object.hasOwn(initial, name) || (ui && defaulted(ui)))) continue;
    if (out === values) out = { ...values };
    out[name] = null;
  }
  return out;
}
