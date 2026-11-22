import { expect, type Page, test } from "@playwright/test";
import { bothThemes, scan, theme } from "./axe";
import { chooseItem, tabTo } from "./keyboard";

// X1.2: the deployments list's filters, sort, layouts and saved views, and the
// Resources page's VMs, in mock mode.

async function settled(page: Page) {
  await page.mouse.move(0, 0);
  await expect(page.locator("[data-sonner-toast]")).toHaveCount(0, { timeout: 10_000 });
}

async function pick(page: Page, select: string, option: string) {
  await page.getByRole("combobox", { name: select }).click();
  await page.getByRole("option", { name: option, exact: true }).click();
}

/** Scans a menu or a dialog in each theme: the theme button is behind it, so it's opened in each. */
async function overlayInBothThemes(page: Page, open: () => Promise<void>, where: string) {
  await settled(page);
  for (const name of ["Light", "Dark"] as const) {
    await theme(page, name);
    await open();
    await scan(page, `${where}, ${name.toLowerCase()}`);
    await page.keyboard.press("Escape");
    await expect(page.getByRole("menu").or(page.getByRole("dialog")).or(page.getByRole("alertdialog"))).toHaveCount(0);
  }
  await theme(page, "Light");
}

test("filter and sort the deployments, show them as cards, and save the view", async ({ page }) => {
  test.setTimeout(60_000);
  await page.goto("/deployments");
  const table = page.getByRole("table", { name: "Deployments" });
  await expect(table.getByRole("link", { name: "web-prd-front" })).toBeVisible();

  await pick(page, "Catalog item", "Ubuntu 24.04 server");
  await expect(table.getByRole("row")).toHaveCount(3);
  await table.getByRole("button", { name: "Lease" }).click();
  await expect(page).toHaveURL(/item=ubuntu-server/);
  await expect(page).toHaveURL(/sort=lease_expires_at/);
  await expect(table.getByRole("columnheader", { name: /Lease/ })).toHaveAttribute("aria-sort", "ascending");

  await page.getByRole("button", { name: "Cards" }).click();
  const cards = page.getByRole("list", { name: "Deployments" });
  await expect(cards.getByRole("listitem")).toHaveCount(2);
  await expect(page).toHaveURL(/layout=cards/);
  await bothThemes(page, "deployments as cards");

  const views = page.getByRole("button", { name: "Views" });
  await overlayInBothThemes(
    page,
    async () => {
      await views.click();
      await expect(page.getByRole("menuitem", { name: "Save as a new view…" })).toBeVisible();
    },
    "the Views menu",
  );
  const dialog = page.getByRole("dialog", { name: "Save this view" });
  const askToSave = async () => {
    await views.click();
    await page.getByRole("menuitem", { name: "Save as a new view…" }).click();
    await expect(dialog.getByRole("textbox", { name: "Name" })).toBeFocused();
  };
  await overlayInBothThemes(page, askToSave, "saving a view");
  await askToSave();
  await dialog.getByRole("textbox", { name: "Name" }).fill("Ubuntu by lease");
  await dialog.getByRole("button", { name: "Save view" }).click();
  await expect(page.getByRole("button", { name: "Saved view: Ubuntu by lease" })).toBeVisible();

  // Another list, then the view again: its filters, sort and layout come back.
  await page.getByRole("button", { name: "Table" }).click();
  await page.getByRole("button", { name: "Clear filters" }).first().click();
  await expect(table.getByRole("link", { name: "web-prd-front" })).toBeVisible();
  await views.click();
  await page.getByRole("menuitem", { name: "Ubuntu by lease", exact: true }).click();
  await expect(cards.getByRole("listitem")).toHaveCount(2);
  await expect(cards.getByRole("link", { name: "web-prd-edge" })).toBeVisible();
  await expect(page).toHaveURL(/item=ubuntu-server/);
  await expect(page).toHaveURL(/sort=lease_expires_at/);

  const shown = page.getByRole("button", { name: "Saved view: Ubuntu by lease" });
  const confirm = page.getByRole("alertdialog", { name: "Delete Ubuntu by lease?" });
  const askToDelete = async () => {
    await shown.click();
    await page.getByRole("menuitem", { name: "Delete Ubuntu by lease…" }).click();
    await expect(confirm).toBeVisible();
  };
  await overlayInBothThemes(page, askToDelete, "deleting a view");
  await askToDelete();
  await confirm.getByRole("button", { name: "Delete view" }).click();
  await expect(views).toBeVisible();
});

test("every project's deployments, with each one's project", async ({ page }) => {
  await page.goto("/deployments");
  const table = page.getByRole("table", { name: "Deployments" });
  await expect(table.getByRole("link", { name: "web-prd-front" })).toBeVisible();
  await expect(table.getByRole("link", { name: "data-prd-pg-ledger" })).toHaveCount(0);
  await page.getByRole("button", { name: "All my projects" }).click();
  await expect(table.getByRole("link", { name: "data-prd-pg-ledger" })).toBeVisible();
  await expect(table.getByRole("columnheader", { name: /Project/ })).toBeVisible();
  await bothThemes(page, "every project's deployments");
});

test("open a VM from the Resources page, and its deployment", async ({ page }) => {
  await page.goto("/resources");
  const table = page.getByRole("table", { name: "VMs" });
  await expect(table.getByRole("button", { name: "web-prd-front001" })).toBeVisible();
  // One project's VMs fit their card at 1440 px; every project's, a column wider, may scroll.
  expect(await table.locator("xpath=..").evaluate((e) => e.scrollWidth - e.clientWidth)).toBeLessThanOrEqual(0);

  await pick(page, "Power", "Powered off");
  await expect(table.getByRole("row")).toHaveCount(3);
  await expect(page).toHaveURL(/power=off/);
  await table.getByRole("button", { name: "Memory" }).click();
  await expect(page).toHaveURL(/sort=memory_mb/);
  await bothThemes(page, "Resources, powered off");

  const name = table.getByRole("button", { name: "web-tst-search002" });
  const sheet = page.getByRole("dialog", { name: "web-tst-search002" });
  const openSheet = async () => {
    await name.click();
    await expect(sheet.getByText("Network adapters")).toBeVisible();
  };
  await overlayInBothThemes(page, openSheet, "a VM's properties from Resources");
  await openSheet();
  await expect(page).toHaveURL(/\/resources\?/);
  await page.keyboard.press("Escape");
  await expect(sheet).toBeHidden();
  await expect(name).toBeFocused();

  await table.getByRole("link", { name: "web-tst-search" }).click();
  await expect(page.getByRole("heading", { level: 1, name: "web-tst-search" })).toBeVisible();
});

test("the deployments' layout, a saved view and a VM's properties with the keyboard only", async ({ page }) => {
  await page.goto("/deployments");
  await expect(
    page.getByRole("table", { name: "Deployments" }).getByRole("link", { name: "web-prd-front" }),
  ).toBeVisible();

  await tabTo(page, page.getByRole("button", { name: "Cards", exact: true }));
  await page.keyboard.press("Enter");
  await expect(page.getByRole("list", { name: "Deployments" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Cards" })).toHaveAttribute("aria-pressed", "true");

  await tabTo(page, page.getByRole("button", { name: "Views", exact: true }));
  await page.keyboard.press("Enter");
  await expect(page.getByRole("menu")).toBeVisible();
  await chooseItem(page, page.getByRole("menuitem", { name: "Save as a new view…" }));
  const dialog = page.getByRole("dialog", { name: "Save this view" });
  await expect(dialog.getByRole("textbox", { name: "Name" })).toBeFocused();
  await page.keyboard.type("Cards");
  await page.keyboard.press("Enter");
  await expect(dialog).toBeHidden();
  const saved = page.getByRole("button", { name: "Saved view: Cards" });
  await expect(saved).toBeVisible();
  await expect(saved).toBeFocused();

  await page.goto("/resources");
  await expect(
    page.getByRole("table", { name: "VMs" }).getByRole("button", { name: "web-prd-front001" }),
  ).toBeVisible();
  await tabTo(page, page.getByRole("button", { name: "web-prd-cache001", exact: true }));
  await page.keyboard.press("Enter");
  const sheet = page.getByRole("dialog", { name: "web-prd-cache001" });
  await expect(sheet).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(sheet).toBeHidden();
  await expect(page.getByRole("button", { name: "web-prd-cache001" })).toBeFocused();
});
