import { describe, expect, it } from "vitest";
import { permissionsAnswer } from "@/mocks/roles";
import { allows, byArea, inOrder, scopeOf } from "./permissions";

const c = permissionsAnswer();

describe("what permissions allow", () => {
  it("groups the catalogue by area, in its order", () => {
    const areas = byArea(c).map(([a]) => a);
    expect(areas[0]).toBe("Projects");
    expect(new Set(areas).size).toBe(areas.length);
  });

  it("says each in the catalogue's words, then the day-2 ones", () => {
    const text = allows(c, ["day2.power:power_off", "project.see", "day2.power:power_on", "day2.snapshots"]).map(
      (s) => s.text,
    );
    expect(text[0]).toMatch(/^See the project/);
    expect(text).toContain("Run the power day-2 actions Power off and Power on.");
    expect(text).toContain("Run every snapshots day-2 action, those added later included.");
  });

  it("gives a role holding a permission for all of rvav only there", () => {
    expect(scopeOf(c, ["project.see", "request.submit"])).toEqual({ scope: "any" });
    expect(scopeOf(c, ["project.see", "policies.read", "access.read"])).toEqual({
      scope: "rvav",
      why: "access.read and policies.read are permissions for all of rvav only.",
    });
  });

  it("sorts as the catalogue lists them, day-2 ones last", () => {
    expect(inOrder(c, ["day2.power", "audit.read", "project.see", "audit.read"])).toEqual([
      "project.see",
      "audit.read",
      "day2.power",
    ]);
  });
});
