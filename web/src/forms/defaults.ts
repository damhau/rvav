// Defaults (F1.4): a default rule's, or a value source's for a field that
// takes one value. The server works them out with the rules; the form shows
// a field's default while the requester hasn't given it a value, and once
// they have, keeps theirs.
import type { FieldStates, UiField, UiWidget } from "@/lib/api/types";

const valueWidgets: UiWidget[] = ["text", "textarea", "number", "slider", "date", "datetime", "toggle"];

/** A field whose `rest` or `cel` source gives it one value: a computed field's, another's default. */
export function singleSource(ui: UiField): boolean {
  const kind = ui.value_source?.kind;
  if ((kind !== "rest" && kind !== "cel") || ui.secret) return false;
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

/** The values with the defaults of the fields the requester hasn't given a value: what the form shows. */
export function withDefaults(values: Record<string, unknown>, states?: FieldStates): Record<string, unknown> {
  let out = values;
  for (const [name, st] of Object.entries(states ?? {})) {
    if (st.default === undefined || st.default === null || Object.hasOwn(values, name)) continue;
    if (out === values) out = { ...values };
    out[name] = st.default;
  }
  return out;
}

/**
 * The inputs to send: those given, a field with a default the requester
 * emptied as null, so the server gives it none. The others the server
 * fills with their defaults.
 */
export function sentInputs(fields: Record<string, UiField>, values: Record<string, unknown>): Record<string, unknown> {
  let out = values;
  for (const [name, v] of Object.entries(values)) {
    const ui = fields[name];
    if (v !== undefined || !ui || !defaulted(ui)) continue;
    if (out === values) out = { ...values };
    out[name] = null;
  }
  return out;
}
