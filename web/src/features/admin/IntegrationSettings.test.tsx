import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { adminState } from "@/mocks/administration";
import { db } from "@/mocks/seed";
import { renderApp } from "@/test/render";

// The settings moved into Administration with U1.4 (ADR-0030), against the
// mock API: the console on vCenter's page, vRA, the allowed hosts and the
// sync intervals, each read on next use.

function open(kind: string) {
  renderApp(`/admin/integrations/${kind}`);
  return userEvent.setup();
}

describe("integration settings", () => {
  it("says at the field that an environment variable set it since the page read it, then locks it", async () => {
    const user = open("nsx");
    const nsx = await screen.findByRole("region", { name: "NSX connection" });
    const url = within(nsx).getByRole("textbox", { name: "URL" });
    await user.clear(url);
    await user.type(url, "https://nsx-lab.corp.example");
    adminState(db).environment = { ...adminState(db).environment, nsx: { url: "RVAV_NSX_URL" } };
    await user.click(within(nsx).getByRole("button", { name: "Save settings" }));
    const refused = "Set by RVAV_NSX_URL, an environment variable of rvav's processes: change it there.";
    await waitFor(() => expect(url).toHaveAccessibleDescription(refused));
    await waitFor(() => expect(url).toBeDisabled());
    expect(url).toHaveValue("https://nsx.corp.example");
    expect(adminState(db).integrations.nsx!.settings.url).toBe("https://nsx.corp.example");
  });

  it("saves nothing when nothing changed, and says so", async () => {
    const user = open("awx");
    const awx = await screen.findByRole("region", { name: "AWX connection" });
    const revision = adminState(db).revision;
    await user.click(within(awx).getByRole("button", { name: "Save settings" }));
    expect(await screen.findByText("AWX settings unchanged")).toBeInTheDocument();
    expect(screen.getByText("They're the ones rvav runs with: nothing restarts.")).toBeInTheDocument();
    expect(adminState(db).revision).toBe(revision);
  });

  it("has the console's panel on vCenter's page", async () => {
    const user = open("vcenter");
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
    const user = open("awx");
    const awx = await screen.findByRole("region", { name: "AWX connection" });
    const every = within(awx).getByRole("textbox", { name: "Sync interval" });
    await user.clear(every);
    await user.type(every, "10s");
    await user.click(within(awx).getByRole("button", { name: "Save settings" }));
    expect(await within(awx).findByText("A duration from 30s to 24h, for example 5m or 1h30m.")).toBeInTheDocument();
    expect(every).toHaveAttribute("aria-invalid", "true");
  });

  it("lists the allowed hosts, with no secret, and checks them", async () => {
    const user = open("hosts");
    const hosts = await screen.findByRole("region", { name: "Hosts rvav may call" });
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
    const user = open("awx");
    const panel = await screen.findByRole("region", { name: "Inventory token" });
    expect(within(panel).getByText(/rvav hasn't made one/)).toBeInTheDocument();
    const address = within(panel).getByRole("textbox", { name: "rvav's address, as AWX reaches it" });
    await user.type(address, "ftp://rvav.corp.example");
    await user.click(within(panel).getByRole("button", { name: "Create a token and put it in AWX" }));
    expect(await within(panel).findByText(/such as https:\/\/rvav.corp.example/)).toBeInTheDocument();
    await user.clear(address);
    await user.click(within(panel).getByRole("button", { name: "Create a token and put it in AWX" }));
    const made = await within(panel).findByRole("region", { name: "Inventory token made" });
    expect(made).toHaveTextContent(
      "In AWX's credential rvav API, now set on the inventory source, reading rvav at https://rvav.corp.example.",
    );
    expect(made).toHaveTextContent("AWX is syncing the inventory source with it");
    expect(await within(panel).findByText(/not read yet/)).toBeInTheDocument();
    // Again, at the address in the cluster: the one before is revoked.
    await user.type(address, "http://rvav-api.rvav.svc:8080");
    await user.click(within(panel).getByRole("button", { name: "Create a new token and put it in AWX" }));
    expect(await within(panel).findByText("The token made before is revoked.")).toBeInTheDocument();
    expect(within(panel).getByRole("region", { name: "Inventory token made" })).toHaveTextContent(
      "In AWX's credential rvav API, reading rvav at http://rvav-api.rvav.svc:8080.",
    );
  });

  it("takes vRA's password or refresh token in one field", async () => {
    const user = open("vra");
    const vra = await screen.findByRole("region", { name: "vRA connection" });
    await user.type(within(vra).getByRole("textbox", { name: "URL" }), "https://vra.corp.example");
    await user.type(within(vra).getByLabelText("Password or refresh token"), "a-refresh-token");
    await user.click(within(vra).getByRole("button", { name: "Save settings" }));
    expect(await screen.findByText("They apply from the next read of vRA.")).toBeInTheDocument();
  });

  it("binds to Active Directory over TLS, warning of plain LDAP", async () => {
    const user = open("ad");
    const ad = await screen.findByRole("region", { name: "Active Directory connection" });
    expect(within(ad).getByRole("textbox", { name: "Base DN" })).toHaveValue("DC=corp,DC=example");
    await user.click(within(ad).getByRole("button", { name: "Test connection" }));
    expect(
      await within(ad).findByText(
        "Bound to ldaps://dc01.corp.example as svc-rvav@corp.example and read DC=corp,DC=example.",
      ),
    ).toBeInTheDocument();
    expect(within(ad).getByText(/dc01\.corp\.example:636/)).toBeInTheDocument();
    const plain = within(ad).getByRole("checkbox", { name: "Allow plain LDAP, without StartTLS" });
    expect(within(ad).queryByText(/in the clear/)).not.toBeInTheDocument();
    await user.click(plain);
    expect(within(ad).getByRole("status")).toHaveTextContent(
      /password and every object go over the network in the clear/,
    );
    // Plain LDAP sends the saved password elsewhere: it is asked for again.
    await user.click(within(ad).getByRole("button", { name: "Save settings" }));
    expect(await within(ad).findByText(/needs the password or token again/)).toBeInTheDocument();
  }, 20_000);

  it("signs in to ServiceNow over https as an OAuth client", async () => {
    const user = open("servicenow");
    const snow = await screen.findByRole("region", { name: "ServiceNow connection" });
    // The two ways the server takes, to choose from.
    const signIn = within(snow).getByRole("combobox", { name: "Sign-in" });
    expect(signIn).toHaveTextContent("oauth");
    await user.click(signIn);
    expect((await screen.findAllByRole("option")).map((o) => o.textContent)).toEqual(["basic", "oauth"]);
    await user.click(screen.getByRole("option", { name: "oauth" }));
    await user.click(within(snow).getByRole("button", { name: "Test connection" }));
    expect(
      await within(snow).findByText(
        "Signed in to https://corp.service-now.com as the OAuth client 3f1c0b7e9a2d4c58b6e1f0a9d7c3b5e2 and read sc_req_item.",
      ),
    ).toBeInTheDocument();
    const url = within(snow).getByRole("textbox", { name: "Instance URL" });
    await user.clear(url);
    await user.type(url, "http://corp.service-now.com");
    await user.click(within(snow).getByRole("button", { name: "Save settings" }));
    expect(await within(snow).findByText(/The instance's https URL/)).toBeInTheDocument();
    await user.clear(url);
    await user.type(url, "https://corp.service-now.com");
    expect(within(snow).getByRole("checkbox", { name: "Keep VMs as CIs" })).toBeChecked();
    await user.type(within(snow).getByRole("textbox", { name: "Projects" }), "nope");
    await user.click(within(snow).getByRole("button", { name: "Save settings" }));
    expect(await within(snow).findByText('No project has the ID or code "nope".')).toBeInTheDocument();
  }, 20_000);
});
