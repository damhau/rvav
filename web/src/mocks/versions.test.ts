// The mocks' requestable versions against the server's (ADR-0046):
// published beside the others, listed by the catalog, an earlier one's
// form, a retired one refused as stale, and withdrawing retires them all.
import { describe, expect, it } from "vitest";
import { api, call } from "@/lib/api/client";
import type { RequestDraft } from "@/lib/api/types";
import { db } from "./seed";

describe("the mocks' requestable versions", () => {
  it("keep an earlier version requestable beside a new one, until it is retired", async () => {
    const item = db.items.find((i) => i.slug === "rhel9-server")!;
    const v1 = item.version;
    const at = (versionId: string) => ({ params: { path: { itemId: item.id, versionId } } });
    const draft = await call(
      api.POST("/admin/catalog/items/{itemId}/versions", { params: { path: { itemId: item.id } }, body: {} }),
    );
    let admin = await call(
      api.POST("/admin/catalog/items/{itemId}/versions/{versionId}/publish", {
        ...at(draft.id),
        body: { keep_published: true },
      }),
    );
    expect(admin.published_version?.id).toBe(draft.id);
    expect(admin.versions.filter((v) => v.state === "published").map((v) => v.id)).toEqual([draft.id, v1.id]);

    const listed = await call(
      api.GET("/catalog/items/{itemId}", { params: { path: { itemId: item.slug }, query: { project_id: "prj_web" } } }),
    );
    expect(listed.version.id).toBe(draft.id);
    expect(listed.versions?.map((v) => v.number)).toEqual([draft.number, v1.number]);
    const form = await call(
      api.GET("/catalog/items/{itemId}/form", {
        params: { path: { itemId: item.slug }, query: { project_id: "prj_web", version_id: v1.id } },
      }),
    );
    expect(form.version_id).toBe(v1.id);

    const request = (version_id: string): RequestDraft => ({
      catalog_item_id: item.id,
      version_id,
      project_id: "prj_web",
      environment: "dev",
      inputs: {},
      count: 1,
    });
    const onV1 = await call(api.POST("/requests/preview", { body: request(v1.id) }));
    expect(onV1.errors.filter((e) => e.field === "/version_id")).toEqual([]);

    admin = await call(
      api.POST("/admin/catalog/items/{itemId}/versions/{versionId}/deprecate", { ...at(v1.id), body: {} }),
    );
    expect(admin.published_version?.id).toBe(draft.id);
    const stale = await call(api.POST("/requests/preview", { body: request(v1.id) }));
    expect(stale.valid).toBe(false);
    expect(stale.errors).toContainEqual({
      field: "/version_id",
      code: "stale_version",
      message: `Version ${v1.number} of ${item.name} is no longer offered: request ${draft.number}.`,
    });
    const after = await call(
      api.GET("/catalog/items/{itemId}", { params: { path: { itemId: item.slug }, query: { project_id: "prj_web" } } }),
    );
    expect(after.versions).toBeUndefined();

    await call(
      api.POST("/admin/catalog/items/{itemId}/versions/{versionId}/publish", {
        ...at(v1.id),
        body: { keep_published: true },
      }),
    );
    admin = await call(
      api.POST("/admin/catalog/items/{itemId}/versions/{versionId}/deprecate", {
        ...at(draft.id),
        body: { withdraw: true },
      }),
    );
    expect(admin.published_version).toBeUndefined();
    expect(admin.versions.some((v) => v.state === "published")).toBe(false);
  });
});
