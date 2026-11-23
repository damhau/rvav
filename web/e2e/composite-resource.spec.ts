import { expect, type Page, test } from "@playwright/test";
import { bothThemes } from "./axe";
import { choose } from "./keyboard";

// A composite's custom resource step (E1.4, ADR-0056) on the mock backend:
// added in the designer from the keyboard by its type, among the types turned
// on, with no undo of its own; after two VM steps it names the one whose
// deployment it joins, and follows that step's new key. Checked with axe in
// both themes.

async function addStep(page: Page, runs: string | RegExp, key: string, title: string) {
  await page.getByRole("button", { name: "Add a step", exact: true }).click();
  const add = page.getByRole("dialog", { name: "Add a step" });
  await expect(add.getByRole("combobox", { name: "What it runs" })).toBeFocused();
  await choose(page, runs, { click: true, exact: false });
  await add.getByRole("textbox", { name: "Key" }).fill(key);
  await add.getByRole("textbox", { name: "Title" }).fill(title);
  await add.getByRole("button", { name: "Add step" }).click();
  const details = page.getByRole("region", { name: `Selected: ${title}` });
  await expect(details.getByRole("heading", { name: title })).toBeFocused();
  return details;
}

test("a custom resource step, by its type, in the deployment of the VM step it names", async ({ page }) => {
  test.slow();
  await page.goto("/admin/catalog-items");
  await page.getByRole("button", { name: "New item" }).click();
  const dialog = page.getByRole("dialog", { name: "New item" });
  await dialog.getByRole("textbox", { name: "Name" }).fill("Balanced pair");
  await dialog.getByRole("textbox", { name: "Slug" }).fill("balanced-pair");
  await dialog.getByRole("textbox", { name: "Summary" }).fill("Two servers behind a load balancer.");
  await dialog.getByRole("combobox", { name: "Type" }).click();
  await page.getByRole("option", { name: /composite/i }).click();
  await dialog.getByRole("button", { name: "Create item" }).click();
  await expect(page.getByRole("heading", { name: "Balanced pair" })).toBeVisible();
  await page.getByRole("tab", { name: "Definition" }).click();

  await addStep(page, /^RHEL 9 server/, "web", "Web server");
  await addStep(page, /^Windows Server 2025/, "app", "App server");
  const lb = await addStep(page, /^Load balancer/, "lb", "Front");
  await expect(lb.getByRole("group", { name: "Undo" })).toHaveCount(0);
  await expect(lb).toContainText("If a later step fails, its type's delete step undoes it.");

  // Two VM steps come before it: it names the one whose deployment it joins.
  const deployment = lb.getByRole("combobox", { name: "Deployment" });
  await deployment.focus();
  await choose(page, /^Web server/, { click: true });
  const editor = page.getByRole("textbox", { name: /^Definition of Balanced pair/ });
  await expect(editor).toContainText("type: load-balancer");
  await expect(editor).toContainText("deployment: web");
  await expect(page.getByRole("application").getByRole("button", { name: /^Resource Front\./ })).toBeVisible();

  await bothThemes(page, "the designer, a custom resource step selected");
});
