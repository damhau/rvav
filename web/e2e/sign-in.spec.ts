import { expect, test } from "@playwright/test";

// Screenshots go to test-results; UPDATE_SCREENSHOTS=1 refreshes the
// committed ones in e2e/__screenshots__.
const shots = process.env.UPDATE_SCREENSHOTS ? "e2e/__screenshots__" : "test-results/screenshots";

// The local administrator's first sign-in (ADR-0030), against the mocks:
// the install password, then a password of one's own before the app.
test("the local administrator signs in with the install password and changes it", async ({ page }) => {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));

  await page.goto("/sign-in");
  await expect(page.getByRole("heading", { name: "Sign in to rvav" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Sign in with single sign-on" })).toBeVisible();

  const local = page.getByRole("form", { name: "Local administrator" });
  await local.getByLabel("Username").fill("admin");
  await local.getByLabel("Password").fill("not-the-password");
  await local.getByRole("button", { name: "Sign in as the local administrator" }).click();
  await expect(local.getByRole("alert")).toHaveText("The username or password is wrong.");
  await page.screenshot({ path: `${shots}/sign-in.png` });

  await local.getByLabel("Password").fill("install-password");
  await local.getByRole("button", { name: "Sign in as the local administrator" }).click();

  await expect(page).toHaveURL(/\/change-password$/);
  await expect(page.getByRole("heading", { name: "Change the administrator's password" })).toBeVisible();
  const form = page.getByRole("form", { name: "Change password" });
  await form.getByLabel("Current password").fill("install-password");
  await form.getByLabel("New password", { exact: true }).fill("install-password");
  await form.getByLabel("New password again").fill("install-password");
  await form.getByRole("button", { name: "Change password" }).click();
  await expect(form.getByText("Choose a password other than the current one.")).toBeVisible();
  await page.screenshot({ path: `${shots}/change-password.png` });

  await form.getByLabel("New password", { exact: true }).fill("a password of my own");
  await form.getByLabel("New password again").fill("a password of my own");
  await form.getByRole("button", { name: "Change password" }).click();

  // In the app.
  await expect(page).toHaveURL(/\/$/);
  await expect(page.getByRole("button", { name: /^Account/ })).toBeVisible();
  expect(errors).toEqual([]);
});
