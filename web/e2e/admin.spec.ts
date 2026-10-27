import { expect, test } from "@playwright/test";

// Administration against the mock backend.

test("run the EfficientIP reconcile check", async ({ page }) => {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));

  await page.goto("/");
  await page.getByRole("link", { name: "Integrations" }).click();
  await expect(page.getByRole("heading", { name: "Integrations" })).toBeVisible();
  await page.getByRole("button", { name: "Run the check" }).click();

  const orphans = page.getByRole("region", { name: "Addresses no deployment owns" });
  await expect(orphans.getByText("10.31.10.61")).toBeVisible();
  await expect(
    page.getByRole("region", { name: "DNS records no deployment owns" }).getByText("A", { exact: true }),
  ).toBeVisible();
  const missing = page.getByRole("region", { name: "Missing in EfficientIP" });
  await expect(missing.getByText("PTR 57.10.31.10.in-addr.arpa")).toBeVisible();
  await expect(page.getByRole("button", { name: "Check again" })).toBeEnabled();
  expect(errors).toEqual([]);
});
