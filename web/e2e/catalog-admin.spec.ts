import { expect, test } from "@playwright/test";
import { bothThemes, scan, theme } from "./axe";
import { chooseIn } from "./keyboard";

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
  await page.getByRole("button", { name: "Actions for version 3.2" }).click();
  await page.getByRole("menuitem", { name: "Roll back to 3.2" }).click();
  await expect(page.getByText("Rolled back to 3.2")).toBeVisible();
  await page.getByRole("button", { name: "Compare" }).click();
  const compare = page.getByRole("dialog", { name: "Compare versions" });
  await expect(compare.getByText(/No difference|Changes/).first()).toBeVisible();
  await page.keyboard.press("Escape");

  // Open it to one project only.
  await page.getByRole("tab", { name: "Sharing" }).click();
  await page.getByRole("radio", { name: /Chosen projects/ }).check();
  await page.getByRole("checkbox", { name: "Web Platform" }).click();
  await page.getByRole("button", { name: "Save projects" }).click();
  await expect(page.getByText("Projects saved")).toBeVisible();
  expect(errors).toEqual([]);
});

test("create an item and a category", async ({ page }) => {
  await page.goto("/admin/catalog-items");
  await page.getByRole("button", { name: "New item" }).click();
  const dialog = page.getByRole("dialog", { name: "New item" });
  await dialog.getByRole("textbox", { name: "Name" }).fill("Ubuntu mini");
  await dialog.getByRole("textbox", { name: "Slug" }).fill("ubuntu-mini");
  await dialog.getByRole("textbox", { name: "Summary" }).fill("A small Ubuntu VM.");
  await dialog.getByRole("combobox", { name: "Category" }).click();
  await page.getByRole("option", { name: "Compute" }).click();
  await dialog.getByRole("button", { name: "Create item" }).click();
  await expect(page.getByRole("heading", { name: "Ubuntu mini" })).toBeVisible();
  await expect(page.getByText("Draft 1.0").first()).toBeVisible();
  await expect(page.getByText("Not published").first()).toBeVisible();

  await page.getByRole("link", { name: "Catalog items" }).first().click();
  await page.getByRole("tab", { name: "Categories" }).click();
  const categories = page.getByRole("region", { name: "Categories" });
  await categories.getByRole("button", { name: "New category" }).click();
  const sheet = page.getByRole("dialog", { name: "New category" });
  await sheet.getByRole("textbox", { name: "Key" }).fill("storage");
  await sheet.getByRole("textbox", { name: "Name" }).fill("Storage");
  await sheet.getByRole("button", { name: "Create category" }).click();
  await expect(page.getByText("Storage added")).toBeVisible();
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
  await dialog.getByRole("combobox", { name: "Category" }).click();
  await page.getByRole("option", { name: "Compute" }).click();
  await dialog.getByRole("button", { name: "Create item" }).click();
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
  await page.getByRole("option", { name: "Thomas Nguyen (Member)" }).click();
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
  await page.goto("/admin/catalog-items?tab=secrets");
  await page.getByRole("button", { name: "New secret" }).click();
  const secret = page.getByRole("dialog", { name: "New secret" });
  await secret.getByRole("textbox", { name: "Name" }).fill("cmdb");
  await secret.getByLabel("Value").fill("cmdb-token");
  await secret.getByRole("button", { name: "Create secret" }).click();
  await expect(page.getByText("cmdb stored")).toBeVisible();

  await page.getByRole("tab", { name: "Items" }).click();
  await page.getByRole("button", { name: "New item" }).click();
  const dialog = page.getByRole("dialog", { name: "New item" });
  await dialog.getByRole("textbox", { name: "Name" }).fill("Ubuntu web");
  await dialog.getByRole("textbox", { name: "Slug" }).fill("ubuntu-web");
  await dialog.getByRole("textbox", { name: "Summary" }).fill("Ubuntu with nginx and a backup policy.");
  await dialog.getByRole("combobox", { name: "Category" }).click();
  await page.getByRole("option", { name: "Compute" }).click();
  await dialog.getByRole("button", { name: "Create item" }).click();

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
  await page.getByRole("option", { name: "Thomas Nguyen (Member)" }).click();
  await props.getByRole("button", { name: "Test the source" }).click();
  await expect(props.getByRole("status")).toContainText("2 options: Service 01, Service 02");
  await expect(preview.getByRole("combobox", { name: "Backup policy" })).toBeVisible();

  // Open to Web Platform, published as 1.0: members get it.
  await page.getByRole("tab", { name: "Sharing" }).click();
  await page.getByRole("radio", { name: /Chosen projects/ }).check();
  await page.getByRole("checkbox", { name: "Web Platform" }).click();
  await page.getByRole("button", { name: "Save projects" }).click();
  await expect(page.getByText("Projects saved")).toBeVisible();
  await page.getByRole("tab", { name: "Versions" }).click();
  await page.getByRole("button", { name: "Publish" }).click();
  await expect(page.getByText("1.0 published")).toBeVisible();
  await page.getByRole("navigation", { name: "Main" }).getByRole("link", { name: "Catalog items" }).click();
  await page.getByRole("tab", { name: "Secrets" }).click();
  await expect(page.getByRole("region", { name: "Secrets" }).getByRole("row", { name: /cmdb/ })).toContainText(
    "ubuntu-web",
  );
  // In-app navigation: a reload would start the mock backend afresh.
  // Other items are at 1.0 too (the three-tier application): the version is
  // read on this item's card, the innermost element with its link and summary.
  const card = page
    .locator("div")
    .filter({ has: page.getByRole("link", { name: "Ubuntu web", exact: true }) })
    .filter({ hasText: "Ubuntu with nginx and a backup policy." })
    .last();
  const requestForm = async (label: string, version: string) => {
    await page.getByRole("navigation", { name: "Main" }).getByRole("link", { name: "Catalog", exact: true }).click();
    await expect(card.getByText(`v${version}`, { exact: true })).toBeVisible();
    await page.getByRole("link", { name: "Request Ubuntu web" }).click();
    await expect(page.getByRole("combobox", { name: label })).toBeVisible();
  };
  const backToItem = async () => {
    await page.getByRole("navigation", { name: "Main" }).getByRole("link", { name: "Catalog items" }).click();
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
  await page.getByRole("button", { name: "Actions for version 1.0" }).click();
  await page.getByRole("menuitem", { name: "Roll back to 1.0" }).click();
  await expect(page.getByText("Rolled back to 1.0")).toBeVisible();
  await requestForm("Backup policy", "1.0");
  expect(errors).toEqual([]);
});

// From the review of S8: the designer's working copy outlives its tab.
test("designer changes survive tab switches, saved or refused", async ({ page }) => {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/admin/catalog-items");
  await page.getByRole("region", { name: "Items" }).getByRole("link", { name: "RHEL 9 server" }).click();
  await page.getByRole("button", { name: "New draft" }).click();
  await expect(page.getByText("Draft 3.3 made from the published version")).toBeVisible();
  await page.getByRole("tab", { name: "Form designer" }).click();
  const fields = page.getByRole("region", { name: "Fields" });
  const props = page.getByRole("region", { name: "Properties" });
  const status = page.getByRole("status").filter({ hasText: /^Draft 3\.3/ });

  // A change, then straight to another tab: it is saved, and there when coming back.
  await props.getByRole("textbox", { name: "Label", exact: true }).fill("Service role");
  await page.getByRole("tab", { name: "Versions" }).click();
  await page.getByRole("tab", { name: "Form designer" }).click();
  await expect(props.getByRole("textbox", { name: "Label", exact: true })).toHaveValue("Service role");
  await expect(status).toHaveText("Draft 3.3: saved");

  // A rule the server refuses: kept, Publish waits, the YAML shows it on its line.
  const shown = props.getByRole("textbox", { name: "Shown when" });
  await shown.fill("((");
  await shown.press("Enter");
  await expect(status).toContainText("not saved, 1 problem to fix");
  await page.getByRole("tab", { name: "Versions" }).click();
  await expect(page.getByRole("button", { name: "Publish" })).toBeDisabled();
  await page.getByRole("tab", { name: "Definition" }).click();
  await expect(page.getByRole("note")).toContainText("the designer's changes");
  await expect(page.getByRole("list", { name: "Problems in the definition" })).toContainText(
    /Line \d+: .*not valid CEL/,
  );
  await page.getByRole("tab", { name: "Form designer" }).click();
  await expect(props.getByRole("textbox", { name: "Shown when" })).toHaveValue("((");
  await expect(props.getByText(/This rule is not valid CEL/)).toBeVisible();
  await props.getByRole("textbox", { name: "Shown when" }).fill("");
  await props.getByRole("textbox", { name: "Shown when" }).press("Enter");
  await expect(status).toHaveText("Draft 3.3: saved");
  await page.getByRole("tab", { name: "Versions" }).click();
  await expect(page.getByRole("button", { name: "Publish" })).toBeEnabled();

  // Moves by keyboard keep the focus on the moved field, and say where it went.
  await page.getByRole("tab", { name: "Form designer" }).click();
  await fields.getByRole("button", { name: "Move VM name up" }).focus();
  await page.keyboard.press("Enter");
  await expect(fields.getByText("VM name moved to position 1 of 2 in Basics.")).toBeAttached();
  const focused = await page.evaluate(() => document.activeElement?.getAttribute("aria-label"));
  expect(focused).toBe("Move VM name down");
  expect(errors).toEqual([]);
});

test("a property group, changed as a new version", async ({ page }) => {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/");
  await page.getByRole("link", { name: "Property groups" }).click();
  await expect(page.getByRole("heading", { name: "Property groups" })).toBeVisible();
  const table = page.getByRole("table", { name: "Property groups" });
  await expect(table.getByRole("row", { name: /Corporate operations/ })).toContainText("cost_center, backup");

  await page.getByRole("button", { name: "New property group" }).click();
  const sheet = page.getByRole("dialog", { name: "New property group" });
  await sheet.locator("#pg-key").fill("network");
  await sheet.locator("#pg-name").fill("Network");
  await sheet.getByRole("button", { name: "Add input" }).click();
  await sheet.locator("#pg-input-0-name").fill("zone");
  await sheet.locator("#pg-input-0-label").fill("Zone");
  await sheet.locator("#pg-input-0-options").fill("dmz, internal");
  await sheet.getByRole("checkbox", { name: "Required" }).check();
  await sheet.getByRole("button", { name: "Add constant" }).click();
  await sheet.locator("#pg-const-0-key").fill("dns_domain");
  await sheet.locator("#pg-const-0-value").fill("corp.example");
  await sheet.getByRole("button", { name: "Create property group", exact: true }).click();
  await expect(page.getByText("Network saved as version 1")).toBeVisible();
  const row = table.getByRole("row", { name: /Network/ });
  await expect(row).toContainText("zone");

  // A typed constant, and the sheet scanned in both themes: the theme
  // button is behind the modal sheet, so the sheet is opened in each.
  const change = page.getByRole("dialog", { name: "Change Network" });
  // A fading toast fails axe's contrast check, and one under the pointer
  // (the sheet's Add was there) doesn't go.
  await page.mouse.move(0, 0);
  await expect(page.locator("[data-sonner-toast]")).toHaveCount(0, { timeout: 10_000 });
  for (const name of ["Light", "Dark"] as const) {
    await theme(page, name);
    await row.getByRole("button", { name: "Actions for Network" }).click();
    await page.getByRole("menuitem", { name: "Change" }).click();
    await expect(change.getByText(/Saving makes version 2/)).toBeVisible();
    await change.getByRole("button", { name: "Add constant" }).click();
    await change.locator("#pg-const-1-key").fill("ttl");
    await change.locator("#pg-const-1-type").click();
    await page.getByRole("option", { name: "Number" }).click();
    await change.locator("#pg-const-1-value").fill("300");
    await scan(page, `a property group's sheet, ${name.toLowerCase()}`);
    if (name === "Light") {
      await page.keyboard.press("Escape");
      await expect(change).toHaveCount(0);
    }
  }
  await change.locator("#pg-const-0-value").fill("new.example");
  await change.getByRole("button", { name: "Save as a new version" }).click();
  await expect(page.getByText("Network saved as version 2")).toBeVisible();
  await expect(row).toContainText("dns_domain, ttl");
  await theme(page, "Light");
  await expect(row.getByRole("cell").nth(1)).toHaveText("2");

  await row.getByRole("button", { name: "Actions for Network" }).click();
  await page.getByRole("menuitem", { name: "Delete" }).click();
  const confirm = page.getByRole("alertdialog", { name: "Delete Network?" });
  await confirm.getByRole("button", { name: "Delete property group" }).click();
  await expect(page.getByText("Network deleted")).toBeVisible();
  await expect(row).toHaveCount(0);
  expect(errors).toEqual([]);
});

test("a project's own secret, opened instead of the global one", async ({ page }) => {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/admin/catalog-items?tab=secrets");
  const region = page.getByRole("region", { name: "Secrets" });
  const sheet = page.getByRole("dialog", { name: "New secret" });
  const store = async (project?: string) => {
    await region.getByRole("button", { name: "New secret" }).click();
    await sheet.locator("#secret-name").fill("itsm-token");
    if (project) {
      await sheet.getByRole("combobox", { name: "Project" }).click();
      await page.getByRole("option", { name: new RegExp(project) }).click();
    }
    await sheet.locator("#secret-value").fill("s3cret");
    await sheet.getByRole("button", { name: "Create secret" }).click();
    await expect(sheet).toHaveCount(0);
  };
  await store();
  await store("Web Platform");

  const rows = region.getByRole("row", { name: /itsm-token/ });
  await expect(rows).toHaveCount(2);
  await expect(rows.nth(0)).toContainText("Every project");
  await expect(rows.nth(0)).not.toContainText("instead of");
  await expect(rows.nth(1)).toContainText("Web Platform");
  await expect(rows.nth(1)).toContainText("Opened instead of the global one");
  await expect(page.getByText("s3cret")).toHaveCount(0);
  // A fading toast fails axe's contrast check.
  await page.mouse.move(0, 0);
  await expect(page.locator("[data-sonner-toast]")).toHaveCount(0, { timeout: 10_000 });
  await bothThemes(page, "secrets, global and a project's own");

  // The sheet and the question in each theme: the theme button is behind them.
  const confirm = page.getByRole("alertdialog", { name: "Remove itsm-token of Web Platform?" });
  for (const name of ["Light", "Dark"] as const) {
    await theme(page, name);
    await region.getByRole("button", { name: "New secret" }).click();
    await sheet.getByRole("combobox", { name: "Project" }).click();
    await page.getByRole("option", { name: /Web Platform/ }).click();
    await scan(page, `a new project secret, ${name.toLowerCase()}`);
    await page.keyboard.press("Escape");
    await expect(sheet).toHaveCount(0);
    await region.getByRole("button", { name: "Actions for itsm-token of Web Platform" }).click();
    await page.getByRole("menuitem", { name: "Remove" }).click();
    await expect(confirm).toContainText("Web Platform's requests open the global itsm-token instead.");
    await scan(page, `removing a project secret, ${name.toLowerCase()}`);
    if (name === "Light") {
      await confirm.getByRole("button", { name: "Cancel" }).click();
      await expect(confirm).toHaveCount(0);
    }
  }
  await confirm.getByRole("button", { name: "Remove secret" }).click();
  await expect(rows).toHaveCount(1);
  await expect(rows).toContainText("Every project");
  await theme(page, "Light");
  expect(errors).toEqual([]);
});

// C2.1 (ADR-0046): a version kept requestable beside the newest, picked by a requester.
test("an earlier version kept requestable is picked on the request form", async ({ page }) => {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/admin/catalog-items");
  await page.getByRole("region", { name: "Items" }).getByRole("link", { name: "RHEL 9 server" }).click();
  await page.getByRole("button", { name: "New draft" }).click();
  await expect(page.getByText("Draft 3.3 made from the published version")).toBeVisible();
  await page.getByRole("tab", { name: "Versions" }).click();
  await page.getByRole("button", { name: "Actions for version 3.3" }).click();
  await page.getByRole("menuitem", { name: "Publish 3.3, keeping 3.2 requestable" }).click();
  await expect(page.getByText("3.3 published beside the others")).toBeVisible();
  await expect(page.getByText("Published 3.3, 3.2")).toBeVisible();
  await bothThemes(page, "an item with two requestable versions");

  // In-app navigation: a reload would start the mock backend afresh.
  await page.getByRole("navigation", { name: "Main" }).getByRole("link", { name: "Catalog", exact: true }).click();
  await page.getByRole("link", { name: "RHEL 9 server", exact: true }).first().click();
  await expect(page.getByText("The newest. Requests use it unless you pick 3.2, still offered.")).toBeVisible();
  await page.getByRole("main").getByRole("link", { name: "Request", exact: true }).click();
  const version = page.getByRole("combobox", { name: "Version" });
  await expect(version).toHaveText("3.3 (newest)");
  await page.getByRole("textbox", { name: "Name (optional)" }).fill("Legacy batch");
  await version.click();
  await page.getByRole("option", { name: "3.2", exact: true }).click();
  await expect(version).toHaveText("3.2");
  await expect(page.getByText("An earlier version: the newest is 3.3.")).toBeVisible();
  await expect(page.getByRole("textbox", { name: "Name (optional)" })).toHaveValue("Legacy batch");
  await bothThemes(page, "a request form on an earlier version");
  await page.getByRole("button", { name: "Submit request" }).click();
  await expect(page).toHaveURL(/\/requests\/REQ-\d+$/);
  await expect(page.getByText("RHEL 9 server 3.2")).toBeVisible();
  expect(errors).toEqual([]);
});

// C2.4: picking a version remounts the form, yet Version keeps the keyboard focus, and the
// Update page keeps the inputs changed.
test("Version keeps the keyboard focus when another version is picked", async ({ page }) => {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/admin/catalog-items");
  await page.getByRole("region", { name: "Items" }).getByRole("link", { name: "RHEL 9 server" }).click();
  await page.getByRole("button", { name: "New draft" }).click();
  await expect(page.getByText("Draft 3.3 made from the published version")).toBeVisible();
  await page.getByRole("tab", { name: "Versions" }).click();
  await page.getByRole("button", { name: "Actions for version 3.3" }).click();
  await page.getByRole("menuitem", { name: "Publish 3.3, keeping 3.2 requestable" }).click();
  await expect(page.getByText("Published 3.3, 3.2")).toBeVisible();

  await page.getByRole("navigation", { name: "Main" }).getByRole("link", { name: "Catalog", exact: true }).click();
  await page.getByRole("link", { name: "RHEL 9 server", exact: true }).first().click();
  await page.getByRole("main").getByRole("link", { name: "Request", exact: true }).click();
  const version = page.getByRole("combobox", { name: "Version" });
  await expect(version).toHaveText("3.3 (newest)");
  await page.getByRole("textbox", { name: "Name (optional)" }).fill("Legacy batch");
  await chooseIn(page, version, "3.2", { focus: true });
  await expect(page.getByText("An earlier version: the newest is 3.3.")).toBeVisible();
  await expect(version).toHaveText("3.2");
  await expect(version).toBeFocused();
  await expect(version).toHaveAttribute("aria-expanded", "false");
  await expect(page.getByRole("textbox", { name: "Name (optional)" })).toHaveValue("Legacy batch");

  await page.getByRole("navigation", { name: "Main" }).getByRole("link", { name: "Deployments", exact: true }).click();
  await page.getByRole("link", { name: "web-dev-api", exact: true }).click();
  await page.getByRole("button", { name: "Actions", exact: true }).click();
  await page.getByRole("menuitem", { name: "Update", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Update web-dev-api", level: 1 })).toBeVisible();
  const to = page.getByRole("combobox", { name: "Version" });
  await expect(to).toHaveText("3.3 (newest)");
  await page.getByRole("textbox", { name: "Role" }).fill("gateway");
  await chooseIn(page, to, "3.2 (this deployment's)", { focus: true });
  await expect(page.getByText("Its own version: the update changes its inputs.")).toBeVisible();
  await expect(to).toBeFocused();
  await expect(to).toHaveAttribute("aria-expanded", "false");
  await expect(page.getByRole("textbox", { name: "Role" })).toHaveValue("gateway");
  expect(errors).toEqual([]);
});
