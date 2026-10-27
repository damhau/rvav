import { expect, test } from "@playwright/test";
import { oidc, signIn } from "./auth";

test.skip(!oidc, "needs Keycloak sign-in, to act as two people (set RVAV_STACK_AUTH=oidc)");

// S3 acceptance on the real stack: Thomas asks for a 48 GB production VM, Marc
// (Infrastructure leads) lowers it to 32 GB and approves, and it provisions.
test("an infrastructure lead lowers a production VM to 32 GB and approves it", async ({ page }) => {
  test.setTimeout(150_000);
  const role =
    "ap" + Array.from({ length: 6 }, () => String.fromCharCode(97 + Math.floor(Math.random() * 26))).join("");

  await page.goto("/catalog/rhel9-server/request");
  await signIn(page, "thomas");
  await expect(page.getByRole("heading", { name: "Request RHEL 9 server" })).toBeVisible();
  await page.getByRole("radio", { name: "prd" }).click();
  await page.getByRole("textbox", { name: "Role" }).fill(role);
  await page.getByRole("radio", { name: /^X-Large/ }).check();
  await expect(page.getByText("Needs approval from Infrastructure leads")).toBeVisible();
  await page.getByRole("button", { name: "Submit for approval" }).click();
  await expect(page).toHaveURL(/\/requests\/REQ-\d+$/);
  const number = page.url().split("/").pop()!;
  await expect(page.getByText("Awaiting approval").first()).toBeVisible();

  await page.getByRole("button", { name: "Account" }).click();
  await page.getByRole("menuitem", { name: "Sign out" }).click();
  await signIn(page, "marc");
  await page.getByRole("link", { name: /Approvals/ }).click();
  const detail = page.getByRole("article", { name: `Approval for ${number}` });
  await page.getByRole("button", { name: new RegExp(number) }).click();
  await expect(detail.getByText(/Production VM above 32 GB RAM/)).toBeVisible();
  await detail.getByRole("radio", { name: /^Large/ }).check();
  await detail.getByLabel("Comment").fill("32 GB is enough for the JVM.");
  await detail.getByRole("button", { name: "Approve with Large" }).click();
  await expect(page.getByText(`Approved: ${number}`)).toBeVisible();

  await page.goto(`/requests/${number}`);
  const approval = page.getByRole("region", { name: "Approval" });
  await expect(approval.getByText("Approved by Marc Keller")).toBeVisible();
  await expect(approval.getByText(/Changed size from XL to L/)).toBeVisible();
  await expect(page.getByText("Succeeded", { exact: true }).first()).toBeVisible({ timeout: 90_000 });
});
