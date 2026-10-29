import { expect, test, type Page } from "@playwright/test";

// Reports (S13.5) against the mock backend: the figures, the chart read by
// keyboard and as a table, the filters, and a CSV export.

async function choose(page: Page, combobox: string, option: string) {
  await page.getByRole("combobox", { name: combobox }).click();
  await page.getByRole("option", { name: option, exact: true }).click();
}

test("read the reports, filter them and export a section", async ({ page }) => {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/");
  await page.getByRole("link", { name: "Reports" }).click();
  await expect(page.getByRole("heading", { name: "Reports" })).toBeVisible();
  await expect(page.getByText("Success rate", { exact: true }).first()).toBeVisible();

  // The chart, read with the arrow keys: each day's figures.
  const chart = page.getByRole("img", { name: /^Requests by day: \d+ in all/ });
  await chart.focus();
  await page.keyboard.press("ArrowRight");
  const tip = page.getByRole("tooltip");
  await expect(tip).toContainText("requests");
  await expect(tip).toContainText("succeeded");
  await page.keyboard.press("Escape");
  await expect(tip).toBeHidden();

  // And as a table: one row per day.
  await page.getByText("Show as a table").click();
  const table = page.getByRole("table", { name: "Requests by period" });
  await expect(table.getByRole("row")).toHaveCount(31);

  // A year, by month.
  await choose(page, "Period", "Last 12 months");
  await expect(page.getByRole("combobox", { name: "By" })).toHaveText(/Month/);
  await expect(page.getByRole("heading", { name: "Requests by month" })).toBeVisible();
  await expect(table.getByRole("row")).toHaveCount(14);

  // One project: its quota only.
  await choose(page, "Project", "Data Engineering");
  await expect(page.getByRole("region", { name: "Quota of Data Engineering" })).toBeVisible();
  await expect(page.getByRole("region", { name: "Quota of Web Platform" })).toHaveCount(0);

  // A section as CSV.
  const download = page.waitForEvent("download");
  await page.getByRole("region", { name: "Usage against quota" }).getByRole("button", { name: "CSV" }).click();
  expect((await download).suggestedFilename()).toBe("rvav-quota.csv");
  expect(errors).toEqual([]);
});
