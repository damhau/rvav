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

  it("shows the OU vRA's Active Directory integration gives each project", async () => {
    renderApp("/admin/vra-import?tab=projects");
    const table = await screen.findByRole("table", { name: "vRA projects" });
    const ou = (id: string) => (within(table).getByText(id).closest("tr") as HTMLTableRowElement).cells[5];
    expect(ou("vra-prj-web")).toHaveTextContent(/^OU=Web,OU=ServersTemplates may choose theirs$/);
    expect(ou("vra-prj-data")).toHaveTextContent(/^None$/);
    expect(ou("vra-prj-legacy")).toHaveTextContent(/^No computer objects$/);
  });

  it("says what vRA's deployment sharing becomes, and where the rvav project's differs", async () => {
    const user = userEvent.setup();
    renderApp("/admin/vra-import?tab=projects");
    const table = await screen.findByRole("table", { name: "vRA projects" });
    const sharing = (id: string) => (within(table).getByText(id).closest("tr") as HTMLTableRowElement).cells[4];
    expect(sharing("vra-prj-web")).toHaveTextContent(/^All members$/);
    expect(sharing("vra-prj-data")).toHaveTextContent(/^Owners only$/);

    await user.click(within(table).getByRole("combobox", { name: "rvav project for Data Services" }));
    await user.click(await screen.findByRole("option", { name: "Data Engineering" }));
    await waitFor(() =>
      expect(sharing("vra-prj-data")).toHaveTextContent(
        /^Owners onlyData Engineering has all members: set it in the project's Members tab\.$/,
      ),
    );
  });

  it("gives each lease policy its enforcement", async () => {
    renderApp("/admin/vra-import?tab=policies");
    const lease = await screen.findByRole("region", { name: "Development leases" });
    expect(within(lease).getByText("soft")).toBeInTheDocument();
    expect(lease).toHaveTextContent("its project admins can write it");
    expect(lease).toHaveTextContent("Lifetime: 90 days from the deployment's creation; an extension past it warns.");
    const limit = screen.getByRole("region", { name: "Deployment limit" });
    expect(within(limit).getByText("hard")).toBeInTheDocument();
    expect(limit).toHaveTextContent("A deployment limit policy for every project (Administration › Policies › Limits)");
    expect(limit).toHaveTextContent("vCPU per deployment: 16.");
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
    expect(within(sheet).getByRole("checkbox", { name: "Deployments" })).toHaveAccessibleDescription(
      /to adopt through onboarding, every project's/,
    );
    expect(sheet).toHaveTextContent(/2 property groups to make from vRA's;/);
    expect(sheet).toHaveTextContent(/1 form library entry to make from vRO's actions;/);
    await user.click(within(sheet).getByRole("button", { name: "Import" }));
    const result = (await screen.findAllByRole("region", { name: "Imported" })).at(-1);
    expect(result).toHaveTextContent(/Projects given vRA's custom properties: Web Platform/);
    expect(result).toHaveTextContent(/Property groups made: corpInputs, dnsSettings/);
    expect(result).toHaveTextContent(/Form library entries made: getCostCentres/);
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

  it("lists the form library entries vRO's actions become, and why one can't be", async () => {
    const user = userEvent.setup();
    renderApp("/admin/vra-import?tab=items");
    const table = await screen.findByRole("table", { name: "Form library" });
    const row = (action: string) => within(table).getByText(action).closest("tr") as HTMLTableRowElement;
    expect(row("com.corp.finance/getCostCentres")).toHaveTextContent(
      "getCostCentres · https://finance.corp.example/api/cost-centres?env=${params.environment}Create",
    );
    expect(row("com.corp.db/getVersions")).toHaveTextContent(/getVersionsNot imported.*see why in the notes/);
    await user.click(within(table).getByRole("button", { name: "Notes on com.corp.db/getVersions: 1 left out" }));
    const sheet = await screen.findByRole("dialog", { name: "Notes on com.corp.db/getVersions" });
    expect(sheet).toHaveTextContent(/vRO's REST host dbaas, whose URL the import doesn't know/);
  });

  it("lists the templates vRA's image mappings become, with what the import does and why", async () => {
    const user = userEvent.setup();
    renderApp("/admin/vra-import?tab=items");
    const table = await screen.findByRole("table", { name: "Templates" });
    const row = (label: string) => within(table).getAllByText(label)[0]!.closest("tr") as HTMLTableRowElement;
    expect(row("rhel9")).toHaveTextContent(/^rhel9rhel9 · LinuxCreatevc01\.corp\.example: tpl-rhel9\.4-2026\.10/);
    expect(row("win2022")).toHaveTextContent(/win2022 · Windows · 1 constraintCreate/);
    expect(row("ubuntu22")).toHaveTextContent(
      /ubuntu22 · Linux · cloud-initDiffers in rvavLeft as it is: rvav's template isn't what vRA's mappings give\./,
    );
    await user.click(within(table).getByRole("button", { name: "Notes on win2022: 1 left out" }));
    const sheet = await screen.findByRole("dialog", { name: "Notes on win2022" });
    expect(sheet).toHaveTextContent(/which rvav doesn't manage: its template tpl-win2025-std-zrh is left out/);
    await user.keyboard("{Escape}");
    await user.click(within(table).getByRole("button", { name: "Notes on ubuntu22: 2 to check" }));
    expect(await screen.findByRole("dialog", { name: "Notes on ubuntu22" })).toHaveTextContent(
      /rvav has one template per vCenter, tpl-ubuntu22\.04/,
    );
  });

  it("marks the VMs kept from deletion, before adoption and once adopted", async () => {
    const user = userEvent.setup();
    renderApp("/admin/vra-import?tab=deployments");
    const vm = async (name: string) =>
      (await within(await screen.findByRole("table", { name: "vRA deployments" })).findByText(name)).closest("li");
    expect(await vm("vra-api-001")).toHaveTextContent(/^vra-api-001 Kept from deletion$/);
    expect(await vm("vra-api-002")).toHaveTextContent(/^vra-api-002$/);
    await user.click(screen.getByRole("button", { name: "Notes on VRA Web Pair: 2 notes" }));
    expect(await screen.findByRole("dialog", { name: "Notes on VRA Web Pair" })).toHaveTextContent(
      /vRA keeps vra-api-001 from deletion \(preventDelete\): rvav does too once adopted/,
    );
    await user.keyboard("{Escape}");

    await user.click(screen.getByRole("tab", { name: /Cutover/ }));
    await user.click(await screen.findByRole("button", { name: "Adopt 2 deployments (Web Platform)" }));
    expect(await screen.findByText("Adopted 2 deployments of Web Platform")).toBeInTheDocument();
    await user.click(screen.getByRole("tab", { name: /Deployments/ }));
    await user.click(await screen.findByRole("button", { name: "Notes on VRA Web Pair: 1 note" }));
    expect(await screen.findByRole("dialog", { name: "Notes on VRA Web Pair" })).toHaveTextContent(
      "Kept from deletion, as in vRA: vra-api-001.",
    );
    await user.keyboard("{Escape}");
    expect(await vm("vra-api-001")).toHaveTextContent(/^vra-api-001 Kept from deletion$/);
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
