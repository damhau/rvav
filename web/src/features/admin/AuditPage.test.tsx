import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it } from "vitest";
import { resetDb } from "@/mocks/seed";
import { renderApp } from "@/test/render";

afterEach(() => localStorage.removeItem("rvav.mock.fresh"));

describe("Audit log", () => {
  it("filters from its toolbar, says when nothing matches, and clears the filters", async () => {
    const user = userEvent.setup();
    renderApp("/admin/audit");
    const table = await screen.findByRole("table", { name: "Audit events" });
    expect(await within(table).findByText("catalog.version.publish")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Clear filters" })).toBeNull();

    await user.click(screen.getByRole("combobox", { name: "What" }));
    await user.click(await screen.findByRole("option", { name: "API tokens" }));
    await waitFor(() => expect(within(table).queryByText("catalog.version.publish")).toBeNull());
    expect(within(table).getByText("token.create")).toBeInTheDocument();

    await user.type(screen.getByRole("textbox", { name: "Search" }), "nobody-did-this");
    expect(await screen.findByText("No event matches these filters.")).toBeInTheDocument();
    const clear = screen.getAllByRole("button", { name: "Clear filters" });
    await user.click(clear[clear.length - 1]!);
    expect(await screen.findByText("catalog.version.publish")).toBeInTheDocument();
    expect(screen.getByRole("textbox", { name: "Search" })).toHaveValue("");
    expect(screen.queryByRole("button", { name: "Clear filters" })).toBeNull();
  });

  it("shows failed sign-ins only, from where, by nobody known", async () => {
    const user = userEvent.setup();
    renderApp("/admin/audit");
    const table = await screen.findByRole("table", { name: "Audit events" });
    expect(await within(table).findByText("auth.sign_in")).toBeInTheDocument();
    await user.click(screen.getByRole("combobox", { name: "What" }));
    await user.click(await screen.findByRole("option", { name: "Failed sign-ins" }));
    await waitFor(() => expect(within(table).queryByText("auth.sign_in")).toBeNull());
    const row = within(table).getByText("auth.sign_in_failed").closest("tr")!;
    expect(row).toHaveTextContent("Unknown");
    expect(row).toHaveTextContent("from 10.20.4.17");
    expect(within(table).queryByText("catalog.version.publish")).toBeNull();
  });

  it("opens an event in a side panel with its detail", async () => {
    const user = userEvent.setup();
    renderApp("/admin/audit");
    const table = await screen.findByRole("table", { name: "Audit events" });
    const row = (await within(table).findByText("member.set")).closest("tr")!;
    expect(row).toHaveTextContent("2 details");
    await user.click(within(row).getByRole("button", { name: /^Open the event from/ }));
    const sheet = await screen.findByRole("dialog", { name: "member.set" });
    expect(sheet).toHaveTextContent("Léa Martin");
    expect(sheet).toHaveTextContent("Web Platform");
    expect(sheet).toHaveTextContent("usr_tnguyen");
  });

  it("says nothing is recorded on a fresh install", async () => {
    localStorage.setItem("rvav.mock.fresh", "1");
    resetDb();
    renderApp("/admin/audit");
    expect(await screen.findByText("Nothing recorded yet")).toBeInTheDocument();
  });
});
