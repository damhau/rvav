// The mocks check a VM item's draft and publish it as the server does
// (internal/core/blueprint_check.go): the size and naming New item gives
// it, and the CPU and memory publishing needs, at the binding; what its
// bindings are and read (blueprint_inputs.go); what its custom resources
// bind (custom_resources.go); a request nothing names refused, and the
// version that names nothing warned of it (#216); an item saved without a
// category refused at the field, as a new one is (catalog_admin.go).
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
    item,
    def: draft.definition,
    save: (definition: Def) =>
      call(api.PUT("/admin/catalog/items/{itemId}/versions/{versionId}", { ...at, body: { definition } })),
    publish: () => call(api.POST("/admin/catalog/items/{itemId}/versions/{versionId}/publish", { ...at, body: {} })),
    warnings: async () =>
      ((await call(api.GET("/admin/catalog/items/{itemId}/versions/{versionId}", at))).warnings ?? []).filter(
        (w) => w.code === "no_naming",
      ),
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

  it("name a new VM item's VMs, and refuse a request for one nothing names until a naming policy does", async () => {
    const published = async (slug: string, naming?: string) => {
      const { item, def, save, publish } = await newVmItem(slug);
      const bp = def.blueprint as Record<string, unknown>;
      expect(bp.naming).toBe("{project}-{environment}-{component}{seq:03}");
      if (naming !== undefined) await save(withBlueprint(def, { ...bp, naming }));
      await publish();
      await call(
        api.PUT("/admin/catalog/items/{itemId}/entitlements", {
          params: { path: { itemId: item.id } },
          body: { all_projects: false, project_ids: ["prj_web"] },
        }),
      );
      return () =>
        call(
          api.POST("/requests/preview", {
            body: {
              catalog_item_id: item.id,
              version_id: item.draft_version!.id,
              project_id: "prj_web",
              environment: "dev",
              inputs: { name: "first" },
              count: 1,
            },
          }),
        );
    };
    const unnamed = (p: S["RequestPreview"]) => p.errors.filter((e) => e.code === "no_naming");

    const starter = await published("named-vm");
    expect(unnamed(await starter())).toEqual([]);

    const bare = await published("unnamed-vm", "");
    const refused = await bare();
    expect(refused.valid).toBe(false);
    expect(refused.names).toEqual([]);
    expect(unnamed(refused)).toEqual([
      {
        field: "",
        code: "no_naming",
        message:
          "No naming policy applies to Web Platform in Development, and the item gives the VMs no naming of its own. Ask a catalog admin to check this item.",
      },
    ]);

    const policy = await call(
      api.POST("/admin/naming-policies", {
        body: {
          name: "Web names",
          template: "{project}-{environment}-pol{seq:02}",
          counter: "prefix",
          start: 1,
          increment: 1,
          offers: [{ project_id: "prj_web" }],
        },
      }),
    );
    expect(unnamed(await bare())).toEqual([]);
    await call(api.DELETE("/admin/naming-policies/{id}", { params: { path: { id: policy.id } } }));
  });

  it("refuse an item saved without a category at the field, as a new one", async () => {
    const { item } = await newVmItem("uncategorised-vm");
    const saved = (category: string) =>
      call(
        api.PATCH("/admin/catalog/items/{itemId}", { params: { path: { itemId: item.id } }, body: { category } }),
      ).catch((e: { status: number; fieldErrors: S["FieldError"][] }) => ({ status: e.status, errors: e.fieldErrors }));
    expect(await saved("")).toEqual({
      status: 422,
      errors: [{ field: "/category", code: "required", message: "Choose a category." }],
    });
    expect(await saved("nope")).toEqual({
      status: 422,
      errors: [{ field: "/category", code: "unknown", message: 'There is no category "nope".' }],
    });
  });

  it("warn of VMs only a naming policy names, unless one applies everywhere", async () => {
    const { def, save, warnings } = await newVmItem("warned-vm");
    const bp = def.blueprint as Record<string, unknown>;
    expect(await warnings()).toEqual([]);
    const needs =
      "its requests need a naming policy, and are refused in a project and environment no policy applies to.";

    await save(withBlueprint(def, { ...bp, naming: "" }));
    expect(await warnings()).toEqual([
      {
        field: "/definition/blueprint/naming",
        code: "no_naming",
        message: `The item gives its VMs no naming: ${needs}`,
      },
    ]);

    await save(
      withBlueprint(def, {
        ...bp,
        naming: "",
        components: [
          { key: "web", naming: "{project}-{environment}-web{seq:02}" },
          { key: "db", title: "Database" },
        ],
      }),
    );
    expect(await warnings()).toEqual([
      {
        field: "/definition/blueprint/components/1/naming",
        code: "no_naming",
        message: `Database has no naming, and the item gives its VMs none: ${needs}`,
      },
    ]);

    const everywhere = await call(
      api.POST("/admin/naming-policies", {
        body: {
          name: "All names",
          template: "{project}-{environment}-{seq}",
          counter: "prefix",
          start: 1,
          increment: 1,
          offers: [{}],
        },
      }),
    );
    expect(await warnings()).toEqual([]);
    await call(api.DELETE("/admin/naming-policies/{id}", { params: { path: { id: everywhere.id } } }));
  });

  it("take a number for a binding as its constant, and refuse a list or an input the form hasn't, at the field", async () => {
    const { def, save, publish } = await newVmItem("bound-vm");
    const bp = def.blueprint as Record<string, unknown>;

    const list = await fields(save(withBlueprint(def, { ...bp, bindings: { vcpu: "2", memory_gb: [4] } })));
    expect(list).toEqual({ status: 422, at: ["/definition/blueprint/bindings/memory_gb invalid"] });

    const reads = {
      ...bp,
      bindings: { vcpu: "2", memory_gb: "4", network: "inputs.zone", folder: "inputs.cost_center" },
      property_groups: [{ key: "corp_ops", version: 1 }],
      components: [{ key: "web", count: "inputs.nodes" }],
    };
    expect(await fields(save(withBlueprint(def, reads)))).toEqual({
      status: 422,
      at: [
        "/definition/blueprint/bindings/network unknown_input",
        "/definition/blueprint/components/0/count unknown_input",
      ],
    });
    const named = await save(withBlueprint(def, reads)).catch((e: { fieldErrors: S["FieldError"][] }) => e.fieldErrors);
    expect(named).toContainEqual({
      field: "/definition/blueprint/bindings/network",
      code: "unknown_input",
      message: 'This reads inputs.zone, and "zone" is not an input of the form\'s schema.',
    });

    await save(withBlueprint(def, { ...bp, bindings: { vcpu: 2, memory_gb: 4 } }));
    expect((await publish()).published_version).toBeDefined();
  });

  it("refuse an input a custom resource binds that its type's create workflow doesn't take, at its key", async () => {
    const { def, save } = await newVmItem("aliased-vm");
    const bp = { ...(def.blueprint as Record<string, unknown>), components: [{ key: "web", count: 2 }] };
    const resources = (alias: Record<string, unknown>) =>
      withBlueprint(def, {
        ...bp,
        resources: [
          { key: "lb", type: "load-balancer", with: { members: "components.web.addresses", anything: "1" } },
          { key: "alias", type: "dns-alias", with: alias },
          { key: "later", type: "not-yet", with: { anything: "1" } },
        ],
      });

    const refused = await save(resources({ fqdn: '"web.corp.example"', target: "components.web.fqdns[0]" })).catch(
      (e: { fieldErrors: S["FieldError"][] }) => e.fieldErrors,
    );
    expect(refused).toEqual([
      {
        field: "/definition/blueprint/resources/1/with/target",
        code: "unknown_input",
        message: "DNS alias takes fqdn and name: target isn't one of its inputs.",
      },
    ]);
    expect((await save(resources({ fqdn: '"web.corp.example"', name: '"web-alias"' }))).id).toBeDefined();
  });
});
