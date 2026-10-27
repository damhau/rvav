import { describe, expect, it } from "vitest";
import {
  addField,
  addSection,
  moveField,
  removeField,
  renameField,
  setRequired,
  setRule,
  setWidget,
  sortProblems,
  type Def,
} from "./model";

const empty: Def = {
  form_schema: { type: "object", properties: {} },
  ui_schema: { layout: { kind: "page", sections: [] }, fields: {} },
  initial_values: {},
  blueprint: { kind: "awx", awx: { title: "T", job_template: "t" } },
};

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
    def = setRule(def, "text", "visible", 'environment == "prd"');
    def = setRule(def, "text", "visible", " ");
    expect(def.ui_schema.fields.text!.rules).toBeUndefined();
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
