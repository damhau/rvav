import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { renderApp } from "@/test/render";

const rows = (region: HTMLElement) =>
  within(region)
    .getAllByRole("row")
    .slice(1)
    .map((r) => within(r).getAllByRole("cell")[0]!.textContent);

describe("Policies", () => {
  it("opens the tab its address names", async () => {
    renderApp("/admin/policies?tab=lease");
    expect(await screen.findByRole("tab", { name: "Lease", selected: true })).toBeInTheDocument();
    expect(await screen.findByRole("region", { name: "Lease policies" })).toBeInTheDocument();
  });

  it("moves, turns off and sets the priority of a day-2 policy from its row menu", async () => {
    const user = userEvent.setup();
    const { router } = renderApp("/admin/policies");
    await user.click(await screen.findByRole("tab", { name: "Day-2" }));
    expect(router.state.location.search).toEqual({ tab: "day2" });
    const region = await screen.findByRole("region", { name: "Day-2 policies" });
    await within(region).findByRole("row", { name: /Members operate their VMs/ });
    expect(rows(region)[2]).toMatch(/^Members operate their VMs/);

    await user.click(within(region).getByRole("button", { name: "Actions for Members operate their VMs" }));
    const down = await screen.findByRole("menuitem", { name: /^Move down/ });
    expect(down).toHaveAttribute("aria-disabled", "true");
    expect(down).toHaveTextContent("It comes last already");
    await user.click(screen.getByRole("menuitem", { name: "Move up" }));
    await waitFor(() => expect(rows(region)[1]).toMatch(/^Members operate their VMs/));

    await user.click(within(region).getByRole("button", { name: "Actions for Administrators" }));
    await user.click(await screen.findByRole("menuitem", { name: "Turn off" }));
    expect(await screen.findByText("Administrators turned off")).toBeInTheDocument();
    expect(await within(region).findByRole("row", { name: /Administrators/ })).toHaveTextContent("Off");

    await user.click(within(region).getByRole("button", { name: "Actions for Administrators" }));
    await user.click(await screen.findByRole("menuitem", { name: "Set priority" }));
    const dialog = await screen.findByRole("dialog", { name: "Priority of Administrators" });
    const priority = within(dialog).getByRole("spinbutton", { name: "Priority" });
    await user.clear(priority);
    await user.type(priority, "99");
    await user.click(within(dialog).getByRole("button", { name: "Set priority" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    await waitFor(() => expect(rows(region)[2]).toMatch(/^Administrators/));
  });

  it("removes an approval policy after saying what changes", async () => {
    const user = userEvent.setup();
    renderApp("/admin/policies");
    const region = await screen.findByRole("region", { name: "Approval policies" });
    const menu = await within(region).findByRole("button", { name: "Actions for Production VMs" });
    await user.click(menu);
    await user.click(await screen.findByRole("menuitem", { name: "Remove" }));
    const confirm = await screen.findByRole("alertdialog", { name: "Remove Production VMs?" });
    expect(confirm).toHaveAccessibleDescription("Requests stop waiting for rvav-infra-leads because of it.");
    expect(confirm).toHaveTextContent("Requests already waiting keep the approvals they were given.");
    await user.click(within(confirm).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(menu).toHaveFocus());

    await user.click(menu);
    await user.click(await screen.findByRole("menuitem", { name: "Remove" }));
    await user.click(within(await screen.findByRole("alertdialog")).getByRole("button", { name: "Remove policy" }));
    expect(await screen.findByText("Production VMs removed")).toBeInTheDocument();
    expect(await within(region).findByText("No approval policy")).toBeInTheDocument();
    expect(within(region).getByRole("button", { name: "New approval policy" })).toBeInTheDocument();
  });

  it("makes an approval policy in a side panel", async () => {
    const user = userEvent.setup();
    renderApp("/admin/policies");
    const region = await screen.findByRole("region", { name: "Approval policies" });
    await user.click(await within(region).findByRole("button", { name: "New approval policy" }));
    const sheet = await screen.findByRole("dialog", { name: "New approval policy" });
    expect(within(sheet).getByRole("textbox", { name: "Name" })).toHaveFocus();
    await user.type(within(sheet).getByRole("textbox", { name: "Name" }), "Big VMs");
    await user.type(within(sheet).getByRole("textbox", { name: "OIDC group" }), "rvav-capacity");
    await user.click(within(sheet).getByRole("button", { name: "Save policy" }));
    expect(await screen.findByText("Big VMs saved")).toBeInTheDocument();
    expect(await within(region).findByRole("row", { name: /Big VMs/ })).toHaveTextContent("rvav-capacity");
  });

  it("removes a project's own lease, and only that", async () => {
    const user = userEvent.setup();
    renderApp("/admin/policies?tab=lease");
    const region = await screen.findByRole("region", { name: "Lease policies" });
    await user.click(await within(region).findByRole("button", { name: "Actions for the lease in dev" }));
    expect(await screen.findByRole("menuitem", { name: "Change" })).toBeInTheDocument();
    expect(screen.queryByRole("menuitem", { name: "Remove" })).toBeNull();
    await user.keyboard("{Escape}");

    await user.click(within(region).getByRole("button", { name: "New lease policy" }));
    const sheet = await screen.findByRole("dialog", { name: "New lease policy" });
    await user.click(within(sheet).getByRole("combobox", { name: "Project" }));
    await user.click(await screen.findByRole("option", { name: "Web Platform" }));
    await user.click(within(sheet).getByRole("button", { name: "Save lease policy" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());

    await user.click(await within(region).findByRole("button", { name: "Actions for the lease in dev, Web Platform" }));
    await user.click(await screen.findByRole("menuitem", { name: "Remove" }));
    const confirm = await screen.findByRole("alertdialog", { name: "Remove the lease in dev, Web Platform?" });
    expect(confirm).toHaveTextContent("Deployments keep the lease they have.");
    await user.click(within(confirm).getByRole("button", { name: "Remove lease policy" }));
    await waitFor(() => expect(within(region).queryByRole("row", { name: /Web Platform/ })).toBeNull());
  });

  it(
    "previews and saves a naming policy whose counter starts at 100 and moves by 10",
    { timeout: 15_000 },
    async () => {
      const user = userEvent.setup();
      renderApp("/admin/policies?tab=naming");
      const region = await screen.findByRole("region", { name: "Naming policies" });
      await within(region).findByText("No naming policy");
      await user.click(within(region).getByRole("button", { name: "New naming policy" }));
      const sheet = await screen.findByRole("dialog", { name: "New naming policy" });
      await user.type(within(sheet).getByRole("textbox", { name: "Name" }), "Hundreds");
      const start = within(sheet).getByRole("spinbutton", { name: "Start at" });
      await user.clear(start);
      await user.paste("100");
      const increment = within(sheet).getByRole("spinbutton", { name: "Increment" });
      await user.clear(increment);
      await user.paste("10");
      await user.click(within(sheet).getByRole("button", { name: "Preview names" }));
      expect(await within(sheet).findByText("web-dev-app110, web-dev-app120, web-dev-app130")).toBeInTheDocument();
      await user.click(within(sheet).getByRole("button", { name: "Save naming policy" }));
      expect(await screen.findByText("Hundreds saved")).toBeInTheDocument();
      expect(await within(region).findByRole("row", { name: /Hundreds/ })).toHaveTextContent("From 100, by 10");
    },
  );
});
