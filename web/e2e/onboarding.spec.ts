import { expect, test, type Page } from "@playwright/test";

// Onboarding (S13.3) against the mock backend: administrators find the VMs
// rvav doesn't manage, select many, and adopt them into deployments.

async function choose(page: Page, combobox: string, option: string) {
  await page.getByRole("combobox", { name: combobox }).click();
  await page.getByRole("option", { name: option, exact: true }).click();
}

test("adopt twelve existing VMs, each into its own deployment", async ({ page }) => {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/admin/onboarding");
  await expect(page.getByRole("heading", { name: "Onboarding" })).toBeVisible();
  await expect(page.getByText("60 VMs")).toBeVisible();

  await page.getByRole("textbox", { name: "Search" }).fill("erp-app");
  const table = page.getByRole("table", { name: "VMs to adopt" });
  await expect(page.getByText("12 VMs")).toBeVisible();
  await table.getByRole("checkbox", { name: "Select all shown" }).check();
  await expect(page.getByText("12 VMs · 12 selected")).toBeVisible();
  await page.getByRole("button", { name: "Adopt 12 VMs…" }).click();

  const dialog = page.getByRole("dialog", { name: "Adopt 12 VMs" });
  await choose(page, "Project", "Web Platform");
  await choose(page, "Environment", "prd");
  await choose(page, "Catalog item", "RHEL 9 server");
  // Without an owner, the server says why.
  await dialog.getByRole("button", { name: "Adopt 12 VMs" }).click();
  await expect(dialog.getByText(/Choose a member of Web Platform/)).toBeVisible();
  await choose(page, "Owner", "Thomas Nguyen");
  await dialog.getByRole("spinbutton", { name: "Lease (days)" }).fill("90");
  await dialog.getByRole("button", { name: "Adopt 12 VMs" }).click();
  await expect(dialog).toBeHidden();

  const adopted = page.getByRole("region", { name: "Adopted 12 VMs into 12 deployments" });
  await expect(adopted.getByRole("link", { name: "erp-app01" })).toBeVisible();
  await expect(adopted.getByText("1 address, 2 DNS records").first()).toBeVisible();
  await expect(page.getByText("Nothing to adopt")).toBeVisible();

  // The deployment is like any other.
  await adopted.getByRole("link", { name: "erp-app01" }).click();
  await expect(page.getByRole("heading", { name: "erp-app01" })).toBeVisible();
  await expect(page.getByText("Thomas Nguyen").first()).toBeVisible();
  expect(errors).toEqual([]);
});

test("several VMs into one deployment", async ({ page }) => {
  await page.goto("/admin/onboarding");
  await page.getByRole("textbox", { name: "Search" }).fill("app-old");
  await expect(page.getByText("8 VMs")).toBeVisible();
  const table = page.getByRole("table", { name: "VMs to adopt" });
  await table.getByRole("checkbox", { name: "Select app-old01" }).check();
  await table.getByRole("checkbox", { name: "Select app-old02" }).check();
  await page.getByRole("button", { name: "Adopt 2 VMs…" }).click();
  const dialog = page.getByRole("dialog", { name: "Adopt 2 VMs" });
  await choose(page, "Project", "Web Platform");
  await choose(page, "Environment", "dev");
  await choose(page, "Owner", "Léa Martin");
  await choose(page, "Catalog item", "RHEL 9 server");
  await dialog.getByRole("checkbox", { name: /All in one deployment/ }).check();
  await dialog.getByRole("textbox", { name: "Deployment name" }).fill("legacy-apps");
  await dialog.getByRole("button", { name: "Adopt 2 VMs" }).click();
  const adopted = page.getByRole("region", { name: "Adopted 2 VMs into 1 deployment" });
  await expect(adopted.getByRole("link", { name: "legacy-apps" })).toBeVisible();
  await expect(page.getByText("6 VMs")).toBeVisible();
});
