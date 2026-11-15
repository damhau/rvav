import { screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { customRoles } from "@/mocks/roles";
import { server } from "@/mocks/node";
import { db, resetDb } from "@/mocks/seed";
import { renderApp } from "@/test/render";

afterEach(() => localStorage.removeItem("rvav.mock.as"));

// Who holds a page's read permission alone for all of rvav is refused every
// project's settings (GET /admin/projects): the page still shows its lists,
// a project by its ID.
describe.each([
  { path: "/admin/policies", permission: "policies.read", panel: "Approval policies", names: true },
  { path: "/admin/infrastructure", permission: "infrastructure.read", panel: "VM templates", names: true },
  // Its projects are asked for by the registration dialog only, for who manages workflows.
  { path: "/admin/workflows", permission: "extensibility.read", panel: "Registered workflows", names: false },
])("$path for who holds $permission alone", ({ path, permission, panel, names }) => {
  it("shows its lists without every project's settings", async () => {
    localStorage.setItem("rvav.mock.as", "project-admin");
    resetDb();
    customRoles(db).push({
      key: "reader",
      name: "Reader",
      description: "",
      permissions: [permission],
      built_in: false,
    });
    db.me.permissions = [...db.me.permissions, permission];
    db.me.roles = [...db.me.roles, { key: "reader", name: "Reader" }];
    let refused = false;
    const seen = ({ request, response }: { request: Request; response: Response }) => {
      if (new URL(request.url).pathname.endsWith("/admin/projects") && response.status === 403) refused = true;
    };
    server.events.on("response:mocked", seen);
    try {
      renderApp(path);
      const region = await screen.findByRole("region", { name: panel });
      if (names) await waitFor(() => expect(refused).toBe(true));
      expect(await within(region).findAllByRole("row")).not.toHaveLength(0);
      expect(screen.queryByRole("alert")).toBeNull();
    } finally {
      server.events.removeListener("response:mocked", seen);
    }
  });
});
