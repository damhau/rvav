import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { renderApp } from "@/test/render";

// The settings moved into Administration with U1.4 (ADR-0030), against the
// mock API: the console in vCenter's tab, vRA, the allowed hosts and the
// sync intervals, each read on next use.

async function tab(name: string) {
  const user = userEvent.setup();
  renderApp("/admin/integrations");
  await user.click(await screen.findByRole("tab", { name }));
  return user;
}

describe("integration settings", () => {
  it("has the console's panel in vCenter's tab, not a tab of its own", async () => {
    const user = await tab("vCenter");
    expect(screen.queryByRole("tab", { name: "Console" })).not.toBeInTheDocument();
    const vcenter = await screen.findByRole("region", { name: "vCenter connection" });
    expect(within(vcenter).getByRole("textbox", { name: "Sync interval" })).toHaveValue("2m");
    const console = screen.getByRole("region", { name: "Console" });
    expect(within(console).getByRole("textbox", { name: "Console account" })).toHaveValue(
      "svc-rvav-console@vsphere.local",
    );
    expect(within(console).getByText(/only goes to the vCenter it was given for/)).toBeInTheDocument();
    const sdk = within(console).getByRole("textbox", { name: "WebMKS SDK scripts" });
    await user.type(sdk, "/wmks/wmks.min.js");
    await user.click(within(console).getByRole("button", { name: "Test console" }));
    expect(await within(console).findByText("1 WebMKS SDK scripts, loaded by the browser.")).toBeInTheDocument();
    await user.click(within(console).getByRole("button", { name: "Save settings" }));
    expect(await screen.findByText("They apply from the next console opened.")).toBeInTheDocument();
    expect(screen.queryByRole("region", { name: "Applying the settings" })).not.toBeInTheDocument();
  });

  it("refuses a sync interval out of range", async () => {
    const user = await tab("AWX");
    const awx = await screen.findByRole("region", { name: "AWX connection" });
    const every = within(awx).getByRole("textbox", { name: "Sync interval" });
    await user.clear(every);
    await user.type(every, "10s");
    await user.click(within(awx).getByRole("button", { name: "Save settings" }));
    expect(await within(awx).findByText("A duration from 30s to 24h, for example 5m or 1h30m.")).toBeInTheDocument();
    expect(every).toHaveAttribute("aria-invalid", "true");
  });

  it("lists the allowed hosts, with no secret, and checks them", async () => {
    const user = await tab("Allowed hosts");
    const hosts = await screen.findByRole("region", { name: "Allowed hosts" });
    expect(within(hosts).getByRole("textbox", { name: "REST value sources and calls" })).toHaveValue(
      "cmdb.corp.example",
    );
    expect(within(hosts).queryByLabelText(/password|token|secret/i)).not.toBeInTheDocument();
    const hooks = within(hosts).getByRole("textbox", { name: "Webhooks" });
    await user.clear(hooks);
    await user.type(hooks, "hooks.corp.example, *.svc.corp.example");
    await user.click(within(hosts).getByRole("button", { name: "Check" }));
    expect(
      await within(hosts).findByText(
        "REST value sources and calls may reach cmdb.corp.example; webhooks hooks.corp.example, *.svc.corp.example.",
      ),
    ).toBeInTheDocument();
    await user.click(within(hosts).getByRole("button", { name: "Save settings" }));
    expect(await screen.findByText("They apply from the next call to a host.")).toBeInTheDocument();
  });

  it("makes the inventory token and puts it in AWX", async () => {
    const user = await tab("AWX");
    const panel = await screen.findByRole("region", { name: "Inventory token" });
    expect(within(panel).getByText(/rvav hasn't made one/)).toBeInTheDocument();
    const address = within(panel).getByRole("textbox", { name: "rvav's address, as AWX reaches it" });
    await user.type(address, "ftp://rvav.corp.example");
    await user.click(within(panel).getByRole("button", { name: "Make a token and put it in AWX" }));
    expect(await within(panel).findByText(/such as https:\/\/rvav.corp.example/)).toBeInTheDocument();
    await user.clear(address);
    await user.click(within(panel).getByRole("button", { name: "Make a token and put it in AWX" }));
    const made = await within(panel).findByRole("region", { name: "Inventory token made" });
    expect(made).toHaveTextContent(
      "In AWX's credential rvav API, now set on the inventory source, reading rvav at https://rvav.corp.example.",
    );
    expect(made).toHaveTextContent("AWX is syncing the inventory source with it");
    expect(await within(panel).findByText(/not read yet/)).toBeInTheDocument();
    // Again, at the address in the cluster: the one before is revoked.
    await user.type(address, "http://rvav-api.rvav.svc:8080");
    await user.click(within(panel).getByRole("button", { name: "Make a new token and put it in AWX" }));
    expect(await within(panel).findByText("The token made before is revoked.")).toBeInTheDocument();
    expect(within(panel).getByRole("region", { name: "Inventory token made" })).toHaveTextContent(
      "In AWX's credential rvav API, reading rvav at http://rvav-api.rvav.svc:8080.",
    );
  });

  it("takes vRA's password or refresh token in one field", async () => {
    const user = await tab("vRA");
    const vra = await screen.findByRole("region", { name: "vRA connection" });
    await user.type(within(vra).getByRole("textbox", { name: "URL" }), "https://vra.corp.example");
    await user.type(within(vra).getByLabelText("Password or refresh token"), "a-refresh-token");
    await user.click(within(vra).getByRole("button", { name: "Save settings" }));
    expect(await screen.findByText("They apply from the next read of vRA.")).toBeInTheDocument();
  });
});
