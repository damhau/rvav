import { expect, type Locator, type Page, test } from "@playwright/test";
import { bothThemes, scan, theme } from "./axe";
import { chooseIn, press, typeInto } from "./keyboard";

// Onboarding plans (X1.5), by keyboard on the mock backend: Deployments
// sends an administrator to the plans, a plan saved with a filter and where
// its VMs go runs, adopts what it matches, and its next run skips those,
// saying why.

// A fading toast fails axe's contrast check.
async function noToast(page: Page) {
  await page.mouse.move(0, 0);
  await expect(page.locator("[data-sonner-toast]")).toHaveCount(0, { timeout: 10_000 });
}

async function fillPlan(page: Page, sheet: Locator) {
  await page.keyboard.type("ERP servers");
  await typeInto(page, sheet.getByRole("textbox", { name: /^Tag/ }), "app/erp");
  await expect(sheet.getByText("12 VMs rvav doesn't manage match now.")).toBeVisible();
  await chooseIn(page, sheet.getByRole("combobox", { name: "Project" }), "Web Platform");
  await chooseIn(page, sheet.getByRole("combobox", { name: "Environment" }), "prd");
  await chooseIn(page, sheet.getByRole("combobox", { name: "Owner" }), "Thomas Nguyen");
  await chooseIn(page, sheet.getByRole("combobox", { name: "Catalog item" }), "RHEL 9 server");
  await typeInto(page, sheet.getByRole("spinbutton", { name: /^Run every/ }), "24");
}

test("a plan saved by keyboard adopts what it matches, then skips it", async ({ page }) => {
  test.setTimeout(150_000);
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/deployments");
  await expect(page.getByRole("heading", { name: "Deployments", level: 1 })).toBeVisible();
  await press(page, page.getByRole("link", { name: "Adopt existing VMs" }));
  await expect(page.getByRole("heading", { name: "Onboarding", level: 1 })).toBeVisible();
  await expect(page.getByRole("tab", { name: "Plans" })).toHaveAttribute("aria-selected", "true");
  await expect(page.getByText("No plan yet")).toBeVisible();
  await bothThemes(page, "onboarding plans, none yet");

  // The theme button is behind the modal sheet: it is opened in each theme.
  const sheet = page.getByRole("dialog", { name: "New plan" });
  await theme(page, "Dark");
  await press(page, page.getByRole("button", { name: "New plan" }));
  await expect(sheet).toBeVisible();
  await fillPlan(page, sheet);
  await scan(page, "a new onboarding plan, dark");
  await page.keyboard.press("Escape");
  await expect(sheet).toHaveCount(0);
  await theme(page, "Light");
  await press(page, page.getByRole("button", { name: "New plan" }));
  await fillPlan(page, sheet);
  await scan(page, "a new onboarding plan, light");
  await press(page, sheet.getByRole("button", { name: "Save plan" }));

  await expect(page.getByRole("heading", { name: "ERP servers", level: 1 })).toBeVisible();
  await expect(page.getByText("12 VMs it matches no deployment has")).toBeVisible();
  await expect(page.getByText("Every 24 hours, next run in 24 hours", { exact: true })).toBeVisible();
  await noToast(page);
  await bothThemes(page, "an onboarding plan, never run");

  const runs = page.getByRole("table", { name: "Runs" });
  await press(page, page.getByRole("button", { name: "Run now" }));
  await expect(runs.getByText("12 adopted, 0 skipped, 0 failed")).toBeVisible({ timeout: 10_000 });
  await expect(page.getByText("0 VMs it matches no deployment has")).toBeVisible();
  // Running, the button stays where the focus is.
  await expect(page.getByRole("button", { name: "Run now" })).toBeFocused();
  // Tabbing past a toast sends the focus back to where it was.
  await noToast(page);
  await press(page, page.getByRole("button", { name: "Run now" }));
  await expect(runs.getByText("0 adopted, 12 skipped, 0 failed")).toBeVisible({ timeout: 10_000 });
  await noToast(page);
  await bothThemes(page, "an onboarding plan's runs");

  const run = page.getByRole("dialog", { name: /^Run of / });
  for (const name of ["Dark", "Light"] as const) {
    await theme(page, name);
    await press(page, runs.getByRole("button", { name: /^Open the run from / }).first());
    const vms = run.getByRole("table", { name: "VMs of the run" });
    await expect(vms.getByText("Skipped")).toHaveCount(12);
    await expect(vms.getByText("erp-app07 has it.")).toBeVisible();
    await scan(page, `an onboarding run, ${name.toLowerCase()}`);
    await page.keyboard.press("Escape");
    await expect(run).toHaveCount(0);
  }

  // The adopted VMs' deployments, from the first run.
  await press(page, runs.getByRole("button", { name: /^Open the run from / }).last());
  await press(page, run.getByRole("link", { name: "erp-app03", exact: true }));
  await expect(page.getByRole("heading", { name: "erp-app03", level: 1 })).toBeVisible();
  await expect(page.getByText("Thomas Nguyen").first()).toBeVisible();

  await page.goBack();
  await press(page, page.getByRole("navigation", { name: "Breadcrumb" }).getByRole("link", { name: "Onboarding" }));
  const plans = page.getByRole("table", { name: "Onboarding plans" });
  await expect(plans.getByRole("link", { name: "ERP servers" })).toBeVisible();
  await expect(plans.getByText("0 adopted, 12 skipped, 0 failed")).toBeVisible();
  await bothThemes(page, "onboarding plans");
  expect(errors).toEqual([]);
});

test("a plan into an existing deployment refuses what its item doesn't have", async ({ page }) => {
  test.setTimeout(90_000);
  await page.goto("/admin/onboarding?tab=plans");
  await press(page, page.getByRole("button", { name: "New plan" }));
  const sheet = page.getByRole("dialog", { name: "New plan" });
  await page.keyboard.type("Old apps");
  await typeInto(page, sheet.getByRole("textbox", { name: /^Folder/ }), "/GVA/vm/legacy");
  await expect(sheet.getByText("8 VMs rvav doesn't manage match now.")).toBeVisible();
  await chooseIn(page, sheet.getByRole("combobox", { name: "Project" }), "Web Platform");
  await chooseIn(page, sheet.getByRole("combobox", { name: "Where they go" }), "Into an existing deployment");
  await expect(sheet.getByRole("combobox", { name: "Owner" })).toHaveCount(0);
  await chooseIn(page, sheet.getByRole("combobox", { name: "Deployment" }), "web-prd-cache");
  await typeInto(page, sheet.getByRole("textbox", { name: /^Component/ }), "web");
  await press(page, sheet.getByRole("button", { name: "Save plan" }));
  await expect(sheet.getByText("RHEL 9 server's blueprint has no components: leave the component out.")).toBeVisible();
  await expect(sheet.getByRole("textbox", { name: /^Component/ })).toBeFocused();
  await scan(page, "an onboarding plan refused");

  await typeInto(page, sheet.getByRole("textbox", { name: /^Component/ }), "");
  await press(page, sheet.getByRole("button", { name: "Save plan" }));
  await expect(page.getByRole("heading", { name: "Old apps", level: 1 })).toBeVisible();
  await press(page, page.getByRole("button", { name: "Run now" }));
  const runs = page.getByRole("table", { name: "Runs" });
  await expect(runs.getByText("8 adopted, 0 skipped, 0 failed")).toBeVisible({ timeout: 10_000 });
  await press(page, page.getByRole("link", { name: "Open web-prd-cache" }));
  await expect(page.getByRole("heading", { name: "web-prd-cache", level: 1 })).toBeVisible();
  await expect(page.getByText("app-old08").first()).toBeVisible();
});
