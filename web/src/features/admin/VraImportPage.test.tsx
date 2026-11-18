import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it } from "vitest";
import { server } from "@/mocks/node";
import { renderApp } from "@/test/render";

// The vRA import's page, against the mock API.
describe("vRA import", () => {
  it("without vRA set, links to its tab in Integrations", async () => {
    server.use(http.get("*/api/v1/admin/vra", () => HttpResponse.json({ configured: false, url: "" })));
    renderApp("/admin/vra-import");
    expect(await screen.findByRole("link", { name: "Integrations › vRA" })).toHaveAttribute(
      "href",
      "/admin/integrations/vra",
    );
  });

  it("gives each project the strategy that places as vRA's placement policy", async () => {
    const user = userEvent.setup();
    renderApp("/admin/vra-import");
    await user.click(await screen.findByRole("tab", { name: "Projects" }));
    const table = await screen.findByRole("table", { name: "vRA projects" });
    const strategy = (id: string) => (within(table).getByText(id).closest("tr") as HTMLTableRowElement).cells[2];
    expect(strategy("vra-prj-web")).toHaveTextContent(/^SpreadvRA: SPREAD$/);
    expect(strategy("vra-prj-legacy")).toHaveTextContent(/^None$/);
  });
});
