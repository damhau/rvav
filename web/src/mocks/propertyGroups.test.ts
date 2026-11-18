// The mocks' property groups against the server's: checkGroup's rules
// (internal/core/property_groups.go), and a version's form with its groups'
// inputs, as store.WithGroups serves it (ADR-0038).
import { describe, expect, it } from "vitest";
import { api, call } from "@/lib/api/client";
import { db } from "./seed";

describe("the mocks' property groups", () => {
  it("refuse what checkGroup refuses", async () => {
    await expect(
      call(
        api.POST("/admin/property-groups", {
          body: {
            key: "net",
            name: "Network",
            inputs: [
              { name: "zone", label: "Zone", type: "integer", options: ["dmz"] },
              { name: "zone", label: "", type: "string", secret: true, default: "x" },
              { name: "size", label: "Size", type: "integer", default: 1.5 },
            ],
            constants: { "dns-domain": "corp.example", ok: { nested: true } },
          },
        }),
      ),
    ).rejects.toMatchObject({
      status: 422,
      fieldErrors: [
        { field: "/inputs/0/options", message: "Only a string input is chosen from options." },
        { field: "/inputs/1/name", message: "The group has zone twice." },
        { field: "/inputs/1/label", message: "Give the input a label." },
        { field: "/inputs/1/default", message: "A secret input has no default: the group would hold its value." },
        { field: "/inputs/2/default", message: "The default must be a integer." },
        { field: "/constants/dns-domain", code: "pattern" },
        { field: "/constants/ok", message: "A constant is text, a number or a boolean." },
      ],
    });
  });

  it("put a group's inputs in the form of a version that names it", async () => {
    const item = db.items.find((i) => i.slug === "rhel9-server")!;
    const admin = await call(api.GET("/admin/catalog/items/{itemId}", { params: { path: { itemId: item.id } } }));
    const published = await call(
      api.GET("/admin/catalog/items/{itemId}/versions/{versionId}", {
        params: { path: { itemId: item.id, versionId: admin.published_version!.id } },
      }),
    );
    const d = published.definition;
    const draft = await call(
      api.POST("/admin/catalog/items/{itemId}/versions", {
        params: { path: { itemId: item.id } },
        body: {
          definition: { ...d, blueprint: { ...d.blueprint, property_groups: [{ key: "corp_ops", version: 1 }] } },
        },
      }),
    );
    await call(
      api.POST("/admin/catalog/items/{itemId}/versions/{versionId}/publish", {
        params: { path: { itemId: item.id, versionId: draft.id } },
      }),
    );

    const form = await call(
      api.GET("/catalog/items/{itemId}/form", {
        params: { path: { itemId: item.id }, query: { project_id: db.projects[0]!.id } },
      }),
    );
    const props = (form.schema as { properties: Record<string, unknown>; required?: string[] }).properties;
    expect(props.cost_center).toEqual({ type: "string", enum: ["CC-1001", "CC-2002", "CC-3003"], minLength: 1 });
    expect(props.backup).toEqual({ type: "boolean" });
    expect((form.schema as { required?: string[] }).required).toContain("cost_center");
    expect(form.ui_schema.fields.cost_center).toMatchObject({ widget: "select", label: "Cost centre" });
    expect(form.ui_schema.fields.backup).toMatchObject({ widget: "toggle", label: "Back up nightly" });
    expect(form.ui_schema.layout.sections.at(-1)).toMatchObject({
      key: "propgroup-corp_ops",
      title: "Corporate operations",
      fields: ["cost_center", "backup"],
    });
    expect(form.initial_values.backup).toBe(true);
    // What the version saved is left as written.
    const saved = await call(
      api.GET("/admin/catalog/items/{itemId}/versions/{versionId}", {
        params: { path: { itemId: item.id, versionId: draft.id } },
      }),
    );
    expect(saved.definition.form_schema).toEqual(d.form_schema);
  });
});
