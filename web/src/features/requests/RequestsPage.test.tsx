import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { db } from "@/mocks/seed";
import { renderApp } from "@/test/render";

describe("the requests list against the mock API", () => {
  it("filters by status from the address, and a row opens its request", async () => {
    const user = userEvent.setup();
    const { router } = renderApp("/requests?status=failed");
    const table = await screen.findByRole("table", { name: "Requests" }, { timeout: 5000 });
    await within(table).findByText("REQ-10484");
    const rows = within(table).getAllByRole("row").slice(1);
    expect(rows.length).toBeGreaterThan(0);
    for (const row of rows) expect(row).toHaveTextContent("Failed");
    expect(screen.getByRole("combobox", { name: "Status" })).toHaveTextContent("Failed");

    await user.click(within(table).getByText("REQ-10484"));
    await waitFor(() => expect(router.state.location.pathname).toBe("/requests/REQ-10484"));
  });

  it("keeps the search in the address, and clears it from the empty state", async () => {
    const user = userEvent.setup();
    const { router } = renderApp("/requests");
    await screen.findByRole("table", { name: "Requests" }, { timeout: 5000 });
    await user.type(screen.getByRole("searchbox", { name: "Search requests" }), "no such thing");
    expect(await screen.findByText("No requests match")).toBeInTheDocument();
    await waitFor(() => expect(router.state.location.search).toEqual({ q: "no such thing" }));
    await user.click(screen.getByRole("checkbox", { name: "Only mine" }));
    await waitFor(() => expect(router.state.location.search).toEqual({ q: "no such thing", mine: true }));

    const empty = screen.getByText("No requests match").parentElement!;
    await user.click(within(empty).getByRole("button", { name: "Clear filters" }));
    expect(await screen.findByRole("table", { name: "Requests" })).toBeInTheDocument();
    expect(router.state.location.search).toEqual({});
  });

  it("points to the catalog when nothing was requested yet", async () => {
    db.requests.length = 0;
    renderApp("/requests");
    expect(await screen.findByText("Nothing requested in Web Platform yet")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Request something from the catalog" })).toHaveAttribute(
      "href",
      "/catalog",
    );
  });
});

describe("the overview", () => {
  it("says what to do when nothing is open", async () => {
    db.requests.length = 0;
    renderApp("/");
    const open = await screen.findByRole("region", { name: "Open requests" });
    expect(await within(open).findByText("No open requests")).toBeInTheDocument();
    expect(within(open).getByRole("link", { name: "Request something from the catalog" })).toHaveAttribute(
      "href",
      "/catalog",
    );
  });
});

describe("your drafts", () => {
  it("asks before discarding one", async () => {
    const user = userEvent.setup();
    const item = db.items.find((i) => i.slug === "rhel9-server")!;
    const project = db.projects[0]!;
    const now = new Date().toISOString();
    Object.assign(db, {
      drafts: [
        {
          id: "drf_batch",
          name: "Batch servers",
          draft: { catalog_item_id: item.id, project_id: project.id, environment: "dev", inputs: {} },
          catalog_item: { id: item.id, slug: item.slug, name: item.name },
          project: { id: project.id, code: project.code, name: project.name },
          created_at: now,
          updated_at: now,
        },
      ],
    });
    renderApp("/requests");
    await user.click(await screen.findByRole("button", { name: "Discard Batch servers" }, { timeout: 5000 }));
    const ask = await screen.findByRole("alertdialog", { name: "Discard Batch servers?" });
    await user.click(within(ask).getByRole("button", { name: "Discard draft" }));
    expect(await screen.findByText("Draft discarded")).toBeInTheDocument();
    await waitFor(() => expect(screen.queryByRole("list", { name: "Your drafts" })).toBeNull());
  });
});
