// The mocks check a VM item's draft and publish it as the server does
// (internal/core/blueprint_check.go): the size New item gives it, and the
// CPU and memory publishing needs, at the binding.
import { describe, expect, it } from "vitest";
import { api, call } from "@/lib/api/client";
import type { components } from "@/lib/api/schema";

type S = components["schemas"];
type Def = S["CatalogVersionDefinition"];

const fields = async (p: Promise<unknown>) => {
  const e = (await p.then(
    () => ({}),
    (x: unknown) => x,
  )) as { status?: number; fieldErrors?: S["FieldError"][] };
  return { status: e.status, at: (e.fieldErrors ?? []).map((f) => `${f.field} ${f.code}`).sort() };
};

async function newVmItem(slug: string) {
  const item = await call(
    api.POST("/admin/catalog/items", {
      body: { slug, name: slug, summary: "As New item makes it.", category: "compute", type: "vm_blueprint" },
    }),
  );
  const at = { params: { path: { itemId: item.id, versionId: item.draft_version!.id } } };
  const draft = await call(api.GET("/admin/catalog/items/{itemId}/versions/{versionId}", at));
  return {
    def: draft.definition,
    save: (definition: Def) =>
      call(api.PUT("/admin/catalog/items/{itemId}/versions/{versionId}", { ...at, body: { definition } })),
    publish: () => call(api.POST("/admin/catalog/items/{itemId}/versions/{versionId}/publish", { ...at, body: {} })),
  };
}

const withBlueprint = (def: Def, blueprint: Record<string, unknown>): Def => ({ ...def, blueprint });

describe("the mocks' VM item drafts", () => {
  it("give a new VM item a size, and publish none whose CPU or memory can't be worked out", async () => {
    const { def, save, publish } = await newVmItem("starter-vm");
    const bp = def.blueprint as Record<string, unknown>;
    expect(bp.bindings).toEqual({ vcpu: "2", memory_gb: "4" });

    await save(withBlueprint(def, { ...bp, bindings: { memory_gb: "0" } }));
    expect(await fields(publish())).toEqual({
      status: 422,
      at: ["/definition/blueprint/bindings/memory_gb out_of_range", "/definition/blueprint/bindings/vcpu required"],
    });

    await save(
      withBlueprint(def, {
        ...bp,
        bindings: { vcpu: "2" },
        components: [{ key: "web", bindings: { memory_gb: "8" } }, { key: "db" }],
      }),
    );
    expect(await fields(publish())).toEqual({
      status: 422,
      at: ["/definition/blueprint/components/1/bindings/memory_gb required"],
    });

    await save(def);
    expect((await publish()).published_version).toBeDefined();
  });
});
