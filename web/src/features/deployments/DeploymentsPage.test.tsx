import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { db } from "@/mocks/seed";
import { renderApp } from "@/test/render";

describe("the deployments list against the mock API", () => {
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
        mine: true,
        expiring: true,
        q: "no such thing",
      }),
    );

    const empty = screen.getByText("No deployments match").parentElement!;
    await user.click(within(empty).getByRole("button", { name: "Clear filters" }));
    expect(await screen.findByRole("table", { name: "Deployments" })).toBeInTheDocument();
    expect(router.state.location.search).toEqual({ sort: "-lease_expires_at" });
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
