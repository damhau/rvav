import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { rangeState } from "@/mocks/ipRanges";
import { db } from "@/mocks/seed";
import { renderApp } from "@/test/render";

async function rangesRegion() {
  renderApp("/admin/infrastructure?tab=ip-ranges");
  return screen.findByRole("region", { name: "IP ranges" });
}

// rvav's own IP ranges (ADR-0063), on the mock backend.
describe("IP ranges", () => {
  it("lists a range with its usage, and who holds each address", async () => {
    const user = userEvent.setup();
    const region = await rangesRegion();
    const row = await within(region).findByRole("row", { name: /Test lab/ });
    expect(row).toHaveTextContent("dvpg-app-tst-vl410vc01.corp.example");
    expect(row).toHaveTextContent("10.41.10.200 – 10.41.10.239");
    expect(row).toHaveTextContent("10.41.10.0/24 via 10.41.10.1, 2 excluded");
    // lab-tst-build01 holds .230, which the range didn't give it: skipped.
    expect(row).toHaveTextContent("29 free5 / 34");
    expect(row).toHaveTextContent("lab.corp.example");

    await user.click(within(region).getByRole("button", { name: "Actions for Test lab" }));
    await user.click(await screen.findByRole("menuitem", { name: "Addresses given" }));
    const sheet = await screen.findByRole("dialog", { name: "Addresses of Test lab" });
    const backup = await within(sheet).findByRole("row", { name: /10\.41\.10\.202/ });
    expect(backup).toHaveTextContent("web-tst-lab002adapter backup");
    expect(backup).toHaveTextContent("web-tst-lab");
    expect(within(sheet).getByRole("row", { name: /10\.41\.10\.205/ })).toHaveTextContent("Gone");
    expect(sheet).toHaveTextContent("rvav skips 1 address of this range");
  });

  it("refuses a domain with its final dot, a DNS server it would give, and an address a VM holds", async () => {
    rangeState(db).inventory.push({ vm: "db-prd-old01", address: "10.22.30.120" });
    const user = userEvent.setup();
    const region = await rangesRegion();
    await user.click(await within(region).findByRole("button", { name: "New IP range" }));
    const sheet = await screen.findByRole("dialog", { name: "New IP range" });
    const box = (name: string) => within(sheet).getByRole("textbox", { name });
    await user.type(box("Name"), "Prod lab");
    await user.click(within(sheet).getByRole("combobox", { name: "Network" }));
    await user.click(await screen.findByRole("option", { name: "dvpg-db-prd-vl230" }));
    await user.type(box("Subnet"), "10.22.30.0/25");
    await user.type(box("Gateway"), "10.22.30.1");
    await user.type(box("First address"), "10.22.30.100");
    await user.type(box("Last address"), "10.22.30.126");
    await user.type(box("DNS servers"), "10.22.30.110");
    await user.type(box("DNS domain"), "prd.corp.example.");
    const save = () => user.click(within(sheet).getByRole("button", { name: "Save range" }));
    await save();
    expect(await within(sheet).findByText("Without the final dot: prd.corp.example.")).toBeInTheDocument();

    await user.type(box("DNS domain"), "{Backspace}");
    await save();
    expect(
      await within(sheet).findByText("The range would give 10.22.30.110, a DNS server, to a VM: exclude it."),
    ).toBeInTheDocument();

    await user.clear(box("DNS servers"));
    await user.type(box("DNS servers"), "10.22.30.53");
    await save();
    expect(
      await within(sheet).findByText(
        "A VM holds an address the range would give: 10.22.30.120 (db-prd-old01, as vCenter reports). Exclude it, or keep the range apart from it.",
      ),
    ).toBeInTheDocument();

    await user.type(box("Excluded addresses"), "10.22.30.120");
    await save();
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(await within(region).findByRole("row", { name: /Prod lab/ })).toHaveTextContent("26 free0 / 26");
  }, 20_000);

  it("adds a range after the refusals its fields show, and the network shows its source", async () => {
    const user = userEvent.setup();
    const region = await rangesRegion();
    await user.click(await within(region).findByRole("button", { name: "New IP range" }));
    const sheet = await screen.findByRole("dialog", { name: "New IP range" });
    expect(sheet).toHaveAccessibleDescription(/rvav writes no DNS records for them/);
    await user.type(within(sheet).getByRole("textbox", { name: "Name" }), "Prod lab");
    await user.click(within(sheet).getByRole("combobox", { name: "Network" }));
    await user.click(await screen.findByRole("option", { name: "dvpg-db-prd-vl230" }));
    await user.type(within(sheet).getByRole("textbox", { name: "Subnet" }), "10.22.30.0/25");
    await user.type(within(sheet).getByRole("textbox", { name: "Gateway" }), "10.22.30.1");
    await user.type(within(sheet).getByRole("textbox", { name: "First address" }), "10.22.30.200");
    await user.type(within(sheet).getByRole("textbox", { name: "Last address" }), "10.22.30.120");
    await user.type(within(sheet).getByRole("textbox", { name: "Excluded addresses" }), "10.22.30.100-10.22.30.90");
    await user.click(within(sheet).getByRole("button", { name: "Save range" }));
    expect(await within(sheet).findByText("The start must be in the subnet 10.22.30.0/25.")).toBeInTheDocument();
    expect(within(sheet).getByRole("textbox", { name: "Excluded addresses" })).toHaveAccessibleDescription(
      "An IPv4 address, or two joined by a dash such as 10.51.0.50-10.51.0.59.",
    );

    await user.clear(within(sheet).getByRole("textbox", { name: "First address" }));
    await user.type(within(sheet).getByRole("textbox", { name: "First address" }), "10.22.30.100");
    await user.clear(within(sheet).getByRole("textbox", { name: "Excluded addresses" }));
    await user.type(within(sheet).getByRole("textbox", { name: "Excluded addresses" }), "10.22.30.110");
    await user.type(within(sheet).getByRole("textbox", { name: "DNS servers" }), "10.22.30.53, 10.22.30.54");
    await user.click(within(sheet).getByRole("button", { name: "Save range" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    const row = await within(region).findByRole("row", { name: /Prod lab/ });
    expect(row).toHaveTextContent("20 free0 / 20");
    expect(row).toHaveTextContent("The blueprint's domain");
    expect(rangeState(db).ranges.at(-1)?.dns_servers).toEqual(["10.22.30.53", "10.22.30.54"]);

    await user.click(screen.getByRole("tab", { name: "Networks" }));
    const networks = await screen.findByRole("region", { name: "Network mappings" });
    const ranged = await within(networks).findByRole("row", { name: /dvpg-db-prd-vl230.*rvav IP ranges/ });
    expect(ranged).toHaveTextContent("dvpg-db-prd-vl230vc01.corp.example10.22.30.0/25");
    expect(ranged).toHaveTextContent("No DNS records");
    await user.click(within(ranged).getByRole("link", { name: "rvav IP ranges" }));
    expect(await screen.findByRole("region", { name: "IP ranges" })).toBeInTheDocument();
  }, 20_000);

  it("keeps a range VMs hold addresses from, and removes one they don't", async () => {
    const user = userEvent.setup();
    const region = await rangesRegion();
    await user.click(await within(region).findByRole("button", { name: "Actions for Test lab" }));
    await user.click(await screen.findByRole("menuitem", { name: "Change" }));
    const sheet = await screen.findByRole("dialog", { name: "Change Test lab" });
    expect(within(sheet).getByRole("textbox", { name: "Subnet" })).toBeDisabled();
    expect(sheet).toHaveTextContent("VMs hold 4 addresses from this range");
    await user.click(within(sheet).getByRole("button", { name: "Cancel" }));

    await user.click(within(region).getByRole("button", { name: "Actions for Test lab" }));
    await user.click(await screen.findByRole("menuitem", { name: "Remove" }));
    const confirm = await screen.findByRole("alertdialog", { name: "Remove Test lab?" });
    expect(confirm).toHaveTextContent("VMs hold 4 addresses from it");
    await user.click(within(confirm).getByRole("button", { name: "Remove range" }));
    expect(await screen.findByText(/VMs hold addresses from Test lab/)).toBeInTheDocument();
    expect(rangeState(db).ranges.map((r) => r.name)).toEqual(["Test lab"]);

    rangeState(db).allocations.length = 0;
    await user.click(within(confirm).getByRole("button", { name: "Remove range" }));
    await waitFor(() => expect(within(region).queryByRole("row", { name: /Test lab/ })).toBeNull());
    expect(await within(region).findByText("No IP ranges")).toBeInTheDocument();
  });

  it("reconciles: gives back an address no deployment holds, and lists VMs on a range's address", async () => {
    const user = userEvent.setup();
    renderApp("/admin/infrastructure?tab=ip-ranges");
    const reconcile = await screen.findByRole("region", { name: "Reconcile the IP ranges" });
    await user.click(within(reconcile).getByRole("button", { name: "Run the check" }));
    const orphans = await within(reconcile).findByRole("region", { name: "Addresses of VMs rvav no longer has" });
    expect(within(orphans).getByRole("row", { name: /10\.41\.10\.205/ })).toHaveTextContent("web-tst-lab009");
    const strays = within(reconcile).getByRole("region", { name: "VMs on an address their range didn't give" });
    expect(within(strays).getByRole("row", { name: /lab-tst-build01/ })).toHaveTextContent("No VM");
    expect(within(strays).getByRole("row", { name: /web-tst-old01/ })).toHaveTextContent("web-tst-lab002");

    // vCenter still reports the VM on it: not given back yet.
    rangeState(db).inventory.push({ vm: "web-tst-lab009", address: "10.41.10.205" });
    await user.click(within(orphans).getByRole("button", { name: "Actions for 10.41.10.205" }));
    await user.click(await screen.findByRole("menuitem", { name: "Give back" }));
    const confirm = await screen.findByRole("alertdialog", { name: "Give back 10.41.10.205?" });
    await user.click(within(confirm).getByRole("button", { name: "Give back address" }));
    expect(
      await screen.findByText(
        "vCenter vc01.corp.example still reports VM web-tst-lab009 on 10.41.10.205: delete the VM, or change its address, first.",
      ),
    ).toBeInTheDocument();
    rangeState(db).inventory.pop();
    await user.click(within(confirm).getByRole("button", { name: "Give back address" }));
    await waitFor(() => expect(within(reconcile).queryByRole("region", { name: /no longer has/ })).toBeNull());
    expect(rangeState(db).allocations.map((a) => a.address)).not.toContain("10.41.10.205");

    // Another VM holds .201: one of the two moves, nothing to exclude.
    expect(within(strays).queryByRole("button", { name: "Actions for web-tst-old01" })).toBeNull();
    await user.click(within(strays).getByRole("button", { name: "Actions for lab-tst-build01" }));
    await user.click(await screen.findByRole("menuitem", { name: "Exclude from Test lab" }));
    await waitFor(() => expect(within(strays).queryByRole("row", { name: /lab-tst-build01/ })).toBeNull());
    expect(rangeState(db).ranges[0]?.excluded).toEqual(["10.41.10.210", "10.41.10.220-10.41.10.224", "10.41.10.230"]);
  }, 15_000);

  it("is read only for those who may only see the infrastructure", async () => {
    db.me.permissions = db.me.permissions.filter((p) => p !== "infrastructure.manage");
    const user = userEvent.setup();
    const region = await rangesRegion();
    await user.click(await within(region).findByRole("button", { name: "Actions for Test lab" }));
    expect((await screen.findAllByRole("menuitem")).map((i) => i.textContent)).toEqual(["Addresses given"]);
    expect(within(region).queryByRole("button", { name: "New IP range" })).toBeNull();
    expect(screen.queryByRole("region", { name: "Reconcile the IP ranges" })).toBeNull();
  });
});
