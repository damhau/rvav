import { expect, test, type Page } from "@playwright/test";

// Screenshots go to test-results; UPDATE_SCREENSHOTS=1 refreshes the
// committed ones in e2e/__screenshots__.
const shots = process.env.UPDATE_SCREENSHOTS ? "e2e/__screenshots__" : "test-results/screenshots";

async function setTheme(page: Page, theme: "light" | "dark") {
  await page.evaluate((t) => {
    localStorage.setItem("rvav.theme", t);
    document.documentElement.dataset.theme = t;
  }, theme);
}

test("request a RHEL 9 server from the catalog and follow it to Ready", async ({ page }) => {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));

  await page.goto("/catalog");
  await expect(page.getByRole("heading", { name: "Catalog" })).toBeVisible();
  await page.getByRole("link", { name: "RHEL 9 server", exact: true }).click();

  await expect(page.getByRole("heading", { name: "Request RHEL 9 server" })).toBeVisible();
  // Server-resolved defaults and the preview summary.
  await expect(page.getByText("web-dev-app001").first()).toBeVisible();
  await expect(page.getByText("No approval needed")).toBeVisible();
  await page.getByRole("textbox", { name: "Role" }).fill("api");
  await expect(page.locator("#f-vm_name")).toHaveText(/web-dev-api005/);
  await page.getByRole("button", { name: "Submit request" }).click();

  await expect(page).toHaveURL(/\/requests\/REQ-\d+$/);
  await expect(page.getByRole("heading", { name: /RHEL 9 server · web-dev-api005/ })).toBeVisible();
  const steps = page.getByRole("region", { name: "Steps" });
  // The mock runs the steps on timers: slower on CI runners.
  await expect(steps.getByText(/Reserved 10\.31\.10\.\d+/)).toBeVisible({ timeout: 15_000 });
  await expect(steps.getByText(/Cloned from tpl-rhel9\.4-2026\.10/)).toBeVisible({ timeout: 15_000 });
  await expect(page.getByRole("log", { name: /Output of AWX job/ })).toContainText("changed: [web-dev-api005]");
  await expect(page.getByText("Succeeded", { exact: true }).first()).toBeVisible({ timeout: 30_000 });

  await page.getByRole("link", { name: "Open deployment" }).click();
  await expect(page.getByRole("heading", { name: "web-dev-api" })).toBeVisible();
  // In the resources table (the Actions panel names the VM too, once loaded).
  await expect(page.getByRole("table").getByText("web-dev-api005", { exact: true })).toBeVisible();

  await page.getByRole("link", { name: "Deployments" }).first().click();
  await expect(page.getByRole("link", { name: "web-dev-api", exact: true })).toHaveCount(2);
  expect(errors).toEqual([]);
});

test("command palette finds a VM by IP address", async ({ page }) => {
  await page.goto("/");
  await expect(page.getByRole("heading", { name: "Overview" })).toBeVisible();
  await page.keyboard.press("ControlOrMeta+k");
  await page.getByPlaceholder("Search VMs, IPs, requests, or type a command").fill("10.21.10.37");
  await page.getByRole("option", { name: /web-prd-front/ }).click();
  await expect(page.getByRole("heading", { name: "web-prd-front" })).toBeVisible();
});

test("open a VM's console: VMRC, as no web console client is set up", async ({ page }) => {
  await page.goto("/");
  await expect(page.getByRole("heading", { name: "Overview" })).toBeVisible();
  await page.keyboard.press("ControlOrMeta+k");
  await page.getByPlaceholder("Search VMs, IPs, requests, or type a command").fill("10.21.10.37");
  await page.getByRole("option", { name: /web-prd-front/ }).click();
  await expect(page.getByRole("heading", { name: "web-prd-front" })).toBeVisible();
  await page
    .getByRole("link", { name: /Open the console of/ })
    .first()
    .click();
  await expect(page.getByRole("heading", { name: /^Console · / })).toBeVisible();
  await expect(page.getByRole("button", { name: "Open in VMRC" })).toBeEnabled();
  await expect(page.getByText(/The web console is not set up here/)).toBeVisible();
});

for (const theme of ["light", "dark"] as const) {
  test(`screenshots (${theme})`, async ({ page }) => {
    await page.goto("/");
    await setTheme(page, theme);

    await page.goto("/catalog");
    await expect(page.getByText("9 items")).toBeVisible();
    await page.screenshot({ path: `${shots}/catalog-${theme}.png` });

    await page.goto("/catalog/rhel9-server/request");
    await expect(page.getByText("No approval needed")).toBeVisible();
    await page.screenshot({ path: `${shots}/request-form-${theme}.png` });

    await page.getByRole("button", { name: "Submit request" }).click();
    await expect(page.getByRole("log", { name: /Output of AWX job/ })).toContainText("changed:", { timeout: 20_000 });
    await page.screenshot({ path: `${shots}/request-detail-${theme}.png` });

    await page.goto("/approvals");
    await expect(page.getByRole("article", { name: "Approval for REQ-10477" })).toBeVisible();
    await page.screenshot({ path: `${shots}/approvals-${theme}.png` });
  });
}
