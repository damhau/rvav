// The mocks' roles against the server's: internal/auth/testdata/roles.json,
// which TestRolesFixture keeps current. The mocks hold a copy (the web app's
// build sees web/ only); a change to the catalogue or a built-in role is
// copied here.
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";
import copy from "./roles.json";
import { heldIn, meProject } from "./roles";

// Vitest runs in web/, where its configuration is.
const server = JSON.parse(
  readFileSync(resolve(process.cwd(), "../internal/auth/testdata/roles.json"), "utf8"),
) as unknown;

describe("the mocks' roles", () => {
  it("are the server's", () => {
    expect(copy).toEqual(server);
  });

  it("grant what the server grants", () => {
    expect(heldIn(null, ["user", "admin"], [])).not.toContain("approval.project");
    expect(heldIn(null, ["user"], ["member"])).toEqual(["project.see", "request.submit"]);
    const ref = { id: "prj_web", code: "web", name: "Web Platform" };
    expect(meProject(null, ref, ["user", "auditor"], [])).toMatchObject({ roles: [] });
    expect(meProject(null, ref, ["user", "admin"], ["member"])).toMatchObject({
      roles: [{ key: "member", name: "Member" }],
    });
    expect(meProject(null, ref, ["user", "admin"], ["member"])).not.toHaveProperty("role");
  });
});
