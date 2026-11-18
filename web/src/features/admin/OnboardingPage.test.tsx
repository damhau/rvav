import { screen, within } from "@testing-library/react";
import userEvent, { type UserEvent } from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { renderApp } from "@/test/render";

async function choose(user: UserEvent, combobox: string, option: string) {
  await user.click(screen.getByRole("combobox", { name: combobox }));
  await user.click(await screen.findByRole("option", { name: option }));
}

/** Searches, then waits for the list the search gives: its rows are replaced once the search applies. */
async function search(user: UserEvent, text: string, found: string | RegExp) {
  const box = await screen.findByRole("textbox", { name: "Search" });
  await user.clear(box);
  await user.click(box);
  await user.paste(text);
  await screen.findByText(found, {}, { timeout: 5000 });
}

// Onboarding against the mock API.
describe("Onboarding", () => {
  it("keeps a selection the filters hide, and says so", { timeout: 15_000 }, async () => {
    const user = userEvent.setup();
    renderApp("/admin/onboarding");
    await search(user, "app-old", "8 VMs");
    const table = screen.getByRole("table", { name: "VMs to adopt" });
    await user.click(within(table).getByRole("checkbox", { name: "Select app-old01" }));
    expect(screen.getByRole("button", { name: "Adopt 1 VM…" })).toBeEnabled();

    await search(user, "erp-app", "12 VMs · 1 selected, 1 not shown");
    await user.click(within(table).getByRole("checkbox", { name: "Select all shown" }));
    expect(screen.getByText("12 VMs · 13 selected, 1 not shown")).toBeInTheDocument();
    await user.click(within(table).getByRole("checkbox", { name: "Select all shown" }));
    expect(screen.getByText("12 VMs · 1 selected, 1 not shown")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Clear selection" }));
    expect(screen.getByRole("button", { name: "Adopt VMs…" })).toBeDisabled();
  });

  it("adopts the selected VMs from a side panel", { timeout: 15_000 }, async () => {
    const user = userEvent.setup();
    renderApp("/admin/onboarding");
    await search(user, "app-old", "8 VMs");
    const table = screen.getByRole("table", { name: "VMs to adopt" });
    await user.click(within(table).getByRole("checkbox", { name: "Select app-old01" }));
    await user.click(within(table).getByRole("checkbox", { name: "Select app-old02" }));
    await user.click(screen.getByRole("button", { name: "Adopt 2 VMs…" }));

    const sheet = await screen.findByRole("dialog", { name: "Adopt 2 VMs" });
    expect(within(sheet).getByRole("list", { name: "VMs to adopt" })).toHaveTextContent(/app-old01.*app-old02/);
    await choose(user, "Project", "Web Platform");
    await choose(user, "Environment", "dev");
    await choose(user, "Owner", "Léa Martin");
    await choose(user, "Catalog item", "RHEL 9 server");
    await user.click(within(sheet).getByRole("button", { name: "Adopt 2 VMs" }));

    expect(await screen.findByRole("region", { name: "Adopted 2 VMs into 2 deployments" })).toBeInTheDocument();
    expect(screen.queryByRole("dialog", { name: "Adopt 2 VMs" })).toBeNull();
    expect(screen.getByRole("button", { name: "Adopt VMs…" })).toBeDisabled();
  });

  it("offers to clear filters that match nothing", { timeout: 15_000 }, async () => {
    const user = userEvent.setup();
    renderApp("/admin/onboarding");
    await search(user, "no-such-vm", "No VM rvav doesn't manage matches these filters.");
    await user.click(screen.getByRole("button", { name: "Clear filters" }));
    expect(screen.getByRole("textbox", { name: "Search" })).toHaveValue("");
    expect(await screen.findByText("60 VMs")).toBeInTheDocument();
  });
});
