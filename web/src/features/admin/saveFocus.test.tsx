// Saving with the keyboard on the pages that save in place: the focus never
// falls to the page, a refusal takes it to the field to fix, and a save that
// replaces what had it gives it to what stands in for it.
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it } from "vitest";
import { applyBranding, rvavBranding } from "@/lib/branding";
import { resetDb } from "@/mocks/seed";
import { renderApp } from "@/test/render";

type User = ReturnType<typeof userEvent.setup>;

afterEach(() => {
  localStorage.removeItem("rvav.mock.as");
  applyBranding(rvavBranding);
  resetDb();
});

async function press(user: User, button: HTMLElement) {
  button.focus();
  await user.keyboard("{Enter}");
}

async function replace(user: User, box: HTMLElement, text: string) {
  await user.clear(box);
  await user.click(box);
  await user.paste(text);
}

describe("saving with the keyboard", () => {
  it(
    "in a destination's sheet, goes to the refused field, then back to the page's button",
    { timeout: 20_000 },
    async () => {
      const user = userEvent.setup();
      renderApp("/admin/audit-forwarding");
      const add = await screen.findByRole("button", { name: "New destination" });
      await press(user, add);
      const sheet = await screen.findByRole("dialog", { name: "New destination" });
      await user.click(within(sheet).getByRole("combobox", { name: "Kind" }));
      await user.click(await screen.findByRole("option", { name: "Webhook" }));
      await user.type(within(sheet).getByRole("textbox", { name: "Name" }), "Splunk");
      const url = within(sheet).getByRole("textbox", { name: "URL" });
      await user.type(url, "https://splunk.example.org/hec");
      await user.click(within(sheet).getByRole("combobox", { name: "Signing secret" }));
      await user.click(await screen.findByRole("option", { name: "webhook-signing" }));

      await press(user, within(sheet).getByRole("button", { name: "Create destination" }));
      await waitFor(() => expect(url).toHaveFocus());
      expect(url).toHaveAccessibleDescription(/is not one of the hosts webhooks may call/);

      await replace(user, url, "https://hooks.corp.example/splunk");
      await press(user, within(sheet).getByRole("button", { name: "Create destination" }));
      await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
      expect(add).toHaveFocus();
    },
  );

  it(
    "on the branding, goes to the refused accent, to it again from a suggestion, and stays on Save",
    { timeout: 20_000 },
    async () => {
      const user = userEvent.setup();
      renderApp("/admin/branding");
      const accent = await screen.findByLabelText("Accent colour");
      await user.type(accent, "#ffcc00");
      const save = screen.getByRole("button", { name: "Save branding" });
      await press(user, save);
      await waitFor(() => expect(accent).toHaveFocus());
      expect(accent).toBeInvalid();

      await press(user, await screen.findByRole("button", { name: /^Use #[0-9a-f]{6}$/ }));
      expect(accent).toHaveFocus();
      await waitFor(() => expect(screen.queryByRole("button", { name: /^Use #/ })).toBeNull());
      expect(accent).toHaveFocus();

      await press(user, save);
      expect(await screen.findByText("Branding saved")).toBeInTheDocument();
      expect(save).toHaveFocus();
      expect(save).toBeEnabled();
    },
  );

  it(
    "in a template, goes to the refused part, then to the status line after a save and a reset",
    { timeout: 30_000 },
    async () => {
      const user = userEvent.setup();
      renderApp("/admin/notification-templates/approval_needed?locale=en");
      const subject = await screen.findByLabelText("Subject");
      await replace(user, subject, "{{.Deployment.Name}} waits");
      await press(user, screen.getByRole("button", { name: "Save template" }));
      await waitFor(() => expect(screen.getByLabelText("Subject")).toHaveFocus());
      expect(screen.getByLabelText("Subject")).toBeInvalid();

      await replace(user, screen.getByLabelText("Subject"), "Decide on {{.Request.Number}}");
      await press(user, screen.getByRole("button", { name: "Save template" }));
      expect(await screen.findByText("Template saved")).toBeInTheDocument();
      await waitFor(() => expect(screen.getByText(/^Edited by .+, /)).toHaveFocus());

      await press(user, screen.getByRole("button", { name: "Reset to built-in" }));
      const dialog = await screen.findByRole("alertdialog", { name: "Go back to the built-in template?" });
      await press(user, within(dialog).getByRole("button", { name: "Reset to built-in" }));
      expect(await screen.findByText("Built-in template back")).toBeInTheDocument();
      await waitFor(() => expect(screen.getByText("The built-in template.")).toHaveFocus());
      expect(document.activeElement).not.toBe(document.body);
    },
  );
});

// A refusal is heard with its field: the focus reaches it once it is marked invalid.
async function refusedAt(field: HTMLElement, message: RegExp | string) {
  await waitFor(() => expect(field).toHaveFocus());
  expect(field).toBeInvalid();
  expect(field).toHaveAccessibleDescription(message);
}

describe("a save refused with the keyboard goes to the field it names", () => {
  it("in the identity provider's settings", async () => {
    const user = userEvent.setup();
    renderApp("/admin/integrations/oidc");
    const p = await screen.findByRole("region", { name: "Provider settings" });
    const scopes = within(p).getByLabelText("Scopes");
    await replace(user, scopes, "profile email");
    await press(user, within(p).getByRole("button", { name: "Save settings" }));
    await refusedAt(scopes, "The scopes must include openid.");
  });

  it("in an integration's settings", async () => {
    const user = userEvent.setup();
    renderApp("/admin/integrations/awx");
    const awx = await screen.findByRole("region", { name: "AWX connection" });
    const every = within(awx).getByRole("textbox", { name: "Sync interval" });
    await replace(user, every, "10s");
    await press(user, within(awx).getByRole("button", { name: "Save settings" }));
    await refusedAt(every, "A duration from 30s to 24h, for example 5m or 1h30m.");
  });

  it("in the AWX inventory's token", async () => {
    const user = userEvent.setup();
    renderApp("/admin/integrations/awx");
    const panel = await screen.findByRole("region", { name: "Inventory token" });
    const address = within(panel).getByRole("textbox", { name: "rvav's address, as AWX reaches it" });
    await replace(user, address, "ftp://rvav.corp.example");
    await press(user, within(panel).getByRole("button", { name: "Create a token and put it in AWX" }));
    await refusedAt(address, /such as https:\/\/rvav.corp.example/);
  });

  it("in an NSX Manager's sheet", { timeout: 15_000 }, async () => {
    const user = userEvent.setup();
    renderApp("/admin/integrations/nsx");
    const region = await screen.findByRole("region", { name: "NSX Managers" });
    await press(user, within(region).getByRole("button", { name: "Add an NSX Manager" }));
    const sheet = await screen.findByRole("dialog", { name: "Add an NSX Manager" });
    await user.type(within(sheet).getByRole("textbox", { name: "Name" }), "nsx02.corp.example");
    await user.type(within(sheet).getByRole("textbox", { name: "Address" }), "https://nsx02.corp.example");
    await user.type(within(sheet).getByRole("textbox", { name: "Account" }), "svc-rvav");
    await press(user, within(sheet).getByRole("button", { name: "Add NSX Manager" }));
    await refusedAt(within(sheet).getByLabelText("Password"), "Give the account's password.");
  });

  it("in a vCenter's sheet", { timeout: 15_000 }, async () => {
    const user = userEvent.setup();
    renderApp("/admin/integrations/vcenter");
    const region = await screen.findByRole("region", { name: "vCenters" });
    await press(user, within(region).getByRole("button", { name: "Add a vCenter" }));
    const sheet = await screen.findByRole("dialog", { name: "Add a vCenter" });
    await user.type(within(sheet).getByRole("textbox", { name: "Name" }), "vc02.corp.example");
    await user.type(within(sheet).getByRole("textbox", { name: "SDK address" }), "https://vc02.corp.example/sdk");
    await user.type(within(sheet).getByRole("textbox", { name: "Account" }), "svc-rvav@vsphere.local");
    await press(user, within(sheet).getByRole("button", { name: "Add vCenter" }));
    await refusedAt(within(sheet).getByLabelText("Password"), "Give the account's password.");
  });

  it("in a project's details", async () => {
    const user = userEvent.setup();
    renderApp("/admin/projects/prj_web?tab=details");
    const details = await screen.findByRole("region", { name: "Details" });
    const name = within(details).getByRole("textbox", { name: "Name" });
    await user.clear(name);
    await press(user, within(details).getByRole("button", { name: "Save details" }));
    await refusedAt(name, "Give the project a name.");
  });

  it("in a catalog item's details", async () => {
    const user = userEvent.setup();
    renderApp("/admin/catalog-items/itm_rhel9?tab=details");
    const details = await screen.findByRole("region", { name: "Details" });
    const summary = within(details).getByRole("textbox", { name: "Summary" });
    await user.clear(summary);
    await press(user, within(details).getByRole("button", { name: "Save details" }));
    await refusedAt(summary, "Say in a line what the item gives.");
  });
});
