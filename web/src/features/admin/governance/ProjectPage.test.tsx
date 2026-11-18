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
    await user.click(await within(members).findByRole("button", { name: "Actions for Thomas Nguyen" }));
    expect(await screen.findByRole("menuitem", { name: "Change roles" })).toBeInTheDocument();
    expect(screen.queryByRole("menuitem", { name: "Remove" })).toBeNull();

    await user.click(screen.getByRole("menuitem", { name: "Change roles" }));
    expect(await screen.findByRole("dialog", { name: "Roles of Thomas Nguyen" })).toHaveTextContent(
      "VM operator(you may not take it away)",
    );
    await user.click(screen.getByRole("button", { name: "Cancel" }));

    await user.click(within(members).getByRole("button", { name: "Actions for Léa Martin" }));
    await user.click(await screen.findByRole("menuitem", { name: "Remove" }));
    const confirm = await screen.findByRole("alertdialog", { name: "Remove Léa Martin?" });
    await user.click(within(confirm).getByRole("button", { name: "Remove member" }));
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

describe("a project's placement and quotas", () => {
  it("removes a placement rule after saying what is left", async () => {
    const user = userEvent.setup();
    renderApp("/admin/projects/prj_web?tab=placement");
    const rules = await screen.findByRole("region", { name: "Placement rules" });
    const menus = await within(rules).findAllByRole("button", { name: /^Actions for the rule of priority/ });
    await user.click(menus[0]!);
    await user.click(await screen.findByRole("menuitem", { name: "Remove" }));
    const confirm = await screen.findByRole("alertdialog", { name: /^Remove the rule of priority \d+ in dev\?$/ });
    expect(confirm).toHaveTextContent(/nowhere to go|other rules? stay/);
    await user.click(within(confirm).getByRole("button", { name: "Remove rule" }));
    expect(await screen.findByText("Placement rule removed")).toBeInTheDocument();
  });

  it("adds a limit in a sheet, refuses a second one on the same resource and place, and shows what is taken", async () => {
    const user = userEvent.setup();
    renderApp("/admin/projects/prj_web?tab=quotas");
    const quotas = await screen.findByRole("region", { name: "Quotas" });
    const vcpu = await within(quotas).findByRole("row", { name: /^vCPU/ });
    await waitFor(() => expect(vcpu).toHaveTextContent("112 of 160"));
    await user.click(within(quotas).getByRole("button", { name: "Add a limit" }));
    const sheet = await screen.findByRole("dialog", { name: "New limit" });
    expect(within(sheet).getByText("This resource has a limit there already: change that one.")).toBeInTheDocument();
    expect(within(sheet).getByRole("button", { name: "Save limit" })).toBeDisabled();
    await user.click(within(sheet).getByRole("combobox", { name: "In" }));
    await user.click(await screen.findByRole("option", { name: "dev" }));
    await user.type(within(sheet).getByRole("spinbutton", { name: "Limit" }), "16");
    await user.click(within(sheet).getByRole("button", { name: "Save limit" }));
    expect(await screen.findByText("Limit saved")).toBeInTheDocument();
    expect(await within(quotas).findByRole("row", { name: /^vCPU dev 16/ })).toBeInTheDocument();
  });

  it("opens the tab the address names", async () => {
    renderApp("/admin/projects/prj_web?tab=details");
    expect(await screen.findByRole("region", { name: "Details" })).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "Details" })).toHaveAttribute("aria-selected", "true");
  });
});
