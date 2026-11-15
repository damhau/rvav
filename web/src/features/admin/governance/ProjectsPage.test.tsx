import { QueryClient } from "@tanstack/react-query";
import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { renderApp } from "@/test/render";

// OIDC group mappings on the Projects page, with the app's own staleTime
// (main.tsx): what a mapping changes elsewhere is fetched again.
describe("a group mapping", () => {
  it("shows at once in the roles' holders", async () => {
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false, staleTime: 30_000 }, mutations: { retry: false } },
    });
    const user = userEvent.setup();
    const { router } = renderApp("/admin/roles", client);
    expect(await screen.findByRole("row", { name: /^Auditor/ })).toHaveTextContent("Nobody");

    await router.navigate({ to: "/admin/projects" });
    const groups = await screen.findByRole("region", { name: "OIDC group mappings" });
    await user.type(within(groups).getByRole("textbox", { name: "OIDC group" }), "rvav-audit");
    await user.click(within(groups).getByRole("button", { name: "Map group" }));
    expect(await within(groups).findByRole("cell", { name: "rvav-audit" })).toBeInTheDocument();

    await router.navigate({ to: "/admin/roles" });
    expect(await screen.findByRole("row", { name: /^Auditor/ })).toHaveTextContent("1 time");
  });
});
