// The mocks' sharing against the server's (internal/core/catalog_sharing.go,
// TestItemSharing): a share for chosen people in a project hides the item
// from the project's other members, in the list, the categories' counts,
// the item and its form, and refuses their submit; the requesters preview
// says who is in and out.
import { describe, expect, it } from "vitest";
import { api, call, isApiError } from "@/lib/api/client";
import { db } from "./seed";

const web = "prj_web";

const share = (audiences: { project_id: string; user_ids: string[]; groups: string[]; roles: string[] }[]) =>
  call(
    api.PUT("/admin/catalog/items/{itemId}/entitlements", {
      params: { path: { itemId: "ubuntu-server" } },
      body: { all_projects: false, project_ids: [web, "prj_data"], audiences },
    }),
  );

const listed = async () => {
  const page = await call(api.GET("/catalog/items", { params: { query: { project_id: web, limit: 200 } } }));
  return page.items.some((i) => i.slug === "ubuntu-server");
};

const computeCount = async () => {
  const cats = await call(api.GET("/catalog/categories", { params: { query: { project_id: web } } }));
  return cats.items.find((c) => c.key === "compute")?.item_count;
};

const status = async (p: Promise<unknown>) => {
  try {
    await p;
    return 200;
  } catch (e) {
    return isApiError(e) ? e.status : 0;
  }
};

describe("the mocks' sharing", () => {
  it("hides an item shared with others from the mock user, and says who can request", async () => {
    const before = await computeCount();
    expect(await listed()).toBe(true);
    const saved = await share([{ project_id: web, user_ids: [], groups: ["rvav-web"], roles: [] }]);
    expect(saved.audiences).toEqual([{ project_id: web, user_ids: [], groups: ["rvav-web"], roles: [] }]);

    expect(await listed()).toBe(false);
    expect(await computeCount()).toBe(before! - 1);
    expect(
      await status(
        call(
          api.GET("/catalog/items/{itemId}", {
            params: { path: { itemId: "ubuntu-server" }, query: { project_id: web } },
          }),
        ),
      ),
    ).toBe(404);
    expect(
      await status(
        call(
          api.GET("/catalog/items/{itemId}/form", {
            params: { path: { itemId: "ubuntu-server" }, query: { project_id: web } },
          }),
        ),
      ),
    ).toBe(404);
    const item = db.items.find((i) => i.slug === "ubuntu-server")!;
    expect(
      await status(
        call(
          api.POST("/requests", {
            params: { header: { "Idempotency-Key": crypto.randomUUID() } },
            body: {
              catalog_item_id: item.id,
              version_id: item.version.id,
              project_id: web,
              environment: "dev",
              inputs: {},
              count: 1,
            },
          }),
        ),
      ),
    ).toBe(403);

    const who = await call(
      api.POST("/admin/catalog/items/{itemId}/entitlements/requesters", {
        params: { path: { itemId: "ubuntu-server" } },
        body: { project_id: web, user_ids: [], groups: ["rvav-web"], roles: [] },
      }),
    );
    expect(who.people.map((p) => [p.user.id, p.groups])).toEqual([["usr_tnguyen", ["rvav-web"]]]);
    expect(who.left_out.map((u) => u.id)).toContain(db.me.user.id);
    expect(who.groups).toEqual([{ name: "rvav-web", seen: 1 }]);

    // By the role Léa holds in Web Platform, she finds it again.
    await share([{ project_id: web, user_ids: [], groups: ["rvav-web"], roles: ["project-admin"] }]);
    expect(await listed()).toBe(true);
  });

  it("refuses an audience for a project the item isn't shared with", async () => {
    await expect(
      call(
        api.PUT("/admin/catalog/items/{itemId}/entitlements", {
          params: { path: { itemId: "ubuntu-server" } },
          body: {
            all_projects: false,
            project_ids: ["prj_data"],
            audiences: [{ project_id: web, user_ids: [], groups: ["rvav-web"], roles: [] }],
          },
        }),
      ),
    ).rejects.toMatchObject({ fieldErrors: [{ field: "/audiences/0/project_id", code: "not_shared" }] });
  });
});
