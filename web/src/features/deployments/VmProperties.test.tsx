import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { TooltipProvider } from "@/components/ui/tooltip";
import type { VmResource } from "@/lib/api/types";
import { db } from "@/mocks/seed";
import { renderApp } from "@/test/render";
import { VmPropertiesSheet } from "./VmProperties";

const vm = (over: Partial<VmResource["vm"]> = {}): VmResource => ({
  id: "res_1",
  type: "vsphere.vm",
  name: "web-dev-api004",
  sync_state: "in_sync",
  vm: {
    moref: "vm-48217",
    instance_uuid: "5029abcd-1c2d-4e5f-8a9b-0c0000048217",
    vcenter: "vc01.corp.example",
    power_state: "on",
    guest_state: "running",
    guest_os: "Red Hat Enterprise Linux 9 (64-bit)",
    cpu_count: 4,
    memory_mb: 16384,
    disks: [
      { label: "Hard disk 1", capacity_gb: 80, datastore: "vsan-gva-dev-cl01", provisioning: "thin" },
      { label: "Hard disk 2", capacity_gb: 200, datastore: "nfs-gva-dev-01", provisioning: "thick_eager_zeroed" },
    ],
    nics: [
      {
        label: "Network adapter 1",
        network: "dvpg-app-dev-vl310",
        mac: "00:50:56:a1:11:3c",
        ip_addresses: ["10.31.10.84", "fd00:31:10::84"],
        dhcp: false,
      },
      { label: "Network adapter 2", network: "dvpg-db-dev-vl320", ip_addresses: ["10.32.10.9"], dhcp: true },
      { label: "Network adapter 3", network: "dvpg-bk-dev-vl330" },
    ],
    snapshots: [{ name: "Before patching", description: "July patches", created_at: "2026-11-17T08:00:00Z" }],
    iso: "[nfs-iso] rhel-9.4.iso",
    cluster: "GVA-DEV-CL01",
    host: "esx-gva-dev-02.corp.example",
    resource_pool: "rvav/dev",
    folder: "/GVA/vm/rvav/web/dev",
    template: "tpl-rhel9.4-2026.10",
    custom_properties: { costCenter: "CC-1001", auth: "••••" },
    vsphere_url:
      "https://vc01.corp.example/ui/app/vm;nav=h/urn:vmomi:VirtualMachine:vm-48217:6f1c2a5e-0b8d-4c1e-9d2f-3a4b5c6d7e8f/summary",
    ...over,
  },
});

function sheet(resource: VmResource) {
  const onClose = vi.fn();
  render(
    <TooltipProvider>
      <VmPropertiesSheet resource={resource} onClose={onClose} />
    </TooltipProvider>,
  );
  return { dialog: screen.getByRole("dialog", { name: resource.name }), onClose };
}

const value = (box: HTMLElement, term: string) => within(box).getByText(term, { selector: "dt" }).nextElementSibling;

describe("a VM's properties sheet", () => {
  it("shows what vCenter reports, where the VM runs and what it was built with", async () => {
    const user = userEvent.setup();
    const { dialog, onClose } = sheet(vm());
    expect(value(dialog, "Guest OS")).toHaveTextContent("Red Hat Enterprise Linux 9 (64-bit)");
    expect(value(dialog, "Power")).toHaveTextContent("On");
    expect(value(dialog, "VMware Tools")).toHaveTextContent("Running");
    expect(value(dialog, "ISO")).toHaveTextContent("[nfs-iso] rhel-9.4.iso");

    const nics = within(dialog).getByRole("region", { name: "Network adapters" });
    const [first, second, third] = within(nics).getAllByRole("listitem");
    expect(value(first!, "IPv4")).toHaveTextContent("10.31.10.84");
    expect(value(first!, "IPv6")).toHaveTextContent("fd00:31:10::84");
    expect(value(first!, "MAC address")).toHaveTextContent("00:50:56:a1:11:3c");
    expect(value(first!, "Address from")).toHaveTextContent("Fixed");
    expect(value(second!, "Address from")).toHaveTextContent("DHCP");
    expect(value(second!, "IPv6")).toHaveTextContent("None reported");
    expect(value(third!, "Address from")).toHaveTextContent("Not known");

    const disks = within(dialog).getByRole("region", { name: "Disks" });
    expect(disks).toHaveTextContent("Hard disk 1 80 GB");
    expect(disks).toHaveTextContent("vsan-gva-dev-cl01 · Thin");
    expect(disks).toHaveTextContent("nfs-gva-dev-01 · Thick, eagerly zeroed");
    expect(within(dialog).getByRole("region", { name: "Snapshots" })).toHaveTextContent("Before patching");

    const placement = within(dialog).getByRole("region", { name: "Placement" });
    expect(value(placement, "Resource pool")).toHaveTextContent("rvav/dev");
    expect(value(placement, "Host")).toHaveTextContent("esx-gva-dev-02.corp.example");
    expect(value(placement, "Datastores")).toHaveTextContent("vsan-gva-dev-cl01, nfs-gva-dev-01");

    const props = within(dialog).getByRole("region", { name: "Custom properties" });
    expect(value(props, "costCenter")).toHaveTextContent("CC-1001");
    expect(value(props, "auth")).toHaveTextContent("••••");
    expect(props).toHaveTextContent("reads a secret input");

    const link = within(dialog).getByRole("link", { name: /Open web-dev-api004 in the vSphere Client/ });
    expect(link).toHaveAttribute("href", vm().vm.vsphere_url);
    expect(link).toHaveAttribute("target", "_blank");

    await user.keyboard("{Escape}");
    expect(onClose).toHaveBeenCalled();
  });

  it("says why there is no vSphere Client link, and what it doesn't know", () => {
    const { dialog } = sheet(
      vm({
        vsphere_url: undefined,
        custom_properties: undefined,
        resource_pool: undefined,
        snapshots: [],
        iso: undefined,
        disks: [{ label: "Hard disk 1", capacity_gb: 80, datastore: "vsan-gva-dev-cl01" }],
      }),
    );
    expect(within(dialog).queryByRole("link")).not.toBeInTheDocument();
    expect(dialog).toHaveTextContent("No vSphere Client link yet: it needs the instance UUID of vc01.corp.example");
    expect(value(dialog, "ISO")).toHaveTextContent("None mounted");
    expect(within(dialog).getByRole("region", { name: "Snapshots" })).toHaveTextContent("No snapshots.");
    const placement = within(dialog).getByRole("region", { name: "Placement" });
    expect(value(placement, "Resource pool")).toHaveTextContent("The cluster's root pool");
    expect(value(placement, "Datastore")).toHaveTextContent("vsan-gva-dev-cl01");
    expect(within(dialog).getByRole("region", { name: "Custom properties" })).toHaveTextContent(
      "None: it was built without custom properties",
    );
  });

  it("opens from a VM's name in the Overview list, and gives the focus back", { timeout: 15_000 }, async () => {
    const user = userEvent.setup();
    const dep = db.deployments.find((d) => d.name === "web-prd-front")!;
    renderApp(`/deployments/${dep.id}`);
    const name = await screen.findByRole("button", { name: "web-prd-front002 properties" }, { timeout: 5000 });
    name.focus();
    await user.keyboard("{Enter}");
    const dialog = await screen.findByRole("dialog", { name: "web-prd-front002" });
    expect(value(dialog, "Guest OS")).toHaveTextContent("Red Hat Enterprise Linux 9");
    expect(within(dialog).getByRole("link", { name: /in the vSphere Client/ })).toHaveAttribute(
      "href",
      expect.stringContaining("/ui/app/vm;nav=h/urn:vmomi:VirtualMachine:vm-"),
    );
    await user.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    await waitFor(() => expect(name).toHaveFocus());
  });
});
