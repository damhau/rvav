import { expect, test } from "@playwright/test";

// A multi-VM item against the mock backend (S12.2, ADR-0024): the form says
// what each component builds, and the request makes one deployment whose
// VMs are listed by component, with the DRS rule keeping the web servers
// on separate hosts.

test("a web application: its components, then one deployment", async ({ page }) => {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/catalog/web-app/request");
  await expect(page.getByRole("heading", { name: "Request Web application" })).toBeVisible();
  // How many of each is in its form, not in the options.
  await expect(page.getByRole("spinbutton", { name: "How many" })).toHaveCount(0);
  await page.getByRole("spinbutton", { name: "Web servers" }).fill("3");
  const builds = page.getByRole("group", { name: "What it builds" });
  await expect(builds).toContainText("What it builds · 4 VMs");
  const web = builds.getByRole("listitem", { name: "Web servers" });
  await expect(web).toContainText("3 VMs");
  await expect(web).toContainText("On separate hosts");
  await expect(web).toContainText("web-dev-shopweb001, web-dev-shopweb002, web-dev-shopweb003");
  await expect(builds.getByRole("listitem", { name: "Database" })).toContainText("1 VM");

  await page.getByRole("button", { name: "Submit request" }).click();
  await expect(page).toHaveURL(/\/requests\/REQ-\d+$/);
  await expect(page.getByRole("heading", { name: /Web application · web-dev-shop/ })).toBeVisible();
  await expect(page.getByText("Succeeded", { exact: true }).first()).toBeVisible({ timeout: 45_000 });

  await page.getByRole("link", { name: "Open deployment" }).click();
  await expect(page.getByRole("heading", { name: "web-dev-shop" })).toBeVisible();
  const vms = page.getByRole("table");
  await expect(vms.getByRole("columnheader", { name: "web · 3 VMs" })).toBeVisible();
  await expect(vms.getByRole("columnheader", { name: "db · 1 VM" })).toBeVisible();
  await expect(vms).toContainText("web-dev-shopdb001");
  const apart = page.getByRole("list", { name: "Kept on separate hosts" });
  await expect(apart).toContainText(
    "web on separate hosts: web-dev-shopweb001, web-dev-shopweb002, web-dev-shopweb003",
  );
  expect(errors).toEqual([]);
});
