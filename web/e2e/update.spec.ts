import { expect, test, type Page } from "@playwright/test";
import { bothThemes, scan, theme } from "./axe";
import { chooseItem, press, selectTab } from "./keyboard";

// Updating a deployment (X1.4, ADR-0042), by keyboard on the mock backend:
// the Actions menu opens the update's page, the summary previews what a new
// input changes, a confirmation asks before it runs, and a change rvav can't
// make in place says why.

// A fading toast fails axe's contrast check.
async function noToast(page: Page) {
  await page.mouse.move(0, 0);
  await expect(page.locator("[data-sonner-toast]")).toHaveCount(0, { timeout: 10_000 });
}

async function openUpdate(page: Page, name: string) {
  await page.goto("/deployments");
  await press(page, page.getByRole("link", { name, exact: true }));
  await expect(page.getByRole("heading", { name, level: 1 })).toBeVisible();
  await press(page, page.getByRole("button", { name: "Actions", exact: true }));
  await chooseItem(page, page.getByRole("menuitem", { name: "Update", exact: true }));
  await expect(page.getByRole("heading", { name: `Update ${name}`, level: 1 })).toBeVisible();
}

test("an update is previewed, then applied once confirmed", async ({ page }) => {
  test.setTimeout(90_000);
  await openUpdate(page, "web-tst-search");
  const summary = page.getByRole("region", { name: "What the update does" });
  await expect(summary.getByText("Change an input to see what the update does.")).toBeVisible();
  await expect(summary.getByRole("button", { name: "Review the update" })).toBeDisabled();

  await press(page, page.getByRole("radio", { name: /^X-Large/ }), "Space");
  const changes = summary.getByRole("list", { name: "Changes" });
  await expect(changes.getByRole("listitem")).toHaveCount(2);
  await expect(changes).toContainText("Set 8 vCPU and 48 GB on web-tst-search001");
  await expect(changes).toContainText("Set 8 vCPU and 48 GB on web-tst-search002");
  await expect(summary).toContainText("Quota after this update");
  await expect(summary).toContainText("No approval needed");
  await bothThemes(page, "an update's preview");

  // The theme button is behind the modal dialog: it is opened in each theme.
  const dialog = page.getByRole("alertdialog", { name: "Update web-tst-search?" });
  for (const name of ["Dark", "Light"] as const) {
    await theme(page, name);
    await press(page, summary.getByRole("button", { name: "Review the update" }));
    await expect(dialog).toContainText("rvav makes these 2 changes now.");
    await scan(page, `an update's confirmation, ${name.toLowerCase()}`);
    if (name === "Dark") {
      await page.keyboard.press("Escape");
      await expect(dialog).toHaveCount(0);
    }
  }
  await press(page, dialog.getByRole("button", { name: "Update", exact: true }));
  await expect(page).toHaveURL(/\/requests\/REQ-\d+$/);
  await expect(page.getByRole("heading", { name: "Update · web-tst-search", level: 1 })).toBeVisible();
  await expect(page.getByText("Succeeded", { exact: true }).first()).toBeVisible({ timeout: 20_000 });
  await noToast(page);
  await bothThemes(page, "an update's request");

  await press(page, page.getByRole("main").getByRole("link", { name: "web-tst-search", exact: true }).first());
  await expect(page.getByRole("tablist", { name: "Deployment sections" })).toBeVisible();
  await selectTab(page, "History");
  await expect(page.getByRole("region", { name: "History" }).getByRole("link", { name: "Updated" })).toBeVisible();
});

test("a change rvav can't make in place is refused, saying why", async ({ page }) => {
  test.setTimeout(60_000);
  await openUpdate(page, "web-dev-api");
  const summary = page.getByRole("region", { name: "What the update does" });
  await press(page, page.getByRole("radio", { name: /^Large/ }), "Space");
  const refused = summary.getByRole("list", { name: "Refused" });
  await expect(refused).toContainText("web-dev-api004 has snapshots");
  await expect(summary.getByRole("button", { name: "Review the update" })).toBeDisabled();
  await bothThemes(page, "an update refused");
});
