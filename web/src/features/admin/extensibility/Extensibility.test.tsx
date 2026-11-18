import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { renderApp } from "@/test/render";

async function openMenu(user: ReturnType<typeof userEvent.setup>, name: string) {
  await user.click(await screen.findByRole("button", { name: `Actions for ${name}` }));
}

describe("Subscriptions", () => {
  it("opens a subscription from its row, and removes one once confirmed", async () => {
    const user = userEvent.setup();
    const { router } = renderApp("/admin/subscriptions");
    const table = await screen.findByRole("table", { name: "Subscriptions" });
    expect(await within(table).findByRole("row", { name: /CMDB pre-check/ })).toHaveTextContent("Blocking");

    await openMenu(user, "CMDB pre-check");
    await user.click(await screen.findByRole("menuitem", { name: "Remove" }));
    const confirm = await screen.findByRole("alertdialog", { name: "Remove CMDB pre-check?" });
    expect(confirm).toHaveTextContent("Requests stop waiting for its answer on provisioning.pre.");
    await user.click(within(confirm).getByRole("button", { name: "Remove subscription" }));
    expect(await screen.findByText("CMDB pre-check removed")).toBeInTheDocument();
    await waitFor(() => expect(within(table).queryByRole("row", { name: /CMDB pre-check/ })).toBeNull());

    await user.click(within(table).getByRole("link", { name: "ServiceNow change record" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/admin/subscriptions/sub_servicenow"));
    expect(await screen.findByRole("heading", { level: 1, name: "ServiceNow change record" })).toBeInTheDocument();
  });

  it("turns one off from its row menu", async () => {
    const user = userEvent.setup();
    renderApp("/admin/subscriptions");
    await openMenu(user, "ServiceNow change record");
    await user.click(await screen.findByRole("menuitem", { name: "Turn off" }));
    expect(await screen.findByText("ServiceNow change record is off")).toBeInTheDocument();
    const row = screen.getByRole("row", { name: /ServiceNow change record/ });
    await waitFor(() => expect(within(row).getByText("Off")).toBeInTheDocument());
  });
});

describe("A subscription's page", () => {
  it("changes it from its header, and filters its runs by status", async () => {
    const user = userEvent.setup();
    renderApp("/admin/subscriptions/sub_servicenow");
    await screen.findByRole("heading", { level: 1, name: "ServiceNow change record" });
    const runs = await screen.findByRole("table", { name: "Runs" });
    await within(runs).findAllByRole("button", { name: /Open the run from/ });

    await user.click(screen.getByRole("combobox", { name: "Status" }));
    await user.click(await screen.findByRole("option", { name: "Stopped the request" }));
    expect(await screen.findByText("No run with the status Stopped the request")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Show every run" }));
    expect(await screen.findByRole("table", { name: "Runs" })).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Change subscription" }));
    const sheet = await screen.findByRole("dialog", { name: "Change ServiceNow change record" });
    const description = within(sheet).getByRole("textbox", { name: "Description" });
    await user.clear(description);
    await user.type(description, "Closes the change record.");
    await user.click(within(sheet).getByRole("button", { name: "Save subscription" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(await screen.findByText("Closes the change record.")).toBeInTheDocument();
  });

  it("goes back to the list once removed", async () => {
    const user = userEvent.setup();
    const { router } = renderApp("/admin/subscriptions/sub_servicenow");
    await user.click(await screen.findByRole("button", { name: "More actions for ServiceNow change record" }));
    await user.click(await screen.findByRole("menuitem", { name: "Remove subscription" }));
    const confirm = await screen.findByRole("alertdialog", { name: "Remove ServiceNow change record?" });
    expect(confirm).toHaveTextContent("Webhook to servicenow.corp.example stops hearing about provisioning.post.");
    await user.click(within(confirm).getByRole("button", { name: "Remove subscription" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/admin/subscriptions"));
    expect(await screen.findByText("ServiceNow change record removed")).toBeInTheDocument();
  });
});

describe("Custom workflows", () => {
  it("says why a running workflow can't be removed, and removes one nothing uses", async () => {
    const user = userEvent.setup();
    renderApp("/admin/workflows");
    await openMenu(user, "Decommission an application");
    expect(await screen.findByRole("menuitem", { name: "Remove: runs haven't ended" })).toHaveAttribute(
      "aria-disabled",
      "true",
    );
    await user.keyboard("{Escape}");

    await openMenu(user, "Register in monitoring");
    await user.click(await screen.findByRole("menuitem", { name: "Remove" }));
    const confirm = await screen.findByRole("alertdialog", { name: "Remove Register in monitoring?" });
    expect(confirm).toHaveTextContent("It is no longer offered as composite step.");
    await user.click(within(confirm).getByRole("button", { name: "Remove workflow" }));
    expect(await screen.findByText("Register in monitoring removed")).toBeInTheDocument();
    await waitFor(() => expect(screen.queryByRole("row", { name: /Register in monitoring/ })).toBeNull());
  });

  it("changes a workflow in a side panel", async () => {
    const user = userEvent.setup();
    renderApp("/admin/workflows");
    await openMenu(user, "Register in monitoring");
    await user.click(await screen.findByRole("menuitem", { name: "Change" }));
    const sheet = await screen.findByRole("dialog", { name: "Change Register in monitoring" });
    expect(within(sheet).getByRole("textbox", { name: "Key" })).toBeDisabled();
    const title = within(sheet).getByRole("textbox", { name: "Title" });
    await user.clear(title);
    await user.type(title, "Monitoring registration");
    await user.click(within(sheet).getByRole("button", { name: "Save workflow" }));
    expect(await screen.findByText("Monitoring registration saved")).toBeInTheDocument();
    expect(await screen.findByRole("row", { name: /Monitoring registration/ })).toBeInTheDocument();
  });
});
