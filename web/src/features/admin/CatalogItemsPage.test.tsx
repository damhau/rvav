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
    const { router } = renderApp("/admin/catalog-items");
    const items = await screen.findByRole("region", { name: "Items" });
    await within(items).findByRole("row", { name: /RHEL 9 server/ });
    await u.click(within(items).getByRole("combobox", { name: "Kind" }));
    await u.click(await screen.findByRole("option", { name: "AWX workflow" }));
    await waitFor(() => expect(within(items).queryByRole("row", { name: /RHEL 9 server/ })).toBeNull());
    expect(within(items).getByRole("row", { name: /Open firewall flow/ })).toBeInTheDocument();

    await u.type(within(items).getByRole("searchbox", { name: "Search items" }), "nothing like it");
    expect(await within(items).findByText("No item matches")).toBeInTheDocument();
    expect(router.state.location.search).toMatchObject({ kind: "awx_workflow", q: "nothing like it" });
    await u.click(within(items).getAllByRole("button", { name: "Clear filters" }).at(-1)!);
    expect(await within(items).findByRole("row", { name: /RHEL 9 server/ })).toBeInTheDocument();
    expect(router.state.location.search).toEqual({});
  });

  it("keeps the items' search while another tab is open", async () => {
    const u = user();
    const { router } = renderApp("/admin/catalog-items?q=rhel");
    await screen.findByRole("region", { name: "Items" });
    await u.click(screen.getByRole("tab", { name: "Categories" }));
    await waitFor(() => expect(router.state.location.search).toEqual({ tab: "categories", q: "rhel" }));
    await u.click(screen.getByRole("tab", { name: "Items" }));
    const items = await screen.findByRole("region", { name: "Items" });
    expect(within(items).getByRole("searchbox", { name: "Search items" })).toHaveValue("rhel");
    expect(router.state.location.search).toEqual({ tab: "items", q: "rhel" });
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
    const category = within(sheet).getByRole("combobox", { name: "Category" });
    expect(category).toHaveTextContent("Select…");

    // Without a category, the save is refused at the field, which takes the focus.
    await u.click(within(sheet).getByRole("button", { name: "Create item" }));
    expect(await within(sheet).findByText("Choose a category.")).toBeInTheDocument();
    expect(category).toHaveAttribute("aria-invalid", "true");
    await waitFor(() => expect(category).toHaveFocus());

    await u.click(category);
    await u.click(await screen.findByRole("option", { name: "Databases" }));
    await u.click(within(sheet).getByRole("button", { name: "Create item" }));
    expect(await screen.findByText("Team share created, with a draft 1.0")).toBeInTheDocument();
    await waitFor(() => expect(router.state.location.pathname).toBe("/admin/catalog-items/team-share"));
  });

  it("imports an item from YAML, after naming the line of a missing category", { timeout: 15_000 }, async () => {
    const u = user();
    const { router } = renderApp("/admin/catalog-items");
    await u.click(await screen.findByRole("button", { name: "Import YAML" }));
    const sheet = await screen.findByRole("dialog", { name: "Import an item" });
    const file = (category: string) =>
      [
        "apiVersion: rvav.dev/v1",
        "kind: CatalogItem",
        "metadata:",
        "  slug: team-vm",
        "  name: Team VM",
        "  summary: A VM for a team.",
        `  category: ${category}`,
        "  type: vm_blueprint",
        "versions:",
        '  - number: "1.0"',
        "    form_schema: { type: object, properties: {} }",
        "    ui_schema: { layout: { sections: [] }, fields: {} }",
        "    blueprint: {}",
      ].join("\n");
    const yaml = within(sheet).getByRole("textbox", { name: "YAML" });
    await u.click(yaml);
    await u.paste(file('""'));
    await u.click(within(sheet).getByRole("button", { name: "Import" }));
    const problems = await within(sheet).findByRole("list", { name: "Import problems" });
    expect(
      within(problems)
        .getAllByRole("listitem")
        .map((li) => li.textContent),
    ).toEqual(["Choose a category. (line 7)"]);

    await u.clear(yaml);
    await u.paste(file("compute"));
    await u.click(within(sheet).getByRole("button", { name: "Import" }));
    expect(await screen.findByText("Imported as a draft")).toBeInTheDocument();
    await waitFor(() => expect(router.state.location.pathname).toMatch(/^\/admin\/catalog-items\/itm_/));
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
    await u.click(within(sheet).getByRole("button", { name: "Create category" }));
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
    await u.click(within(sheet).getByRole("button", { name: "Create secret" }));
    expect(await screen.findByText("cmdb created")).toBeInTheDocument();

    await u.click(await within(region).findByRole("button", { name: "Actions for cmdb" }));
    await u.click(await screen.findByRole("menuitem", { name: "Remove" }));
    const confirm = await screen.findByRole("alertdialog", { name: "Remove cmdb?" });
    expect(confirm).toHaveTextContent("No item or template uses it today.");
    await u.click(within(confirm).getByRole("button", { name: "Remove secret" }));
    await waitFor(() => expect(within(region).queryByRole("row", { name: /cmdb/ })).toBeNull());
  });

  it("stores a project's own secret beside the global one of its name", { timeout: 15_000 }, async () => {
    const u = user();
    renderApp("/admin/catalog-items?tab=secrets");
    const region = await screen.findByRole("region", { name: "Secrets" });
    const store = async (project?: string) => {
      await u.click(await within(region).findByRole("button", { name: "New secret" }));
      const sheet = await screen.findByRole("dialog", { name: "New secret" });
      await u.type(within(sheet).getByRole("textbox", { name: "Name" }), "ci-token");
      if (project) {
        await u.click(within(sheet).getByRole("combobox", { name: "Project" }));
        await u.click(await screen.findByRole("option", { name: new RegExp(project) }));
      }
      await u.click(within(sheet).getByLabelText("Value"));
      await u.paste("token");
      await u.click(within(sheet).getByRole("button", { name: "Create secret" }));
      await waitFor(() => expect(screen.queryByRole("dialog", { name: "New secret" })).toBeNull());
    };
    await store();
    await store("Web Platform");
    const rows = await within(region).findAllByRole("row", { name: /ci-token/ });
    expect(rows.map((r) => r.textContent)).toEqual([
      expect.stringContaining("Every project"),
      expect.stringContaining("Web PlatformOpened instead of the global one"),
    ]);
    expect(rows[0]).not.toHaveTextContent("instead of");

    await u.click(within(region).getByRole("button", { name: "Actions for ci-token of Web Platform" }));
    await u.click(await screen.findByRole("menuitem", { name: "Replace the value" }));
    const replace = await screen.findByRole("dialog", { name: "Replace ci-token" });
    expect(within(replace).getByRole("combobox", { name: "Project" })).toHaveTextContent("Web Platform");
    expect(within(replace).getByRole("combobox", { name: "Project" })).toBeDisabled();
    await u.click(within(replace).getByRole("button", { name: "Cancel" }));

    await u.click(within(region).getByRole("button", { name: "Actions for ci-token of Web Platform" }));
    await u.click(await screen.findByRole("menuitem", { name: "Remove" }));
    const confirm = await screen.findByRole("alertdialog", { name: "Remove ci-token of Web Platform?" });
    expect(confirm).toHaveTextContent("Web Platform's requests open the global ci-token instead.");
    await u.click(within(confirm).getByRole("button", { name: "Remove secret" }));
    await waitFor(() => expect(within(region).getAllByRole("row", { name: /ci-token/ })).toHaveLength(1));
  });
});
