import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { adminState } from "@/mocks/administration";
import { db } from "@/mocks/seed";
import { renderApp } from "@/test/render";

describe("Templates, sizes and networks", () => {
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
});
