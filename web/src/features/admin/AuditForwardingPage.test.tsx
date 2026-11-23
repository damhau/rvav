import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it } from "vitest";
import { resetDb } from "@/mocks/seed";
import { renderApp } from "@/test/render";

afterEach(() => localStorage.removeItem("rvav.mock.as"));

async function choose(user: ReturnType<typeof userEvent.setup>, scope: HTMLElement, label: string, option: string) {
  await user.click(within(scope).getByRole("combobox", { name: label }));
  await user.click(await screen.findByRole("option", { name: option }));
}

describe("Audit forwarding", () => {
  it("lists each destination with how its delivery goes", async () => {
    renderApp("/admin/audit-forwarding");
    const region = await screen.findByRole("region", { name: "Destinations" });
    const siem = await within(region).findByRole("row", { name: /Corporate SIEM/ });
    expect(siem).toHaveTextContent("siem.corp.example:6514");
    expect(siem).toHaveTextContent("TLS · audit · rvav");
    expect(siem).toHaveTextContent("Every event");
    expect(siem).toHaveTextContent("Up to date");
    const soc = within(region).getByRole("row", { name: /SOC sign-ins/ });
    expect(soc).toHaveTextContent("Sign-ins and sign-outs, API tokens");
    expect(soc).toHaveTextContent("Failing");
    expect(soc).toHaveTextContent("37 waiting");
    expect(soc).toHaveTextContent("503 Service Unavailable from hooks.corp.example");
    expect(soc).toHaveTextContent(/Next try in \d+ minutes/);
  });

  it("sends a test event and says how it went", async () => {
    const user = userEvent.setup();
    renderApp("/admin/audit-forwarding");
    const region = await screen.findByRole("region", { name: "Destinations" });
    await within(region).findByRole("row", { name: /Corporate SIEM/ });
    await user.click(within(region).getByRole("button", { name: "Actions for Corporate SIEM" }));
    await user.click(await screen.findByRole("menuitem", { name: "Send a test event" }));
    const ok = await screen.findByRole("dialog", { name: "Test of Corporate SIEM" });
    expect(ok).toHaveTextContent("Delivered in 84 ms. Sent to siem.corp.example:6514 over TLS.");
    await user.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());

    await user.click(within(region).getByRole("button", { name: "Actions for SOC sign-ins" }));
    await user.click(await screen.findByRole("menuitem", { name: "Send a test event" }));
    const bad = await screen.findByRole("dialog", { name: "Test of SOC sign-ins" });
    expect(bad).toHaveTextContent("Not delivered in 3012 ms. 503 Service Unavailable");
  });

  it(
    "adds a syslog server with a client certificate, refusing what the server would",
    { timeout: 20_000 },
    async () => {
      const user = userEvent.setup();
      renderApp("/admin/audit-forwarding");
      const region = await screen.findByRole("region", { name: "Destinations" });
      await within(region).findByRole("row", { name: /Corporate SIEM/ });
      await user.click(screen.getByRole("button", { name: "New destination" }));
      const sheet = await screen.findByRole("dialog", { name: "New destination" });
      expect(within(sheet).getByRole("textbox", { name: "Name" })).toHaveFocus();
      await user.type(within(sheet).getByRole("textbox", { name: "Name" }), "Lab syslog");
      await user.type(within(sheet).getByRole("textbox", { name: "Host" }), "not a host");
      await user.type(within(sheet).getByRole("textbox", { name: "Port" }), "70000");
      await user.click(within(sheet).getByRole("textbox", { name: "CA certificates" }));
      await user.paste("-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----");
      await user.click(within(sheet).getByRole("textbox", { name: "Client certificate" }));
      await user.paste("-----BEGIN CERTIFICATE-----\nMIIC\n-----END CERTIFICATE-----");
      await user.click(within(sheet).getByRole("checkbox", { name: "Sign-ins and sign-outs" }));
      await user.click(within(sheet).getByRole("button", { name: "Create destination" }));
      expect(
        await within(sheet).findByText("A host name or an IP address, such as siem.corp.example."),
      ).toBeInTheDocument();
      expect(within(sheet).getByText("A port from 1 to 65535.")).toBeInTheDocument();
      expect(
        within(sheet).getByText("Give the client certificate with the stored secret that holds its key."),
      ).toBeInTheDocument();
      expect(within(sheet).getByRole("textbox", { name: "Host" })).toHaveAttribute("aria-invalid", "true");

      await user.clear(within(sheet).getByRole("textbox", { name: "Host" }));
      await user.type(within(sheet).getByRole("textbox", { name: "Host" }), "syslog.lab.example");
      await user.clear(within(sheet).getByRole("textbox", { name: "Port" }));
      await choose(user, sheet, "Facility", "audit");
      await choose(user, sheet, "Client key", "webhook-signing");
      await user.click(within(sheet).getByRole("button", { name: "Create destination" }));
      await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
      const row = await within(region).findByRole("row", { name: /Lab syslog/ });
      expect(row).toHaveTextContent("syslog.lab.example:6514");
      expect(row).toHaveTextContent("TLS · audit · rvav · client certificate");
      expect(row).toHaveTextContent("Sign-ins and sign-outs");
      expect(row).toHaveTextContent("Nothing delivered yet");
    },
  );

  it(
    "adds a webhook on an allowed host only, and hides the TLS fields of a syslog in clear",
    { timeout: 20_000 },
    async () => {
      const user = userEvent.setup();
      renderApp("/admin/audit-forwarding");
      const region = await screen.findByRole("region", { name: "Destinations" });
      await within(region).findByRole("row", { name: /Corporate SIEM/ });
      await user.click(screen.getByRole("button", { name: "New destination" }));
      const sheet = await screen.findByRole("dialog", { name: "New destination" });
      await choose(user, sheet, "Transport", "UDP, for labs");
      expect(within(sheet).queryByRole("textbox", { name: "CA certificates" })).toBeNull();
      expect(within(sheet).getByRole("textbox", { name: "Port" })).toHaveAttribute("placeholder", "514");

      await choose(user, sheet, "Kind", "Webhook");
      await user.type(within(sheet).getByRole("textbox", { name: "Name" }), "Splunk");
      await user.type(within(sheet).getByRole("textbox", { name: "URL" }), "https://splunk.example.org/hec");
      await choose(user, sheet, "Signing secret", "webhook-signing");
      await user.click(within(sheet).getByRole("button", { name: "Create destination" }));
      expect(
        await within(sheet).findByText(/splunk\.example\.org is not one of the hosts webhooks may call/),
      ).toBeInTheDocument();

      await user.clear(within(sheet).getByRole("textbox", { name: "URL" }));
      await user.type(within(sheet).getByRole("textbox", { name: "URL" }), "https://hooks.corp.example/splunk");
      await user.click(within(sheet).getByRole("button", { name: "Create destination" }));
      await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
      const row = await within(region).findByRole("row", { name: /Splunk/ });
      expect(row).toHaveTextContent("https://hooks.corp.example/splunk");
      expect(row).toHaveTextContent("Signed with webhook-signing");
    },
  );

  it("turns one off, and removes one once told what waits for it", async () => {
    const user = userEvent.setup();
    renderApp("/admin/audit-forwarding");
    const region = await screen.findByRole("region", { name: "Destinations" });
    await within(region).findByRole("row", { name: /SOC sign-ins/ });
    const menu = within(region).getByRole("button", { name: "Actions for SOC sign-ins" });
    await user.click(menu);
    await user.click(await screen.findByRole("menuitem", { name: "Turn off" }));
    expect(await screen.findByText("SOC sign-ins is off")).toBeInTheDocument();
    await waitFor(() => expect(within(region).getByRole("row", { name: /SOC sign-ins/ })).toHaveTextContent("Off"));

    await user.click(menu);
    await user.click(await screen.findByRole("menuitem", { name: "Remove" }));
    const confirm = await screen.findByRole("alertdialog", { name: "Stop sending audit events to SOC sign-ins?" });
    expect(confirm).toHaveTextContent("The 37 events waiting for it are dropped. The audit log keeps them.");
    await user.click(within(confirm).getByRole("button", { name: "Remove destination" }));
    await waitFor(() => expect(within(region).queryByRole("row", { name: /SOC sign-ins/ })).toBeNull());
  });

  it(
    "sends a webhook's credential in a header whose value is kept, never in its URL",
    { timeout: 20_000 },
    async () => {
      const user = userEvent.setup();
      renderApp("/admin/audit-forwarding");
      const region = await screen.findByRole("region", { name: "Destinations" });
      const soc = await within(region).findByRole("row", { name: /SOC sign-ins/ });
      expect(soc).toHaveTextContent("https://hooks.corp.example/soc/rvav?index=REDACTED");
      expect(soc).toHaveTextContent("Signed with webhook-signing · sends Authorization");

      await user.click(within(region).getByRole("button", { name: "Actions for SOC sign-ins" }));
      await user.click(await screen.findByRole("menuitem", { name: "Change" }));
      const sheet = await screen.findByRole("dialog", { name: "Change SOC sign-ins" });
      expect(within(sheet).getByRole("textbox", { name: "Authentication header" })).toHaveValue("Authorization");
      const value = within(sheet).getByLabelText("Header's value");
      expect(value).toHaveValue("");
      expect(value).toHaveAccessibleDescription(/^Kept: type a new one to replace it/);

      const url = within(sheet).getByRole("textbox", { name: "URL" });
      await user.clear(url);
      await user.type(url, "https://soc:pw@hooks.corp.example/soc/rvav");
      await user.click(within(sheet).getByRole("button", { name: "Save destination" }));
      expect(await within(sheet).findByText(/^Leave the user name and password out of the URL/)).toBeInTheDocument();
      expect(url).toHaveFocus();

      await user.clear(url);
      await user.type(url, "https://cmdb.corp.example/soc");
      await user.click(within(sheet).getByRole("button", { name: "Save destination" }));
      expect(
        await within(sheet).findByText("Give the header's value: a new URL's server doesn't get the one kept."),
      ).toBeInTheDocument();
      expect(value).toHaveFocus();

      await user.type(value, "Splunk new-token");
      await user.click(within(sheet).getByRole("button", { name: "Save destination" }));
      await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
      expect(await within(region).findByRole("row", { name: /SOC sign-ins/ })).toHaveTextContent(
        "https://cmdb.corp.example/soc",
      );
    },
  );

  it("shows an auditor the destinations without letting them change any", async () => {
    localStorage.setItem("rvav.mock.as", "auditor");
    resetDb();
    renderApp("/admin/audit-forwarding");
    const region = await screen.findByRole("region", { name: "Destinations" });
    expect(await within(region).findByRole("row", { name: /Corporate SIEM/ })).toHaveTextContent("Up to date");
    expect(within(region).queryByRole("button", { name: "Actions for Corporate SIEM" })).toBeNull();
    expect(screen.queryByRole("button", { name: "New destination" })).toBeNull();
  });
});
