import { expect, test } from "@playwright/test";
import { bothThemes } from "./axe";

// A form's layouts (F1.3): built as tabs and then as a wizard in the
// designer, its pages reordered there, published, and requested by
// keyboard. Next checks each page and skips the one its rules hide, focus
// moves to each new page's heading, and the review masks the password.

const definition = `form_schema:
  type: object
  required: [name, database, password]
  properties:
    name: { type: string, minLength: 1 }
    with_db: { type: boolean }
    database: { type: string, pattern: "^[a-z_]{3,}$" }
    password: { type: string, minLength: 12 }
ui_schema:
  layout:
    kind: page
    sections:
      - { key: server, title: Server, columns: 2, fields: [name, with_db] }
      - { key: access, title: Access, columns: 1, fields: [password] }
      - { key: db, title: Database, columns: 1, fields: [database] }
  fields:
    name: { widget: text, label: Name }
    with_db: { widget: toggle, label: Add a database }
    database: { widget: text, label: Database name, rules: { visible: "inputs.with_db == true" } }
    password: { widget: secret, label: Admin password }
initial_values: { with_db: false }
blueprint:
  kind: vm
  naming: "{project}-{environment}-{seq}"
  template: tpl-rhel9.4-2026.10
  guest: { os_family: linux, domain: corp.example }
  bindings: { vcpu: "2", memory_gb: "4" }
`;

// Accessible names put a space before a visually hidden span: "Server , step 1 of 3".
const spoken = (shown: string, hidden: string) => new RegExp(`^${shown} ?, ${hidden}$`);
const heading = (title: string, n: number, of: number) => spoken(title, `step ${n} of ${of}`);

test("a form as tabs and as a wizard, built in the designer and requested by keyboard", async ({ page }) => {
  test.setTimeout(120_000);
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/admin/catalog-items");
  await page.getByRole("button", { name: "New item" }).click();
  const dialog = page.getByRole("dialog", { name: "New item" });
  await dialog.getByRole("textbox", { name: "Name" }).fill("Database server");
  await dialog.getByRole("textbox", { name: "Slug" }).fill("database-server");
  await dialog.getByRole("textbox", { name: "Summary" }).fill("A server, with a database if you want one.");
  await dialog.getByRole("combobox", { name: "Category" }).click();
  await page.getByRole("option", { name: "Compute" }).click();
  await dialog.getByRole("button", { name: "Create item" }).click();

  await page.getByRole("tab", { name: "Definition" }).click();
  const editor = page.getByRole("textbox", { name: "Definition of Database server 1.0" });
  await expect(editor).toContainText("form_schema:");
  await editor.click();
  await page.keyboard.press("Control+A");
  await page.keyboard.insertText(definition);
  await page.getByRole("button", { name: "Save draft" }).click();
  await expect(page.getByText("Draft 1.0 saved")).toBeVisible();

  // Tabs: one per section that shows, in the tablist pattern; a tab with a problem says so.
  await page.getByRole("tab", { name: "Form designer" }).click();
  const fields = page.getByRole("region", { name: "Fields" });
  const preview = page.getByRole("region", { name: "Preview" });
  const status = page.getByRole("status").filter({ hasText: /^Draft / });
  const layout = fields.getByRole("combobox", { name: "Layout" });
  await layout.click();
  await page.getByRole("option", { name: "Tabs" }).click();
  await expect(status).toHaveText("Draft 1.0: saved");
  const tabs = preview.getByRole("tablist", { name: "Form sections" });
  await expect(tabs.getByRole("tab")).toHaveText(["Server", "Access"]);
  await tabs.getByRole("tab", { name: "Server" }).focus();
  await page.keyboard.press("ArrowRight");
  await expect(tabs.getByRole("tab", { name: "Access" })).toBeFocused();
  await expect(tabs.getByRole("tab", { name: "Access" })).toHaveAttribute("aria-selected", "true");
  await expect(preview.getByLabel("Admin password", { exact: true })).toBeVisible();
  await preview.getByRole("button", { name: "Show them" }).click();
  await expect(tabs.getByRole("tab", { name: spoken("Server", "1 input needs attention") })).toBeVisible();
  await bothThemes(page, "designer, tabs");

  // A wizard, its Database page moved before Access.
  await layout.click();
  await page.getByRole("option", { name: "Wizard" }).click();
  await expect(layout).toHaveAccessibleDescription(/A page per section/);
  await fields.getByRole("button", { name: "Move page Database up" }).focus();
  await page.keyboard.press("Enter");
  await expect(fields.getByRole("group", { name: /^Page / })).toHaveCount(3);
  await expect(fields.getByRole("group", { name: /^Page / }).nth(1)).toHaveAccessibleName("Page Database");
  await expect(status).toHaveText("Draft 1.0: saved");
  await expect(preview.getByRole("list", { name: "Steps" }).getByRole("listitem")).toHaveText([
    /Server/,
    /Access/,
    /Review/,
  ]);
  await bothThemes(page, "designer, wizard");

  await page.getByRole("tab", { name: "Sharing" }).click();
  await page.getByRole("radio", { name: /Chosen projects/ }).check();
  await page.getByRole("checkbox", { name: "Web Platform" }).click();
  await page.getByRole("button", { name: "Save projects" }).click();
  await expect(page.getByText("Projects saved")).toBeVisible();
  await page.getByRole("tab", { name: "Versions" }).click();
  await page.getByRole("button", { name: "Publish" }).click();
  await expect(page.getByText("1.0 published")).toBeVisible();

  await page.getByRole("navigation", { name: "Main" }).getByRole("link", { name: "Catalog", exact: true }).click();
  await page.getByRole("link", { name: "Request Database server" }).click();
  await expect(page.getByRole("heading", { name: "Request Database server" })).toBeVisible();
  await expect(page.getByText("No approval needed")).toBeVisible();
  const steps = page.getByRole("list", { name: "Steps" }).getByRole("listitem");
  await expect(steps).toHaveText([/Server/, /Access/, /Review/]);
  await expect(page.getByRole("button", { name: /^Submit/ })).toHaveCount(0);

  // Next checks the page: the empty name keeps it, and takes the focus.
  const next = page.getByRole("button", { name: "Next" });
  await next.press("Enter");
  const name = page.getByRole("textbox", { name: "Name", exact: true });
  await expect(name).toBeFocused();
  await expect(name).toHaveAttribute("aria-invalid", "true");
  await page.keyboard.type("db01");
  await next.press("Enter");
  await expect(page.getByRole("heading", { name: heading("Access", 2, 3) })).toBeFocused();

  // Back, and a database after all: its page comes before Access.
  await page.getByRole("button", { name: "Back" }).press("Enter");
  await expect(page.getByRole("heading", { name: heading("Server", 1, 3) })).toBeFocused();
  await page.getByRole("checkbox", { name: "Add a database" }).press("Space");
  await next.press("Enter");
  await expect(page.getByRole("heading", { name: heading("Database", 2, 4) })).toBeFocused();
  await expect(steps).toHaveText([/Server ?, done/, /Database/, /Access/, /Review/]);
  await page.keyboard.press("Tab");
  await expect(page.getByRole("textbox", { name: "Database name" })).toBeFocused();
  await page.keyboard.type("orders");
  await next.press("Enter");
  await expect(page.getByRole("heading", { name: heading("Access", 3, 4) })).toBeFocused();
  await page.keyboard.press("Tab");
  await expect(page.getByLabel("Admin password", { exact: true })).toBeFocused();
  await page.keyboard.type("correct-horse-battery");
  await next.press("Enter");

  const review = page.getByRole("region", { name: heading("Review", 4, 4) });
  await expect(page.getByRole("heading", { name: heading("Review", 4, 4) })).toBeFocused();
  const server = review.getByRole("region", { name: "Server" });
  await expect(server.getByRole("definition")).toHaveText(["Development", "db01", "Yes"]);
  await expect(review.getByRole("region", { name: "Database" })).toContainText("orders");
  await expect(review.getByRole("region", { name: "Access" })).toContainText("••••");
  await expect(review).not.toContainText("correct-horse-battery");
  await bothThemes(page, "request form, review");

  await page.getByRole("button", { name: "Submit request" }).press("Enter");
  await expect(page).toHaveURL(/\/requests\/REQ-\d+$/);
  await expect(page.getByText("correct-horse-battery")).toHaveCount(0);
  expect(errors).toEqual([]);
});
