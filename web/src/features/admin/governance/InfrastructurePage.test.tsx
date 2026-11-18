import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { adminState } from "@/mocks/administration";
import { db } from "@/mocks/seed";
import { renderApp } from "@/test/render";

describe("Templates and networks", () => {
  it("removes a template after saying what it is offered to", async () => {
    const user = userEvent.setup();
    renderApp("/admin/infrastructure");
    const region = await screen.findByRole("region", { name: "VM templates" });
    const menu = await within(region).findByRole("button", { name: "Actions for RHEL 9.4" });
    await user.click(menu);
    await user.click(await screen.findByRole("menuitem", { name: "Remove" }));

    const confirm = await screen.findByRole("alertdialog", { name: "Remove RHEL 9.4?" });
    expect(confirm).toHaveAccessibleDescription("VM forms stop offering rhel9 in their template picker.");
    expect(confirm).toHaveTextContent("Offered to All projects.");
    await user.click(within(confirm).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(menu).toHaveFocus());
    expect(within(region).getByRole("row", { name: /RHEL 9.4/ })).toBeInTheDocument();

    await user.click(menu);
    await user.click(await screen.findByRole("menuitem", { name: "Remove" }));
    await user.click(within(await screen.findByRole("alertdialog")).getByRole("button", { name: "Remove template" }));
    await waitFor(() => expect(within(region).queryByRole("row", { name: /RHEL 9.4/ })).toBeNull());
    expect(screen.queryByRole("alertdialog")).toBeNull();
  });

  it("gives a template its own source in each vCenter", async () => {
    adminState(db).vcenters.push({
      name: "vc02.corp.example",
      url: "https://vc02.corp.example/sdk",
      username: "svc-rvav@vsphere.local",
      insecure: false,
      password_set: true,
      primary: false,
    });
    const user = userEvent.setup();
    renderApp("/admin/infrastructure");
    const region = await screen.findByRole("region", { name: "VM templates" });
    expect(await within(region).findByRole("row", { name: /RHEL 9.4/ })).toHaveTextContent("1 vCenter");
    await user.click(await within(region).findByRole("button", { name: "Actions for RHEL 9.4" }));
    await user.click(await screen.findByRole("menuitem", { name: "Change" }));

    const sheet = await screen.findByRole("dialog", { name: "Change RHEL 9.4" });
    const first = within(sheet).getByRole("group", { name: "Source 1" });
    expect(within(first).getByRole("combobox", { name: "vCenter" })).toHaveTextContent("vc01.corp.example");
    expect(within(first).getByRole("textbox", { name: "Template" })).toHaveValue("tpl-rhel9.4-2026.10");
    await user.click(within(sheet).getByRole("button", { name: "Add a vCenter" }));
    const second = within(sheet).getByRole("group", { name: "Source 2" });
    expect(within(second).getByRole("combobox", { name: "vCenter" })).toHaveTextContent("vc02.corp.example");
    // Each vCenter is named once: none is left to add.
    expect(within(sheet).queryByRole("button", { name: "Add a vCenter" })).toBeNull();

    await user.click(within(sheet).getByRole("button", { name: "Save template" }));
    expect(await within(second).findByText("Name the template in this vCenter.")).toBeInTheDocument();
    expect(within(second).getByRole("textbox", { name: "Template" })).toHaveAttribute("aria-invalid", "true");
    await user.type(within(second).getByRole("textbox", { name: "Template" }), "tpl-rhel9.4-zrh");
    await user.click(within(sheet).getByRole("button", { name: "Save template" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    const row = await within(region).findByRole("row", { name: /RHEL 9.4/ });
    await waitFor(() => expect(row).toHaveTextContent("2 vCenters"));
    expect(row).toHaveTextContent("tpl-rhel9.4-2026.10, tpl-rhel9.4-zrh");
  });

  it("gives a template its own cloud-init user data and constraint tags", async () => {
    const user = userEvent.setup();
    renderApp("/admin/infrastructure");
    const region = await screen.findByRole("region", { name: "VM templates" });
    expect(await within(region).findByRole("row", { name: /RHEL 9.4/ })).toHaveTextContent("cloud-init");
    await user.click(await within(region).findByRole("button", { name: "Actions for RHEL 9.4" }));
    await user.click(await screen.findByRole("menuitem", { name: "Change" }));

    const sheet = await screen.findByRole("dialog", { name: "Change RHEL 9.4" });
    const userData = within(sheet).getByRole("textbox", { name: "Cloud-init user data" });
    expect(userData).toHaveValue("#cloud-config\npackages: [chrony, qualys-agent]");
    await user.clear(userData);
    await user.paste("packages: [chrony]");
    const compute = within(sheet).getByRole("textbox", { name: "Compute" });
    await user.click(compute);
    await user.paste("rvav-os/rhel9, env:prod");
    await user.click(within(sheet).getByRole("button", { name: "Save template" }));
    expect(await within(sheet).findByText(/Start the user data with #cloud-config/)).toBeInTheDocument();
    expect(userData).toHaveAttribute("aria-invalid", "true");
    expect(within(sheet).getByText(/"env:prod" is not category\/tag/)).toBeInTheDocument();
    expect(compute).toHaveAttribute("aria-invalid", "true");

    await user.clear(userData);
    await user.paste("#cloud-config\npackages: [chrony]");
    await user.clear(compute);
    await user.paste("rvav-os/rhel9, !rvav-zone/pci");
    await user.click(within(sheet).getByRole("button", { name: "Save template" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    await waitFor(() =>
      expect(within(region).getByRole("row", { name: /RHEL 9.4/ })).toHaveTextContent("rvav-os/rhel9, !rvav-zone/pci"),
    );

    // A Windows template has no cloud-init.
    await user.click(await within(region).findByRole("button", { name: "Actions for Windows Server 2025" }));
    await user.click(await screen.findByRole("menuitem", { name: "Change" }));
    const win = await screen.findByRole("dialog", { name: "Change Windows Server 2025" });
    expect(within(win).queryByRole("textbox", { name: "Cloud-init user data" })).toBeNull();
  });

  it("makes a template's VMs linked clones, but not of a content library item", async () => {
    const user = userEvent.setup();
    renderApp("/admin/infrastructure");
    const region = await screen.findByRole("region", { name: "VM templates" });
    await user.click(await within(region).findByRole("button", { name: "Actions for RHEL 9.4" }));
    await user.click(await screen.findByRole("menuitem", { name: "Change" }));

    const sheet = await screen.findByRole("dialog", { name: "Change RHEL 9.4" });
    const linked = within(sheet).getByRole("checkbox", { name: "Linked clones" });
    expect(linked).toHaveAccessibleDescription(/current snapshot/);
    expect(linked).not.toBeChecked();
    await user.click(linked);
    const library = within(sheet).getByRole("textbox", { name: "Content library" });
    await user.click(library);
    await user.paste("rvav-templates");
    await user.click(within(sheet).getByRole("button", { name: "Save template" }));
    await waitFor(() => expect(linked).toHaveAccessibleDescription(/A content library item is deployed, not cloned/));

    await user.clear(library);
    await user.click(within(sheet).getByRole("button", { name: "Save template" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    await waitFor(() =>
      expect(within(region).getByRole("row", { name: /RHEL 9.4/ })).toHaveTextContent("linked clones"),
    );
  });

  it("marks a Windows template as using cloudbase-init, which takes user data", { timeout: 15_000 }, async () => {
    const user = userEvent.setup();
    renderApp("/admin/infrastructure");
    const region = await screen.findByRole("region", { name: "VM templates" });
    await user.click(await within(region).findByRole("button", { name: "Actions for Windows Server 2025" }));
    await user.click(await screen.findByRole("menuitem", { name: "Change" }));

    const sheet = await screen.findByRole("dialog", { name: "Change Windows Server 2025" });
    const cbi = within(sheet).getByRole("checkbox", { name: "Uses cloudbase-init" });
    expect(cbi).toHaveAccessibleDescription(/VMware GuestInfo service/);
    expect(within(sheet).queryByRole("textbox", { name: "cloudbase-init user data" })).toBeNull();
    await user.click(cbi);
    await user.click(within(sheet).getByRole("textbox", { name: "Customization spec" }));
    await user.paste("win-base");
    await user.click(within(sheet).getByRole("button", { name: "Save template" }));
    await waitFor(() => expect(cbi).toHaveAccessibleDescription(/doesn't use a customization specification/));

    await user.clear(within(sheet).getByRole("textbox", { name: "Customization spec" }));
    await user.click(within(sheet).getByRole("textbox", { name: "cloudbase-init user data" }));
    await user.paste("#ps1_sysnative\nSet-TimeZone -Id 'W. Europe Standard Time'");
    await user.click(within(sheet).getByRole("button", { name: "Save template" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    await waitFor(() =>
      expect(within(region).getByRole("row", { name: /Windows Server 2025/ })).toHaveTextContent("cloudbase-init"),
    );
  });

  it("changes a size in a side panel opened from its row", async () => {
    const user = userEvent.setup();
    renderApp("/admin/infrastructure");
    await user.click(await screen.findByRole("tab", { name: "Sizes" }));
    const region = await screen.findByRole("region", { name: "Sizes" });
    await user.click(await within(region).findByRole("button", { name: "Actions for Medium" }));
    await user.click(await screen.findByRole("menuitem", { name: "Change" }));

    const sheet = await screen.findByRole("dialog", { name: "Change Medium" });
    expect(within(sheet).getByRole("textbox", { name: "Key" })).toBeDisabled();
    const memory = within(sheet).getByRole("spinbutton", { name: "Memory (GB)" });
    await user.clear(memory);
    await user.type(memory, "12");
    await user.click(within(sheet).getByRole("button", { name: "Save size" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(await within(region).findByRole("row", { name: /Medium/ })).toHaveTextContent("4 vCPU, 12 GB, 80 GB");
  });

  it("offers what each mapping's origin allows", async () => {
    const mapping = { gateway: "10.88.0.1", dns_zone: "corp.example", dns_servers: [] };
    adminState(db).mappings.push(
      { ...mapping, network: "seg-web-prd", subnet: "10.88.0.0/24", source: "nsx" },
      { ...mapping, network: "dvpg-by-hand", subnet: "10.89.0.0/24", source: "manual" },
    );
    const user = userEvent.setup();
    renderApp("/admin/infrastructure");
    await user.click(await screen.findByRole("tab", { name: "Networks" }));
    const region = await screen.findByRole("region", { name: "Network mappings" });
    const items = async (network: string) => {
      await user.click(await within(region).findByRole("button", { name: `Actions for ${network}` }));
      const names = (await screen.findAllByRole("menuitem")).map((i) => i.textContent);
      await user.keyboard("{Escape}");
      return names;
    };

    // A segment made for a deployment goes with it (ADR-0028).
    expect(await within(region).findByRole("row", { name: /seg-web-prd/ })).toHaveTextContent("NSX segment");
    expect(within(region).queryByRole("button", { name: "Actions for seg-web-prd" })).toBeNull();
    expect(await items("dvpg-by-hand")).toEqual(["Change", "Remove"]);
    const synced = adminState(db).mappings.find((m) => m.source === "efficientip")!.network;
    expect(await items(synced)).toEqual(["Map by hand"]);
  });

  it("maps a network's IPv6 subnet by hand", async () => {
    adminState(db).mappings.push({
      network: "dvpg-by-hand",
      subnet: "10.89.0.0/24",
      gateway: "10.89.0.1",
      dns_zone: "corp.example",
      dns_servers: [],
      source: "manual",
    });
    const user = userEvent.setup();
    renderApp("/admin/infrastructure");
    await user.click(await screen.findByRole("tab", { name: "Networks" }));
    const region = await screen.findByRole("region", { name: "Network mappings" });
    expect(await within(region).findByRole("row", { name: /dvpg-app-dev-vl310/ })).toHaveTextContent(
      "2001:db8:310::/64 via 2001:db8:310::1",
    );

    await user.click(within(region).getByRole("button", { name: "Actions for dvpg-by-hand" }));
    await user.click(await screen.findByRole("menuitem", { name: "Change" }));
    const sheet = await screen.findByRole("dialog", { name: "Map dvpg-by-hand by hand" });
    await user.type(within(sheet).getByRole("textbox", { name: "IPv6 subnet" }), "2001:db8:89::1");
    await user.click(within(sheet).getByRole("button", { name: "Save mapping" }));
    expect(await within(sheet).findByText("An IPv6 subnet such as 2001:db8:310::/64.")).toBeInTheDocument();

    await user.clear(within(sheet).getByRole("textbox", { name: "IPv6 subnet" }));
    await user.type(within(sheet).getByRole("textbox", { name: "IPv6 subnet" }), "2001:db8:89::/64");
    await user.type(within(sheet).getByRole("textbox", { name: "IPv6 gateway" }), "2001:db8:89::1");
    await user.click(within(sheet).getByRole("button", { name: "Save mapping" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(within(region).getByRole("row", { name: /dvpg-by-hand/ })).toHaveTextContent(
      "10.89.0.0/24 via 10.89.0.12001:db8:89::/64 via 2001:db8:89::1",
    );
  });

  it("lists vCenter objects with their tags, by name, kind or tag", async () => {
    const user = userEvent.setup();
    renderApp("/admin/infrastructure");
    await user.click(await screen.findByRole("tab", { name: "vCenter objects" }));
    const region = await screen.findByRole("region", { name: "vCenter objects" });
    expect(await within(region).findByRole("row", { name: /^GVA-PRD-CL01 Cluster/ })).toHaveTextContent(
      "rvav-tier/gold",
    );
    expect(within(region).getByRole("row", { name: /^dvpg-web-prd-vl210/ })).toHaveTextContent("rvav-zone/dmz");
    expect(within(region).getByRole("row", { name: /^dvpg-app-dev-vl310/ })).toHaveTextContent("None");
    // Hosts, resource pools and datastore clusters too (ADR-0036).
    expect(within(region).getByRole("row", { name: /^esx-gva-prd1-03\.corp\.example Host/ })).toHaveTextContent(
      "rvav-hw/gpu",
    );
    expect(within(region).getByRole("row", { name: /^esx-gva-tst-01\.corp\.example Host/ })).toHaveTextContent(
      "in maintenance",
    );
    expect(within(region).getByRole("row", { name: /^dsc-gva-dev-01 Datastore cluster/ })).toHaveTextContent(
      "on nfs-gva-dev-sdrs-01, nfs-gva-dev-sdrs-02, Storage DRS on",
    );

    const search = within(region).getByRole("searchbox", { name: "Search by name or tag" });
    await user.type(search, "gold");
    const names = () =>
      within(region)
        .getAllByRole("row")
        .slice(1)
        .map((r) => within(r).getAllByRole("cell")[0]?.textContent);
    await waitFor(() => expect(names()).toEqual(["GVA-PRD-CL01", "vsan-gva-prd-cl01"]));

    await user.click(within(region).getByRole("combobox", { name: "Kind" }));
    await user.click(await screen.findByRole("option", { name: "Datastores" }));
    await waitFor(() => expect(names()).toEqual(["vsan-gva-prd-cl01"]));

    await user.clear(search);
    await user.click(within(region).getByRole("combobox", { name: "Kind" }));
    await user.click(await screen.findByRole("option", { name: "Resource pools" }));
    await waitFor(() => expect(names()).toEqual(["rvav", "rvav", "rvav/dev", "rvav/prd"]));
    expect(within(region).getByRole("row", { name: /^rvav\/dev/ })).toHaveTextContent(
      "In GVA-DEV-CL01, limited to 512 GB memory",
    );
  });
});
