import { expect, test } from "@playwright/test";

// Day-2 actions against the mock backend: each runs as a request, the VM
// shows the result, and the deployment's history lists it.

test("power off a VM, snapshot it, then delete the deployment", async ({ page }) => {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));

  await page.goto("/deployments");
  await page.getByRole("link", { name: "web-dev-api", exact: true }).click();
  await expect(page.getByRole("heading", { name: "web-dev-api" })).toBeVisible();

  // Power off from the VM's menu: a confirmation first.
  await page.getByRole("button", { name: "More actions for web-dev-api004" }).click();
  await page.getByRole("menuitem", { name: /^Power off/ }).click();
  const dialog = page.getByRole("dialog", { name: "Power off · web-dev-api004" });
  await expect(dialog.getByText(/like pulling the plug/)).toBeVisible();
  await dialog.getByRole("button", { name: "Power off" }).click();
  await expect(dialog).toBeHidden();
  await expect(page.getByText(/^REQ-\d+ has started\.$/)).toBeVisible();
  await expect(page.getByText(/vm-\d+ · off/)).toBeVisible({ timeout: 15_000 });
  // Now it can be powered on, not off.
  await page.getByRole("button", { name: "More actions for web-dev-api004" }).click();
  await expect(page.getByRole("menuitem", { name: /^Power on/ })).toBeEnabled();
  await expect(page.getByRole("menuitem", { name: /^Power off/ })).toHaveAttribute("data-disabled", "");
  await page.keyboard.press("Escape");

  // A snapshot, from the Actions panel, with its form.
  await page.getByRole("button", { name: /^Take a snapshot/ }).click();
  const snap = page.getByRole("dialog", { name: "Take a snapshot · web-dev-api004" });
  await snap.getByRole("textbox", { name: "Name" }).fill("before-cleanup");
  await snap.getByRole("button", { name: "Take a snapshot" }).click();
  await expect(snap).toBeHidden();

  // The history lists both, and the creating request.
  const sections = page.getByRole("navigation", { name: "Deployment sections" });
  await sections.getByRole("link", { name: "History" }).click();
  const history = page.getByRole("region", { name: "History" });
  await expect(history.getByRole("link", { name: "Power off" })).toBeVisible();
  await expect(history.getByRole("link", { name: "Take a snapshot" })).toBeVisible();
  await expect(history.getByText("Succeeded").first()).toBeVisible({ timeout: 15_000 });

  // Delete asks for the name.
  await sections.getByRole("link", { name: "Overview" }).click();
  await page.getByRole("button", { name: /^Delete/ }).click();
  const del = page.getByRole("dialog", { name: "Delete · web-dev-api" });
  const confirm = del.getByRole("button", { name: "Delete" });
  await expect(confirm).toBeDisabled();
  await del.getByRole("textbox", { name: /to confirm/ }).fill("web-dev-api");
  await confirm.click();
  await expect(page.getByText("Deleted", { exact: true }).first()).toBeVisible({ timeout: 15_000 });
  expect(errors).toEqual([]);
});

test("shut down the guests of two deployments at once", async ({ page }) => {
  await page.goto("/deployments");
  await page.getByRole("checkbox", { name: "Select web-dev-api" }).check();
  await page.getByRole("checkbox", { name: "Select web-prd-cache" }).check();
  const bar = page.getByRole("region", { name: "Bulk actions" });
  await expect(bar.getByText("2 deployments selected")).toBeVisible();
  await bar.getByRole("button", { name: "Shut down guest" }).click();
  const dialog = page.getByRole("dialog", { name: "Shut down guest · 2 deployments" });
  await dialog.getByRole("button", { name: "Shut down guest" }).click();
  const results = dialog.getByRole("list", { name: "Results" });
  await expect(results.getByText(/REQ-\d+ started/)).toHaveCount(3);
  await dialog.getByRole("button", { name: "Close", exact: true }).last().click();
  await expect(bar).toBeHidden();
});
