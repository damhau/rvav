import { expect, test } from "@playwright/test";
import { focused, press, skipToContent } from "./keyboard";

// A failed request's Retry and Roll back from the keyboard only (#201): each
// takes its button away, Retry with the whole failure, so the focus goes to
// what stays, and the toast says what happens.

const failure = "The VM could not join IdM: DNS discovery failed for idm.corp.example.";

test.beforeEach(async ({ page }) => {
  await page.goto("/requests/REQ-10482");
  await expect(page.getByRole("heading", { level: 1, name: "RHEL 9 server · web-dev-worker002" })).toBeVisible();
  await expect(page.getByRole("alert").filter({ hasText: failure })).toBeVisible();
  await skipToContent(page);
});

test("Retry puts the focus on the request's status, and Tab goes on from there", async ({ page }) => {
  await press(page, page.getByRole("button", { name: "Retry from the failed step" }));
  await expect(page.getByText("REQ-10482: running again from the failed step")).toBeVisible();
  await expect(page.getByText(failure)).toBeHidden();
  await expect.poll(() => focused(page)).toMatch(/^span "(Queued|In progress)"$/);
  expect(await page.evaluate(() => Boolean(document.activeElement?.closest("main header")))).toBe(true);
  await page.keyboard.press("Tab");
  expect(await page.evaluate(() => Boolean(document.activeElement?.closest("main")))).toBe(true);
});

test("Roll back puts the focus back on the failure, which says it was rolled back", async ({ page }) => {
  const box = page.getByRole("alert").filter({ hasText: failure });
  await press(page, box.getByRole("button", { name: "Roll back" }));
  const dialog = page.getByRole("alertdialog", { name: "Roll back REQ-10482?" });
  await expect(dialog).toContainText("Deletes the VM, its IP address and DNS records.");
  await press(page, dialog.getByRole("button", { name: "Roll back" }));
  await expect(dialog).toBeHidden();
  await expect(page.getByText("REQ-10482: rolling back")).toBeVisible();
  await expect(box).toContainText("It was rolled back");
  await expect(box.getByRole("button", { name: "Roll back" })).toHaveCount(0);
  await expect(box).toBeFocused();
  // Retry, in it, is the next Tab stop.
  await page.keyboard.press("Tab");
  await expect(box.getByRole("button", { name: "Retry", exact: true })).toBeFocused();
});
