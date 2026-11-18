import { screen, waitFor, within } from "@testing-library/react";
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

  it("adds vRA's custom properties and property groups when the catalog is imported", { timeout: 15_000 }, async () => {
    const user = userEvent.setup();
    renderApp("/admin/vra-import?tab=projects");
    const table = await screen.findByRole("table", { name: "vRA projects" });
    const properties = (id: string) => (within(table).getByText(id).closest("tr") as HTMLTableRowElement).cells[3];
    // rvav's Web Platform has its own backupPolicy, which stays.
    await waitFor(() =>
      expect(properties("vra-prj-web")).toHaveTextContent(/^costCenter=CC-1001Left out: cost center$/),
    );
    expect(properties("vra-prj-legacy")).toHaveTextContent(/^None to add$/);

    await user.click(screen.getByRole("button", { name: "Import…" }));
    const sheet = await screen.findByRole("dialog", { name: "Import from vRA" });
    expect(sheet).toHaveTextContent(/2 property groups to make from vRA's;/);
    await user.click(within(sheet).getByRole("button", { name: "Import" }));
    const result = (await screen.findAllByRole("region", { name: "Imported" })).at(-1);
    expect(result).toHaveTextContent(/Projects given vRA's custom properties: Web Platform/);
    expect(result).toHaveTextContent(/Property groups made: corpInputs, dnsSettings/);
    await waitFor(() => expect(properties("vra-prj-web")).toHaveTextContent(/^Left out: cost center$/));
  });

  it("says which step the move is at, and keeps the plan's tab in the address", async () => {
    const user = userEvent.setup();
    const { router } = renderApp("/admin/vra-import");
    const steps = await screen.findByRole("list", { name: "Steps" });
    await within(steps).findByText(/with no rvav project of the same name/);
    const now = within(steps)
      .getAllByRole("listitem")
      .find((li) => li.getAttribute("aria-current") === "step");
    expect(now).toHaveTextContent(/Map the projects: now/);
    expect(within(steps).getAllByRole("listitem")[0]).toHaveTextContent(/Read vRA: done/);
    // The plan opens where that step is done.
    expect(await screen.findByRole("tab", { name: "Projects", selected: true })).toBeInTheDocument();

    await user.click(await screen.findByRole("tab", { name: /Cutover/ }));
    await waitFor(() => expect(router.state.location.search).toEqual({ tab: "cutover" }));
  });

  it("opens a template's notes in a side panel", async () => {
    const user = userEvent.setup();
    renderApp("/admin/vra-import?tab=items");
    await user.click(await screen.findByRole("button", { name: "Notes on RHEL 9 Server: 2 notes" }));
    const sheet = await screen.findByRole("dialog", { name: "Notes on RHEL 9 Server" });
    expect(sheet).toHaveTextContent(/Placed by its constraint tags/);
  });

  it("lists vRA's property groups under the templates, with their notes", async () => {
    const user = userEvent.setup();
    renderApp("/admin/vra-import?tab=items");
    const table = await screen.findByRole("table", { name: "Property groups" });
    const row = (label: string) => within(table).getByText(label).closest("tr") as HTMLTableRowElement;
    expect(row("Corporate inputs")).toHaveTextContent(/corpInputs · 2 inputsCreate/);
    expect(row("DNS settings")).toHaveTextContent(/dnsSettings · 2 constantsCreate/);
    await user.click(within(table).getByRole("button", { name: "Notes on Corporate inputs: 1 note" }));
    const sheet = await screen.findByRole("dialog", { name: "Notes on Corporate inputs" });
    expect(sheet).toHaveTextContent(/the form shows their values/);
  });

  it("marks a project moved from a side panel, and not moved once confirmed", async () => {
    const user = userEvent.setup();
    renderApp("/admin/vra-import?tab=cutover");
    await user.click(await screen.findByRole("button", { name: "Mark as moved… (Web Platform)" }));
    const sheet = await screen.findByRole("dialog", { name: "Move Web Platform to rvav" });
    await user.type(within(sheet).getByRole("textbox", { name: "Note" }), "Cutover weekend");
    await user.click(within(sheet).getByRole("button", { name: "Mark as moved" }));
    expect(await screen.findByText("Web Platform moved to rvav")).toBeInTheDocument();

    await user.click(await screen.findByRole("button", { name: "Not moved… (Web Platform)" }));
    const confirm = await screen.findByRole("alertdialog", { name: "Mark Web Platform not moved?" });
    expect(confirm).toHaveTextContent(/deleting an adopted deployment in vRA deletes its VMs/);
    await user.click(within(confirm).getByRole("button", { name: "Not moved" }));
    expect(await screen.findByText("Web Platform is no longer moved")).toBeInTheDocument();
  });
});
