import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { renderApp } from "@/test/render";

const user = () => userEvent.setup();

describe("Catalog items", () => {
  it("deletes an item nobody requested, after saying what goes with it", async () => {
    const u = user();
    renderApp("/admin/catalog-items");
    const items = await screen.findByRole("region", { name: "Items" });
    const menu = await within(items).findByRole("button", { name: "Actions for Windows Server 2025" });
    await u.click(menu);
    await u.click(await screen.findByRole("menuitem", { name: "Delete" }));

    const confirm = await screen.findByRole("alertdialog", { name: "Delete Windows Server 2025?" });
    expect(confirm).toHaveTextContent("Nobody has requested it, so no deployment depends on it.");
    expect(confirm).toHaveTextContent("It is open to 2 projects, and leaves their catalog.");
    await u.click(within(confirm).getByRole("button", { name: "Delete item" }));
    expect(await screen.findByText("Windows Server 2025 deleted")).toBeInTheDocument();
    await waitFor(() => expect(within(items).queryByRole("row", { name: /Windows Server 2025/ })).toBeNull());
  });

  it("offers no delete for a requested item, and asks before withdrawing it", async () => {
    const u = user();
    renderApp("/admin/catalog-items");
    const items = await screen.findByRole("region", { name: "Items" });
    await u.click(await within(items).findByRole("button", { name: "Actions for RHEL 9 server" }));
    const menu = await screen.findByRole("menu");
    expect(within(menu).getByRole("menuitem", { name: "Open the request form" })).toBeInTheDocument();
    expect(within(menu).queryByRole("menuitem", { name: "Delete" })).toBeNull();
    await u.click(within(menu).getByRole("menuitem", { name: "Withdraw" }));

    const confirm = await screen.findByRole("alertdialog", { name: "Withdraw RHEL 9 server?" });
    expect(confirm).toHaveTextContent(/deployments? built from it keeps? running/);
    await u.click(within(confirm).getByRole("button", { name: "Withdraw" }));
    expect(await screen.findByText("RHEL 9 server withdrawn")).toBeInTheDocument();
    expect(await within(items).findByRole("row", { name: /RHEL 9 server/ })).toHaveTextContent("Not published");
  });

  it("filters the items by kind, and says how to clear a filter that matches nothing", async () => {
    const u = user();
    renderApp("/admin/catalog-items");
    const items = await screen.findByRole("region", { name: "Items" });
    await within(items).findByRole("row", { name: /RHEL 9 server/ });
    await u.click(within(items).getByRole("combobox", { name: "Kind" }));
    await u.click(await screen.findByRole("option", { name: "AWX workflow" }));
    await waitFor(() => expect(within(items).queryByRole("row", { name: /RHEL 9 server/ })).toBeNull());
    expect(within(items).getByRole("row", { name: /Open firewall flow/ })).toBeInTheDocument();

    await u.type(within(items).getByRole("searchbox", { name: "Search items" }), "nothing like it");
    expect(await within(items).findByText("No item matches")).toBeInTheDocument();
    await u.click(within(items).getByRole("button", { name: "Clear the filters" }));
    expect(await within(items).findByRole("row", { name: /RHEL 9 server/ })).toBeInTheDocument();
  });

  it("creates an item in a side panel", async () => {
    const u = user();
    const { router } = renderApp("/admin/catalog-items");
    await u.click(await screen.findByRole("button", { name: "New item" }));
    const sheet = await screen.findByRole("dialog", { name: "New item" });
    expect(within(sheet).getByRole("textbox", { name: "Name" })).toHaveFocus();
    await u.type(within(sheet).getByRole("textbox", { name: "Name" }), "Team share");
    await u.type(within(sheet).getByRole("textbox", { name: "Slug" }), "team-share");
    await u.type(within(sheet).getByRole("textbox", { name: "Summary" }), "A file share for a team.");
    await u.click(within(sheet).getByRole("button", { name: "Create" }));
    expect(await screen.findByText("Team share created, with a draft 1.0")).toBeInTheDocument();
    await waitFor(() => expect(router.state.location.pathname).toBe("/admin/catalog-items/team-share"));
  });

  it("shows the repository in a line, and its files that need someone in a side panel", async () => {
    const u = user();
    renderApp("/admin/catalog-items");
    const banner = await screen.findByRole("region", { name: "Git repository" });
    expect(banner).toHaveTextContent(/\d files need you/);
    await u.click(within(banner).getByRole("button", { name: "Details of the Git repository" }));

    const sheet = await screen.findByRole("dialog", { name: "Git repository" });
    expect(within(sheet).getByRole("list", { name: "Files that need you" })).toHaveTextContent("Conflict in Git");
    expect(within(sheet).getByRole("link", { name: "Change or disconnect" })).toHaveAttribute(
      "href",
      "/admin/integrations/git",
    );
    await u.click(within(sheet).getByRole("button", { name: "Read the repository now" }));
    expect(await screen.findByText("Repository read")).toBeInTheDocument();
  });
});

describe("Categories and secrets", () => {
  it("opens the tab the address names, and adds a category in a side panel", async () => {
    const u = user();
    renderApp("/admin/catalog-items?tab=categories");
    const region = await screen.findByRole("region", { name: "Categories" });
    expect(await within(region).findByRole("row", { name: /Databases/ })).toBeInTheDocument();
    await u.click(within(region).getByRole("button", { name: "New category" }));
    const sheet = await screen.findByRole("dialog", { name: "New category" });
    await u.type(within(sheet).getByRole("textbox", { name: "Key" }), "storage");
    await u.type(within(sheet).getByRole("textbox", { name: "Name" }), "Storage");
    await u.click(within(sheet).getByRole("button", { name: "Add category" }));
    expect(await within(region).findByRole("row", { name: /Storage/ })).toHaveTextContent("storage");
  });

  it("removes only a category without items", async () => {
    const u = user();
    renderApp("/admin/catalog-items?tab=categories");
    const region = await screen.findByRole("region", { name: "Categories" });
    await u.click(await within(region).findByRole("button", { name: "Actions for Compute" }));
    expect(await screen.findByRole("menuitem", { name: "Remove: move its items first" })).toHaveAttribute(
      "aria-disabled",
      "true",
    );
  });

  it("stores a secret, then removes it after asking", async () => {
    const u = user();
    renderApp("/admin/catalog-items?tab=secrets");
    const region = await screen.findByRole("region", { name: "Secrets" });
    await u.click(await within(region).findByRole("button", { name: "New secret" }));
    const sheet = await screen.findByRole("dialog", { name: "New secret" });
    await u.type(within(sheet).getByRole("textbox", { name: "Name" }), "cmdb");
    await u.type(within(sheet).getByLabelText("Value"), "cmdb-token");
    await u.click(within(sheet).getByRole("button", { name: "Store" }));
    expect(await screen.findByText("cmdb stored")).toBeInTheDocument();

    await u.click(await within(region).findByRole("button", { name: "Actions for cmdb" }));
    await u.click(await screen.findByRole("menuitem", { name: "Remove" }));
    const confirm = await screen.findByRole("alertdialog", { name: "Remove cmdb?" });
    expect(confirm).toHaveTextContent("No item or template uses it today.");
    await u.click(within(confirm).getByRole("button", { name: "Remove secret" }));
    await waitFor(() => expect(within(region).queryByRole("row", { name: /cmdb/ })).toBeNull());
  });
});
