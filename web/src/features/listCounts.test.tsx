// Each list with a FilterBar says how many rows its filters leave, in the
// bar, where a screen reader hears it change.
import { screen, waitFor, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { renderApp } from "@/test/render";

const lists: [string, RegExp][] = [
  ["/deployments", /^\d+ (deployments|shown) · \d+ VMs, \d+ powered on/],
  ["/resources", /^\d+ (VMs|shown) · \d+ (of them )?powered on$/],
  ["/requests", /^\d+ (requests|shown)$/],
  ["/admin/audit", /^\d+ (events|shown)$/],
  ["/admin/projects", /^\d+ projects$/],
  ["/admin/infrastructure?tab=inventory", /^\d+ objects$/],
  ["/admin/catalog-items", /^\d+ items$/],
  ["/admin/subscriptions/sub_servicenow", /^\d+ (runs|shown)$/],
];

describe("a list's count", () => {
  it.each(lists)("is said in the toolbar of %s", async (path, count) => {
    renderApp(path);
    const bar = await screen.findByRole("group", { name: "Filters" }, { timeout: 5000 });
    const said = await within(bar).findByText(count, undefined, { timeout: 5000 });
    expect(said).toHaveAttribute("aria-live", "polite");
  });

  it("follows the filters", async () => {
    renderApp("/admin/infrastructure?tab=inventory&kind=cluster");
    const bar = await screen.findByRole("group", { name: "Filters" }, { timeout: 5000 });
    const said = await within(bar).findByText(/^\d+ objects?$/, undefined, { timeout: 5000 });
    const clusters = Number(said.textContent.split(" ")[0]);
    await waitFor(() =>
      expect(screen.getByRole("table", { name: "vCenter objects" }).querySelectorAll("tbody tr")).toHaveLength(
        clusters,
      ),
    );
  });
});
