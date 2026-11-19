import { expect, type Locator, type Page, test } from "@playwright/test";
import { bothThemes, scan, theme } from "./axe";

// The form library (F1.5, ADR-0043), by keyboard on the mock backend: an
// administrator adds a validation to the library and tests it, an item's form
// names it in the designer, its parameter bound to an input; a requester's
// Next to the review step is refused on that input, on its page, then the
// request goes through once the input is fixed.

const definition = `form_schema:
  type: object
  required: [host, owner]
  properties:
    host: { type: string, minLength: 1 }
    owner: { type: string, minLength: 1 }
ui_schema:
  layout:
    kind: wizard
    sections:
      - { key: host, title: Host, columns: 1, fields: [host] }
      - { key: owner, title: Owner, columns: 1, fields: [owner] }
  fields:
    host: { widget: text, label: Host name }
    owner: { widget: text, label: Owner }
blueprint:
  kind: vm
  template: tpl-rhel9.4-2026.10
  guest: { os_family: linux, domain: corp.example }
`;

const check = 'params.name.startsWith("app-") ? {} : {"name": "Start the host name with app-."}';

/** Types into a field reached by its label. */
async function typeInto(field: Locator, text: string) {
  await field.focus();
  await field.page().keyboard.press("Control+A");
  await field.page().keyboard.type(text);
}

/** Opens the focused list with Enter, then the arrow keys to an option, then Enter. */
async function choose(page: Page, select: Locator, name: string | RegExp) {
  await select.focus();
  await page.keyboard.press("Enter");
  const option = page.getByRole("option", { name, exact: typeof name === "string" });
  await expect(page.getByRole("listbox")).toBeVisible();
  for (let i = 0; i < 20; i++) {
    if (await option.evaluate((e) => e === document.activeElement || e.hasAttribute("data-highlighted"))) {
      await page.keyboard.press("Enter");
      await expect(page.getByRole("listbox")).toHaveCount(0);
      return;
    }
    await page.keyboard.press("ArrowDown");
  }
  throw new Error(`no option ${name.toString()}`);
}

/** Follows a link of the main navigation: a reload would start the mock backend afresh. */
async function nav(page: Page, link: string) {
  await page.getByRole("navigation", { name: "Main" }).getByRole("link", { name: link, exact: true }).press("Enter");
  await expect(page.getByRole("heading", { name: link, level: 1 })).toBeVisible();
}

const spoken = (shown: string, hidden: string) => new RegExp(`^${shown} ?, ${hidden}$`);
const heading = (title: string, n: number, of: number) => spoken(title, `step ${n} of ${of}`);

test("a form library validation refuses a request on its input, then lets it through", async ({ page }) => {
  test.setTimeout(150_000);
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));

  // The entry: a CEL check of one parameter, tested before it is saved.
  await page.goto("/");
  await nav(page, "Form library");
  await page.getByRole("button", { name: "New entry" }).press("Enter");
  const sheet = page.getByRole("dialog", { name: "New form library entry" });
  await typeInto(sheet.getByRole("textbox", { name: "Key" }), "hostPrefix");
  await typeInto(sheet.getByRole("textbox", { name: "Name", exact: true }), "Host prefix");
  await choose(page, sheet.getByRole("combobox", { name: "Purpose" }), "Validation");
  await choose(page, sheet.getByRole("combobox", { name: "Kind" }), "CEL expression");
  await typeInto(sheet.getByRole("textbox", { name: "Check" }), check);
  await sheet.getByRole("button", { name: "Add parameter" }).press("Enter");
  const parameters = sheet.getByRole("group", { name: "Parameters" });
  await typeInto(parameters.getByRole("textbox", { name: "Name" }), "name");
  await typeInto(parameters.getByRole("textbox", { name: "Label" }), "Host name");
  await parameters.getByRole("checkbox", { name: /^Required/ }).press("Space");
  const tryIt = sheet.getByRole("group", { name: "Test" });
  await typeInto(tryIt.getByRole("textbox", { name: "Host name" }), "web-01");
  await tryIt.getByRole("button", { name: "Run test" }).press("Enter");
  await expect(tryIt).toContainText("It refuses:name: Start the host name with app-.");
  await typeInto(tryIt.getByRole("textbox", { name: "Host name" }), "app-01");
  await tryIt.getByRole("button", { name: "Run test" }).press("Enter");
  await expect(tryIt).toContainText("It passes.");
  await sheet.getByRole("button", { name: "Add", exact: true }).press("Enter");
  await expect(page.getByText("Host prefix saved as version 1")).toBeVisible();
  const table = page.getByRole("table", { name: "Form library entries" });
  await expect(table).toContainText("hostPrefix");

  // Its side panel, tested again, in each theme: the theme button is behind the modal sheet.
  await expect(page.locator("[data-sonner-toast]")).toHaveCount(0, { timeout: 10_000 });
  const change = page.getByRole("dialog", { name: "Change Host prefix" });
  for (const name of ["Light", "Dark"] as const) {
    await theme(page, name);
    await table.getByRole("button", { name: "Actions for Host prefix" }).press("Enter");
    await expect(page.getByRole("menuitem", { name: "Change or test" })).toBeFocused();
    await page.keyboard.press("Enter");
    await expect(change.getByText(/Saving makes version 2/)).toBeVisible();
    const trial = change.getByRole("group", { name: "Test" });
    await typeInto(trial.getByRole("textbox", { name: "Host name" }), "db-01");
    await trial.getByRole("button", { name: "Run test" }).press("Enter");
    await expect(trial).toContainText("It refuses:name: Start the host name with app-.");
    await scan(page, `a form library entry's side panel, ${name.toLowerCase()}`);
    await page.keyboard.press("Escape");
    await expect(change).toHaveCount(0);
  }
  await theme(page, "Light");

  // An item whose form names it, its parameter the host name.
  await nav(page, "Catalog items");
  await page.getByRole("button", { name: "New item" }).press("Enter");
  const dialog = page.getByRole("dialog", { name: "New item" });
  await typeInto(dialog.getByRole("textbox", { name: "Name" }), "App host");
  await typeInto(dialog.getByRole("textbox", { name: "Slug" }), "app-host");
  await typeInto(dialog.getByRole("textbox", { name: "Summary" }), "A host whose name starts with app-.");
  await dialog.getByRole("button", { name: "Create" }).press("Enter");
  await page.getByRole("tab", { name: "Definition" }).press("Enter");
  const editor = page.getByRole("textbox", { name: "Definition of App host 1.0" });
  await expect(editor).toContainText("form_schema:");
  await editor.focus();
  await page.keyboard.press("Control+A");
  await page.keyboard.insertText(definition);
  await page.getByRole("button", { name: "Save draft" }).press("Enter");
  await expect(page.getByText("Draft 1.0 saved")).toBeVisible();

  await page.getByRole("tab", { name: "Form designer" }).press("Enter");
  const fields = page.getByRole("region", { name: "Fields" });
  const status = page.getByRole("status").filter({ hasText: /^Draft / });
  await fields.getByRole("button", { name: /^Form validations/ }).press("Enter");
  const props = page.getByRole("region", { name: "Form validations" });
  await props.getByRole("button", { name: "Add one from the form library" }).press("Enter");
  const added = props.getByRole("group", { name: "check1" });
  const entry = added.getByRole("combobox", { name: "Entry" });
  await expect(entry).toBeFocused();
  await choose(page, entry, "Host prefix (hostPrefix)");
  await expect(added.getByRole("combobox", { name: "Version" })).toHaveText("1 (latest)");
  const name = added.getByRole("textbox", { name: "Host name" });
  await typeInto(name, "inputs.host");
  await page.keyboard.press("Enter");
  await expect(status).toHaveText("Draft 1.0: saved");
  await expect(fields.getByRole("button", { name: /^Form validations/ })).toContainText("1");
  await bothThemes(page, "designer, a form library validation");

  await page.getByRole("tab", { name: "Sharing" }).press("Enter");
  await page.getByRole("radio", { name: /Chosen projects/ }).focus();
  await page.keyboard.press("Space");
  await page.getByRole("checkbox", { name: "Web Platform" }).press("Space");
  await page.getByRole("button", { name: "Save projects" }).press("Enter");
  await expect(page.getByText("Projects saved")).toBeVisible();
  await page.getByRole("tab", { name: "Versions" }).press("Enter");
  await page.getByRole("button", { name: "Publish" }).press("Enter");
  await expect(page.getByText("1.0 published")).toBeVisible();

  // The request: refused on the host name, on its page, before the review.
  await nav(page, "Catalog");
  await page.getByRole("link", { name: "Request App host" }).press("Enter");
  await expect(page.getByRole("heading", { name: "Request App host" })).toBeVisible();
  await expect(page.getByText("No approval needed")).toBeVisible();
  const host = page.getByRole("textbox", { name: "Host name" });
  await typeInto(host, "web-01");
  const next = page.getByRole("button", { name: "Next" });
  await next.press("Enter");
  await expect(page.getByRole("heading", { name: heading("Owner", 2, 3) })).toBeFocused();
  await typeInto(page.getByRole("textbox", { name: "Owner" }), "jdoe");
  await next.press("Enter");
  await expect(host).toBeFocused();
  await expect(host).toHaveAccessibleDescription(/Start the host name with app-\./);
  await bothThemes(page, "request form, refused by a library validation");

  await typeInto(host, "app-web");
  await next.press("Enter");
  await expect(page.getByRole("heading", { name: heading("Owner", 2, 3) })).toBeFocused();
  await next.press("Enter");
  await expect(page.getByRole("heading", { name: heading("Review", 3, 3) })).toBeFocused();
  await expect(page.getByRole("region", { name: heading("Review", 3, 3) })).toContainText("app-web");
  await page.getByRole("button", { name: "Submit request" }).press("Enter");
  await expect(page).toHaveURL(/\/requests\/REQ-\d+$/);
  expect(errors).toEqual([]);
});
