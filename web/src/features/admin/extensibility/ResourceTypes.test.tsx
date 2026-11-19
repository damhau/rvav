import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it } from "vitest";
import { db, resetDb } from "@/mocks/seed";
import { renderApp } from "@/test/render";

type User = ReturnType<typeof userEvent.setup>;

async function openMenu(user: User, name: string) {
  await user.click(await screen.findByRole("button", { name: `Actions for ${name}` }));
}

/** Picks an option of a field in one of the sheet's steps (Creates it, Deletes it…). */
async function choose(user: User, phase: string, field: string, option: string) {
  const group = within(screen.getByRole("dialog")).getByRole("group", { name: phase });
  await user.click(within(group).getByRole("combobox", { name: field }));
  await user.click(await screen.findByRole("option", { name: option }));
}

afterEach(() => {
  localStorage.removeItem("rvav.mock.as");
  resetDb();
});

describe("Resource types", () => {
  it("lists each type with what makes and deletes it, its actions and its resources", async () => {
    renderApp("/admin/resource-types");
    const table = await screen.findByRole("table", { name: "Resource types" });
    const lb = await within(table).findByRole("row", { name: /Load balancer/ });
    expect(lb).toHaveTextContent("load-balancer");
    expect(lb).toHaveTextContent("AWX · create-load-balancer");
    expect(lb).toHaveTextContent("Workflow · lb-delete");
    expect(lb).toHaveTextContent("wf.lb-drain");
    expect(within(lb).getAllByRole("cell").at(-2)).toHaveTextContent("1");
    expect(within(table).getByRole("row", { name: /DNS alias/ })).toHaveTextContent("Off");
  });

  it("defines a type with a workflow and an AWX template, and a day-2 action", { timeout: 15_000 }, async () => {
    const user = userEvent.setup();
    renderApp("/admin/resource-types");
    await user.click(await screen.findByRole("button", { name: "New resource type" }));
    const sheet = await screen.findByRole("dialog", { name: "New resource type" });
    await user.type(within(sheet).getByLabelText("Key"), "dns-record");
    await user.type(within(sheet).getByLabelText("Title"), "DNS record");
    await choose(user, "Creates it", "Workflow", "DNS alias");
    await choose(user, "Deletes it", "Run", "AWX template");
    await user.click(within(sheet).getByRole("combobox", { name: "Job template" }));
    expect((await screen.findAllByRole("option")).map((o) => o.textContent)).not.toContain("configure-vm");
    await user.click(screen.getByRole("option", { name: "delete-load-balancer" }));
    await user.click(within(sheet).getByRole("checkbox", { name: /Drain/ }));
    await user.click(within(sheet).getByRole("button", { name: "Save resource type" }));

    expect(await screen.findByText("DNS record saved")).toBeInTheDocument();
    const row = await screen.findByRole("row", { name: /DNS record/ });
    expect(row).toHaveTextContent("Workflow · dns-alias");
    expect(row).toHaveTextContent("AWX · delete-load-balancer");
    expect(row).toHaveTextContent("wf.lb-drain");
  });

  it("says next to each field what the server refused", { timeout: 15_000 }, async () => {
    const user = userEvent.setup();
    renderApp("/admin/resource-types");
    await user.click(await screen.findByRole("button", { name: "New resource type" }));
    const sheet = await screen.findByRole("dialog", { name: "New resource type" });
    await user.type(within(sheet).getByLabelText("Key"), "Bad Key");
    const schema = within(sheet).getByLabelText(/Property schema/);
    await user.clear(schema);
    await user.click(schema);
    await user.paste('{"type": "array"}');
    await user.click(within(sheet).getByRole("button", { name: "Save resource type" }));

    expect(await within(sheet).findByText(/2 to 41 lower-case letters/)).toBeInTheDocument();
    expect(within(sheet).getByText("Give it a title.")).toBeInTheDocument();
    expect(within(sheet).getAllByText("Name the custom workflow or the AWX template.")).toHaveLength(2);
    expect(within(sheet).getByText("A resource's properties are an object.")).toBeInTheDocument();

    await user.clear(schema);
    await user.click(schema);
    await user.paste("{ not json");
    await user.click(within(sheet).getByRole("button", { name: "Save resource type" }));
    expect(await within(sheet).findByText("This is not valid JSON.")).toBeInTheDocument();
  });

  it("refuses a key another type has", { timeout: 15_000 }, async () => {
    const user = userEvent.setup();
    renderApp("/admin/resource-types");
    await user.click(await screen.findByRole("button", { name: "New resource type" }));
    const sheet = await screen.findByRole("dialog", { name: "New resource type" });
    await user.type(within(sheet).getByLabelText("Key"), "load-balancer");
    await user.type(within(sheet).getByLabelText("Title"), "Another");
    await choose(user, "Creates it", "Workflow", "DNS alias");
    await choose(user, "Deletes it", "Workflow", "DNS alias");
    await user.click(within(sheet).getByRole("button", { name: "Save resource type" }));
    expect(await screen.findByText("A resource type is defined as load-balancer.")).toBeInTheDocument();
    expect(screen.getByRole("dialog", { name: "New resource type" })).toBeInTheDocument();
  });

  it("changes a type, its key fixed", { timeout: 15_000 }, async () => {
    const user = userEvent.setup();
    renderApp("/admin/resource-types");
    await openMenu(user, "Load balancer");
    await user.click(await screen.findByRole("menuitem", { name: "Change" }));
    const sheet = await screen.findByRole("dialog", { name: "Change Load balancer" });
    expect(within(sheet).getByLabelText("Key")).toBeDisabled();
    expect(within(sheet).getByLabelText<HTMLTextAreaElement>(/Property schema/).value).toContain(
      '"title": "Virtual IP"',
    );
    expect(within(sheet).getByRole("checkbox", { name: /Drain/ })).toBeChecked();
    const title = within(sheet).getByLabelText("Title");
    await user.clear(title);
    await user.type(title, "Load balancer (F5)");
    await user.click(within(sheet).getByRole("button", { name: "Save resource type" }));
    expect(await screen.findByText("Load balancer (F5) saved")).toBeInTheDocument();
    await waitFor(() => expect(screen.getByRole("row", { name: /Load balancer \(F5\)/ })).toBeInTheDocument());
  });

  it("removes a type nothing uses, and says why it can't remove one deployments have", async () => {
    const user = userEvent.setup();
    renderApp("/admin/resource-types");
    await openMenu(user, "Load balancer");
    expect(await screen.findByRole("menuitem", { name: "Remove: deployments have 1 resource" })).toHaveAttribute(
      "data-disabled",
    );
    await user.keyboard("{Escape}");

    await openMenu(user, "DNS alias");
    await user.click(await screen.findByRole("menuitem", { name: "Remove" }));
    const confirm = await screen.findByRole("alertdialog", { name: "Remove DNS alias?" });
    await user.click(within(confirm).getByRole("button", { name: "Remove resource type" }));
    expect(await screen.findByText("DNS alias removed")).toBeInTheDocument();
    await waitFor(() => expect(screen.queryByRole("row", { name: /DNS alias/ })).toBeNull());
  });

  it("says why the server refused to remove a type deployments came to have", async () => {
    const user = userEvent.setup();
    renderApp("/admin/resource-types");
    await openMenu(user, "DNS alias");
    const edge = db.deployments.find((d) => d.name === "web-prd-edge")!;
    edge.resources.push({
      id: "res_alias",
      type: "custom",
      name: "web-prd-edge-alias",
      sync_state: "in_sync",
      resource_type: { key: "dns-alias", title: "DNS alias" },
      key: "alias",
      properties: { fqdn: "edge.corp.example" },
    });
    await user.click(await screen.findByRole("menuitem", { name: "Remove" }));
    const confirm = await screen.findByRole("alertdialog", { name: "Remove DNS alias?" });
    await user.click(within(confirm).getByRole("button", { name: "Remove resource type" }));
    expect(await screen.findByText(/Deployments have 1 resource of it: disable it instead/)).toBeInTheDocument();
    expect(await screen.findByRole("row", { name: /DNS alias/ })).toBeInTheDocument();
  });

  it("shows an auditor the types without a way to change them", async () => {
    localStorage.setItem("rvav.mock.as", "auditor");
    resetDb();
    renderApp("/admin/resource-types");
    expect(await screen.findByRole("row", { name: /Load balancer/ })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "New resource type" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Actions for Load balancer" })).toBeNull();
  });
});
