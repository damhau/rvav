import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http } from "msw";
import { beforeEach, describe, expect, it } from "vitest";
import { server } from "@/mocks/node";
import { db } from "@/mocks/seed";
import { renderApp } from "@/test/render";

const names = (table: HTMLElement) =>
  within(table)
    .getAllByRole("row")
    .slice(1)
    .map((r) => within(r).getAllByRole("link")[0]!.textContent);

describe("the deployments list against the mock API", () => {
  beforeEach(() => localStorage.removeItem("rvav.layout.deployments.usr_lmartin"));

  it("counts the deployments it shows while more pages remain", async () => {
    server.use(
      http.get("*/api/v1/deployments", ({ request }) => {
        const url = new URL(request.url);
        if (url.searchParams.get("limit") === "3") return undefined;
        url.searchParams.set("limit", "3");
        return fetch(url, { headers: request.headers });
      }),
    );
    renderApp("/deployments?layout=table");
    const table = await screen.findByRole("table", { name: "Deployments" }, { timeout: 5000 });
    await waitFor(() => expect(names(table)).toHaveLength(3));
    expect(screen.getByText(/^3 shown · \d+ VMs, \d+ powered on/)).toBeInTheDocument();
  });

  it("filters by status from the address, and a row opens its deployment", async () => {
    const user = userEvent.setup();
    const { router } = renderApp("/deployments?status=failed");
    const table = await screen.findByRole("table", { name: "Deployments" }, { timeout: 5000 });
    await within(table).findByText("web-dev-worker");
    const rows = within(table).getAllByRole("row").slice(1);
    expect(rows.length).toBeGreaterThan(0);
    for (const row of rows) expect(row).toHaveTextContent("Failed");
    expect(screen.getByRole("combobox", { name: "Status" })).toHaveTextContent("Failed");

    const row = within(table).getByText("web-dev-worker").closest("tr")!;
    await user.click(within(row).getByText(/of \d+ on/));
    const id = db.deployments.find((d) => d.name === "web-dev-worker")!.id;
    await waitFor(() => expect(router.state.location.pathname).toBe(`/deployments/${id}`));
  });

  it("keeps the filters in the address, and clears them from the empty state", async () => {
    const user = userEvent.setup();
    const { router } = renderApp("/deployments?sort=-lease_expires_at");
    const table = await screen.findByRole("table", { name: "Deployments" }, { timeout: 5000 });
    await within(table).findByText("web-dev-api");
    expect(within(table).getByRole("columnheader", { name: /Lease/ })).toHaveAttribute("aria-sort", "descending");

    await user.click(screen.getByRole("checkbox", { name: "Only mine" }));
    await user.click(screen.getByRole("checkbox", { name: "Lease ends within 7 days" }));
    await user.type(screen.getByRole("searchbox", { name: "Search deployments" }), "no such thing");
    expect(await screen.findByText("No deployments match")).toBeInTheDocument();
    await waitFor(() =>
      expect(router.state.location.search).toEqual({
        sort: "-lease_expires_at",
        owner: "me",
        expiring: true,
        q: "no such thing",
      }),
    );

    const empty = screen.getByText("No deployments match").parentElement!;
    await user.click(within(empty).getByRole("button", { name: "Clear filters" }));
    expect(await screen.findByRole("table", { name: "Deployments" })).toBeInTheDocument();
    expect(router.state.location.search).toEqual({ sort: "-lease_expires_at" });
  });

  it("reads an old Only mine link as the owner filter", async () => {
    const { router } = renderApp("/deployments?mine=true");
    const table = await screen.findByRole("table", { name: "Deployments" }, { timeout: 5000 });
    await within(table).findByText("web-dev-worker");
    expect(screen.getByRole("checkbox", { name: "Only mine" })).toBeChecked();
    for (const row of within(table).getAllByRole("row").slice(1)) expect(row).toHaveTextContent("Léa Martin");

    await userEvent.setup().click(screen.getByRole("checkbox", { name: "Only mine" }));
    await waitFor(() => expect(router.state.location.search).toEqual({}));
    expect(await within(table).findByText("web-prd-edge")).toBeInTheDocument();
  });

  it("filters by catalog item and lease ended, from what the deployments have", async () => {
    const user = userEvent.setup();
    const { router } = renderApp("/deployments");
    const table = await screen.findByRole("table", { name: "Deployments" }, { timeout: 5000 });
    await within(table).findByText("web-prd-edge");

    await user.click(screen.getByRole("combobox", { name: "Catalog item" }));
    expect(await screen.findByRole("option", { name: "Windows Server 2025" })).toBeInTheDocument();
    expect(screen.queryByRole("option", { name: "PostgreSQL 16 cluster" })).not.toBeInTheDocument();
    await user.click(screen.getByRole("option", { name: "Ubuntu 24.04 server" }));
    await waitFor(() => expect(names(table)).toEqual(["web-prd-edge", "web-tst-search"]));
    expect(router.state.location.search).toEqual({ item: "ubuntu-server" });

    await user.click(screen.getByRole("combobox", { name: "VM template" }));
    await user.click(await screen.findByRole("option", { name: "tpl-win2025-std-2026.09" }));
    expect(await screen.findByText("No deployments match")).toBeInTheDocument();

    db.deployments.find((d) => d.name === "web-dev-api")!.lease.expires_at = new Date(
      Date.now() - 3_600_000,
    ).toISOString();
    renderApp("/deployments?status=expired");
    const expired = (await screen.findAllByRole("table", { name: "Deployments" }))[0]!;
    await waitFor(() => expect(names(expired)).toEqual(["web-dev-api"]));
  });

  it("finds an owner by name, and names them from the address", async () => {
    const user = userEvent.setup();
    const { router, unmount } = renderApp("/deployments");
    const table = await screen.findByRole("table", { name: "Deployments" }, { timeout: 5000 });
    await within(table).findByText("web-prd-edge");

    const owner = screen.getByRole("combobox", { name: "Owner" });
    expect(screen.getByText("Owner", { selector: "label" })).not.toHaveClass("sr-only");
    expect(owner).toHaveAttribute("placeholder", "Anyone");
    await user.type(owner, "thom");
    expect(await screen.findByRole("option", { name: /Thomas Nguyen/ })).toBeInTheDocument();
    await user.keyboard("{Enter}");
    await waitFor(() => expect(router.state.location.search).toEqual({ owner: "usr_tnguyen" }));
    await waitFor(() => expect(names(table)).toEqual(["web-dev-api", "web-dev-legacy", "web-prd-edge"]));
    unmount();

    renderApp("/deployments?owner=usr_tnguyen");
    await waitFor(() => expect(screen.getByRole("combobox", { name: "Owner" })).toHaveValue("Thomas Nguyen"));
    await user.click(screen.getByRole("button", { name: "Clear owner" }));
    expect(screen.getByRole("combobox", { name: "Owner" })).toHaveValue("");
  });

  it("sorts the whole list by a column's header, kept in the address", async () => {
    const user = userEvent.setup();
    const { router } = renderApp("/deployments");
    const table = await screen.findByRole("table", { name: "Deployments" }, { timeout: 5000 });
    await within(table).findByText("web-prd-edge");

    await user.click(within(table).getByRole("button", { name: "Status" }));
    await waitFor(() => expect(router.state.location.search).toEqual({ sort: "status" }));
    await waitFor(() => expect(names(table)[0]).toBe("web-dev-worker"));
    expect(within(table).getByRole("columnheader", { name: /Status/ })).toHaveAttribute("aria-sort", "ascending");

    await user.click(within(table).getByRole("button", { name: "VMs" }));
    await user.click(within(table).getByRole("button", { name: "VMs" }));
    await waitFor(() => expect(router.state.location.search).toEqual({ sort: "-vm_count" }));
    await waitFor(() => expect(names(table)[0]).toBe("web-prd-front"));
  });

  it("lists every project of the person's with All my projects, with each one's project", async () => {
    const user = userEvent.setup();
    const { router } = renderApp("/deployments");
    const table = await screen.findByRole("table", { name: "Deployments" }, { timeout: 5000 });
    await within(table).findByText("web-prd-edge");
    expect(within(table).queryByText("data-prd-pg-ledger")).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "All my projects" }));
    expect(await within(table).findByText("data-prd-pg-ledger")).toBeInTheDocument();
    expect(router.state.location.search).toEqual({ project: "all" });
    expect(screen.getByRole("button", { name: "All my projects" })).toHaveAttribute("aria-pressed", "true");
    expect(within(table).getByRole("columnheader", { name: /Project/ })).toBeInTheDocument();
    expect(screen.getByText(/What requests built in your projects/)).toBeInTheDocument();
  });

  it("shows cards, and remembers the layout for the person", async () => {
    const user = userEvent.setup();
    const { router, unmount } = renderApp("/deployments");
    await screen.findByRole("table", { name: "Deployments" }, { timeout: 5000 });

    await user.click(screen.getByRole("button", { name: "Cards" }));
    const cards = await screen.findByRole("list", { name: "Deployments" });
    expect(router.state.location.search).toEqual({ layout: "cards" });
    const card = within(cards).getByRole("link", { name: "web-dev-worker" }).closest("li")!;
    expect(card).toHaveTextContent("Failed");
    expect(within(card).getByRole("button", { name: "Actions for web-dev-worker" })).toBeInTheDocument();

    await user.click(screen.getByRole("combobox", { name: "Sort by" }));
    await user.click(await screen.findByRole("option", { name: "Most VMs first" }));
    await waitFor(() => expect(router.state.location.search).toEqual({ layout: "cards", sort: "-vm_count" }));
    unmount();

    renderApp("/deployments");
    expect(await screen.findByRole("list", { name: "Deployments" }, { timeout: 5000 })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Cards" })).toHaveAttribute("aria-pressed", "true");
  });

  it("saves the filters as a view, and reopens it from the menu", async () => {
    const user = userEvent.setup();
    const { router } = renderApp("/deployments?status=failed");
    const table = await screen.findByRole("table", { name: "Deployments" }, { timeout: 5000 });
    await within(table).findByText("web-dev-worker");

    await user.click(screen.getByRole("button", { name: "Views" }));
    expect(await screen.findByText("None yet. Filter the list, then save the view.")).toBeInTheDocument();
    await user.click(screen.getByRole("menuitem", { name: "Save as a new view…" }));
    const dialog = await screen.findByRole("dialog", { name: "Save this view" });
    await user.type(within(dialog).getByRole("textbox", { name: "Name" }), "Broken");
    await user.click(within(dialog).getByRole("button", { name: "Save view" }));
    expect(await screen.findByRole("button", { name: "Saved view: Broken" })).toBeInTheDocument();

    await user.click(screen.getByRole("checkbox", { name: "Only mine" }));
    await user.click(await screen.findByRole("button", { name: "Views" }));
    await user.click(await screen.findByRole("menuitem", { name: "Broken" }));
    await waitFor(() => expect(router.state.location.search).toEqual({ status: "failed", layout: "table" }));
    expect(await screen.findByRole("button", { name: "Saved view: Broken" })).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Saved view: Broken" }));
    await user.click(await screen.findByRole("menuitem", { name: "Save as a new view…" }));
    const again = await screen.findByRole("dialog", { name: "Save this view" });
    await user.type(within(again).getByRole("textbox", { name: "Name" }), "Broken");
    await user.click(within(again).getByRole("button", { name: "Save view" }));
    expect(await within(again).findByText(/You have a view with this name/)).toBeInTheDocument();
  });

  it("updates the view last opened with new filters, renames it and deletes it", async () => {
    const user = userEvent.setup();
    const { router } = renderApp("/deployments?status=failed");
    const table = await screen.findByRole("table", { name: "Deployments" }, { timeout: 5000 });
    await within(table).findByText("web-dev-worker");
    await user.click(screen.getByRole("button", { name: "Views" }));
    await user.click(await screen.findByRole("menuitem", { name: "Save as a new view…" }));
    const dialog = await screen.findByRole("dialog", { name: "Save this view" });
    await user.type(within(dialog).getByRole("textbox", { name: "Name" }), "Broken");
    await user.click(within(dialog).getByRole("button", { name: "Save view" }));
    await screen.findByRole("button", { name: "Saved view: Broken" });

    await user.click(screen.getByRole("checkbox", { name: "Only mine" }));
    await user.click(await screen.findByRole("button", { name: "Views" }));
    await user.click(await screen.findByRole("menuitem", { name: "Update Broken" }));
    expect(await screen.findByRole("button", { name: "Saved view: Broken" })).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Saved view: Broken" }));
    await user.click(await screen.findByRole("menuitem", { name: "Rename Broken…" }));
    const rename = await screen.findByRole("dialog", { name: "Rename Broken" });
    await user.clear(within(rename).getByRole("textbox", { name: "Name" }));
    await user.type(within(rename).getByRole("textbox", { name: "Name" }), "My failures");
    await user.click(within(rename).getByRole("button", { name: "Rename view" }));
    expect(await screen.findByRole("button", { name: "Saved view: My failures" })).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Saved view: My failures" }));
    await user.click(await screen.findByRole("menuitem", { name: "Delete My failures…" }));
    const confirm = await screen.findByRole("alertdialog", { name: "Delete My failures?" });
    await user.click(within(confirm).getByRole("button", { name: "Delete view" }));
    expect(await screen.findByRole("button", { name: "Views" })).toBeInTheDocument();
    expect(router.state.location.search).toEqual({ status: "failed", owner: "me" });
  });

  it("offers a row's actions in its menu, each one it can't run with the reason", async () => {
    const user = userEvent.setup();
    renderApp("/deployments");
    const table = await screen.findByRole("table", { name: "Deployments" }, { timeout: 5000 });
    await user.click(await within(table).findByRole("button", { name: "Actions for web-dev-worker" }));
    const menu = await screen.findByRole("menu");
    const powerOff = await within(menu).findByRole("menuitem", { name: /^Power off/ });
    expect(powerOff).toHaveAttribute("data-disabled");
    expect(powerOff).toHaveTextContent("The deployment is failed.");
    expect(within(menu).getByRole("menuitem", { name: "Delete" })).not.toHaveAttribute("data-disabled");
  });

  it("selects rows for a bulk action", async () => {
    const user = userEvent.setup();
    renderApp("/deployments");
    const table = await screen.findByRole("table", { name: "Deployments" }, { timeout: 5000 });
    await user.click(await within(table).findByRole("checkbox", { name: "Select web-dev-api" }));
    await user.click(within(table).getByRole("checkbox", { name: "Select web-prd-cache" }));
    const bar = screen.getByRole("region", { name: "Bulk actions" });
    expect(bar).toHaveTextContent("2 deployments selected");
    await user.click(within(bar).getByRole("button", { name: "Clear" }));
    expect(screen.queryByRole("region", { name: "Bulk actions" })).not.toBeInTheDocument();
  });

  it("points to the catalog when nothing is deployed yet", async () => {
    db.deployments.length = 0;
    renderApp("/deployments");
    expect(await screen.findByText("Nothing deployed in Web Platform yet")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Request something from the catalog" })).toHaveAttribute(
      "href",
      "/catalog",
    );
  });
});
