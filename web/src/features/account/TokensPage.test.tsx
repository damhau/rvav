import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it } from "vitest";
import type { components } from "@/lib/api/schema";
import { server } from "@/mocks/node";
import { renderApp } from "@/test/render";

type ApiToken = components["schemas"]["ApiToken"];

const ago = (days: number) => new Date(Date.now() - days * 86_400_000).toISOString();
const user = { id: "usr_lea", name: "Léa Fontaine" };

function serve(items: ApiToken[]) {
  server.use(http.get("*/api/v1/tokens", () => HttpResponse.json({ items })));
}

const names = (table: HTMLElement) =>
  within(table)
    .getAllByRole("row")
    .slice(1)
    .map((r) => within(r).getAllByRole("cell")[0]!.querySelector(".font-medium")!.textContent);

describe("API tokens", () => {
  it("sorts the tokens by name, last use or expiry", async () => {
    const u = userEvent.setup();
    serve([
      { id: "tok_b", name: "Backups", scope: "all", user, created_at: ago(30), last_used_at: ago(1) },
      { id: "tok_a", name: "Ansible", scope: "inventory", user, created_at: ago(9), expires_at: ago(-5) },
      {
        id: "tok_c",
        name: "CI",
        scope: "all",
        user,
        created_at: ago(3),
        last_used_at: ago(2),
        expires_at: ago(-90),
      },
    ]);
    renderApp("/tokens");
    const table = await screen.findByRole("table", { name: "Your tokens" }, { timeout: 5000 });
    await waitFor(() => expect(names(table)).toEqual(["Backups", "Ansible", "CI"]));

    await u.click(within(table).getByRole("button", { name: "Token" }));
    expect(names(table)).toEqual(["Ansible", "Backups", "CI"]);
    await u.click(within(table).getByRole("button", { name: "Last used" }));
    expect(within(table).getByRole("columnheader", { name: "Last used" })).toHaveAttribute("aria-sort", "ascending");
    expect(names(table)).toEqual(["CI", "Backups", "Ansible"]);
    await u.click(within(table).getByRole("button", { name: "Expires" }));
    expect(names(table)).toEqual(["Ansible", "CI", "Backups"]);
    expect(within(table).getByRole("columnheader", { name: "Actions" })).toBeInTheDocument();
  });

  it("revokes a token from its row once asked, leaving the list's empty state", async () => {
    const u = userEvent.setup();
    renderApp("/tokens");
    const table = await screen.findByRole("table", { name: "Your tokens" }, { timeout: 5000 });
    const row = await within(table).findByRole("row", { name: /GitLab pipeline/ });
    expect(row).toHaveTextContent("Everything you may do · made 20 days ago");
    await u.click(within(row).getByRole("button", { name: "Revoke GitLab pipeline" }));
    const ask = await screen.findByRole("alertdialog", { name: "Revoke GitLab pipeline?" });
    await u.click(within(ask).getByRole("button", { name: "Revoke token" }));
    expect(await screen.findByText("No tokens")).toBeVisible();
    expect(screen.getByText("Create one above for a script or a pipeline.")).toBeVisible();
    expect(screen.queryByRole("table", { name: "Your tokens" })).toBeNull();
  });

  it("names each token's owner among everyone's", async () => {
    const u = userEvent.setup();
    renderApp("/tokens");
    await screen.findByRole("table", { name: "Your tokens" }, { timeout: 5000 });
    expect(screen.queryByRole("columnheader", { name: "Owner" })).toBeNull();
    await u.click(screen.getByRole("checkbox", { name: "Everyone's" }));
    const table = await screen.findByRole("table", { name: "Everyone's tokens" });
    expect(within(table).getByRole("columnheader", { name: "Owner" })).toBeInTheDocument();
  });
});
