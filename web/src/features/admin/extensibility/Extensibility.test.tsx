import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { renderApp } from "@/test/render";

afterEach(() => vi.restoreAllMocks());

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

  it("lists a VM's topics under their own heading, with the VM in their sample, and they don't block", async () => {
    const user = userEvent.setup();
    renderApp("/admin/subscriptions");
    await user.click(await screen.findByRole("button", { name: "New subscription" }));
    const sheet = await screen.findByRole("dialog", { name: "New subscription" });
    await user.click(within(sheet).getByRole("combobox", { name: "Topic" }));
    const machines = await screen.findByRole("group", { name: "A request's VMs, one event each" });
    expect(
      within(machines)
        .getAllByRole("option")
        .map((o) => o.textContent),
    ).toEqual([
      "compute.allocation.pre",
      "network.configure",
      "compute.provision.pre",
      "compute.provision.post",
      "compute.removal.pre",
      "compute.removal.post",
    ]);
    for (const [group, keys] of [
      ["A request's approval levels", ["approval.requested", "approval.decided"]],
      ["Projects", ["project.created", "project.updated"]],
    ] as const) {
      const options = within(screen.getByRole("group", { name: group })).getAllByRole("option");
      expect(options.map((o) => o.textContent)).toEqual(keys);
    }
    await user.click(within(machines).getByRole("option", { name: "compute.provision.pre" }));

    expect(within(sheet).getByText("A VM is about to be cloned.")).toBeInTheDocument();
    expect(within(sheet).getByRole("checkbox", { name: /Blocking/ })).toBeDisabled();
    expect(
      within(sheet).getByText(
        "(only on provisioning.pre, compute.allocation.pre, network.configure, day2.pre and delete.pre)",
      ),
    ).toBeInTheDocument();
    await user.click(within(sheet).getByText("A sample compute.provision.pre event"));
    expect(within(sheet).getByText(/"machine": \{/)).toHaveTextContent('"address": "10.31.10.57"');
  });
});

describe("A blocking subscription on a VM's topic", () => {
  it("says what its answer may change, and gives it a recovery target", async () => {
    const user = userEvent.setup();
    renderApp("/admin/subscriptions");
    await user.click(await screen.findByRole("button", { name: "New subscription" }));
    const sheet = await screen.findByRole("dialog", { name: "New subscription" });
    await user.click(within(sheet).getByRole("textbox", { name: "Name" }));
    await user.paste("IPAM address");
    await user.click(within(sheet).getByRole("combobox", { name: "Topic" }));
    await user.click(await screen.findByRole("option", { name: "network.configure" }));
    await user.click(within(sheet).getByRole("textbox", { name: "URL" }));
    await user.paste("https://cmdb.corp.example/rvav/ip");
    await user.click(within(sheet).getByRole("combobox", { name: "Signing secret" }));
    await user.click(await screen.findByRole("option", { name: "webhook-signing" }));
    await user.click(within(sheet).getByRole("checkbox", { name: /Blocking/ }));
    expect(
      within(sheet).getByText(/Its answer may also change its adapters' networks and addresses/),
    ).toBeInTheDocument();

    await user.click(within(sheet).getByRole("checkbox", { name: "Tell a recovery target when it fails" }));
    const recovery = within(sheet).getByRole("group", { name: "Recovery target" });
    await user.click(within(recovery).getByRole("textbox", { name: "Recovery URL" }));
    await user.paste("https://evil.example/x");
    await user.click(within(recovery).getByRole("combobox", { name: "Recovery signing secret" }));
    await user.click(await screen.findByRole("option", { name: "webhook-signing" }));
    await user.click(within(sheet).getByRole("button", { name: "Create subscription" }));
    expect(
      await within(recovery).findByText(/evil\.example is not one of the hosts webhooks may call/),
    ).toBeInTheDocument();

    const url = within(recovery).getByRole("textbox", { name: "Recovery URL" });
    await user.clear(url);
    await user.click(url);
    await user.paste("https://hooks.corp.example/rvav/ipam-down");
    await user.click(within(sheet).getByRole("button", { name: "Create subscription" }));
    expect(await screen.findByText("IPAM address saved")).toBeInTheDocument();
  }, 15_000);

  it("shows its recovery target, and what each run changed on the VM", async () => {
    const user = userEvent.setup();
    renderApp("/admin/subscriptions/sub_cmdb_naming");
    await screen.findByRole("heading", { level: 1, name: "CMDB naming" });
    expect(screen.getByText("Webhook to hooks.corp.example")).toBeInTheDocument();
    expect(
      await screen.findByText(
        "the VM's name (machine.name) and its cluster, resource pool, datastore and folder (machine.placement)",
      ),
    ).toBeInTheDocument();

    const runs = await screen.findByRole("table", { name: "Runs" });
    expect((await within(runs).findAllByText("Recovery")).length).toBe(2);
    const opens = await within(runs).findAllByRole("button", { name: /Open the run from/ });
    await user.click(opens[0]!);
    let dialog = await screen.findByRole("dialog", { name: "Run" });
    expect(
      await within(dialog).findByText("renamed web-dev-app742; moved to datastore ds-gva-dev-02"),
    ).toBeInTheDocument();
    await user.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());

    // The last run: a recovery that answered without an outcome.
    await user.click(opens.at(-1)!);
    dialog = await screen.findByRole("dialog", { name: "Run" });
    expect(await within(dialog).findByText("Its recovery target, after the subscription failed")).toBeInTheDocument();
    expect(within(dialog).getByText("None: the subscription's setting for when it fails decided")).toBeInTheDocument();
  }, 15_000);
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
    await user.click(screen.getAllByRole("button", { name: "Clear filters" }).at(-1)!);
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

  it("shows a run's reason whole in a tooltip when it is cut", async () => {
    vi.spyOn(HTMLElement.prototype, "scrollWidth", "get").mockReturnValue(320);
    vi.spyOn(HTMLElement.prototype, "clientWidth", "get").mockReturnValue(120);
    renderApp("/admin/subscriptions/sub_servicenow");
    const runs = await screen.findByRole("table", { name: "Runs" }, { timeout: 5000 });
    const reason = await within(runs).findByText("HTTP 503 from servicenow.corp.example");
    expect(reason).toHaveAttribute("tabindex", "0");
    act(() => reason.focus());
    expect(await screen.findByRole("tooltip")).toHaveTextContent("HTTP 503 from servicenow.corp.example");
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
