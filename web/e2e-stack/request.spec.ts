import { expect, test } from "@playwright/test";
import { signIn } from "./auth";

// The whole path for real: the browser, nginx, the API, Temporal, the
// worker, vcsim and the fakes for EfficientIP and AWX. Works with dev
// authentication and with Keycloak.
test("a RHEL 9 server requested in the browser reaches Ready", async ({ page }) => {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  // A role of its own, so the test can run again on the same stack.
  const role =
    "pw" + Array.from({ length: 6 }, () => String.fromCharCode(97 + Math.floor(Math.random() * 26))).join("");

  await page.goto("/catalog");
  await signIn(page);
  await page.getByRole("link", { name: "RHEL 9 server", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Request RHEL 9 server" })).toBeVisible();
  await expect(page.getByText("No approval needed")).toBeVisible();
  await page.getByRole("textbox", { name: "Role" }).fill(role);
  await expect(page.locator("#f-vm_name")).toHaveText(new RegExp(`web-dev-${role}001`));
  await page.getByRole("button", { name: "Submit request" }).click();

  await expect(page).toHaveURL(/\/requests\/REQ-\d+$/);
  const steps = page.getByRole("region", { name: "Steps" });
  await expect(steps.getByText(/Reserved 10\.31\.10\.\d+/)).toBeVisible({ timeout: 30_000 });
  await expect(page.getByText("Succeeded", { exact: true }).first()).toBeVisible({ timeout: 90_000 });

  await page.getByRole("link", { name: "Open deployment" }).click();
  // The VM name shows in its resource row and again once its details load.
  await expect(page.getByText(`web-dev-${role}001`, { exact: true }).first()).toBeVisible();
  expect(errors).toEqual([]);
});
