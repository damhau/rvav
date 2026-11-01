import { describe, expect, it } from "vitest";
import type { Me } from "@/lib/api/types";
import { accountRole } from "./roles";

const me = (global_roles: Me["global_roles"]): Me => ({
  user: { id: "usr_1", name: "Local administrator", email: "" },
  global_roles,
  projects: [],
});

describe("accountRole", () => {
  it("names the role in the current project", () => {
    expect(accountRole(me(["user", "admin"]), "prj_web", "project-admin")).toBe("Project admin");
    expect(accountRole(me(["user"]), "prj_web", "member")).toBe("Member");
  });

  it("names someone's own role when they are in no project, never Viewer", () => {
    // A new install's administrator has no project until they make one; the
    // top bar said "Local administrator · Viewer".
    expect(accountRole(me(["admin"]), "", "viewer")).toBe("Administrator");
    expect(accountRole(me(["user", "auditor", "catalog-admin"]), "", "viewer")).toBe("Catalog admin");
    expect(accountRole(me(["user"]), "", "viewer")).toBeUndefined();
  });
});
