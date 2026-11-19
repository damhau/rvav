import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it } from "vitest";
import { quotaState } from "@/mocks/quotas";
import { db, resetDb } from "@/mocks/seed";
import { renderApp } from "@/test/render";

afterEach(() => localStorage.removeItem("rvav.mock.as"));

describe("each member's quota", () => {
  it("is an administrator's to set, and their lines say so", async () => {
    const user = userEvent.setup();
    renderApp("/admin/projects/prj_web?tab=quotas");
    const panel = await screen.findByRole("region", { name: "Each member's quota" });
    await user.click(await within(panel).findByRole("button", { name: "Add a limit" }));
    const sheet = await screen.findByRole("dialog", { name: "New limit" });
    expect(within(sheet).getByText("Empty for no limit.")).toBeInTheDocument();
    await user.type(within(sheet).getByRole("spinbutton", { name: "Limit" }), "8");
    await user.click(within(sheet).getByRole("button", { name: "Save limit" }));
    expect(await screen.findByText("Limit saved")).toBeInTheDocument();
    const row = await within(panel).findByRole("row", { name: /^vCPU/ });
    expect(row).toHaveTextContent("Set by an administrator");
    expect(row).toHaveTextContent("8");
    expect(quotaState(db).perUser.prj_web).toEqual([
      { resource: "vcpu", limit: 8, enforcement: "hard", set_by_admin: true },
    ]);
  });

  it("lets a project admin write within the project's quota, and leave an administrator's lines alone", async () => {
    localStorage.setItem("rvav.mock.as", "project-admin");
    resetDb();
    quotaState(db).perUser.prj_web = [{ resource: "vcpu", limit: 8, enforcement: "hard", set_by_admin: true }];
    const user = userEvent.setup();
    renderApp("/admin/projects/prj_web?tab=quotas");
    const panel = await screen.findByRole("region", { name: "Each member's quota" });
    await within(panel).findByRole("row", { name: /^vCPU/ });
    expect(within(panel).queryByRole("button", { name: "Actions for the vCPU limit" })).toBeNull();

    await user.click(within(panel).getByRole("button", { name: "Add a limit" }));
    const sheet = await screen.findByRole("dialog", { name: "New limit" });
    expect(within(sheet).getByRole("combobox", { name: "Resource" })).toHaveTextContent("Memory (GB)");
    await waitFor(() => expect(within(sheet).getByText("At most 640: Web Platform's own limit.")).toBeInTheDocument());
    const limit = within(sheet).getByRole("spinbutton", { name: "Limit" });
    await user.type(limit, "1000");
    await user.click(within(sheet).getByRole("button", { name: "Save limit" }));
    await waitFor(() =>
      expect(limit).toHaveAccessibleDescription(
        /Web Platform has 640 GB of memory in all: each member's limit can't be more\./,
      ),
    );
    await user.clear(limit);
    await user.type(limit, "64");
    await user.click(within(sheet).getByRole("button", { name: "Save limit" }));
    expect(await screen.findByText("Limit saved")).toBeInTheDocument();
    expect(quotaState(db).perUser.prj_web).toEqual([
      { resource: "memory_gb", limit: 64, enforcement: "hard", set_by_admin: false },
      { resource: "vcpu", limit: 8, enforcement: "hard", set_by_admin: true },
    ]);
  });
});

describe("rvav's quota", () => {
  it("is on the policies page for administrators, as a whole and for each user", async () => {
    const user = userEvent.setup();
    renderApp("/admin/policies?tab=quotas");
    const each = await screen.findByRole("region", { name: "Each user across rvav" });
    await user.click(await within(each).findByRole("button", { name: "Add a limit" }));
    const sheet = await screen.findByRole("dialog", { name: "New limit" });
    await user.type(within(sheet).getByRole("spinbutton", { name: "Limit" }), "32");
    await user.click(within(sheet).getByRole("button", { name: "Save limit" }));
    expect(await within(each).findByRole("row", { name: /^vCPU 32/ })).toBeInTheDocument();
    expect(quotaState(db).global).toEqual([{ resource: "vcpu", limit: 32, enforcement: "hard", per_user: true }]);
    expect(within(screen.getByRole("region", { name: "All of rvav" })).getByText("No limit")).toBeInTheDocument();
  });

  it("isn't offered to a project admin", async () => {
    localStorage.setItem("rvav.mock.as", "project-admin");
    resetDb();
    renderApp("/admin/policies?tab=quotas");
    expect(await screen.findByRole("tab", { name: "Approval", selected: true })).toBeInTheDocument();
    expect(screen.queryByRole("tab", { name: "Quotas" })).toBeNull();
  });
});
