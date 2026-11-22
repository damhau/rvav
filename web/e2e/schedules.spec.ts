import { expect, type Page, test } from "@playwright/test";
import { bothThemes, scan, theme } from "./axe";

// Recurring schedules (E1.5, ADR-0057) on the mock backend, from the keyboard
// only: a requester schedules a job every 6 hours from its request form, then
// pauses, resumes and deletes it; a schedule's runs name the requests they
// started; a cron expression that isn't one is refused next to the field.
// Scanned in either theme, the sheets too; at 320 px the page reflows
// without scrolling sideways.

/** Tabs forward until the focused element's id, label or text matches, as a keyboard user would; it may be focused already. */
async function tabTo(page: Page, target: RegExp) {
  for (let i = 0; i <= 80; i++) {
    const got = await page.evaluate(() => {
      const e = document.activeElement;
      return [e?.id ?? "", e?.getAttribute("aria-label") ?? "", e?.textContent ?? ""];
    });
    if (got.some((part) => target.test(part))) return;
    await page.keyboard.press("Tab");
  }
  throw new Error(`nothing matching ${target} within 80 tabs`);
}

/** Opens the focused select and picks an option with the arrows, up or down: the list doesn't wrap. */
async function pick(page: Page, option: string) {
  await page.keyboard.press("Enter");
  const wanted = page.getByRole("option", { name: option, exact: true });
  await expect(wanted).toBeVisible();
  const names = await page.getByRole("option").allTextContents();
  for (let i = 0; i < 8 && !(await wanted.evaluate((e) => e === document.activeElement)); i++) {
    const at = names.indexOf((await page.evaluate(() => document.activeElement?.textContent)) ?? "");
    await page.keyboard.press(at > names.indexOf(option) ? "ArrowUp" : "ArrowDown");
  }
  await page.keyboard.press("Enter");
  await expect(wanted).toHaveCount(0);
}

/** Opens a schedule's row menu and runs one of its items. */
async function act(page: Page, schedule: string, item: string) {
  await tabTo(page, new RegExp(`^Actions for ${schedule}$`));
  await page.keyboard.press("Enter");
  const wanted = page.getByRole("menuitem", { name: item, exact: true });
  await expect(wanted).toBeVisible();
  for (let i = 0; i < 5 && !(await wanted.evaluate((e) => e === document.activeElement)); i++)
    await page.keyboard.press("ArrowDown");
  await expect(wanted).toBeFocused();
  await page.keyboard.press("Enter");
}

async function scheduleForm(page: Page) {
  await page.goto("/catalog/tls-cert/request");
  await expect(page.getByRole("heading", { name: "Request Renew TLS certificate" })).toBeVisible();
  await expect(page.getByText("No approval needed")).toBeVisible();
  await tabTo(page, /^f-target$/);
  await page.keyboard.type("api.corp.example");
  // The preview is debounced: one older than the target would put the focus there on a refusal.
  await page.waitForTimeout(400);
  await tabTo(page, /^start$/);
  await pick(page, "Again and again, on a schedule");
  await expect(page.getByRole("button", { name: "Create schedule", exact: true })).toBeVisible();
}

test("a requester schedules a job from the keyboard, then pauses, resumes and deletes it", async ({ page }) => {
  test.slow();
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await scheduleForm(page);
  await tabTo(page, /^schedule-name$/);
  await page.keyboard.press("ControlOrMeta+A");
  await page.keyboard.type("API certificate");
  await tabTo(page, /^every$/);
  await page.keyboard.press("ControlOrMeta+A");
  await page.keyboard.type("6");
  await page.keyboard.press("Tab");
  await expect(page.getByRole("combobox", { name: "Unit", exact: true })).toBeFocused();
  await pick(page, "hours");
  await tabTo(page, /^Create schedule$/);
  await page.keyboard.press("Enter");
  await expect(page.getByText("API certificate scheduled")).toBeVisible();
  await expect(page.getByRole("heading", { name: "Schedules", exact: true })).toBeVisible();
  const row = page.getByRole("row", { name: /^API certificate/ });
  await expect(row).toContainText("Every 6 hours");
  await expect(row).toContainText("None yet");
  await bothThemes(page, "Schedules");

  await act(page, "API certificate", "Pause");
  const pause = page.getByRole("dialog", { name: "Pause API certificate", exact: true });
  await expect(pause).toBeVisible();
  await tabTo(page, /^pause-note$/);
  await page.keyboard.type("Frozen for the sale.");
  await scan(page, "Pausing a schedule");
  await tabTo(page, /^Pause$/);
  await page.keyboard.press("Enter");
  await expect(page.getByText("API certificate paused")).toBeVisible();
  await expect(row).toContainText("PausedFrozen for the sale.");

  await act(page, "API certificate", "Resume");
  await expect(page.getByText("API certificate resumed")).toBeVisible();
  await expect(row).not.toContainText("Paused");

  const runs = page.getByRole("dialog", { name: "Weekly TLS renewal", exact: true });
  for (const name of ["Light", "Dark"] as const) {
    await theme(page, name);
    await act(page, "Weekly TLS renewal", "Show its runs");
    await expect(runs.getByRole("region", { name: "Next runs" }).getByRole("listitem")).toHaveCount(5);
    await expect(runs.getByRole("link", { name: "REQ-10458", exact: true })).toBeVisible();
    await expect(runs).toContainText("Target: the job's inventory has no host shop.corp.example.");
    await scan(page, `A schedule's runs, ${name.toLowerCase()}`);
    await page.keyboard.press("Escape");
    await expect(runs).toHaveCount(0);
  }
  await theme(page, "Light");
  await expect(page.getByRole("row", { name: /^Weekly TLS renewal/ })).toContainText(/Signed in \d+ minutes? ago/);

  await act(page, "DNS zone audit", "Show its runs");
  const dns = page.getByRole("dialog", { name: "DNS zone audit", exact: true });
  await expect(dns).toContainText(/Marc Keller · Signed in 12 days ago/);
  await expect(dns.getByRole("region", { name: "Runs", exact: true })).toContainText(
    "Its owner hasn't signed in for 90 days: a run would have the groups of their last sign-in.",
  );
  await page.keyboard.press("Escape");
  await expect(dns).toHaveCount(0);

  await act(page, "API certificate", "Delete");
  const confirm = page.getByRole("alertdialog", { name: "Delete API certificate?", exact: true });
  await expect(confirm).toContainText("The requests it started stay, and still name it.");
  await tabTo(page, /^Delete schedule$/);
  await page.keyboard.press("Enter");
  await expect(page.getByText("API certificate deleted")).toBeVisible();
  await expect(row).toHaveCount(0);

  await page.setViewportSize({ width: 320, height: 640 });
  await expect(page.getByRole("row", { name: /^Weekly TLS renewal/ })).toBeVisible();
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

test("a cron expression that isn't one is refused next to it, and a run names its schedule", async ({ page }) => {
  for (const name of ["Light", "Dark"] as const) {
    await scheduleForm(page);
    await theme(page, name);
    await tabTo(page, /^repeat-kind$/);
    await pick(page, "At the times of a cron expression");
    await tabTo(page, /^cron$/);
    await page.keyboard.type("0 2 * *");
    await tabTo(page, /^Create schedule$/);
    await page.keyboard.press("Enter");
    const cron = page.getByRole("textbox", { name: "Cron expression", exact: true });
    await expect(cron).toBeFocused();
    await expect(cron).toHaveAccessibleDescription(/^Five fields: minute, hour/);
    await scan(page, `A refused cron expression, ${name.toLowerCase()}`);
  }
  await theme(page, "Light");

  await page.goto("/requests/REQ-10458");
  await expect(page.getByText("Started by the schedule Weekly TLS renewal, as Léa Martin")).toBeVisible();
});
