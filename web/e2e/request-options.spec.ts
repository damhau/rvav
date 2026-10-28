import { expect, test } from "@playwright/test";

// Request options against the mock backend (S11.6): drafts, how many, a
// later start, on behalf of someone, and asking again.

test("save a request as a draft, resume it and submit it", async ({ page }) => {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/catalog/rhel9-server/request");
  await expect(page.getByRole("heading", { name: "Request RHEL 9 server" })).toBeVisible();
  await page.getByRole("textbox", { name: "Role" }).fill("batch");
  await page.getByRole("spinbutton", { name: "How many" }).fill("3");
  await page.getByRole("button", { name: "Save as draft" }).click();
  await expect(page.getByText("Draft saved")).toBeVisible();

  await page.getByRole("link", { name: "Requests" }).first().click();
  const drafts = page.getByRole("list", { name: "Your drafts" });
  await expect(drafts).toContainText("RHEL 9 server");
  await drafts.getByRole("link", { name: "Resume" }).click();
  await expect(page.getByRole("textbox", { name: "Role" })).toHaveValue("batch");
  await expect(page.getByRole("spinbutton", { name: "How many" })).toHaveValue("3");
  await page.getByRole("button", { name: "Submit request" }).click();
  await expect(page).toHaveURL(/\/requests\/REQ-\d+$/);

  // Submitted: the draft is gone.
  await page.getByRole("link", { name: "Requests" }).first().click();
  await expect(page.getByRole("heading", { name: "Requests" })).toBeVisible();
  await expect(page.getByRole("list", { name: "Your drafts" })).toHaveCount(0);
  expect(errors).toEqual([]);
});

test("request for someone else, later, then ask again", async ({ page }) => {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/catalog/rhel9-server/request");
  await page.getByRole("textbox", { name: "Role" }).fill("report");
  await page.getByRole("textbox", { name: "For someone else (optional)" }).fill("Thomas");
  await page.getByRole("button", { name: /Thomas Nguyen/ }).click();
  await expect(page.getByText(/On behalf of\s+Thomas Nguyen/)).toBeVisible();
  await page.getByRole("combobox", { name: "Start" }).click();
  await page.getByRole("option", { name: "At a time I choose" }).click();
  const tomorrow = new Date(Date.now() + 24 * 3600 * 1000);
  const pad = (n: number) => String(n).padStart(2, "0");
  await page
    .getByLabel("Start at")
    .fill(`${tomorrow.getFullYear()}-${pad(tomorrow.getMonth() + 1)}-${pad(tomorrow.getDate())}T09:00`);
  await expect(page.getByText("Provisioning starts at the time you chose.")).toBeVisible();
  await page.getByRole("button", { name: "Submit request" }).click();
  await expect(page).toHaveURL(/\/requests\/REQ-\d+$/);
  await expect(page.getByText("Scheduled", { exact: true }).first()).toBeVisible();

  // Asked again: the same inputs, and for the same person.
  await page.getByRole("link", { name: "Request again" }).click();
  await expect(page.getByRole("heading", { name: "Request RHEL 9 server" })).toBeVisible();
  await expect(page.getByRole("textbox", { name: "Role" })).toHaveValue("report");
  await expect(page.getByText(/On behalf of\s+Thomas Nguyen/)).toBeVisible();
  expect(errors).toEqual([]);
});
