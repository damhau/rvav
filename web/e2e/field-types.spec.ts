import { expect, test } from "@playwright/test";
import { bothThemes, scan } from "./axe";

// Every field type the designer offers (F1.1): a form with one of each,
// built in the designer, passes axe in both themes in the designer's
// preview and as members request it, and the new types work by keyboard.

const widgets = [
  "Text",
  "Long text",
  "Number",
  "Slider",
  "Toggle",
  "Dropdown",
  "Multiple choice dropdown",
  "Segmented choice",
  "Cards",
  "Checklist",
  "Date",
  "Date and time",
  "Tags",
  "Key and value pairs",
  "Table",
  "Secret",
  "Information text",
  "Computed value",
  "vSphere cluster",
  "vSphere datastore",
  "vSphere network",
  "vSphere template",
  "vSphere folder",
  "vSphere VM",
  "IPAM subnet",
  "IP address",
  "DNS zone",
  "AWX inventory",
  "AWX credential",
  "Project",
  "User",
  "Deployment",
];

test("a form with every field type, in the designer and requested", async ({ page }) => {
  test.setTimeout(180_000);
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/admin/catalog-items");
  await page.getByRole("button", { name: "New item" }).click();
  const dialog = page.getByRole("dialog", { name: "New item" });
  await dialog.getByRole("textbox", { name: "Name" }).fill("Every field");
  await dialog.getByRole("textbox", { name: "Slug" }).fill("every-field");
  await dialog.getByRole("textbox", { name: "Summary" }).fill("One field of each type.");
  await dialog.getByRole("button", { name: "Create" }).click();
  await page.getByRole("tab", { name: "Form designer" }).click();

  const fields = page.getByRole("region", { name: "Fields" });
  const preview = page.getByRole("region", { name: "Preview" });
  const status = page.getByRole("status").filter({ hasText: /^Draft / });
  // Each added before the next: the section is an item of the list too.
  for (const [i, widget] of widgets.entries()) {
    await fields.getByRole("combobox", { name: "Add a field" }).click();
    await page.getByRole("option", { name: widget, exact: true }).click();
    await expect(fields.getByRole("listitem")).toHaveCount(i + 2);
  }
  await expect(status).toHaveText("Draft 1.0: saved");
  await expect(preview.getByLabel("Secret", { exact: true })).toBeVisible();
  await bothThemes(page, "designer");

  await page.getByRole("tab", { name: "Entitlements" }).click();
  await page.getByRole("radio", { name: /Chosen projects/ }).check();
  await page.getByRole("checkbox", { name: "Web Platform" }).click();
  await page.getByRole("button", { name: "Save entitlements" }).click();
  await expect(page.getByText("Entitlements saved")).toBeVisible();
  await page.getByRole("tab", { name: "Versions" }).click();
  await page.getByRole("button", { name: "Publish" }).click();
  await expect(page.getByText("1.0 published")).toBeVisible();

  await page.getByRole("navigation", { name: "Main" }).getByRole("link", { name: "Catalog", exact: true }).click();
  await page.getByRole("link", { name: "Request Every field" }).click();
  await expect(page.getByRole("heading", { name: "Request Every field" })).toBeVisible();
  await bothThemes(page, "request form");

  // The new types, by keyboard.
  const secret = page.getByLabel("Secret", { exact: true });
  await secret.fill("hunter2");
  await expect(secret).toHaveAttribute("type", "password");
  await page.getByRole("button", { name: "Show Secret" }).press("Enter");
  await expect(secret).toHaveAttribute("type", "text");
  const tags = page.getByRole("textbox", { name: "Tags" });
  await tags.fill("web");
  await tags.press("Enter");
  await tags.fill("pci");
  await tags.press("Enter");
  await expect(page.getByRole("list", { name: "Tags, 2 added" })).toBeVisible();
  await page.getByRole("button", { name: "Add a pair" }).press("Enter");
  await page.getByRole("textbox", { name: "Key 1" }).fill("team");
  await page.getByRole("textbox", { name: "Value 1" }).fill("web");
  await page.getByRole("button", { name: "Add a row" }).press("Enter");
  await page.getByRole("textbox", { name: "Value, row 1" }).fill("first");
  await page.getByLabel("Date", { exact: true }).fill("2026-12-01");
  const user = page.getByRole("combobox", { name: "User" });
  await user.pressSequentially("ngu", { delay: 30 });
  await expect(page.getByRole("status").filter({ hasText: "1 person found" })).toBeVisible();
  await user.press("ArrowDown");
  await user.press("Enter");
  await expect(user).toHaveValue("Thomas Nguyen");
  await page.getByRole("combobox", { name: "Deployment" }).click();
  await page.getByRole("option", { name: /^web-dev-api/ }).click();
  await page.getByRole("textbox", { name: "Name", exact: true }).fill("every");
  await scan(page, "request form filled in");
  await page.getByRole("button", { name: "Submit request" }).click();
  await expect(page).toHaveURL(/\/requests\/REQ-\d+$/);
  await expect(page.getByText("hunter2")).toHaveCount(0);
  expect(errors).toEqual([]);
});
