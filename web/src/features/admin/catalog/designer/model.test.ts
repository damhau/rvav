import { describe, expect, it } from "vitest";
import type { UiWidget } from "@/lib/api/types";
import { fromYaml, toYaml } from "../definitionYaml";
import {
  addColumn,
  addField,
  addSection,
  isColumnRequired,
  isDecimal,
  moveField,
  removeColumn,
  removeField,
  renameField,
  setDecimal,
  setInitial,
  setRequired,
  setRule,
  setWidget,
  sortProblems,
  updateColumn,
  updateProp,
  updateUi,
  widgetLabels,
  type Def,
} from "./model";

const empty: Def = {
  form_schema: { type: "object", properties: {} },
  ui_schema: { layout: { kind: "page", sections: [] }, fields: {} },
  initial_values: {},
  blueprint: { kind: "awx", awx: { title: "T", job_template: "t" } },
};

const prop = (def: Def, name: string) => (def.form_schema.properties as Record<string, unknown>)[name];

describe("form designer model", () => {
  it("adds fields with a schema, a UI field and a place in a section", () => {
    let { def, name } = addField(empty, "text");
    expect(name).toBe("text");
    expect(def.ui_schema.layout.sections).toEqual([{ key: "main", title: "Details", columns: 2, fields: ["text"] }]);
    ({ def, name } = addField(def, "vsphere_cluster"));
    expect(name).toBe("cluster");
    expect(def.ui_schema.fields.cluster?.value_source).toEqual({ kind: "builtin", name: "vsphere.clusters" });
    ({ def, name } = addField(def, "text"));
    expect(name).toBe("text_2");
    ({ def, name } = addField(def, "info"));
    expect((def.form_schema.properties as Record<string, unknown>)[name]).toBeUndefined();
    for (const [widget, source] of [
      ["project", "rvav.projects"],
      ["user", "rvav.users"],
      ["deployment", "rvav.deployments"],
      ["vsphere_vm", "rvav.vms"],
    ] as const) {
      ({ def, name } = addField(def, widget));
      expect(def.ui_schema.fields[name]?.value_source).toEqual({ kind: "builtin", name: source });
    }
  });

  it("moves fields within and across sections", () => {
    let def = addField(addField(empty, "text").def, "number").def;
    def = addSection(def).def;
    def = moveField(def, "number", -1);
    expect(def.ui_schema.layout.sections[0]!.fields).toEqual(["number", "text"]);
    def = moveField(def, "text", 1);
    expect(def.ui_schema.layout.sections.map((s) => s.fields)).toEqual([["number"], ["text"]]);
  });

  it("renames and removes an input everywhere", () => {
    let def = addField(addField(empty, "text").def, "select").def;
    def = setRequired(def, "text", true);
    def.ui_schema.fields.select!.depends_on = ["text"];
    def.initial_values.text = "a";
    def = renameField(def, "text", "app");
    expect(Object.keys(def.form_schema.properties as object)).toEqual(["select", "app"]);
    expect(def.form_schema.required).toEqual(["app"]);
    expect(def.ui_schema.fields.select!.depends_on).toEqual(["app"]);
    expect(def.initial_values).toEqual({ app: "a" });
    def = removeField(def, "app");
    expect(def.form_schema.required).toBeUndefined();
    expect(def.ui_schema.fields.select!.depends_on).toEqual([]);
    expect(def.ui_schema.layout.sections[0]!.fields).toEqual(["select"]);
  });

  it("changes the schema with the widget, and rules one at a time", () => {
    let def = addField(empty, "text").def;
    def.initial_values.text = "x";
    def = setWidget(def, "text", "toggle");
    expect((def.form_schema.properties as Record<string, unknown>).text).toEqual({ type: "boolean" });
    expect(def.initial_values).toEqual({});
    def = setRequired(def, "text", true);
    def = setWidget(def, "text", "info");
    expect(def.form_schema.required).toBeUndefined();
    let picker = addField(empty, "vsphere_cluster").def;
    picker = setWidget(picker, "cluster", "vsphere_datastore");
    expect(picker.ui_schema.fields.cluster?.value_source).toEqual({ kind: "builtin", name: "vsphere.datastores" });
    picker = setWidget(picker, "cluster", "select");
    expect(picker.ui_schema.fields.cluster?.value_source).toBeUndefined();
    def = setWidget(def, "text", "toggle");
    def = setRule(def, "text", "visible", 'environment == "prd"');
    def = setRule(def, "text", "visible", " ");
    expect(def.ui_schema.fields.text!.rules).toBeUndefined();
  });

  it("gives dates their format, and keeps a decimal number decimal across number widgets", () => {
    let def = addField(empty, "text").def;
    def = setWidget(def, "text", "datetime");
    expect(prop(def, "text")).toEqual({ type: "string", format: "date-time" });
    def = setWidget(def, "text", "date");
    expect(prop(def, "text")).toEqual({ type: "string", format: "date" });
    def = setWidget(def, "text", "textarea");
    expect(prop(def, "text")).toEqual({ type: "string" });

    let n = addField(empty, "number").def;
    n = updateProp(n, "number", { minimum: 0.5, maximum: 4, multipleOf: 0.25 });
    n = setDecimal(n, "number", true);
    expect(isDecimal(n, "number")).toBe(true);
    n = setWidget(n, "number", "slider");
    expect(prop(n, "number")).toEqual({ type: "number", minimum: 0.5, maximum: 4, multipleOf: 0.25 });
    n = setInitial(n, "number", 1.5);
    n = setDecimal(n, "number", false);
    expect(prop(n, "number")).toEqual({ type: "integer", maximum: 4 });
    expect(n.initial_values).toEqual({});
  });

  it("keeps a table's columns and its rows' schema in step", () => {
    let def = addField(empty, "datagrid").def;
    expect(def.ui_schema.fields.datagrid?.columns).toEqual([{ key: "value", label: "Value", widget: "text" }]);
    def = addColumn(def, "datagrid");
    def = updateColumn(def, "datagrid", 1, { key: "size_gb", label: "Size (GB)", widget: "number", required: true });
    def = updateColumn(def, "datagrid", 0, {
      key: "tier",
      widget: "select",
      options: [{ value: "gold", label: "Gold" }],
    });
    expect(prop(def, "datagrid")).toEqual({
      type: "array",
      items: {
        type: "object",
        properties: { tier: { type: "string", enum: ["gold"] }, size_gb: { type: "integer" } },
        required: ["size_gb"],
      },
    });
    def = updateColumn(def, "datagrid", 1, { key: "disk_gb" });
    expect(isColumnRequired(def, "datagrid", "disk_gb")).toBe(true);
    def = removeColumn(def, "datagrid", 1);
    expect((prop(def, "datagrid") as { items: unknown }).items).toEqual({
      type: "object",
      properties: { tier: { type: "string", enum: ["gold"] } },
    });
    def = setWidget(def, "datagrid", "tags");
    expect(def.ui_schema.fields.datagrid?.columns).toBeUndefined();
    expect(prop(def, "datagrid")).toEqual({ type: "array", uniqueItems: true, items: { type: "string" } });
  });

  it("keeps an object's sub-fields and its properties in step, and a table's columns apart", () => {
    let def = addField(empty, "object").def;
    def = addColumn(def, "object");
    def = updateColumn(def, "object", 1, { key: "port", label: "Port", widget: "number", required: true });
    expect(prop(def, "object")).toEqual({
      type: "object",
      properties: { value: { type: "string" }, port: { type: "integer" } },
      required: ["port"],
    });
    expect(isColumnRequired(def, "object", "port")).toBe(true);
    def = removeColumn(def, "object", 0);
    expect(prop(def, "object")).toEqual({
      type: "object",
      properties: { port: { type: "integer" } },
      required: ["port"],
    });
    def = setWidget(def, "object", "datagrid");
    expect(def.ui_schema.fields.object?.columns).toEqual([{ key: "value", label: "Value", widget: "text" }]);
    expect(prop(def, "object")).toEqual({
      type: "array",
      items: { type: "object", properties: { value: { type: "string" } } },
    });
    def = setWidget(addField(empty, "dual_list").def, "dual_list", "tags");
    expect(prop(def, "dual_list")).toEqual({ type: "array", uniqueItems: true, items: { type: "string" } });
  });

  it("gives images and links no input, and drops their address with the widget", () => {
    let def = addField(empty, "link").def;
    expect(prop(def, "link")).toBeUndefined();
    def = updateUi(def, "link", { url: "https://wiki.corp.example/sizes" });
    def = setWidget(def, "link", "image");
    expect(def.ui_schema.fields.link?.url).toBe("https://wiki.corp.example/sizes");
    def = setWidget(updateUi(def, "link", { alt: "Sizes" }), "link", "text");
    expect(def.ui_schema.fields.link).toEqual({ widget: "text", label: "Link" });
    expect(prop(def, "link")).toEqual({ type: "string" });
  });

  it("round-trips every field type through the YAML view", () => {
    let def = empty;
    for (const w of Object.keys(widgetLabels) as UiWidget[]) def = addField(def, w).def;
    def = updateProp(def, "number", { minimum: 0.5, multipleOf: 0.25 });
    def = setDecimal(def, "number", true);
    def = updateProp(def, "datetime", { formatMinimum: "2026-12-01T00:00:00+01:00" });
    def = addColumn(def, "datagrid");
    def = updateColumn(addColumn(def, "object"), "object", 1, { key: "on", label: "On", widget: "toggle" });
    def = updateUi(def, "dual_list", { options: [{ value: "a", label: "A" }] });
    def = updateUi(def, "image", { url: "/favicon.svg", alt: "rvav" });
    const back = fromYaml(toYaml(def));
    expect(back.problems).toEqual([]);
    expect(back.def).toEqual(def);
  });

  it("sorts the server's problems by field and section", () => {
    const p = sortProblems([
      { field: "/definition/ui_schema/fields/app/rules/visible", code: "invalid", message: "bad" },
      { field: "/definition/form_schema/properties/app", code: "no_ui_field", message: "no ui" },
      { field: "/definition/ui_schema/layout/sections/1/key", code: "required", message: "key" },
      { field: "/definition/blueprint/template", code: "required", message: "template" },
    ]);
    expect(p.fields.app).toEqual([
      { at: "rules/visible", message: "bad" },
      { at: "", message: "no ui" },
    ]);
    expect(p.sections[1]).toEqual([{ at: "key", message: "key" }]);
    expect(p.other.map((e) => e.message)).toEqual(["template"]);
  });
});
