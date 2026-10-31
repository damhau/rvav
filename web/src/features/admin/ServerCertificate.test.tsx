import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { renderApp } from "@/test/render";

// Certificates (U1.4, ADR-0030), against the mock API: a lab host's is
// self-signed, another's issued by the corporate CA the system trusts.

async function tab(name: string) {
  const user = userEvent.setup();
  renderApp("/admin/integrations");
  await user.click(await screen.findByRole("tab", { name }));
  return user;
}

describe("server certificates", () => {
  // First: the mock's administration state lasts the file, and a test
  // below adds to the bundle.
  it("refuses a private key in the CA bundle", async () => {
    const user = await tab("Trusted certificates");
    const bundle = await screen.findByRole("region", { name: "CA bundle" });
    expect(within(bundle).queryByLabelText(/password|token|secret/i)).not.toBeInTheDocument();
    await user.click(within(bundle).getByRole("button", { name: "Check" }));
    expect(await within(bundle).findByText(/Empty: only the system's CAs are trusted\./)).toBeInTheDocument();
    const pem = within(bundle).getByRole("textbox", { name: "CA certificates (PEM)" });
    await user.click(pem);
    await user.paste("-----BEGIN PRIVATE KEY-----\nAAAA\n-----END PRIVATE KEY-----\n");
    await user.click(within(bundle).getByRole("button", { name: "Save settings" }));
    expect(
      await within(bundle).findByText(
        "CA certificates in PEM: a private key: the bundle holds the CAs' certificates only.",
      ),
    ).toBeInTheDocument();
  });

  it("shows a trusted certificate under the test", async () => {
    const user = await tab("NSX");
    const nsx = await screen.findByRole("region", { name: "NSX connection" });
    await user.click(within(nsx).getByRole("button", { name: "Test connection" }));
    const cert = await within(nsx).findByRole("region", { name: "Server certificate" });
    expect(cert).toHaveTextContent("Certificate of nsx.corp.example:443");
    expect(cert).toHaveTextContent("A CA rvav trusts vouches for it.");
    expect(cert).toHaveTextContent("Corp Issuing CA 2");
    expect(within(cert).queryByRole("button")).not.toBeInTheDocument();
  });

  it("pins a self-signed certificate: in the form, saved with the token, then trusted", async () => {
    const user = await tab("AWX");
    const awx = await screen.findByRole("region", { name: "AWX connection" });
    const url = within(awx).getByRole("textbox", { name: "URL" });
    await user.clear(url);
    await user.type(url, "https://awx-lab.corp.example");
    await user.type(within(awx).getByLabelText("Token"), "lab-token");
    await user.click(within(awx).getByRole("button", { name: "Test connection" }));
    const test = await screen.findByRole("region", { name: "AWX test" });
    expect(test).toHaveTextContent("A check failed.");
    expect(test).toHaveTextContent("Trust this certificate, or add its CA to the CA bundle.");
    const cert = within(test).getByRole("region", { name: "Server certificate" });
    expect(cert).toHaveTextContent("Not trusted: no CA rvav trusts signed it.");
    await user.click(within(cert).getByRole("button", { name: "Trust this certificate" }));
    const pin = within(awx).getByRole<HTMLInputElement>("textbox", { name: "Trusted certificate (SHA-256)" });
    expect(pin.value).toMatch(/^([0-9A-F]{2}:){31}[0-9A-F]{2}$/);
    expect(cert).toHaveTextContent("Its SHA-256 is in the form: save the settings to trust it.");
    await user.click(within(awx).getByRole("button", { name: "Test connection" }));
    expect(await within(test).findByText(/pinned for this connection\./)).toBeInTheDocument();
    expect(test).toHaveTextContent("Every check passed.");
    await user.click(within(awx).getByRole("button", { name: "Save settings" }));
    expect(await screen.findByText("AWX settings saved")).toBeInTheDocument();
  });

  it("asks for the token again when a certificate is pinned", async () => {
    const user = await tab("EfficientIP");
    const ipam = await screen.findByRole("region", { name: "EfficientIP connection" });
    await user.type(within(ipam).getByRole("textbox", { name: "Trusted certificate (SHA-256)" }), "ab".repeat(32));
    await user.click(within(ipam).getByRole("button", { name: "Save settings" }));
    expect(await within(ipam).findByText(/a certificate pinned, or TLS checks turned off/)).toBeInTheDocument();
    const pin = within(ipam).getByRole("textbox", { name: "Trusted certificate (SHA-256)" });
    await user.clear(pin);
    await user.type(pin, "AB:CD");
    await user.type(within(ipam).getByLabelText("Password"), "pw");
    await user.click(within(ipam).getByRole("button", { name: "Save settings" }));
    expect(
      await within(ipam).findByText("A certificate's SHA-256: 32 bytes in hex, with or without colons."),
    ).toBeInTheDocument();
  });

  it("adds a self-signed certificate to the CA bundle once confirmed, and the bundle lists it", async () => {
    const user = await tab("NSX");
    const nsx = await screen.findByRole("region", { name: "NSX connection" });
    const url = within(nsx).getByRole("textbox", { name: "URL" });
    await user.clear(url);
    await user.type(url, "https://nsx-lab.corp.example");
    await user.type(within(nsx).getByLabelText("Password"), "pw");
    await user.click(within(nsx).getByRole("button", { name: "Test connection" }));
    const cert = await within(nsx).findByRole("region", { name: "Server certificate" });
    await user.click(within(cert).getByRole("button", { name: "Add it to the CA bundle" }));
    const confirm = within(cert).getByRole("group", { name: "Add to the CA bundle" });
    expect(confirm).toHaveTextContent("Every connection will trust this certificate, not only this one's.");
    await user.click(within(confirm).getByRole("button", { name: "Add to the CA bundle" }));
    expect(await screen.findByText("Certificate added to the CA bundle")).toBeInTheDocument();
    await user.click(within(nsx).getByRole("button", { name: "Test connection" }));
    expect(await within(nsx).findByText("A CA rvav trusts vouches for it.")).toBeInTheDocument();

    await user.click(screen.getByRole("tab", { name: "Trusted certificates" }));
    const bundle = await screen.findByRole("region", { name: "CA bundle" });
    expect(within(bundle).getByRole<HTMLTextAreaElement>("textbox", { name: "CA certificates (PEM)" }).value).toContain(
      "-----BEGIN CERTIFICATE-----",
    );
    await user.click(within(bundle).getByRole("button", { name: "Check" }));
    expect(await within(bundle).findByText(/A CA, until 15 January 2031; SHA-256/)).toBeInTheDocument();
    expect(within(bundle).getByText("nsx-lab.corp.example:")).toBeInTheDocument();
  });

  it("pins an added vCenter's certificate from its test", async () => {
    const user = await tab("vCenter");
    await user.click(await screen.findByRole("button", { name: "Add a vCenter" }));
    const dialog = await screen.findByRole("dialog", { name: "Add a vCenter" });
    await user.type(within(dialog).getByRole("textbox", { name: "Name" }), "vc-lab.corp.example");
    await user.type(within(dialog).getByRole("textbox", { name: "SDK address" }), "https://vc-lab.corp.example/sdk");
    await user.type(within(dialog).getByRole("textbox", { name: "Account" }), "rvav");
    await user.type(within(dialog).getByLabelText("Password"), "pw");
    await user.click(within(dialog).getByRole("button", { name: "Test" }));
    const cert = await within(dialog).findByRole("region", { name: "Server certificate" });
    await user.click(within(cert).getByRole("button", { name: "Trust this certificate" }));
    const pin = within(dialog).getByRole<HTMLInputElement>("textbox", { name: "Trusted certificate (SHA-256)" });
    expect(pin.value).toMatch(/^([0-9A-F]{2}:){31}[0-9A-F]{2}$/);
    await user.click(within(dialog).getByRole("button", { name: "Test" }));
    expect(await within(dialog).findByText(/pinned for this connection\./)).toBeInTheDocument();
    await user.click(within(dialog).getByRole("button", { name: "Save vCenter" }));
    expect((await screen.findAllByText(/· certificate pinned/)).length).toBeGreaterThan(0);
  });
});
