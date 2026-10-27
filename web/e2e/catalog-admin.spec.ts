import { expect, test } from "@playwright/test";

// Catalog administration against the mock backend (S8, ADR-0019).

test("draft, publish and roll back a version of an item", async ({ page }) => {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));

  await page.goto("/");
  await page.getByRole("link", { name: "Catalog items" }).click();
  const items = page.getByRole("region", { name: "Items" });
  await items.getByRole("link", { name: "RHEL 9 server" }).click();
  await expect(page.getByRole("heading", { name: "RHEL 9 server" })).toBeVisible();
  await expect(page.getByText("Published 3.2").first()).toBeVisible();

  // A draft from the published version.
  await page.getByRole("button", { name: "New draft" }).click();
  await expect(page.getByText("Draft 3.3 made from the published version")).toBeVisible();

  // Its definition as YAML: a broken one shows where, a valid one is saved.
  await page.getByRole("tab", { name: "Definition" }).click();
  const editor = page.getByRole("textbox", { name: "Definition of RHEL 9 server 3.3" });
  await expect(editor).toContainText("form_schema:");
  await editor.click();
  await page.keyboard.press("Control+Home");
  await page.keyboard.type("x");
  await page.getByRole("button", { name: "Save draft" }).click();
  await expect(page.getByRole("list", { name: "Problems in the definition" })).toContainText("Line 1:");
  await page.getByRole("button", { name: "Undo changes" }).click();
  await editor.click();
  await page.keyboard.press("Control+Home");
  await page.keyboard.type("# reviewed\n");
  await page.getByRole("button", { name: "Save draft" }).click();
  await expect(page.getByText("Draft 3.3 saved")).toBeVisible();

  // Published, then rolled back to 3.2.
  await page.getByRole("tab", { name: "Versions" }).click();
  await page.getByRole("button", { name: "Publish" }).click();
  await expect(page.getByText("3.3 published")).toBeVisible();
  await page.getByRole("button", { name: "Roll back to 3.2" }).click();
  await expect(page.getByText("Rolled back to 3.2")).toBeVisible();
  await page.getByRole("button", { name: "Compare" }).click();
  const compare = page.getByRole("dialog", { name: "Compare versions" });
  await expect(compare.getByText(/No difference|Changes/).first()).toBeVisible();
  await page.keyboard.press("Escape");

  // Open it to one project only.
  await page.getByRole("tab", { name: "Entitlements" }).click();
  await page.getByRole("radio", { name: /Chosen projects/ }).check();
  await page.getByRole("checkbox", { name: "Web Platform" }).click();
  await page.getByRole("button", { name: "Save entitlements" }).click();
  await expect(page.getByText("Entitlements saved")).toBeVisible();
  expect(errors).toEqual([]);
});

test("create an item and a category", async ({ page }) => {
  await page.goto("/admin/catalog-items");
  await page.getByRole("button", { name: "New item" }).click();
  const dialog = page.getByRole("dialog", { name: "New item" });
  await dialog.getByRole("textbox", { name: "Name" }).fill("Ubuntu mini");
  await dialog.getByRole("textbox", { name: "Slug" }).fill("ubuntu-mini");
  await dialog.getByRole("textbox", { name: "Summary" }).fill("A small Ubuntu VM.");
  await dialog.getByRole("button", { name: "Create" }).click();
  await expect(page.getByRole("heading", { name: "Ubuntu mini" })).toBeVisible();
  await expect(page.getByText("Draft 1.0").first()).toBeVisible();
  await expect(page.getByText("Not published").first()).toBeVisible();

  await page.getByRole("link", { name: "Catalog items" }).first().click();
  const categories = page.getByRole("region", { name: "Categories" });
  await categories.getByRole("textbox", { name: "New category key" }).fill("storage");
  await categories.getByRole("textbox", { name: "New category name" }).fill("Storage");
  await categories.getByRole("button", { name: "Add category" }).click();
  await expect(categories.getByText("storage", { exact: true })).toBeVisible();
});
