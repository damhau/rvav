import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { afterEach, describe, expect, it } from "vitest";
import { server } from "@/mocks/node";
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
    const sheet = await screen.findByRole("dialog", { name: "Add a limit" });
    expect(within(sheet).getByText("Empty for no limit.")).toBeInTheDocument();
    await user.type(within(sheet).getByRole("spinbutton", { name: "Limit" }), "8");
    await user.click(within(sheet).getByRole("button", { name: "Add limit" }));
    expect(await screen.findByText("Limit added")).toBeInTheDocument();
    const row = await within(panel).findByRole("row", { name: /^vCPU/ });
    expect(row).toHaveTextContent("Set by an administrator");
    expect(row).toHaveTextContent("8");
    // The empty state's button is gone: the one above the list takes the focus.
    await waitFor(() => expect(within(panel).getByRole("button", { name: "Add a limit" })).toHaveFocus());
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
    const sheet = await screen.findByRole("dialog", { name: "Add a limit" });
    expect(within(sheet).getByRole("combobox", { name: "Resource" })).toHaveTextContent("Memory (GB)");
    await waitFor(() => expect(within(sheet).getByText("At most 640: Web Platform's own limit.")).toBeInTheDocument());
    const limit = within(sheet).getByRole("spinbutton", { name: "Limit" });
    await user.type(limit, "1000");
    await user.click(within(sheet).getByRole("button", { name: "Add limit" }));
    await waitFor(() =>
      expect(limit).toHaveAccessibleDescription(
        /Web Platform has 640 GB of memory in all: each member's limit can't be more\./,
      ),
    );
    await waitFor(() => expect(limit).toHaveFocus());
    await user.clear(limit);
    await user.type(limit, "64");
    await user.click(within(sheet).getByRole("button", { name: "Add limit" }));
    expect(await screen.findByText("Limit added")).toBeInTheDocument();
    expect(quotaState(db).perUser.prj_web).toEqual([
      { resource: "memory_gb", limit: 64, enforcement: "hard", set_by_admin: false },
      { resource: "vcpu", limit: 8, enforcement: "hard", set_by_admin: true },
    ]);
  });

  it("says a problem the server has with another limit, by that limit's name", async () => {
    quotaState(db).perUser.prj_web = [{ resource: "vcpu", limit: 8, enforcement: "hard", set_by_admin: true }];
    server.use(
      http.put("*/api/v1/admin/projects/:id/user-quotas", () =>
        HttpResponse.json(
          {
            title: "Some inputs need attention",
            status: 422,
            errors: [{ field: "/items/0/limit", code: "looser", message: "It can't be more than the project's 4." }],
          },
          { status: 422, headers: { "Content-Type": "application/problem+json" } },
        ),
      ),
    );
    const user = userEvent.setup();
    renderApp("/admin/projects/prj_web?tab=quotas");
    const panel = await screen.findByRole("region", { name: "Each member's quota" });
    await within(panel).findByRole("row", { name: /^vCPU/ });
    await user.click(within(panel).getByRole("button", { name: "Add a limit" }));
    const sheet = await screen.findByRole("dialog", { name: "Add a limit" });
    await user.type(within(sheet).getByRole("spinbutton", { name: "Limit" }), "64");
    await user.click(within(sheet).getByRole("button", { name: "Add limit" }));
    expect(await within(sheet).findByRole("alert")).toHaveTextContent(
      "The vCPU limit: It can't be more than the project's 4.",
    );
    expect(within(sheet).getByRole("spinbutton", { name: "Limit" })).not.toHaveAttribute("aria-invalid");
    await waitFor(() => expect(within(sheet).getByRole("button", { name: "Add limit" })).toHaveFocus());
  });

  it("tells an auditor there is no limit, not to add one", async () => {
    localStorage.setItem("rvav.mock.as", "auditor");
    resetDb();
    renderApp("/admin/projects/prj_web?tab=quotas");
    const panel = await screen.findByRole("region", { name: "Each member's quota" });
    expect(await within(panel).findByText("Each member may take what the project has left.")).toBeInTheDocument();
    expect(within(panel).queryByText(/Add a limit/)).toBeNull();
  });
});

describe("rvav's quota", () => {
  it("is on the policies page for administrators, as a whole and for each user", async () => {
    const user = userEvent.setup();
    renderApp("/admin/policies?tab=quotas");
    const each = await screen.findByRole("region", { name: "Each user across rvav" });
    await user.click(await within(each).findByRole("button", { name: "Add a limit" }));
    const sheet = await screen.findByRole("dialog", { name: "Add a limit" });
    await user.type(within(sheet).getByRole("spinbutton", { name: "Limit" }), "32");
    await user.click(within(sheet).getByRole("button", { name: "Add limit" }));
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
