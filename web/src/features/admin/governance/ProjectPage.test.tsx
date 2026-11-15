import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it } from "vitest";
import { customRoles } from "@/mocks/roles";
import { db, resetDb } from "@/mocks/seed";
import { renderApp } from "@/test/render";

// A project's members as its project admin sees them (ADR-0034): the
// rvav.mock.as persona, whose Web Platform has Thomas Nguyen holding VM
// operator, which they may not take away.
function asProjectAdmin() {
  localStorage.setItem("rvav.mock.as", "project-admin");
  resetDb();
}

afterEach(() => localStorage.removeItem("rvav.mock.as"));

describe("a project's members", () => {
  it("offers no removal the server would refuse, and says a refusal by the row", async () => {
    asProjectAdmin();
    const user = userEvent.setup();
    renderApp("/admin/projects/prj_web");
    const members = await screen.findByRole("region", { name: "Members" });
    expect(await within(members).findByRole("list", { name: "Roles of Thomas Nguyen" })).toHaveTextContent(
      "VM operator",
    );
    await within(members).findByRole("button", { name: "Change roles of Thomas Nguyen" });
    expect(within(members).queryByRole("button", { name: "Remove Thomas Nguyen" })).toBeNull();

    await user.click(within(members).getByRole("button", { name: "Change roles of Thomas Nguyen" }));
    expect(await screen.findByRole("dialog", { name: "Roles of Thomas Nguyen" })).toHaveTextContent(
      "VM operator(you may not take it away)",
    );
    await user.click(screen.getByRole("button", { name: "Cancel" }));

    await user.click(within(members).getByRole("button", { name: "Remove Léa Martin" }));
    expect(await within(members).findByRole("alert")).toHaveTextContent(
      "Web Platform would have no project admin: make someone else one first.",
    );
    expect(screen.getAllByText(/would have no project admin/)).toHaveLength(1);
  });

  it("starts adding someone as a role the caller may give", async () => {
    asProjectAdmin();
    // Members managed through a role without request.submit: Member is not theirs to give.
    customRoles(db).push({
      key: "membership-manager",
      name: "Membership manager",
      description: "",
      permissions: ["project.members.manage", "project.see", "project.settings.read"],
      built_in: false,
    });
    db.me.projects = db.me.projects.map((p) =>
      p.project.id === "prj_web"
        ? {
            ...p,
            roles: [{ key: "membership-manager", name: "Membership manager" }],
            permissions: ["project.members.manage", "project.see", "project.settings.read"],
          }
        : p,
    );
    const user = userEvent.setup();
    renderApp("/admin/projects/prj_web");
    const members = await screen.findByRole("region", { name: "Members" });
    const as = await within(members).findByRole("combobox", { name: "As" });
    await waitFor(() => expect(as).toHaveTextContent("Viewer"));
    await user.type(within(members).getByRole("textbox", { name: "Add someone" }), "Claire");
    await user.click(await within(members).findByRole("button", { name: /Claire Dubois/ }));
    expect(await within(members).findByRole("list", { name: "Roles of Claire Dubois" })).toHaveTextContent("Viewer");
    expect(within(members).queryByRole("alert")).toBeNull();
  });
});
