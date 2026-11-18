// The mocks' input checks for collections, against the server's
// (internal/core/validate.go and locateNested in formfields.go).
import { describe, expect, it } from "vitest";
import type { RequestForm } from "@/lib/api/types";
import { validateInputs } from "./logic";
import type { Db, StoredItem } from "./seed";

const form = {
  schema: {
    type: "object",
    properties: {
      tags: { type: "array", uniqueItems: true, maxItems: 2, items: { type: "string" } },
      labels: { type: "object", additionalProperties: { type: "string" }, propertyNames: { minLength: 1 } },
      disks: {
        type: "array",
        items: {
          type: "object",
          required: ["size_gb"],
          additionalProperties: false,
          properties: { size_gb: { type: "integer", minimum: 10 } },
        },
      },
    },
  },
  ui_schema: {
    layout: { kind: "page", sections: [] },
    fields: {
      tags: { widget: "tags", label: "Tags" },
      labels: { widget: "key_value", label: "Labels" },
      disks: {
        widget: "datagrid",
        label: "Disks",
        columns: [{ key: "size_gb", label: "Size (GB)", widget: "number" }],
      },
    },
  },
} as unknown as RequestForm;

const check = (inputs: Record<string, unknown>) =>
  validateInputs({} as Db, {} as StoredItem, inputs, form).map((e) => `${e.field} ${e.code}: ${e.message}`);

describe("the mocks' collection checks", () => {
  it("say which row, column or key a problem is in", () => {
    expect(check({ disks: [{ size_gb: 50 }, { size_gb: 5 }] })).toEqual([
      "/inputs/disks/1/size_gb minimum: Row 2, Size (GB): Enter 10 or more.",
    ]);
    expect(check({ disks: [{ size_gb: 50, extra: 1 }, {}] })).toEqual([
      "/inputs/disks/0/extra unknown_field: Row 1, extra: This form has no field named extra.",
      "/inputs/disks/1/size_gb required: Row 2, Size (GB): This field is required.",
    ]);
    expect(check({ labels: { "": "x" } })).toEqual(["/inputs/labels key: Give every pair a key."]);
    expect(check({ tags: ["a", "a"] })).toEqual(["/inputs/tags unique: The same value is in the list twice."]);
    expect(check({ tags: ["a", "b"], labels: { app: "billing" }, disks: [{ size_gb: 10 }] })).toEqual([]);
  });
});
