// The mocks' form library against the server's (internal/core/form_library.go,
// validations.go): an entry reads its parameters, a form gives them; a new
// version gives the items using an earlier one a draft; validations refuse
// on the review step and on submit, their messages on the inputs (ADR-0043).
import { describe, expect, it } from "vitest";
import { api, call } from "@/lib/api/client";
import type { components } from "@/lib/api/schema";
import { db } from "./seed";

type Definition = components["schemas"]["CatalogVersionDefinition"];

const definition = (validations: components["schemas"]["FormValidation"][]): Definition => ({
  form_schema: {
    type: "object",
    required: ["cost_center"],
    properties: { cost_center: { type: "string", minLength: 1 }, service: { type: "string" } },
  },
  ui_schema: {
    layout: {
      kind: "page",
      sections: [{ key: "main", title: "Main", columns: 1, fields: ["cost_center", "service"] }],
    },
    fields: {
      cost_center: { widget: "text", label: "Cost centre" },
      service: {
        widget: "select",
        label: "Service",
        value_source: { kind: "library", name: "cmdbServices", version: 1, params: { environment: "environment" } },
      },
    },
    validations,
  },
  initial_values: {},
  blueprint: { kind: "awx", awx: { title: "Checked", job_template: "checked" } },
});

async function published(slug: string, def: Definition) {
  const item = await call(
    api.POST("/admin/catalog/items", {
      body: { slug, name: slug, summary: "Checked.", category: "compute", type: "awx_job_template", definition: def },
    }),
  );
  await call(
    api.POST("/admin/catalog/items/{itemId}/versions/{versionId}/publish", {
      params: { path: { itemId: item.id, versionId: item.draft_version!.id } },
    }),
  );
  await call(
    api.PUT("/admin/catalog/items/{itemId}/entitlements", {
      params: { path: { itemId: item.id } },
      body: { all_projects: true, project_ids: [] },
    }),
  );
  return call(api.GET("/admin/catalog/items/{itemId}", { params: { path: { itemId: item.id } } }));
}

describe("the mocks' form library", () => {
  it("refuses an entry reading a form's inputs, and a form reading params", async () => {
    await expect(
      call(
        api.POST("/admin/form-library", {
          body: {
            key: "apps",
            name: "Apps",
            use: "source",
            kind: "cel",
            params: { options: "inputs.apps" },
            parameters: [],
          },
        }),
      ),
    ).rejects.toMatchObject({ status: 422, fieldErrors: [{ field: "/params/options", code: "invalid" }] });
    const item = db.items.find((i) => i.slug === "rhel9-server")!;
    const admin = await call(api.GET("/admin/catalog/items/{itemId}", { params: { path: { itemId: item.id } } }));
    const draft = await call(
      api.POST("/admin/catalog/items/{itemId}/versions", { params: { path: { itemId: item.id } }, body: {} }),
    );
    const d = draft.definition;
    await expect(
      call(
        api.PUT("/admin/catalog/items/{itemId}/versions/{versionId}", {
          params: { path: { itemId: item.id, versionId: draft.id } },
          body: {
            base_revision: draft.revision,
            definition: {
              ...d,
              ui_schema: {
                ...d.ui_schema,
                validations: [
                  { key: "cc", kind: "library", name: "costCentreOpen", version: 4, params: { cost_center: "x" } },
                  { key: "url", kind: "rest", params: { url: "https://cmdb.corp.example/${params.x}" } },
                ],
              },
            },
          },
        }),
      ),
    ).rejects.toMatchObject({
      status: 422,
      fieldErrors: [
        { field: "/definition/ui_schema/validations/1/params/url", code: "invalid" },
        {
          field: "/definition/ui_schema/validations/0/version",
          message: "Entry costCentreOpen has no version 4: its latest is 1.",
        },
      ],
    });
    expect(admin.published_version).toBeDefined();
  });

  it("runs a form's validations on the review step and on submit", async () => {
    const item = await published(
      "checked",
      definition([
        {
          key: "cc",
          kind: "library",
          name: "costCentreOpen",
          version: 1,
          params: { cost_center: "inputs.cost_center" },
        },
        {
          key: "cmdb",
          title: "The CMDB",
          kind: "rest",
          params: { url: "https://cmdb.corp.example/check?cc=${inputs.cost_center}" },
        },
      ]),
    );
    const project = db.projects[0]!;
    const draft = (cc: string) => ({
      catalog_item_id: item.id,
      version_id: item.published_version!.id,
      count: 1,
      project_id: project.id,
      environment: project.environments[0]!,
      inputs: { cost_center: cc, service: "svc-01" },
    });
    const values = await call(
      api.POST("/forms/values", {
        body: {
          version_id: item.published_version!.id,
          project_id: project.id,
          environment: project.environments[0]!,
          field: "service",
          inputs: {},
        },
      }),
    );
    expect(values.options?.map((o) => o.value)).toEqual(["svc-01", "svc-02"]);

    const plain = await call(api.POST("/requests/preview", { body: draft("X-9") }));
    expect(plain.errors).toEqual([]);
    const review = await call(
      api.POST("/requests/preview", { params: { query: { validate: true } }, body: draft("X-9") }),
    );
    expect(review.errors).toEqual([
      { field: "/inputs/cost_center", code: "refused", message: "Cost centre X-9 is closed." },
    ]);
    await expect(
      call(
        api.POST("/requests", {
          params: { header: { "Idempotency-Key": crypto.randomUUID() } },
          body: draft("CC-down"),
        }),
      ),
    ).rejects.toMatchObject({
      status: 422,
      fieldErrors: [{ field: "/validations/cmdb", code: "validation_failed" }],
    });
    const ok = await call(
      api.POST("/requests", { params: { header: { "Idempotency-Key": crypto.randomUUID() } }, body: draft("CC-1001") }),
    );
    expect(ok.catalog_item.id).toBe(item.id);
  });

  it("gives the items using an entry a draft naming its new version", async () => {
    const item = await published("sourced", definition([]));
    const entry = await call(api.GET("/admin/form-library/{key}", { params: { path: { key: "cmdbServices" } } }));
    expect(entry.used_by).toEqual([expect.objectContaining({ slug: "sourced", state: "published", entry_version: 1 })]);
    await expect(
      call(api.DELETE("/admin/form-library/{key}", { params: { path: { key: "cmdbServices" } } })),
    ).rejects.toMatchObject({ status: 409 });
    const { versions: _v, used_by: _u, version: _n, updated_at: _t, ...w } = entry;
    const saved = await call(
      api.PUT("/admin/form-library/{key}", {
        params: { path: { key: "cmdbServices" } },
        body: { ...w, name: "Services" },
      }),
    );
    expect(saved.version).toBe(2);
    expect(saved.drafts).toEqual([
      { item_id: item.id, slug: "sourced", name: "sourced", number: "1.1", created: true },
    ]);
    const admin = await call(api.GET("/admin/catalog/items/{itemId}", { params: { path: { itemId: item.id } } }));
    const v = await call(
      api.GET("/admin/catalog/items/{itemId}/versions/{versionId}", {
        params: { path: { itemId: item.id, versionId: admin.draft_version!.id } },
      }),
    );
    expect(v.definition.ui_schema.fields.service?.value_source?.version).toBe(2);
    expect(v.changelog).toBe("Uses version 2 of cmdbServices, from the form library.");
  });
});
