import { expect, test } from "@playwright/test";

test("approve a production VM with a smaller size and follow it to Ready", async ({ page }) => {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));

  await page.goto("/");
  await expect(page.getByLabel("2 waiting for you")).toBeVisible();
  await page.getByRole("link", { name: /Approvals/ }).click();

  const detail = page.getByRole("article", { name: "Approval for REQ-10477" });
  await expect(detail.getByText("Why this needs you")).toBeVisible();
  await expect(detail.getByText("Production VM above 32 GB RAM; Production VMs need sign-off")).toBeVisible();
  await expect(detail.getByText(/then it escalates to Claire Dubois/)).toBeVisible();

  await detail.getByRole("radio", { name: /^Large/ }).check();
  await detail.getByLabel("Comment").fill("32 GB is enough for the JVM; page cache can wait.");
  await detail.getByRole("button", { name: "Approve with Large" }).click();

  // The next one is selected, and the badge counts one less.
  await expect(page.getByRole("article", { name: "Approval for REQ-10479" })).toBeVisible();
  await expect(page.getByLabel("1 waiting for you")).toBeVisible();

  // In-app navigation: a reload would reset the mock API.
  await page.getByRole("tab", { name: "Decided" }).click();
  const decided = page.getByRole("article", { name: "Approval for REQ-10477" });
  await expect(decided.getByText("Approved by Léa Martin")).toBeVisible();
  await decided.getByRole("heading").getByRole("link").click();
  await expect(page).toHaveURL(/\/requests\/REQ-10477$/);
  const approval = page.getByRole("region", { name: "Approval" });
  await expect(approval.getByText("Approved by Léa Martin")).toBeVisible();
  await expect(approval.getByText(/Changed size from XL to L/)).toBeVisible();
  await expect(page.getByText("Succeeded", { exact: true }).first()).toBeVisible({ timeout: 30_000 });
  expect(errors).toEqual([]);
});
