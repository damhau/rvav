import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { adminState } from "@/mocks/administration";
import { customRoles } from "@/mocks/roles";
import { db, resetDb } from "@/mocks/seed";
import { renderApp } from "@/test/render";

function withOperator(given: boolean) {
  resetDb();
  customRoles(db).push({
    key: "vm-operator",
    name: "VM operator",
    description: "",
    permissions: ["project.see"],
    built_in: false,
  });
  if (given)
    adminState(db).roleAssignments.push({
      id: "ras_alain",
      user: { id: "usr_afavre", name: "Alain Favre", email: "alain.favre@corp.example" },
      role: { key: "vm-operator", name: "VM operator" },
      fixed: false,
      created_at: new Date().toISOString(),
    });
}

describe("Roles", () => {
  it("makes a role from a built-in one, from its row menu", async () => {
    const user = userEvent.setup();
    const { router } = renderApp("/admin/roles");
    await user.click(await screen.findByRole("button", { name: "Actions for Auditor" }));
    expect(screen.queryByRole("menuitem", { name: /^Delete/ })).toBeNull();
    await user.click(screen.getByRole("menuitem", { name: "New role from it" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/admin/roles/new"));
    expect(router.state.location.search).toEqual({ from: "auditor" });
    expect(await screen.findByText("Starting from what Auditor allows.")).toBeInTheDocument();
  });

  it("goes back to the roles, saying so once, when the role to start from doesn't exist", async () => {
    const { router } = renderApp("/admin/roles/new?from=no-such-role", undefined, { strict: true });
    expect(await screen.findByText(/There is no role no-such-role to start from/)).toBeInTheDocument();
    await waitFor(() => expect(router.state.location.pathname).toBe("/admin/roles"));
    expect(await screen.findByRole("tab", { name: "Roles", selected: true })).toBeInTheDocument();
    expect(screen.getAllByText(/There is no role no-such-role to start from/)).toHaveLength(1);
  });

  it("deletes a custom role given to nobody once confirmed, and says why one given can't be", async () => {
    withOperator(false);
    const user = userEvent.setup();
    renderApp("/admin/roles");
    const table = await screen.findByRole("table", { name: "Roles" });
    await user.click(await screen.findByRole("button", { name: "Actions for VM operator" }));
    await user.click(screen.getByRole("menuitem", { name: "Delete" }));
    const confirm = await screen.findByRole("alertdialog", { name: "Delete VM operator?" });
    await user.click(within(confirm).getByRole("button", { name: "Delete role" }));
    expect(await screen.findByText("VM operator deleted")).toBeInTheDocument();
    await waitFor(() => expect(within(table).queryByRole("link", { name: "VM operator" })).toBeNull());
  });

  it("can't delete a role while it is given", async () => {
    withOperator(true);
    const user = userEvent.setup();
    renderApp("/admin/roles");
    await user.click(await screen.findByRole("button", { name: "Actions for VM operator" }));
    expect(screen.getByRole("menuitem", { name: "Delete: given 1 time" })).toHaveAttribute("aria-disabled", "true");
  });
});

describe("A role's page", () => {
  it("saves from its header and deletes from its menu", async () => {
    withOperator(false);
    const user = userEvent.setup();
    const { router } = renderApp("/admin/roles/vm-operator");
    const name = await screen.findByRole("textbox", { name: "Name" });
    await user.clear(name);
    await user.type(name, "Power operator");
    await user.click(screen.getByRole("button", { name: "Save role" }));
    expect(await screen.findByText("Power operator saved")).toBeInTheDocument();
    expect(await screen.findByRole("heading", { level: 1, name: "Power operator" })).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "More actions for Power operator" }));
    await user.click(screen.getByRole("menuitem", { name: "Delete role" }));
    const confirm = await screen.findByRole("alertdialog", { name: "Delete Power operator?" });
    await user.click(within(confirm).getByRole("button", { name: "Delete role" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/admin/roles"));
  });

  it("offers a built-in role's copy as its primary action, and no menu", async () => {
    renderApp("/admin/roles/auditor");
    expect(await screen.findByRole("link", { name: "New role from it" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /^More actions for/ })).toBeNull();
    expect(screen.queryByRole("button", { name: "Save role" })).toBeNull();
  });

  it("makes a new role from its header", async () => {
    const user = userEvent.setup();
    const { router } = renderApp("/admin/roles/new");
    await user.type(await screen.findByRole("textbox", { name: "Key" }), "reader");
    await user.type(screen.getByRole("textbox", { name: "Name" }), "Reader");
    await user.click(screen.getByRole("checkbox", { name: "project.see" }));
    await user.click(screen.getByRole("button", { name: "Create role" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/admin/roles/reader"));
    expect(await screen.findByText("Reader created")).toBeInTheDocument();
  });
});

describe("For all of rvav", () => {
  it("takes a role away once confirmed; what rvav set has no menu", async () => {
    withOperator(true);
    const user = userEvent.setup();
    renderApp("/admin/roles?tab=rvav");
    const table = await screen.findByRole("table", { name: "Given for all of rvav" });
    const lea = await within(table).findByRole("row", { name: /Léa Martin/ });
    expect(lea).toHaveTextContent("Set by rvav");
    expect(within(lea).queryByRole("button", { name: /^Actions for/ })).toBeNull();

    await user.click(within(table).getByRole("button", { name: "Actions for VM operator of Alain Favre" }));
    await user.click(screen.getByRole("menuitem", { name: "Take it away" }));
    const confirm = await screen.findByRole("alertdialog", { name: "Take VM operator away from Alain Favre?" });
    await user.click(within(confirm).getByRole("button", { name: "Take it away" }));
    expect(await screen.findByText("VM operator taken away from Alain Favre")).toBeInTheDocument();
    await waitFor(() => expect(within(table).queryByRole("row", { name: /Alain Favre/ })).toBeNull());
  });
});
