// A toast says what the sheet's button did (docs/ux.md): created after
// Create, added after Add, saved after Save.
import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { i18n } from "@/lib/i18n";
import { adminState } from "@/mocks/administration";
import { db } from "@/mocks/seed";
import { renderApp } from "@/test/render";

interface SizeSheet {
  region: string;
  new: string;
  key: string;
  label: string;
  huge: string;
  create: string;
}

async function createSize(user: ReturnType<typeof userEvent.setup>, names: SizeSheet) {
  renderApp("/admin/infrastructure?tab=sizes");
  const region = await screen.findByRole("region", { name: names.region });
  await user.click(await within(region).findByRole("button", { name: names.new }));
  const sheet = await screen.findByRole("dialog", { name: names.new });
  await user.type(within(sheet).getByRole("textbox", { name: names.key }), "huge");
  await user.type(within(sheet).getByRole("textbox", { name: names.label }), names.huge);
  await user.click(within(sheet).getByRole("button", { name: names.create }));
}

describe("a toast after a sheet's button", () => {
  it("says created after Create", { timeout: 15_000 }, async () => {
    const user = userEvent.setup();
    await createSize(user, {
      region: "Sizes",
      new: "New size",
      key: "Key",
      label: "Label",
      huge: "Huge",
      create: "Create size",
    });
    expect(await screen.findByText("Huge created")).toBeInTheDocument();
  });

  it("says created as the noun agrees, in French", { timeout: 15_000 }, async () => {
    db.me.locale = "fr";
    await i18n.changeLanguage("fr");
    const user = userEvent.setup();
    await createSize(user, {
      region: "Tailles",
      new: "Nouvelle taille",
      key: "Clé",
      label: "Libellé",
      huge: "Énorme",
      create: "Créer la taille",
    });
    expect(await screen.findByText("Énorme créée")).toBeInTheDocument();
  });

  it("says added after Add", { timeout: 15_000 }, async () => {
    const user = userEvent.setup();
    renderApp("/admin/integrations/vcenter");
    const region = await screen.findByRole("region", { name: "vCenters" });
    await within(region).findByRole("row", { name: /vc01\.corp\.example/ });
    await user.click(within(region).getByRole("button", { name: "Add a vCenter" }));
    const sheet = await screen.findByRole("dialog", { name: "Add a vCenter" });
    await user.type(within(sheet).getByRole("textbox", { name: "Name" }), "vc02.corp.example");
    await user.type(within(sheet).getByRole("textbox", { name: "SDK address" }), "https://vc02.corp.example/sdk");
    await user.type(within(sheet).getByRole("textbox", { name: "Account" }), "svc-rvav@vsphere.local");
    await user.type(within(sheet).getByLabelText("Password"), "s3cret");
    await user.click(within(sheet).getByRole("button", { name: "Add vCenter" }));
    expect(await screen.findByText("vc02.corp.example added")).toBeInTheDocument();
  });

  it("says saved after Save", { timeout: 15_000 }, async () => {
    adminState(db).mappings.push({
      network: "dvpg-by-hand",
      subnet: "10.89.0.0/24",
      gateway: "10.89.0.1",
      dns_zone: "corp.example",
      dns_servers: [],
      source: "manual",
    });
    const user = userEvent.setup();
    renderApp("/admin/infrastructure?tab=networks");
    const region = await screen.findByRole("region", { name: "Network mappings" });
    await user.click(await within(region).findByRole("button", { name: "Actions for dvpg-by-hand" }));
    await user.click(await screen.findByRole("menuitem", { name: "Change" }));
    const sheet = await screen.findByRole("dialog", { name: "Map dvpg-by-hand by hand" });
    await user.click(within(sheet).getByRole("button", { name: "Save mapping" }));
    expect(await screen.findByText("dvpg-by-hand saved")).toBeInTheDocument();
  });
});
