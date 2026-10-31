import { screen } from "@testing-library/react";
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
      "/admin/integrations?tab=vra",
    );
  });
});
