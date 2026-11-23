import { expect, test } from "@playwright/test";

// From the keyboard only: an overlay opens on what it asks first, and gives
// the focus back to what opened it when it closes (V1.6).

test("the phone menu opens on the page it is at, and gives the focus back to its button", async ({ page }) => {
  await page.setViewportSize({ width: 375, height: 800 });
  await page.goto("/deployments");
  await expect(page.getByRole("heading", { name: "Deployments" })).toBeVisible();
  const opener = page.getByRole("button", { name: "Open navigation" });
  await opener.focus();
  await page.keyboard.press("Enter");
  const drawer = page.getByRole("dialog", { name: "Navigation" });
  await expect(drawer.locator('a[aria-current="page"]')).toBeFocused();
  await expect(drawer.locator('a[aria-current="page"]')).toHaveText(/Deployments/);
  await page.keyboard.press("Escape");
  await expect(drawer).toBeHidden();
  await expect(opener).toBeFocused();
});

test("the command palette gives the focus back, opened from its box or with Ctrl+K", async ({ page }) => {
  await page.goto("/requests");
  await expect(page.getByRole("heading", { name: "Requests" })).toBeVisible();
  const box = page.getByRole("button", { name: /Search VMs, IPs, requests/ });
  await box.focus();
  await page.keyboard.press("Enter");
  const palette = page.getByRole("dialog", { name: "Search or run a command" });
  await expect(palette.getByPlaceholder("Search VMs, IPs, requests, or type a command")).toBeFocused();
  await page.keyboard.press("Escape");
  await expect(palette).toBeHidden();
  await expect(box).toBeFocused();

  const first = page.getByRole("table", { name: "Requests" }).getByRole("link").first();
  await first.focus();
  await page.keyboard.press("ControlOrMeta+k");
  await expect(palette).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(palette).toBeHidden();
  await expect(first).toBeFocused();
});

test("an approval's panel gives the focus back to its row", async ({ page }) => {
  await page.goto("/approvals");
  const name = "RHEL 9 server · web-prd-front004";
  const row = page.getByRole("table", { name: "Waiting for you" }).getByRole("link", { name });
  await row.focus();
  await page.keyboard.press("Enter");
  const sheet = page.getByRole("dialog", { name });
  await expect(sheet.getByText("Why this needs you")).toBeVisible();
  await expect(sheet.locator(":focus")).toHaveCount(1);
  await page.keyboard.press("Escape");
  await expect(sheet).toBeHidden();
  await expect(row).toBeFocused();
});

test("a risky day-2 action opens on the name to type, and gives the focus back to Actions", async ({ page }) => {
  await page.goto("/deployments");
  await page.getByRole("link", { name: "web-dev-api", exact: true }).click();
  await expect(page.getByRole("heading", { name: "web-dev-api" })).toBeVisible();
  const actions = page.getByRole("button", { name: "Actions", exact: true });
  await actions.focus();
  await page.keyboard.press("Enter");
  const menu = page.getByRole("menu");
  await expect(menu.getByRole("menuitem", { name: "Delete", exact: true })).toBeVisible();
  // Delete is the menu's last item.
  await page.keyboard.press("End");
  await expect(menu.getByRole("menuitem", { name: "Delete", exact: true })).toBeFocused();
  await page.keyboard.press("Enter");
  const dialog = page.getByRole("dialog", { name: "Delete · web-dev-api" });
  await expect(dialog.getByRole("textbox", { name: /to confirm/ })).toBeFocused();
  await page.keyboard.press("Escape");
  await expect(dialog).toBeHidden();
  await expect(actions).toBeFocused();
});

test("revoking a token asks first, and Escape gives the focus back to Revoke", async ({ page }) => {
  await page.goto("/tokens");
  await expect(page.getByRole("heading", { name: "API tokens" })).toBeVisible();
  await page.getByRole("textbox", { name: "What it is for" }).fill("Backups");
  await page.getByRole("button", { name: "Create token" }).click();
  await page.getByRole("region", { name: "Your new token" }).getByRole("button", { name: "Done" }).click();
  const revoke = page.getByRole("region", { name: "Your tokens" }).getByRole("button", { name: "Revoke Backups" });
  await revoke.focus();
  await page.keyboard.press("Enter");
  const ask = page.getByRole("alertdialog");
  await expect(ask.getByRole("button", { name: "Revoke token" })).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(ask).toBeHidden();
  await expect(revoke).toBeFocused();
});

test("a refused save in a sheet moves the focus to the field, and the first limit gives it to Add a limit", async ({
  page,
}) => {
  await page.addInitScript(() => localStorage.setItem("rvav.mock.as", "project-admin"));
  await page.goto("/admin/projects/prj_web?tab=quotas");
  const panel = page.getByRole("region", { name: "Each member's quota" });
  await expect(panel.getByText("No limit")).toBeVisible();
  await panel.getByRole("button", { name: "Add a limit" }).focus();
  await page.keyboard.press("Enter");
  const sheet = page.getByRole("dialog", { name: "Add a limit" });
  await expect(sheet.getByRole("combobox", { name: "Resource" })).toBeFocused();
  await expect(sheet.getByText("At most 160: Web Platform's own limit.")).toBeVisible();
  await page.keyboard.press("Tab");
  const limit = sheet.getByRole("spinbutton", { name: "Limit" });
  await expect(limit).toBeFocused();
  await page.keyboard.type("200");
  await page.keyboard.press("Enter");
  await expect(limit).toHaveAccessibleDescription(/each member's limit can't be more\./);
  await expect(limit).toBeFocused();

  await page.keyboard.press("ControlOrMeta+a");
  await page.keyboard.type("16");
  await page.keyboard.press("Enter");
  await expect(sheet).toBeHidden();
  await expect(panel.getByRole("row", { name: /^vCPU 16/ })).toBeVisible();
  await expect(panel.getByRole("button", { name: "Add a limit" })).toBeFocused();
});
