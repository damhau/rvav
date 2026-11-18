import { expect, test } from "@playwright/test";
import { bothThemes } from "./axe";

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

test("a value source that fails says so in place, and a refused submit points at what to fix", async ({ page }) => {
  test.setTimeout(90_000);
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/catalog");
  const open = page.getByRole("link", { name: "Request RHEL 9 server" });
  await expect(open).toBeVisible();
  // Three calls: the first and the client's two retries.
  await page.evaluate(() => (window as unknown as MockWindow).__rvavMocks.faults.failFieldValues("cluster", 3));
  await open.click();
  const cluster = page.getByRole("combobox", { name: "Cluster" });
  const summary = page.getByRole("region", { name: "Summary" });
  await expect(page.getByText("Couldn't load the options.")).toBeVisible({ timeout: 15_000 });
  await expect(cluster).toHaveText("Not loaded");
  await expect(summary).toContainText("Network");
  await expect(summary).not.toContainText("Placement");

  await page.getByRole("button", { name: "Submit request" }).click();
  await expect(page.getByText("2 inputs need attention.")).toBeVisible();
  await expect(cluster).toHaveAttribute("aria-invalid", "true");
  const retry = page.getByRole("button", { name: "Try again" });
  await expect(retry).toBeFocused();
  await bothThemes(page, "request form refused, a source failed");

  await retry.focus();
  await page.keyboard.press("Enter");
  await expect(cluster).toHaveText("GVA-DEV-CL01");
  await expect(cluster).toBeFocused();
  await expect(summary).toContainText(/PlacementGVA-DEV-CL01 · \S/);
  await expect(page.getByText(/needs? attention/)).toHaveCount(0);
  await page.getByRole("button", { name: "Submit request" }).click();
  await expect(page).toHaveURL(/\/requests\/REQ-\d+$/);
  expect(errors).toEqual([]);
});

interface MockWindow {
  __rvavMocks: { faults: { failFieldValues(field: string, times: number): void } };
}
