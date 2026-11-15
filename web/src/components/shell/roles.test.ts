import { describe, expect, it } from "vitest";
import type { Me, RoleRef } from "@/lib/api/types";
import { rolesLabel } from "./roles";

const role = (key: string, name: string): RoleRef => ({ key, name });
const user = role("user", "User");
const web = { id: "prj_web", code: "web", name: "Web Platform" };

const me = (roles: RoleRef[], inWeb: RoleRef[] | null): Me => ({
  user: { id: "usr_1", name: "Local administrator", email: "" },
  permissions: [],
  roles,
  projects: inWeb ? [{ project: web, roles: inWeb, permissions: ["project.see"] }] : [],
});

describe("rolesLabel", () => {
  it("names the roles given in the current project", () => {
    expect(
      rolesLabel(me([user, role("admin", "Administrator")], [role("project-admin", "Project admin")]), "prj_web"),
    ).toBe("Project admin");
    expect(rolesLabel(me([user], [role("member", "Member"), role("vm-operator", "VM operator")]), "prj_web")).toBe(
      "Member, VM operator",
    );
  });

  it("names the roles held for all of rvav where the project gives none", () => {
    expect(rolesLabel(me([user, role("auditor", "Auditor")], []), "prj_web")).toBe("Auditor");
  });

  it("names someone's own roles when they are in no project, never Viewer", () => {
    // A new install's administrator has no project until they make one; the
    // top bar said "Local administrator · Viewer".
    expect(rolesLabel(me([role("admin", "Administrator")], null), "")).toBe("Administrator");
    expect(rolesLabel(me([user, role("catalog-admin", "Catalog admin")], null), "")).toBe("Catalog admin");
    expect(rolesLabel(me([user], null), "")).toBeUndefined();
  });
});
