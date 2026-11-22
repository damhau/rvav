import { expect, test, type Page } from "@playwright/test";
import { scan, theme } from "./axe";

// On a phone (V2.1): the top bar holds one row, its search and project
// switcher behind buttons; a list's rows stack as cards; a name too long for
// its place is cut, and shows whole on hover and keyboard focus.
// UPDATE_SCREENSHOTS=1 writes the screenshots to the committed ones in
// e2e/__screenshots__.
const shots = process.env.UPDATE_SCREENSHOTS ? "e2e/__screenshots__" : "test-results/screenshots";

test.use({ viewport: { width: 375, height: 812 } });

async function loaded(page: Page) {
  await expect(page.getByRole("main")).toBeVisible();
  await expect(
    page.locator(".animate-pulse, [role=status][aria-label=Loading], [role=status][aria-label=Chargement]"),
  ).toHaveCount(0);
  await page.waitForTimeout(300);
}

// From the top, the focus on nothing: what opened the page keeps it otherwise.
async function shoot(page: Page, file: string) {
  await page.evaluate(() => {
    (document.activeElement as HTMLElement | null)?.blur();
    document.getElementById("main")!.scrollTo(0, 0);
  });
  await page.screenshot({ path: `${shots}/${file}.png` });
}

for (const width of [375, 320]) {
  test(`the top bar holds one row at ${width} px, its buttons giving the focus back`, async ({ page }) => {
    await page.setViewportSize({ width, height: 700 });
    await page.goto("/deployments");
    await loaded(page);
    const bar = page.getByRole("banner");
    expect((await bar.boundingBox())!.height).toBeLessThan(60);
    for (const name of ["Open navigation", "Notifications, 3 unread", "Project: Web Platform", "Theme: system"]) {
      await expect(bar.getByRole("button", { name })).toBeVisible();
    }
    await expect(bar.getByRole("button", { name: /^Account/ })).toBeVisible();

    const search = bar.getByRole("button", { name: "Search VMs, IPs, requests…" });
    await search.focus();
    await page.keyboard.press("Enter");
    const palette = page.getByRole("dialog", { name: "Search or run a command" });
    await expect(palette.getByPlaceholder("Search VMs, IPs, requests, or type a command")).toBeFocused();
    await page.keyboard.press("Escape");
    await expect(palette).toBeHidden();
    await expect(search).toBeFocused();

    const project = bar.getByRole("button", { name: "Project: Web Platform" });
    await project.focus();
    await page.keyboard.press("Enter");
    await expect(page.getByRole("menu").getByText("Your projects")).toBeVisible();
    await page.keyboard.press("Escape");
    await expect(page.getByRole("menu")).toBeHidden();
    await expect(project).toBeFocused();
  });
}

test("a list's rows stack as cards, sorted, selected and acted on as on a desktop", async ({ page }) => {
  await page.goto("/deployments");
  await loaded(page);
  const table = page.getByRole("table", { name: "Deployments" });
  await expect(table).toHaveAttribute("data-stacked", "true");
  const card = table.getByRole("row", { name: /web-prd-front/ }).first();
  await expect(card.getByText("Environment", { exact: true })).toBeVisible();
  await expect(card.getByRole("link", { name: "web-prd-front", exact: true })).toBeVisible();
  const main = page.locator("#main");
  expect(await main.evaluate((m) => m.scrollWidth - m.clientWidth)).toBe(0);

  await expect(table.getByText("Sort by")).toBeVisible();
  const vms = table.getByRole("columnheader", { name: "VMs" });
  await vms.getByRole("button").click();
  await expect(vms).toHaveAttribute("aria-sort", "ascending");

  await table.getByText("Select all", { exact: true }).click();
  await expect(table.getByRole("checkbox", { name: "Select all" })).toBeChecked();
  await expect(page.getByRole("region", { name: "Bulk actions" })).toBeVisible();
  await table.getByRole("checkbox", { name: "Select all" }).click();
  await expect(page.getByRole("region", { name: "Bulk actions" })).toBeHidden();

  await card.getByRole("button", { name: /^Actions for/ }).click();
  await expect(page.getByRole("menu")).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(card.getByRole("button", { name: /^Actions for/ })).toBeFocused();
  await scan(page, "/deployments on a phone");
});

test("a VM's FQDN cut short shows whole in a tooltip on keyboard focus", async ({ page }) => {
  await page.goto("/deployments/dep_s1");
  await loaded(page);
  const vms = page.getByRole("group", { name: "Virtual machines, scrolls sideways" });
  await expect(vms).toHaveAttribute("tabindex", "0");
  const fqdn = vms.getByText("web-prd-front001.corp.example", { exact: true });
  await fqdn.focus();
  await expect(page.getByRole("tooltip")).toHaveText("web-prd-front001.corp.example");
  await page.keyboard.press("Escape");
  await expect(page.getByRole("tooltip")).toBeHidden();
  await scan(page, "a deployment on a phone");
});

for (const name of ["Light", "Dark"] as const) {
  test(`phone screenshots (${name.toLowerCase()})`, async ({ page }) => {
    await page.goto("/deployments");
    await loaded(page);
    await theme(page, name);
    await scan(page, `/deployments on a phone, ${name.toLowerCase()}`);
    await shoot(page, `phone-deployments-${name.toLowerCase()}`);
    await page.getByRole("link", { name: "web-prd-front", exact: true }).first().click();
    await loaded(page);
    await scan(page, `a deployment on a phone, ${name.toLowerCase()}`);
    await shoot(page, `phone-deployment-${name.toLowerCase()}`);
  });
}

test.describe("in French", () => {
  test.use({ locale: "fr-CH", viewport: { width: 320, height: 640 } });
  for (const name of ["light", "dark"] as const) {
    test(`the top bar and a list at 320 px, ${name} theme`, async ({ page }) => {
      await page.addInitScript((t) => localStorage.setItem("rvav.theme", t), name);
      await page.goto("/resources");
      await loaded(page);
      await expect(page.locator("html")).toHaveAttribute("lang", "fr");
      const bar = page.getByRole("banner");
      expect((await bar.boundingBox())!.height).toBeLessThan(60);
      await expect(bar.getByRole("button", { name: /^Projet/ })).toBeVisible();
      await expect(page.locator("table[data-stacked]")).toHaveCount(1);
      const main = page.locator("#main");
      expect(await main.evaluate((m) => m.scrollWidth - m.clientWidth)).toBe(0);
      await scan(page, `/resources on a phone, in French, ${name}`);
    });
  }

  test("a draft's buttons at 320 px stay inside its box", async ({ page }) => {
    await page.goto("/admin/catalog-items");
    await loaded(page);
    await page.getByRole("link", { name: "Three-tier application" }).first().click();
    await page.getByRole("button", { name: "Nouveau brouillon" }).click();
    await page.getByRole("tab", { name: "Définition" }).click();
    const draft = page.getByRole("region", { name: /^Brouillon [\d.]+$/ });
    const save = draft.getByRole("button", { name: "Enregistrer le brouillon" });
    await expect(save).toBeVisible();
    const box = (await draft.boundingBox())!;
    for (const name of ["Annuler les modifications", "Enregistrer le brouillon"]) {
      const b = (await draft.getByRole("button", { name }).boundingBox())!;
      expect(b.x + b.width, name).toBeLessThanOrEqual(box.x + box.width);
    }
  });
});
