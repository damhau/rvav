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
