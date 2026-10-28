import { expect, test } from "@playwright/test";

// API tokens and the audit log on the mock backend (S9.8, ADR-0020).

test("make an API token, see it once, revoke it", async ({ page }) => {
  page.on("dialog", (d) => void d.accept());
  await page.goto("/");
  await page.getByRole("button", { name: "Account" }).click();
  await page.getByRole("menuitem", { name: "API tokens" }).click();
  await expect(page.getByRole("heading", { name: "API tokens" })).toBeVisible();
  await page.getByRole("textbox", { name: "What it is for" }).fill("Terraform");
  await page.getByRole("button", { name: "Make a token" }).click();
  const shown = page.getByRole("status").filter({ hasText: "Copy it now" });
  await expect(shown).toContainText("rvav_pat_");
  await shown.getByRole("button", { name: "Done" }).click();
  await expect(shown).toBeHidden();
  const tokens = page.getByRole("region", { name: "Your tokens" });
  await expect(tokens.getByText("Terraform")).toBeVisible();
  await tokens.getByRole("button", { name: "Revoke Terraform" }).click();
  await expect(page.getByText("Token revoked")).toBeVisible();
  await expect(tokens.getByText("Terraform")).toBeHidden();
  await page.getByRole("checkbox", { name: "Everyone's" }).click();
  await expect(page.getByRole("region", { name: "Everyone's tokens" }).getByText("GitLab pipeline")).toBeVisible();
});

test("search the audit log and export it", async ({ page }) => {
  await page.goto("/");
  await page.getByRole("navigation").getByRole("link", { name: "Audit log" }).click();
  await expect(page.getByRole("heading", { name: "Audit log" })).toBeVisible();
  await expect(page.getByText("catalog.version.publish")).toBeVisible();
  await page.getByRole("combobox", { name: "What" }).click();
  await page.getByRole("option", { name: "API tokens" }).click();
  await expect(page.getByText("token.create")).toBeVisible();
  await expect(page.getByText("catalog.version.publish")).toBeHidden();
  await page.getByRole("combobox", { name: "What" }).click();
  await page.getByRole("option", { name: "Anything" }).click();
  await page.getByRole("textbox", { name: "Search" }).fill("tnguyen");
  await expect(page.getByText("member.set")).toBeVisible();
  await expect(page.getByText("token.create")).toBeHidden();
  const download = page.waitForEvent("download");
  await page.getByRole("button", { name: "Export CSV" }).click();
  expect((await download).suggestedFilename()).toBe("rvav-audit.csv");
});
