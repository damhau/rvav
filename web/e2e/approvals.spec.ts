import { expect, test } from "@playwright/test";

test("approve a production VM with a smaller size and follow it to Ready", async ({ page }) => {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));

  await page.goto("/");
  await expect(page.getByLabel("2 waiting for you")).toBeVisible();
  await page.getByRole("link", { name: /Approvals/ }).click();

  const front = "RHEL 9 server · web-prd-front004";
  const waiting = page.getByRole("table", { name: "Waiting for you" });
  await waiting.getByRole("link", { name: front }).click();
  const sheet = page.getByRole("dialog", { name: front });
  await expect(sheet.getByText("Why this needs you")).toBeVisible();
  await expect(sheet.getByText("Production VM above 32 GB RAM; Production VMs need sign-off")).toBeVisible();
  await expect(sheet.getByText(/then it escalates to Claire Dubois/)).toBeVisible();

  await sheet.getByRole("radio", { name: /^Large/ }).check();
  await sheet.getByLabel("Comment").fill("32 GB is enough for the JVM; page cache can wait.");
  await sheet.getByRole("button", { name: "Approve with Large" }).click();

  // The panel closes, the row goes, and the badge counts one less.
  await expect(sheet).toBeHidden();
  await expect(waiting.getByRole("link", { name: front })).toBeHidden();
  await expect(page.getByLabel("1 waiting for you")).toBeVisible();

  // In-app navigation: a reload would reset the mock API.
  await page.getByRole("tab", { name: "Decided" }).click();
  await page.getByRole("table", { name: "Decided approvals" }).getByRole("link", { name: front }).click();
  const decided = page.getByRole("dialog", { name: front });
  await expect(decided.getByText("Approved by Léa Martin")).toBeVisible();
  await decided.getByRole("link", { name: "Open the request" }).click();
  await expect(page).toHaveURL(/\/requests\/REQ-10477$/);
  const approval = page.getByRole("region", { name: "Approval" });
  await expect(approval.getByText("Approved by Léa Martin")).toBeVisible();
  await expect(approval.getByText(/Changed size from XL to L/)).toBeVisible();
  await expect(page.getByText("Succeeded", { exact: true }).first()).toBeVisible({ timeout: 30_000 });
  expect(errors).toEqual([]);
});
