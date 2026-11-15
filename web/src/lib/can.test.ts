import { describe, expect, it } from "vitest";
import type { Me } from "./api/types";
import { can, canAnywhere, projectsWith } from "./can";

const web = { id: "prj_web", code: "web", name: "Web Platform" };
const data = { id: "prj_data", code: "data", name: "Data Engineering" };

const me = (permissions: string[], projects: [typeof web, string[]][]): Me => ({
  user: { id: "usr_1", name: "Claire Dubois" },
  permissions,
  roles: [],
  projects: projects.map(([project, held]) => ({ project, roles: [], permissions: held })),
});

describe("can", () => {
  it("is true where the permission is held, false elsewhere", () => {
    const operator = me([], [[web, ["project.see", "day2.power:power_off"]]]);
    expect(can(operator, "day2.power:power_off", "prj_web")).toBe(true);
    expect(can(operator, "day2.power:power_off", "prj_data")).toBe(false);
    expect(can(operator, "day2.power:power_off")).toBe(false);
    expect(can(operator, "day2.power:power_off", "")).toBe(false);
  });

  it("counts a permission held for all of rvav in every project", () => {
    const auditor = me(["project.see", "audit.read"], [[web, ["project.see", "audit.read"]]]);
    expect(can(auditor, "audit.read")).toBe(true);
    expect(can(auditor, "audit.read", "prj_fin")).toBe(true);
    expect(can(auditor, "project.update", "prj_web")).toBe(false);
  });
});

describe("canAnywhere and projectsWith", () => {
  it("find the projects where a permission is held", () => {
    const steward = me(
      [],
      [
        [web, ["project.see", "audit.read"]],
        [data, ["project.see"]],
      ],
    );
    expect(canAnywhere(steward, "audit.read")).toBe(true);
    expect(canAnywhere(steward, "project.update")).toBe(false);
    expect(projectsWith(steward, "audit.read")).toEqual([web]);
    expect(projectsWith(steward, "project.update")).toEqual([]);
    expect(projectsWith(me(["audit.read"], []), "audit.read")).toBe("all");
  });
});
