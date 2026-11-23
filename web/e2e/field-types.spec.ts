import { expect, test } from "@playwright/test";
import { bothThemes, scan } from "./axe";

// Every field type the designer offers (F1.1, F1.2): a form with one of each,
// built in the designer, passes axe in both themes in the designer's
// preview and as members request it, and the new types work by keyboard. A
// picture uploaded shows in the image field, and a file attached comes back
// from the request's page as a download.

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
  "Search",
  "Tree",
  "File upload",
  "Date",
  "Date and time",
  "Tags",
  "Key and value pairs",
  "Table",
  "Dual list",
  "Group of fields",
  "Secret",
  "Information text",
  "Image",
  "Link",
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

// A one-pixel PNG, uploaded as the image field's picture.
const dot = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8DwHwAFBQIAX8jx0gAAAABJRU5ErkJggg==";

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
  await dialog.getByRole("button", { name: "Create item" }).click();
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
  const props = page.getByRole("region", { name: "Properties" });
  await fields.getByRole("button", { name: /^Dual list/ }).click();
  await props.getByRole("textbox", { name: "Options" }).fill("web | Web team\ndb | Database team\nnet | Network team");
  await props.getByRole("textbox", { name: "Options" }).press("Tab");
  await expect(preview.getByRole("option", { name: "Database team" })).toBeVisible();
  await fields.getByRole("button", { name: /^Tree/ }).click();
  await props.getByRole("textbox", { name: "Options" }).fill("dmz | DMZ\ndmz-web | Web zone | dmz\nlan | LAN");
  await props.getByRole("textbox", { name: "Options" }).press("Tab");
  await expect(preview.getByRole("treeitem", { name: "DMZ", exact: true })).toHaveAttribute("aria-expanded", "false");
  await fields.getByRole("button", { name: /^File upload/ }).click();
  await props.getByRole("textbox", { name: "Accepted types" }).fill(".csv, .txt");
  await props.getByRole("textbox", { name: "Accepted types" }).press("Enter");
  await expect(preview.getByText("Takes .csv, .txt, 10 MB at most.")).toBeVisible();
  await fields.getByRole("button", { name: /^Search/ }).click();
  await props.getByRole("combobox", { name: "Value source" }).click();
  await page.getByRole("option", { name: "REST service" }).click();
  const url = props.getByRole("textbox", { name: "URL" });
  await url.fill("https://cmdb.corp.example/services?q=${search}");
  await url.press("Enter");
  await props.getByRole("checkbox", { name: "The source searches by the typed text" }).click();
  await expect(props.getByRole("checkbox", { name: "The source searches by the typed text" })).toBeChecked();
  await fields.getByRole("button", { name: /^Image/ }).click();
  const picture = page.waitForEvent("filechooser");
  await props.getByRole("button", { name: "Upload a picture" }).press("Enter");
  await (await picture).setFiles({ name: "dot.png", mimeType: "image/png", buffer: Buffer.from(dot, "base64") });
  await expect(props.getByRole("textbox", { name: "Address" })).toHaveValue(/^upload:img_/);
  await props.getByRole("textbox", { name: "Text alternative" }).fill("A red dot");
  await expect(preview.getByRole("img", { name: "A red dot" })).toHaveAttribute("src", /^blob:/);
  await fields.getByRole("button", { name: /^Link/ }).click();
  await props.getByRole("textbox", { name: "Address" }).fill("/requests");
  await props.getByRole("textbox", { name: "Address" }).press("Tab");
  await expect(preview.getByRole("link", { name: "Link (opens in a new tab)" })).toHaveAttribute("href", "/requests");
  await expect(status).toHaveText("Draft 1.0: saved");
  await expect(preview.getByLabel("Secret", { exact: true })).toBeVisible();
  await bothThemes(page, "designer");

  await page.getByRole("tab", { name: "Sharing" }).click();
  await page.getByRole("radio", { name: /Chosen projects/ }).check();
  await page.getByRole("checkbox", { name: "Web Platform" }).click();
  await page.getByRole("button", { name: "Save projects" }).click();
  await expect(page.getByText("Projects saved")).toBeVisible();
  await page.getByRole("tab", { name: "Versions" }).click();
  await page.getByRole("button", { name: "Publish" }).click();
  await expect(page.getByText("1.0 published")).toBeVisible();

  await page.getByRole("navigation", { name: "Main" }).getByRole("link", { name: "Catalog", exact: true }).click();
  await page.getByRole("link", { name: "Request Every field" }).click();
  await expect(page.getByRole("heading", { name: "Request Every field" })).toBeVisible();
  await expect(page.getByRole("img", { name: "A red dot" })).toHaveAttribute("src", /^blob:/);
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
  const available = page.getByRole("listbox", { name: "Dual list Available" });
  await available.focus();
  await available.press("End");
  await available.press("Enter");
  await available.press("Home");
  await available.press("Enter");
  const chosen = page.getByRole("listbox", { name: "Dual list Chosen, in order" });
  await expect(chosen.getByRole("option")).toHaveText(["Network team", "Web team"]);
  await chosen.focus();
  await chosen.press("End");
  await chosen.press("Alt+ArrowUp");
  await expect(chosen.getByRole("option")).toHaveText(["Web team", "Network team"]);
  await page.getByRole("group", { name: "Group of fields" }).getByRole("textbox", { name: "Value" }).fill("one");
  await page.getByLabel("Date", { exact: true }).fill("2026-12-01");
  const user = page.getByRole("combobox", { name: "User" });
  await user.pressSequentially("ngu", { delay: 30 });
  await expect(page.getByRole("status").filter({ hasText: "1 person found" })).toBeVisible();
  await user.press("ArrowDown");
  await user.press("Enter");
  await expect(user).toHaveValue("Thomas Nguyen");
  const search = page.getByRole("combobox", { name: "Search", exact: true });
  await search.pressSequentially("01", { delay: 30 });
  await expect(page.getByRole("status").filter({ hasText: "1 match" })).toBeVisible();
  await search.press("Enter");
  await expect(search).toHaveValue("Service 01");
  const tree = page.getByRole("tree", { name: "Tree" });
  await tree.getByRole("treeitem", { name: "DMZ", exact: true }).focus();
  await page.keyboard.press("ArrowRight");
  await page.keyboard.press("ArrowRight");
  await expect(tree.getByRole("treeitem", { name: "Web zone" })).toBeFocused();
  await page.keyboard.press("Enter");
  await expect(tree).toHaveAccessibleDescription("Chosen: DMZ › Web zone");
  const chooser = page.waitForEvent("filechooser");
  await page.getByRole("button", { name: "File upload Choose a file" }).press("Enter");
  await (
    await chooser
  ).setFiles({ name: "rules.csv", mimeType: "text/csv", buffer: Buffer.from("port,source\n443,10.0.0.0/8\n") });
  await expect(page.getByRole("button", { name: "File upload Replace" })).toBeVisible();
  await expect(page.getByText("rules.csv", { exact: true })).toBeVisible();
  await page.getByRole("combobox", { name: "Deployment" }).click();
  await page.getByRole("option", { name: /^web-dev-api/ }).click();
  await page.getByRole("textbox", { name: "Name", exact: true }).fill("every");
  await scan(page, "request form filled in");
  await page.getByRole("button", { name: "Submit request" }).click();
  await expect(page).toHaveURL(/\/requests\/REQ-\d+$/);
  await expect(page.getByText("hunter2")).toHaveCount(0);
  const download = page.waitForEvent("download");
  await page.getByRole("button", { name: "rules.csv 27 B" }).click();
  expect((await download).suggestedFilename()).toBe("rules.csv");
  expect(errors).toEqual([]);
});
