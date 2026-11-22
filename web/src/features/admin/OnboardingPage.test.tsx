import { screen, waitFor, within } from "@testing-library/react";
import userEvent, { type UserEvent } from "@testing-library/user-event";
import { http } from "msw";
import { describe, expect, it } from "vitest";
import { server } from "@/mocks/node";
import { db } from "@/mocks/seed";
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
    // Nothing selected, nothing to adopt: no button waits disabled for a reason it doesn't say.
    expect(screen.queryByRole("region", { name: "Selected VMs" })).toBeNull();
    expect(screen.queryByRole("button", { name: /^Adopt/ })).toBeNull();
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
    expect(screen.queryByRole("region", { name: "Selected VMs" })).toBeNull();
  });

  it("keeps the VMs it adopts from deletion when asked", { timeout: 15_000 }, async () => {
    const sent: unknown[] = [];
    server.use(
      http.post("*/api/v1/admin/onboarding", async ({ request }) => {
        sent.push(await request.clone().json());
      }),
    );
    const user = userEvent.setup();
    renderApp("/admin/onboarding");
    await search(user, "app-old", "8 VMs");
    await user.click(screen.getByRole("checkbox", { name: "Select app-old01" }));
    await user.click(screen.getByRole("button", { name: "Adopt 1 VM…" }));
    const sheet = await screen.findByRole("dialog", { name: "Adopt 1 VM" });
    await choose(user, "Project", "Web Platform");
    await choose(user, "Environment", "dev");
    await choose(user, "Owner", "Léa Martin");
    await choose(user, "Catalog item", "RHEL 9 server");
    await user.click(within(sheet).getByRole("checkbox", { name: /Keep from deletion/ }));
    await user.click(within(sheet).getByRole("button", { name: "Adopt 1 VM" }));

    expect(await screen.findByRole("region", { name: "Adopted 1 VM into 1 deployment" })).toBeInTheDocument();
    expect(sent).toEqual([expect.objectContaining({ vms: [expect.objectContaining({ prevent_delete: true })] })]);
  });

  it("puts the focus on the first field the server refuses", { timeout: 15_000 }, async () => {
    const user = userEvent.setup();
    renderApp("/admin/onboarding");
    await search(user, "app-old", "8 VMs");
    const table = screen.getByRole("table", { name: "VMs to adopt" });
    await user.click(within(table).getByRole("checkbox", { name: "Select app-old01" }));
    await user.click(screen.getByRole("button", { name: "Adopt 1 VM…" }));
    const sheet = await screen.findByRole("dialog", { name: "Adopt 1 VM" });
    await user.click(within(sheet).getByRole("button", { name: "Adopt 1 VM" }));
    expect(await within(sheet).findByText("No such project.")).toBeInTheDocument();
    await waitFor(() => expect(within(sheet).getByRole("combobox", { name: "Project" })).toHaveFocus());
  });

  it("adopts VMs into an existing deployment", { timeout: 15_000 }, async () => {
    const user = userEvent.setup();
    renderApp("/admin/onboarding");
    await search(user, "app-old", "8 VMs");
    const table = screen.getByRole("table", { name: "VMs to adopt" });
    await user.click(within(table).getByRole("checkbox", { name: "Select app-old01" }));
    await user.click(screen.getByRole("button", { name: "Adopt 1 VM…" }));

    const sheet = await screen.findByRole("dialog", { name: "Adopt 1 VM" });
    await choose(user, "Project", "Web Platform");
    await choose(user, "Where they go", "Into an existing deployment");
    // Its owner, environment, item and lease are the deployment's.
    expect(within(sheet).queryByRole("combobox", { name: "Owner" })).toBeNull();
    await choose(user, "Deployment", "web-prd-cache");
    await user.click(within(sheet).getByRole("button", { name: "Adopt 1 VM" }));

    const adopted = await screen.findByRole("region", { name: "Adopted 1 VM into 1 deployment" });
    expect(within(adopted).getByRole("link", { name: "web-prd-cache" })).toBeInTheDocument();
  });

  it("offers every ready deployment of a VM item to adopt into, and no other", { timeout: 15_000 }, async () => {
    const cache = db.deployments.find((d) => d.name === "web-prd-cache")!;
    const dns = db.items.find((i) => i.slug === "dns-record")!;
    const bulk = Array.from({ length: 101 }, (_, i) => ({
      ...structuredClone(cache),
      id: `dep_bulk${i}`,
      name: `web-prd-bulk${String(i).padStart(3, "0")}`,
    }));
    const record = { ...structuredClone(cache), id: "dep_dnsrec", name: "web-prd-dnsrec" };
    record.catalog_item = { id: dns.id, slug: dns.slug, name: dns.name };
    db.deployments.push(...bulk, record);

    const user = userEvent.setup();
    renderApp("/admin/onboarding");
    await search(user, "app-old", "8 VMs");
    const table = screen.getByRole("table", { name: "VMs to adopt" });
    await user.click(within(table).getByRole("checkbox", { name: "Select app-old01" }));
    await user.click(screen.getByRole("button", { name: "Adopt 1 VM…" }));
    await screen.findByRole("dialog", { name: "Adopt 1 VM" });
    await choose(user, "Project", "Web Platform");
    await choose(user, "Where they go", "Into an existing deployment");
    await waitFor(() => expect(screen.getByRole("combobox", { name: "Deployment" })).toBeEnabled());
    await user.click(screen.getByRole("combobox", { name: "Deployment" }));
    const listbox = await screen.findByRole("listbox");
    await waitFor(() =>
      expect(
        within(listbox)
          .getAllByRole("option")
          .map((o) => o.textContent),
      ).toEqual(expect.arrayContaining(["web-prd-cache", ...bulk.map((d) => d.name)])),
    );
    expect(within(listbox).queryByRole("option", { name: "web-prd-dnsrec" })).toBeNull();
  });

  it("filters by folder, and its folders", { timeout: 15_000 }, async () => {
    const user = userEvent.setup();
    renderApp("/admin/onboarding");
    const box = await screen.findByRole("textbox", { name: "Folder" });
    await user.click(box);
    await user.paste("/GVA/vm/erp");
    expect(await screen.findByText("12 VMs", {}, { timeout: 5000 })).toBeInTheDocument();
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

describe("Onboarding plans", () => {
  it("saves a plan, runs it, and shows what each run adopted and skipped", { timeout: 20_000 }, async () => {
    const user = userEvent.setup();
    renderApp("/admin/onboarding?tab=plans");
    await user.click(await screen.findByRole("button", { name: "New plan" }));
    const sheet = await screen.findByRole("dialog", { name: "New plan" });
    await user.type(within(sheet).getByRole("textbox", { name: "Name" }), "ERP servers");
    await user.click(within(sheet).getByRole("textbox", { name: /^Tag/ }));
    await user.paste("app/erp");
    expect(await within(sheet).findByText("12 VMs rvav doesn't manage match now.")).toBeInTheDocument();
    await choose(user, "Project", "Web Platform");
    await choose(user, "Environment", "prd");
    await choose(user, "Owner", "Thomas Nguyen");
    await choose(user, "Catalog item", "RHEL 9 server");
    await user.click(within(sheet).getByRole("button", { name: "Save plan" }));

    // Saved, its page opens.
    expect(await screen.findByRole("heading", { name: "ERP servers", level: 1 })).toBeInTheDocument();
    expect(screen.getByText("12 VMs it matches no deployment has")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Run now" }));
    const runs = await screen.findByRole("table", { name: "Runs" });
    expect(await within(runs).findByText("Succeeded", {}, { timeout: 8000 })).toBeInTheDocument();
    expect(within(runs).getByText("12 adopted, 0 skipped, 0 failed")).toBeInTheDocument();

    // The next run skips what the first adopted, and says why.
    await user.click(await screen.findByRole("button", { name: "Run now" }));
    expect(await within(runs).findByText("0 adopted, 12 skipped, 0 failed", {}, { timeout: 8000 })).toBeInTheDocument();
    await user.click(within(runs).getAllByRole("button", { name: /^Open the run from / })[0]!);
    const run = await screen.findByRole("dialog", { name: /^Run of / });
    const vms = await within(run).findByRole("table", { name: "VMs of the run" });
    expect(within(vms).getAllByText("Skipped")).toHaveLength(12);
    expect(within(vms).getAllByText("erp-app01 has it.")).toHaveLength(1);
  });

  it("saves a plan that adopts into one deployment it names, with inputs", { timeout: 20_000 }, async () => {
    const user = userEvent.setup();
    renderApp("/admin/onboarding?tab=plans");
    await user.click(await screen.findByRole("button", { name: "New plan" }));
    const sheet = await screen.findByRole("dialog", { name: "New plan" });
    await user.type(within(sheet).getByRole("textbox", { name: "Name" }), "ERP servers");
    await user.click(within(sheet).getByRole("textbox", { name: /^Tag/ }));
    await user.paste("app/erp");
    await choose(user, "Project", "Web Platform");
    await choose(user, "Where they go", "All into one new deployment");
    await choose(user, "Environment", "prd");
    await choose(user, "Owner", "Thomas Nguyen");
    await choose(user, "Catalog item", "RHEL 9 server");
    await user.type(within(sheet).getByRole("textbox", { name: "Deployment name" }), "erp-prd");
    await user.click(within(sheet).getByRole("button", { name: "Add input" }));
    await user.type(within(sheet).getByRole("textbox", { name: "Input" }), "role");
    await user.type(within(sheet).getByRole("textbox", { name: "Value" }), "erp");
    await user.click(within(sheet).getByRole("button", { name: "Save plan" }));

    expect(await screen.findByRole("heading", { name: "ERP servers", level: 1 })).toBeInTheDocument();
    expect(screen.getByText("Into erp-prd, one deployment in Web Platform prd")).toBeInTheDocument();
    expect(screen.getByText("role=erp")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Run now" }));
    const runs = await screen.findByRole("table", { name: "Runs" });
    expect(await within(runs).findByText("12 adopted, 0 skipped, 0 failed", {}, { timeout: 8000 })).toBeInTheDocument();
    expect(await screen.findByRole("link", { name: "Open erp-prd" })).toBeInTheDocument();
  });

  it("says what a plan lacks, field by field", { timeout: 15_000 }, async () => {
    const user = userEvent.setup();
    renderApp("/admin/onboarding?tab=plans");
    await user.click(await screen.findByRole("button", { name: "New plan" }));
    const sheet = await screen.findByRole("dialog", { name: "New plan" });
    expect(
      within(sheet).getByText("Give a criterion at least: a plan never adopts every VM rvav doesn't manage."),
    ).toBeInTheDocument();
    await user.type(within(sheet).getByRole("textbox", { name: "Name" }), "Everything");
    await choose(user, "Project", "Web Platform");
    await user.click(within(sheet).getByRole("button", { name: "Save plan" }));
    expect(
      await within(sheet).findByText(
        "Give the filter a criterion at least: without one, the plan would adopt every VM rvav doesn't manage.",
      ),
    ).toBeInTheDocument();
    expect(
      within(sheet).getByText("Choose the environment of Web Platform the deployments are in."),
    ).toBeInTheDocument();
    expect(within(sheet).getByRole("alert")).toHaveTextContent("Give the filter a criterion at least");
    expect(within(sheet).getByRole("group", { name: "Which VMs" })).toHaveAccessibleDescription(
      /^Give the filter a criterion at least/,
    );
    // The filter comes before where the VMs go: its first input takes the focus.
    await waitFor(() => expect(within(sheet).getByRole("textbox", { name: "Name or address" })).toHaveFocus());
  });

  it("ties a filter too long to its field", { timeout: 15_000 }, async () => {
    const user = userEvent.setup();
    renderApp("/admin/onboarding?tab=plans");
    await user.click(await screen.findByRole("button", { name: "New plan" }));
    const sheet = await screen.findByRole("dialog", { name: "New plan" });
    await user.type(within(sheet).getByRole("textbox", { name: "Name" }), "Long tag");
    await user.click(within(sheet).getByRole("textbox", { name: /^Tag/ }));
    await user.paste(`app/${"x".repeat(200)}`);
    await choose(user, "Project", "Web Platform");
    await choose(user, "Environment", "prd");
    await choose(user, "Owner", "Thomas Nguyen");
    await choose(user, "Catalog item", "RHEL 9 server");
    await user.click(within(sheet).getByRole("button", { name: "Save plan" }));
    const tag = within(sheet).getByRole("textbox", { name: /^Tag/ });
    await waitFor(() => expect(tag).toHaveAccessibleDescription("200 characters at most."));
    expect(tag).toHaveAttribute("aria-invalid", "true");
    await waitFor(() => expect(tag).toHaveFocus());
  });

  it("is where Deployments sends administrators to adopt existing VMs", { timeout: 15_000 }, async () => {
    const user = userEvent.setup();
    renderApp("/deployments");
    await user.click(await screen.findByRole("link", { name: "Adopt existing VMs" }));
    expect(await screen.findByRole("tab", { name: "Plans", selected: true })).toBeInTheDocument();
    expect(await screen.findByText("No plan yet")).toBeInTheDocument();
  });
});
