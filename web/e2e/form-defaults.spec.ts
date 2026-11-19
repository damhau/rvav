import { expect, test } from "@playwright/test";
import { bothThemes } from "./axe";

// Editable defaults (F1.4): a default rule set in the designer, a REST and a
// CEL source that give fields their defaults, then the form requested by
// keyboard. The defaults fill the fields until the requester changes them,
// an edited field keeps its value when what its default reads changes, and
// the review lists the defaults that will be sent.

const definition = `form_schema:
  type: object
  required: [app]
  properties:
    app: { type: string, minLength: 1 }
    host: { type: string }
    owner: { type: string }
    cores: { type: integer, minimum: 1, maximum: 16 }
ui_schema:
  layout:
    kind: wizard
    sections:
      - { key: app, title: Application, columns: 1, fields: [app, host] }
      - { key: sizing, title: Sizing, columns: 1, fields: [owner, cores] }
  fields:
    app: { widget: text, label: Application }
    host: { widget: text, label: Host name }
    owner:
      widget: text
      label: Owner
      depends_on: [app]
      value_source:
        kind: rest
        params: { url: "https://cmdb.corp.example/owners?app=\${inputs.app}", value: "$.login" }
    cores:
      widget: number
      label: Cores
      value_source: { kind: cel, params: { value: 'environment == "prd" ? 8 : 2' } }
blueprint:
  kind: vm
  template: tpl-rhel9.4-2026.10
  guest: { os_family: linux, domain: corp.example }
`;

const spoken = (shown: string, hidden: string) => new RegExp(`^${shown} ?, ${hidden}$`);
const heading = (title: string, n: number, of: number) => spoken(title, `step ${n} of ${of}`);

test("a default set in the designer fills a field until the requester changes it", async ({ page }) => {
  test.setTimeout(120_000);
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/admin/catalog-items");
  await page.getByRole("button", { name: "New item" }).click();
  const dialog = page.getByRole("dialog", { name: "New item" });
  await dialog.getByRole("textbox", { name: "Name" }).fill("App server");
  await dialog.getByRole("textbox", { name: "Slug" }).fill("app-server");
  await dialog.getByRole("textbox", { name: "Summary" }).fill("A server named after its application.");
  await dialog.getByRole("button", { name: "Create" }).click();

  await page.getByRole("tab", { name: "Definition" }).click();
  const editor = page.getByRole("textbox", { name: "Definition of App server 1.0" });
  await expect(editor).toContainText("form_schema:");
  await editor.click();
  await page.keyboard.press("Control+A");
  await page.keyboard.insertText(definition);
  await page.getByRole("button", { name: "Save draft" }).click();
  await expect(page.getByText("Draft 1.0 saved")).toBeVisible();

  // The host's default, from the application's name; a default that clashes with an initial value is refused.
  await page.getByRole("tab", { name: "Form designer" }).click();
  const fields = page.getByRole("region", { name: "Fields" });
  const props = page.getByRole("region", { name: "Properties" });
  const preview = page.getByRole("region", { name: "Preview" });
  const status = page.getByRole("status").filter({ hasText: /^Draft / });
  await fields.getByRole("button", { name: /^Host name/ }).click();
  await props.getByRole("textbox", { name: "Initial value" }).fill("web");
  await props.getByRole("textbox", { name: "Initial value" }).press("Enter");
  await expect(status).toHaveText("Draft 1.0: saved");
  const rule = props.getByRole("textbox", { name: "Default", exact: true });
  await expect(rule).toHaveAccessibleDescription(/until the requester changes it/);
  await rule.fill('inputs.app + "-01"');
  await rule.press("Enter");
  await expect(status).toContainText("not saved, 1 problem to fix");
  await expect(
    props.getByText("The field has an initial value too: keep the initial value or the default."),
  ).toBeVisible();
  await props.getByRole("textbox", { name: "Initial value" }).fill("");
  await props.getByRole("textbox", { name: "Initial value" }).press("Enter");
  await expect(status).toHaveText("Draft 1.0: saved");

  await preview.getByRole("textbox", { name: "Application" }).fill("web");
  await expect(preview.getByRole("textbox", { name: "Host name" })).toHaveValue("web-01");
  await fields.getByRole("button", { name: /^Cores/ }).click();
  await expect(
    props.getByText("The source gives the field its default, which the requester can change."),
  ).toBeVisible();
  await expect(props.getByRole("textbox", { name: "Options" })).toHaveCount(0);
  await bothThemes(page, "designer, a default");

  await page.getByRole("tab", { name: "Sharing" }).click();
  await page.getByRole("radio", { name: /Chosen projects/ }).check();
  await page.getByRole("checkbox", { name: "Web Platform" }).click();
  await page.getByRole("button", { name: "Save projects" }).click();
  await expect(page.getByText("Projects saved")).toBeVisible();
  await page.getByRole("tab", { name: "Versions" }).click();
  await page.getByRole("button", { name: "Publish" }).click();
  await expect(page.getByText("1.0 published")).toBeVisible();

  await page.getByRole("navigation", { name: "Main" }).getByRole("link", { name: "Catalog", exact: true }).click();
  await page.getByRole("link", { name: "Request App server" }).click();
  await expect(page.getByRole("heading", { name: "Request App server" })).toBeVisible();
  await expect(page.getByText("No approval needed")).toBeVisible();

  // By keyboard: the host follows the application until the requester gives it a name of their own.
  const app = page.getByRole("textbox", { name: "Application" });
  const host = page.getByRole("textbox", { name: "Host name" });
  await app.focus();
  await page.keyboard.type("shop");
  await expect(host).toHaveValue("shop-01");
  await page.keyboard.press("Tab");
  await expect(host).toBeFocused();
  await page.keyboard.press("Control+A");
  await page.keyboard.type("shop-db");
  await page.keyboard.press("Shift+Tab");
  await expect(app).toBeFocused();
  // Tabbing in selects the text: add to it.
  await page.keyboard.press("End");
  await page.keyboard.type("s");
  await expect(page.getByText("No approval needed")).toBeVisible();
  await expect(host).toHaveValue("shop-db");

  const next = page.getByRole("button", { name: "Next" });
  await next.press("Enter");
  await expect(page.getByRole("heading", { name: heading("Sizing", 2, 3) })).toBeFocused();
  await expect(page.getByRole("textbox", { name: "Owner" })).toHaveValue("svc-01");
  await expect(page.getByRole("spinbutton", { name: "Cores" })).toHaveValue("2");
  await next.press("Enter");

  const review = page.getByRole("region", { name: heading("Review", 3, 3) });
  await expect(page.getByRole("heading", { name: heading("Review", 3, 3) })).toBeFocused();
  await expect(review.getByRole("region", { name: "Application" }).getByRole("definition")).toHaveText([
    "Development",
    "shops",
    "shop-db",
  ]);
  await expect(review.getByRole("region", { name: "Sizing" }).getByRole("definition")).toHaveText(["svc-01", "2"]);
  await bothThemes(page, "request form, defaults in the review");

  await page.getByRole("button", { name: "Submit request" }).press("Enter");
  await expect(page).toHaveURL(/\/requests\/REQ-\d+$/);
  expect(errors).toEqual([]);
});
