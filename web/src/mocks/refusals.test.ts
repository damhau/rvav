// The mocks' refusals to take away a project's or rvav's last manager,
// against the server's (internal/core/roles_admin.go: keepsMembersManager
// and keepsAccessManager, which count group mappings and rvav's own roles).
import { describe, expect, it } from "vitest";
import { api, call } from "@/lib/api/client";

const noAdmin = (project: string) => ({
  status: 409,
  problem: { detail: `${project} would have no project admin: make someone else one first.` },
});

describe("the mocks' last-manager refusals", () => {
  it("count a project's group mappings when someone is removed", async () => {
    await call(
      api.POST("/admin/group-mappings", {
        body: { oidc_group: "rvav-web-admins", project_id: "prj_web", role: "project-admin" },
      }),
    );
    await call(
      api.DELETE("/projects/{projectId}/members/{userId}", {
        params: { path: { projectId: "prj_web", userId: "usr_lmartin" } },
      }),
    );
  });

  it("count rvav's own roles when a group mapping for all of rvav is removed", async () => {
    await call(api.DELETE("/admin/group-mappings/{id}", { params: { path: { id: "gmp_admins" } } }));
  });

  it("keep a project's last group mapping managing its members", async () => {
    await call(api.POST("/admin/projects", { body: { code: "qa", name: "QA", environments: ["dev"] } }));
    const m = await call(
      api.POST("/admin/group-mappings", {
        body: { oidc_group: "rvav-qa-admins", project_id: "prj_qa", role: "project-admin" },
      }),
    );
    await expect(
      call(api.DELETE("/admin/group-mappings/{id}", { params: { path: { id: m.id } } })),
    ).rejects.toMatchObject(noAdmin("QA"));
  });

  it("keep the role a project's members are managed through", async () => {
    await call(api.POST("/admin/projects", { body: { code: "qa", name: "QA", environments: ["dev"] } }));
    await call(
      api.POST("/admin/roles", {
        body: { key: "lead", name: "Lead", permissions: ["project.see", "project.members.manage"] },
      }),
    );
    await call(
      api.PUT("/projects/{projectId}/members/{userId}", {
        params: { path: { projectId: "prj_qa", userId: "usr_tnguyen" } },
        body: { roles: ["lead"] },
      }),
    );
    await expect(
      call(
        api.PATCH("/admin/roles/{key}", { params: { path: { key: "lead" } }, body: { permissions: ["project.see"] } }),
      ),
    ).rejects.toMatchObject(noAdmin("QA"));
  });
});
