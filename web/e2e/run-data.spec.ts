import { expect, type Page, test } from "@playwright/test";
import { bothThemes } from "./axe";

// A run's data (E1.5, ADR-0057) on the mock backend: the seeded PostgreSQL
// cluster that failed (REQ-10484) shows what its failed step was given, and,
// from the keyboard, what another step was given and gave. Scanned in either
// theme; at 320 px the data reflows without scrolling sideways.

/** Tabs forward until the focused element's text matches, as a keyboard user would. */
async function tabTo(page: Page, text: RegExp) {
  for (let i = 0; i < 80; i++) {
    await page.keyboard.press("Tab");
    const got = await page.evaluate(() => document.activeElement?.textContent ?? "");
    if (text.test(got)) return;
  }
  throw new Error(`nothing matching ${text} within 80 tabs`);
}

test("a run's data: the failed step's, then another's from the keyboard", async ({ page }) => {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/requests");
  await page.getByRole("link", { name: /PostgreSQL 16 cluster · orders/ }).click();
  await expect(page).toHaveURL(/\/requests\/REQ-10484$/);

  const data = page.getByRole("region", { name: "Step data", exact: true });
  await expect(data).toContainText("The database's owner · pg-create-role");
  await expect(data.getByRole("region", { name: "Inputs", exact: true })).toContainText(/role_name\s*orders_owner/);
  await expect(data.getByRole("region", { name: "Outputs", exact: true })).toContainText("It gave nothing.");

  await page.getByRole("button", { name: "Timeline", exact: true }).focus();
  await page.keyboard.press("Enter");
  await tabTo(page, /^Show data of Backups · pgbackrest-stanza$/);
  await page.keyboard.press("Enter");
  await expect(
    page.getByRole("button", { name: "Showing data of Backups · pgbackrest-stanza", exact: true }),
  ).toHaveAttribute("aria-pressed", "true");
  await expect(
    page.getByRole("button", { name: "Showing data of Backups · pgbackrest-stanza", exact: true }),
  ).toBeFocused();
  const outputs = data.getByRole("region", { name: "Outputs", exact: true });
  await expect(outputs).toContainText(/job_id\s*18235/);
  await expect(outputs).toContainText("Later steps read them as steps.backups.<name>.");
  await expect(data.getByRole("region", { name: "Inputs", exact: true })).toContainText(
    '"fqdn": "web-dev-pgorders003.corp.example"',
  );

  await bothThemes(page, "a run's data");

  await page.setViewportSize({ width: 320, height: 640 });
  await expect(data).toBeVisible();
  const overflow = await page.evaluate(() => {
    const main = document.getElementById("main")!;
    return {
      page: document.documentElement.scrollWidth - window.innerWidth,
      main: main.scrollWidth - main.clientWidth,
    };
  });
  expect(overflow).toEqual({ page: 0, main: 0 });
  expect(errors).toEqual([]);
});
