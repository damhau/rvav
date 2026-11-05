import { expect, test } from "@playwright/test";

// The composite designer (D1.3, ADR-0032) on the mock backend: the path
// D1.5's acceptance takes. The seeded PostgreSQL cluster gets a step on the
// canvas, from the keyboard, its inputs bound to an earlier step's output
// and to a number and a condition set; the YAML beside it follows, an edit
// is undone and redone; the draft is saved, published and requested, and
// the step runs between the nodes and the database's owner.

test("a composite's steps edited on the canvas, published and requested", async ({ page }) => {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/admin/catalog-items");
  await page.getByRole("region", { name: "Items" }).getByRole("link", { name: "PostgreSQL 16 cluster" }).click();
  await page.getByRole("button", { name: "New draft" }).click();
  await expect(page.getByText(/^Draft [\d.]+ made from the published version/)).toBeVisible();
  await page.getByRole("tab", { name: "Definition" }).click();
  const editor = page.getByRole("textbox", { name: /^Definition of PostgreSQL 16 cluster/ });
  const steps = page.getByRole("region", { name: "Steps of PostgreSQL 16 cluster" });
  const node = (name: RegExp) => steps.getByRole("button", { name });

  // The nodes' step, selected from the keyboard: the graph is one Tab stop.
  await node(/^Request\./).focus();
  await page.keyboard.press("ArrowDown");
  await page.keyboard.press("Enter");
  await expect(page.getByRole("region", { name: "Selected: Database nodes" })).toBeVisible();

  // A step after it, from the dialog.
  await page.getByRole("button", { name: "Add a step" }).click();
  const dialog = page.getByRole("dialog", { name: "Add a step" });
  await expect(dialog.getByRole("combobox", { name: "What it runs" })).toBeFocused();
  await page.keyboard.press("Enter");
  await page.getByRole("option", { name: "Monitoring agent" }).click();
  await dialog.getByRole("textbox", { name: "Key" }).fill("monitor");
  await expect(dialog.getByRole("radio", { name: "After Database nodes" })).toBeChecked();
  await dialog.getByRole("button", { name: "Add step" }).click();

  const details = page.getByRole("region", { name: "Selected: Monitoring agent" });
  await expect(details.getByRole("heading", { name: /Monitoring agent/ })).toBeFocused();
  await expect(
    node(/^AWX Monitoring agent\. monitor\. Runs: monitoring-agent\..*Runs after Database nodes/),
  ).toBeVisible();
  await expect(editor).toContainText("- key: monitor");

  // Its inputs: an earlier step's output (not the steps beside or after it), and a number.
  await details.getByRole("combobox", { name: /^Agent/ }).click();
  await expect(page.getByRole("option", { name: /^The database's owner/ })).toHaveCount(0);
  await page.getByRole("option", { name: "Database nodes: The first VM's FQDN" }).click();
  await details.getByRole("combobox", { name: /^Port/ }).click();
  await page.getByRole("option", { name: "A value or CEL expression" }).click();
  const port = details.getByRole("textbox", { name: "Port: value or CEL expression" });
  await expect(port).toBeFocused();
  await page.keyboard.type("9187");
  await page.keyboard.press("Enter");
  await expect(editor).toContainText("monitoring_agent: steps.nodes.fqdn");
  await expect(editor).toContainText("listen_port: 9187");

  // A condition, undone and redone.
  await details.getByRole("textbox", { name: "When" }).fill("inputs.backup_retention_days >= 30");
  await page.keyboard.press("Enter");
  await expect(editor).toContainText("when: inputs.backup_retention_days >= 30");
  await page.getByRole("button", { name: "Undo", exact: true }).click();
  await expect(editor).not.toContainText("when: inputs.backup_retention_days >= 30");
  await page.getByRole("button", { name: "Redo", exact: true }).click();
  await expect(editor).toContainText("when: inputs.backup_retention_days >= 30");
  await expect(page.getByRole("list", { name: "Problems in the steps" })).toHaveCount(0);

  await page.getByRole("button", { name: "Save draft" }).click();
  await expect(page.getByText(/^Draft [\d.]+ saved$/)).toBeVisible();
  await page.getByRole("tab", { name: "Versions" }).click();
  await page.getByRole("button", { name: "Publish" }).click();
  await expect(page.getByText(/^[\d.]+ published$/)).toBeVisible();

  // Requested: the step is on the form's steps, then runs after the nodes.
  await page.getByRole("link", { name: "Catalog", exact: true }).click();
  await page.getByRole("link", { name: /^PostgreSQL 16 cluster/ }).click();
  await expect(
    page.getByRole("region", { name: "Steps of PostgreSQL 16 cluster" }).getByRole("button", {
      name: /^AWX Monitoring agent\./,
    }),
  ).toBeVisible();
  await page.getByRole("textbox", { name: "Database name" }).fill("ledger");
  await page.getByRole("button", { name: "Submit for approval" }).click();
  await expect(page).toHaveURL(/\/requests\/REQ-\d+$/);
  await expect(page.getByRole("region", { name: "Steps" })).toContainText(
    /Database nodes[\s\S]*Monitoring agent · monitoring-agent[\s\S]*The database's owner/,
  );
  expect(errors).toEqual([]);
});
