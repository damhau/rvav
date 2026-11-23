import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it } from "vitest";
import { adminState } from "@/mocks/administration";
import { customRoles } from "@/mocks/roles";
import { db, resetDb } from "@/mocks/seed";
import { renderApp } from "@/test/render";

afterEach(() => localStorage.removeItem("rvav.mock.as"));

describe("what someone may do", () => {
  it("offers every project in use to who reads access, not projects' settings", async () => {
    // Léa as a project admin of Web Platform, given a role holding
    // access.read alone for all of rvav: no project.settings.read anywhere.
    localStorage.setItem("rvav.mock.as", "project-admin");
    resetDb();
    customRoles(db).push({
      key: "access-reader",
      name: "Access reader",
      description: "",
      permissions: ["access.read"],
      built_in: false,
    });
    db.me.permissions = [...db.me.permissions, "access.read"];
    db.me.roles = [...db.me.roles, { key: "access-reader", name: "Access reader" }];
    db.projects.push({ id: "prj_qa", code: "qa", name: "QA", description: "", cost_center: "", environments: ["dev"] });
    db.projects.push({
      id: "prj_old",
      code: "old",
      name: "Old",
      description: "",
      cost_center: "",
      environments: ["dev"],
    });
    adminState(db).extra.prj_old!.archived_at = new Date().toISOString();

    const user = userEvent.setup();
    renderApp("/admin/roles?tab=explain");
    const panel = await screen.findByRole("region", { name: "What someone may do" });
    await user.click(within(panel).getByRole("combobox", { name: "Where" }));
    await screen.findByRole("option", { name: "QA" });
    expect(screen.getAllByRole("option").map((o) => o.textContent)).toEqual([
      "For all of rvav",
      "Web Platform",
      "Data Engineering",
      "QA",
    ]);
    await user.click(screen.getByRole("option", { name: "QA" }));
    expect(within(panel).getByRole("combobox", { name: "Where" })).toHaveTextContent("QA");
    expect((await within(panel).findAllByText(/in QA/)).length).toBeGreaterThan(0);
  });

  it("keeps who and where chosen across the page's tabs", async () => {
    const user = userEvent.setup();
    const { router } = renderApp("/admin/roles?tab=explain&project=prj_web");
    const panel = await screen.findByRole("region", { name: "What someone may do" });
    await waitFor(() =>
      expect(within(panel).getByRole("combobox", { name: "Where" })).toHaveTextContent("Web Platform"),
    );
    await user.click(screen.getByRole("tab", { name: "Roles" }));
    await waitFor(() => expect(router.state.location.search).toEqual({ tab: "roles", project: "prj_web" }));
    await user.click(screen.getByRole("tab", { name: "What someone may do" }));
    const again = await screen.findByRole("region", { name: "What someone may do" });
    await waitFor(() =>
      expect(within(again).getByRole("combobox", { name: "Where" })).toHaveTextContent("Web Platform"),
    );
    expect(router.state.location.search).toEqual({ tab: "explain", project: "prj_web" });
  });
});
