// The form designer's edits to a version's definition (ADR-0019): pure
// functions over the form schema, UI schema and initial values, so the three
// stay in step. The server checks the result when the draft is saved.
import type { CatalogVersionDefinition, FieldError, UiField, UiWidget } from "@/lib/api/types";

export type Def = CatalogVersionDefinition;
export type Prop = Record<string, unknown>;

export const widgetLabels: Record<UiWidget, string> = {
  text: "Text",
  textarea: "Long text",
  number: "Number",
  slider: "Slider",
  toggle: "Toggle",
  select: "Dropdown",
  multiselect: "Multiple choice dropdown",
  segmented: "Segmented choice",
  radio_cards: "Cards",
  checklist: "Checklist",
  date: "Date",
  datetime: "Date and time",
  tags: "Tags",
  key_value: "Key and value pairs",
  datagrid: "Table",
  secret: "Secret",
  info: "Information text",
  computed: "Computed value",
  vsphere_cluster: "vSphere cluster",
  vsphere_datastore: "vSphere datastore",
  vsphere_network: "vSphere network",
  vsphere_template: "vSphere template",
  vsphere_folder: "vSphere folder",
  vsphere_vm: "vSphere VM",
  ipam_subnet: "IPAM subnet",
  ipam_address: "IP address",
  dns_zone: "DNS zone",
  awx_inventory: "AWX inventory",
  awx_credential: "AWX credential",
  project: "Project",
  user: "User",
  deployment: "Deployment",
};

export const builtinSources = [
  "vsphere.clusters",
  "vsphere.datastores",
  "vsphere.networks",
  "vsphere.templates",
  "vsphere.folders",
  "ipam.subnets",
  "ipam.next_free_address",
  "dns.zones",
  "awx.inventories",
  "awx.credentials",
  "naming.preview",
];

/** The built-in source a widget is made for, set when the field is added. */
const widgetSource: Partial<Record<UiWidget, string>> = {
  vsphere_cluster: "vsphere.clusters",
  vsphere_datastore: "vsphere.datastores",
  vsphere_network: "vsphere.networks",
  vsphere_template: "vsphere.templates",
  vsphere_folder: "vsphere.folders",
  ipam_subnet: "ipam.subnets",
  ipam_address: "ipam.next_free_address",
  dns_zone: "dns.zones",
  awx_inventory: "awx.inventories",
  awx_credential: "awx.credentials",
};

/** Widgets that offer a list of options. */
export const choiceWidgets: UiWidget[] = ["select", "multiselect", "segmented", "radio_cards", "checklist"];

/** The JSON Schema of an input a widget edits; none for information text. */
export function schemaFor(widget: UiWidget): Prop | null {
  switch (widget) {
    case "info":
      return null;
    case "number":
    case "slider":
      return { type: "integer" };
    case "toggle":
      return { type: "boolean" };
    case "multiselect":
    case "checklist":
    case "tags":
      return { type: "array", items: { type: "string" } };
    case "key_value":
      return { type: "object", additionalProperties: { type: "string" } };
    case "datagrid":
      return { type: "array", items: { type: "object" } };
    default:
      return { type: "string" };
  }
}

export function props(def: Def): Record<string, Prop> {
  return (def.form_schema as { properties?: Record<string, Prop> }).properties ?? {};
}

function required(def: Def): string[] {
  return ((def.form_schema as { required?: string[] }).required ?? []).slice();
}

export function isRequired(def: Def, name: string): boolean {
  return required(def).includes(name);
}

/** Input names: a lower-case letter, then letters, digits and underscores. */
export const namePattern = /^[a-z][a-z0-9_]{0,62}$/;

function freeName(def: Def, base: string): string {
  const taken = new Set([...Object.keys(def.ui_schema.fields), ...Object.keys(props(def))]);
  for (let i = 1; ; i++) {
    const name = i === 1 ? base : `${base}_${i}`;
    if (!taken.has(name)) return name;
  }
}

function edit(def: Def, f: (d: Def, p: Record<string, Prop>) => void): Def {
  const d = structuredClone(def);
  const fs = d.form_schema as { properties?: Record<string, Prop> };
  fs.properties ??= {};
  f(d, fs.properties);
  return d;
}

/** Adds a field at the end of a section (the last one by default). */
export function addField(def: Def, widget: UiWidget, sectionKey?: string): { def: Def; name: string } {
  const base = widget === "info" ? "note" : widget.replace(/^(vsphere|ipam|awx)_/, "");
  const name = freeName(def, base);
  const next = edit(def, (d, p) => {
    const schema = schemaFor(widget);
    if (schema) p[name] = schema;
    const ui: UiField = { widget, label: widgetLabels[widget] };
    const source = widgetSource[widget];
    if (source) ui.value_source = { kind: "builtin", name: source };
    d.ui_schema.fields[name] = ui;
    const sections = d.ui_schema.layout.sections;
    if (sections.length === 0) sections.push({ key: "main", title: "Details", columns: 2, fields: [] });
    const section = sections.find((s) => s.key === sectionKey) ?? sections.at(-1)!;
    section.fields.push(name);
  });
  return { def: next, name };
}

export function addSection(def: Def): { def: Def; key: string } {
  const keys = new Set(def.ui_schema.layout.sections.map((s) => s.key));
  let n = def.ui_schema.layout.sections.length + 1;
  while (keys.has(`section_${n}`)) n++;
  const key = `section_${n}`;
  return {
    def: edit(def, (d) => {
      d.ui_schema.layout.sections.push({ key, title: "New section", columns: 2, fields: [] });
    }),
    key,
  };
}

/** Moves a field one place up or down, into the neighbouring section at an edge. */
export function moveField(def: Def, name: string, delta: -1 | 1): Def {
  return edit(def, (d) => {
    const sections = d.ui_schema.layout.sections;
    const si = sections.findIndex((s) => s.fields.includes(name));
    if (si < 0) return;
    const fields = sections[si]!.fields;
    const i = fields.indexOf(name);
    const j = i + delta;
    if (j >= 0 && j < fields.length) {
      [fields[i], fields[j]] = [fields[j]!, fields[i]!];
      return;
    }
    const other = sections[si + delta];
    if (!other) return;
    fields.splice(i, 1);
    if (delta < 0) other.fields.push(name);
    else other.fields.unshift(name);
  });
}

export function moveToSection(def: Def, name: string, sectionKey: string): Def {
  return edit(def, (d) => {
    const target = d.ui_schema.layout.sections.find((s) => s.key === sectionKey);
    if (!target || target.fields.includes(name)) return;
    for (const s of d.ui_schema.layout.sections) s.fields = s.fields.filter((f) => f !== name);
    target.fields.push(name);
  });
}

export function moveSection(def: Def, key: string, delta: -1 | 1): Def {
  return edit(def, (d) => {
    const sections = d.ui_schema.layout.sections;
    const i = sections.findIndex((s) => s.key === key);
    const j = i + delta;
    if (i < 0 || j < 0 || j >= sections.length) return;
    [sections[i], sections[j]] = [sections[j]!, sections[i]!];
  });
}

/** Removes a field everywhere: schema, UI, sections, initial values and other fields' dependencies. */
export function removeField(def: Def, name: string): Def {
  return edit(def, (d, p) => {
    delete p[name];
    const fs = d.form_schema as { required?: string[] };
    if (fs.required) fs.required = fs.required.filter((r) => r !== name);
    if (fs.required?.length === 0) delete fs.required;
    delete d.ui_schema.fields[name];
    for (const s of d.ui_schema.layout.sections) s.fields = s.fields.filter((f) => f !== name);
    delete d.initial_values[name];
    for (const f of Object.values(d.ui_schema.fields)) {
      if (f.depends_on) f.depends_on = f.depends_on.filter((x) => x !== name);
    }
  });
}

/** Removes a section that has no field left. */
export function removeSection(def: Def, key: string): Def {
  return edit(def, (d) => {
    d.ui_schema.layout.sections = d.ui_schema.layout.sections.filter((s) => s.key !== key || s.fields.length > 0);
  });
}

/**
 * Renames an input in the schema, the UI, the sections, the initial values
 * and other fields' dependencies. Rules and the blueprint name inputs in
 * expressions: those are left to the author (the server says where).
 */
export function renameField(def: Def, from: string, to: string): Def {
  if (from === to) return def;
  return edit(def, (d, p) => {
    if (from in p) {
      p[to] = p[from]!;
      delete p[from];
    }
    const fs = d.form_schema as { required?: string[] };
    if (fs.required) fs.required = fs.required.map((r) => (r === from ? to : r));
    const fields = d.ui_schema.fields;
    // Keep the order of the fields map, which the YAML shows.
    d.ui_schema.fields = Object.fromEntries(Object.entries(fields).map(([k, v]) => [k === from ? to : k, v]));
    for (const s of d.ui_schema.layout.sections) s.fields = s.fields.map((f) => (f === from ? to : f));
    if (from in d.initial_values) {
      d.initial_values[to] = d.initial_values[from];
      delete d.initial_values[from];
    }
    for (const f of Object.values(d.ui_schema.fields)) {
      if (f.depends_on) f.depends_on = f.depends_on.map((x) => (x === from ? to : x));
    }
  });
}

export function setRequired(def: Def, name: string, on: boolean): Def {
  return edit(def, (d) => {
    const fs = d.form_schema as { required?: string[] };
    const list = (fs.required ?? []).filter((r) => r !== name);
    if (on) list.push(name);
    if (list.length > 0) fs.required = list;
    else delete fs.required;
  });
}

/** Changes a field's widget; its input's schema follows when the kind of value changes. */
export function setWidget(def: Def, name: string, widget: UiWidget): Def {
  return edit(def, (d, p) => {
    const ui = d.ui_schema.fields[name];
    if (!ui) return;
    ui.widget = widget;
    const schema = schemaFor(widget);
    const current = p[name];
    if (!schema) {
      delete p[name];
      delete d.initial_values[name];
    } else if (!current || current.type !== schema.type) {
      p[name] = schema;
      delete d.initial_values[name];
    }
    const source = widgetSource[widget];
    if (source && !ui.value_source) ui.value_source = { kind: "builtin", name: source };
  });
}

/** Merges into a field's UI; undefined (or an empty string) removes a key. */
export function updateUi(def: Def, name: string, patch: Partial<UiField>): Def {
  return edit(def, (d) => {
    const ui = d.ui_schema.fields[name] as Record<string, unknown> | undefined;
    if (!ui) return;
    for (const [k, v] of Object.entries(patch)) {
      if (v === undefined || v === "" || (Array.isArray(v) && v.length === 0)) delete ui[k];
      else ui[k] = v;
    }
  });
}

/** Sets one rule (visible, required, read_only, value); an empty one is removed. */
export function setRule(
  def: Def,
  name: string,
  rule: "visible" | "required" | "read_only" | "value",
  expr: string,
): Def {
  return edit(def, (d) => {
    const ui = d.ui_schema.fields[name];
    if (!ui) return;
    const rules = { ...(ui.rules ?? {}) };
    if (expr.trim()) rules[rule] = expr;
    else delete rules[rule];
    if (Object.keys(rules).length > 0) ui.rules = rules;
    else delete ui.rules;
  });
}

/** Merges into an input's JSON Schema; undefined removes a key. */
export function updateProp(def: Def, name: string, patch: Prop): Def {
  return edit(def, (_, p) => {
    const prop = p[name];
    if (!prop) return;
    for (const [k, v] of Object.entries(patch)) {
      if (v === undefined || v === "") delete prop[k];
      else prop[k] = v;
    }
  });
}

export function setInitial(def: Def, name: string, value: unknown): Def {
  return edit(def, (d) => {
    if (value === undefined || value === "") delete d.initial_values[name];
    else d.initial_values[name] = value;
  });
}

export function updateSection(
  def: Def,
  key: string,
  patch: { key?: string; title?: string; description?: string; columns?: number },
): Def {
  return edit(def, (d) => {
    const s = d.ui_schema.layout.sections.find((x) => x.key === key);
    if (!s) return;
    if (patch.key !== undefined) s.key = patch.key;
    if (patch.title !== undefined) s.title = patch.title;
    if (patch.description !== undefined) {
      if (patch.description) s.description = patch.description;
      else delete s.description;
    }
    if (patch.columns !== undefined) s.columns = patch.columns;
  });
}

export interface Problem {
  /** Where in the field (`label`, `rules/visible`, `value_source/params/url`), or "" for the field itself. */
  at: string;
  message: string;
}

export interface Problems {
  fields: Record<string, Problem[]>;
  sections: Record<number, Problem[]>;
  /** Problems elsewhere (blueprint, the schema as a whole): the YAML tab shows them on their lines. */
  other: FieldError[];
}

/** Sorts the server's problems with a draft by the field or section they are about. */
export function sortProblems(errors: FieldError[], prefix = "/definition"): Problems {
  const out: Problems = { fields: {}, sections: {}, other: [] };
  const add = (name: string, at: string, message: string) => (out.fields[name] ??= []).push({ at, message });
  for (const e of errors) {
    const path = e.field.startsWith(prefix) ? e.field.slice(prefix.length) : e.field;
    let m: RegExpExecArray | null;
    if ((m = /^\/ui_schema\/fields\/([^/]+)(?:\/(.*))?$/.exec(path))) add(m[1]!, m[2] ?? "", e.message);
    else if ((m = /^\/form_schema\/properties\/([^/]+)(?:\/(.*))?$/.exec(path)))
      add(m[1]!, m[2] ? `schema/${m[2]}` : "", e.message);
    else if ((m = /^\/initial_values\/([^/]+)$/.exec(path))) add(m[1]!, "initial", e.message);
    else if ((m = /^\/ui_schema\/layout\/sections\/(\d+)(?:\/(.*))?$/.exec(path)))
      (out.sections[Number(m[1])] ??= []).push({ at: m[2] ?? "", message: e.message });
    else out.other.push(e);
  }
  return out;
}
