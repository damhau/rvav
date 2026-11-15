import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it } from "vitest";
import { resetDb } from "@/mocks/seed";
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
});
