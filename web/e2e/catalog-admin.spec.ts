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

test("build a small form in the designer", async ({ page }) => {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/admin/catalog-items");
  await page.getByRole("button", { name: "New item" }).click();
  const dialog = page.getByRole("dialog", { name: "New item" });
  await dialog.getByRole("textbox", { name: "Name" }).fill("Team share");
  await dialog.getByRole("textbox", { name: "Slug" }).fill("team-share");
  await dialog.getByRole("textbox", { name: "Summary" }).fill("A file share for a team.");
  await dialog.getByRole("button", { name: "Create" }).click();
  await page.getByRole("tab", { name: "Form designer" }).click();

  const fields = page.getByRole("region", { name: "Fields" });
  const props = page.getByRole("region", { name: "Properties" });
  const preview = page.getByRole("region", { name: "Preview" });
  const status = page.getByRole("status").filter({ hasText: "Draft 1.0" });

  // A dropdown whose options come from a CEL source, tested as two users.
  await fields.getByRole("combobox", { name: "Add a field" }).click();
  await page.getByRole("option", { name: "Dropdown", exact: true }).click();
  await props.getByRole("textbox", { name: "Name" }).fill("group");
  await props.getByRole("textbox", { name: "Name" }).press("Enter");
  await props.getByRole("textbox", { name: "Label", exact: true }).fill("Owning group");
  await props.getByRole("combobox", { name: "Value source" }).click();
  await page.getByRole("option", { name: "CEL expression" }).click();
  await props.getByRole("textbox", { name: "Options" }).fill("user.groups");
  await props.getByRole("textbox", { name: "Options" }).press("Enter");
  await expect(status).toHaveText("Draft 1.0: saved");
  await props.getByRole("button", { name: "Test the source" }).click();
  await expect(props.getByRole("status")).toContainText("2 options: rvav-web-admins, rvav-catalog-admins");
  await preview.getByRole("combobox", { name: "Preview as" }).click();
  await page.getByRole("option", { name: "Thomas Nguyen (member)" }).click();
  await props.getByRole("button", { name: "Test the source" }).click();
  await expect(props.getByRole("status")).toContainText("1 option: rvav-web");

  // A field shown in production only; a broken rule is the server's problem, shown on the rule.
  await fields.getByRole("combobox", { name: "Add a field" }).click();
  await page.getByRole("option", { name: "Text", exact: true }).click();
  await props.getByRole("textbox", { name: "Name" }).fill("reason");
  await props.getByRole("textbox", { name: "Name" }).press("Enter");
  await props.getByRole("textbox", { name: "Label", exact: true }).fill("Why production");
  const shown = props.getByRole("textbox", { name: "Shown when" });
  await shown.fill('environment == "prd" &&');
  await shown.press("Enter");
  await expect(status).toContainText("not saved, 1 problem to fix");
  await expect(props.getByText(/This rule is not valid CEL/)).toBeVisible();
  await expect(fields.getByRole("img", { name: "Why production has problems" })).toBeVisible();
  await shown.fill('environment == "prd"');
  await shown.press("Enter");
  await expect(status).toHaveText("Draft 1.0: saved");
  await expect(preview.getByRole("textbox", { name: "Why production" })).toBeHidden();
  await preview.getByRole("combobox", { name: "Environment" }).click();
  await page.getByRole("option", { name: "Production" }).click();
  await expect(preview.getByRole("textbox", { name: "Why production" })).toBeVisible();

  // Reordered by keyboard; the YAML has it all.
  await fields.getByRole("button", { name: "Move Why production up" }).focus();
  await page.keyboard.press("Enter");
  await expect(fields.getByRole("listitem").nth(1)).toContainText("Why production");
  await expect(status).toHaveText("Draft 1.0: saved");
  await page.getByRole("tab", { name: "Definition" }).click();
  const yaml = page.getByRole("textbox", { name: "Definition of Team share 1.0" });
  await expect(yaml).toContainText("fields:");
  await expect(yaml).toContainText('visible: environment == "prd"');
  expect(errors).toEqual([]);
});

// S8's acceptance scenario on the mocks (S8.9): built with a REST dropdown,
// previewed as a member, published, changed into 1.1 and rolled back.
test("an Ubuntu item from draft to roll back", async ({ page }) => {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/admin/catalog-items");
  await page.getByRole("button", { name: "New secret" }).click();
  const secret = page.getByRole("dialog", { name: "New secret" });
  await secret.getByRole("textbox", { name: "Name" }).fill("cmdb");
  await secret.getByLabel("Value").fill("cmdb-token");
  await secret.getByRole("button", { name: "Store" }).click();
  await expect(page.getByText("cmdb stored")).toBeVisible();

  await page.getByRole("button", { name: "New item" }).click();
  const dialog = page.getByRole("dialog", { name: "New item" });
  await dialog.getByRole("textbox", { name: "Name" }).fill("Ubuntu web");
  await dialog.getByRole("textbox", { name: "Slug" }).fill("ubuntu-web");
  await dialog.getByRole("textbox", { name: "Summary" }).fill("Ubuntu with nginx and a backup policy.");
  await dialog.getByRole("button", { name: "Create" }).click();

  // A dropdown from the CMDB, tested as a member of Web Platform.
  await page.getByRole("tab", { name: "Form designer" }).click();
  const fields = page.getByRole("region", { name: "Fields" });
  const props = page.getByRole("region", { name: "Properties" });
  const preview = page.getByRole("region", { name: "Preview" });
  const status = page.getByRole("status").filter({ hasText: /^Draft / });
  await fields.getByRole("combobox", { name: "Add a field" }).click();
  await page.getByRole("option", { name: "Dropdown", exact: true }).click();
  await props.getByRole("textbox", { name: "Name" }).fill("backup");
  await props.getByRole("textbox", { name: "Name" }).press("Enter");
  await props.getByRole("textbox", { name: "Label", exact: true }).fill("Backup policy");
  await props.getByRole("combobox", { name: "Value source" }).click();
  await page.getByRole("option", { name: "REST service" }).click();
  const url = props.getByRole("textbox", { name: "URL" });
  await url.fill("https://cmdb.corp.example/policies?for=${inputs.name}");
  await url.press("Enter");
  await props.getByRole("textbox", { name: "Secret" }).fill("cmdb");
  await props.getByRole("textbox", { name: "Secret" }).press("Enter");
  await expect(status).toHaveText("Draft 1.0: saved");
  await preview.getByRole("combobox", { name: "Preview as" }).click();
  await page.getByRole("option", { name: "Thomas Nguyen (member)" }).click();
  await props.getByRole("button", { name: "Test the source" }).click();
  await expect(props.getByRole("status")).toContainText("2 options: Service 01, Service 02");
  await expect(preview.getByRole("combobox", { name: "Backup policy" })).toBeVisible();

  // Open to Web Platform, published as 1.0: members get it.
  await page.getByRole("tab", { name: "Entitlements" }).click();
  await page.getByRole("radio", { name: /Chosen projects/ }).check();
  await page.getByRole("checkbox", { name: "Web Platform" }).click();
  await page.getByRole("button", { name: "Save entitlements" }).click();
  await expect(page.getByText("Entitlements saved")).toBeVisible();
  await page.getByRole("tab", { name: "Versions" }).click();
  await page.getByRole("button", { name: "Publish" }).click();
  await expect(page.getByText("1.0 published")).toBeVisible();
  await page.getByRole("navigation").getByRole("link", { name: "Catalog items" }).click();
  await expect(page.getByRole("region", { name: "Secrets" }).getByRole("row", { name: /cmdb/ })).toContainText(
    "ubuntu-web",
  );
  // In-app navigation: a reload would start the mock backend afresh.
  const requestForm = async (label: string, version: string) => {
    await page.getByRole("navigation").getByRole("link", { name: "Catalog", exact: true }).click();
    await expect(page.getByText(`v${version}`, { exact: true })).toBeVisible();
    await page.getByRole("link", { name: "Ubuntu web" }).click();
    await expect(page.getByRole("combobox", { name: label })).toBeVisible();
  };
  const backToItem = async () => {
    await page.getByRole("navigation").getByRole("link", { name: "Catalog items" }).click();
    await page.getByRole("region", { name: "Items" }).getByRole("link", { name: "Ubuntu web" }).click();
  };
  await requestForm("Backup policy", "1.0");

  // 1.1 renames the field; rolled back, members get 1.0 again.
  await backToItem();
  await page.getByRole("button", { name: "New draft" }).click();
  await expect(page.getByText("Draft 1.1 made from the published version")).toBeVisible();
  await page.getByRole("tab", { name: "Form designer" }).click();
  await fields.getByRole("button", { name: /^Backup policy/ }).click();
  await props.getByRole("textbox", { name: "Label", exact: true }).fill("Backup schedule");
  await expect(status).toHaveText("Draft 1.1: saved");
  await page.getByRole("tab", { name: "Versions" }).click();
  await page.getByRole("button", { name: "Publish" }).click();
  await expect(page.getByText("1.1 published")).toBeVisible();
  await requestForm("Backup schedule", "1.1");
  await backToItem();
  await page.getByRole("button", { name: "Roll back to 1.0" }).click();
  await expect(page.getByText("Rolled back to 1.0")).toBeVisible();
  await requestForm("Backup policy", "1.0");
  expect(errors).toEqual([]);
});
