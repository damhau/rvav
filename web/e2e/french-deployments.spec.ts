import { expect, test, type Page } from "@playwright/test";
import { scan } from "./axe";

// The deployments pages in French (O1.3, ADR-0061): the list, a deployment's
// tabs and Actions menu, a VM's properties and the update page, with
// lang="fr", passing axe in both themes. Built-in day-2 actions have French
// names; an AWX action's name and the server's reasons stay as it sends them.
// Switching back to English shows at once.

async function loaded(page: Page) {
  await expect(page.getByRole("main")).toBeVisible();
  await expect(page.locator(".animate-pulse, [role=status][aria-label=Chargement]")).toHaveCount(0);
  await page.waitForTimeout(300);
}

// In the app: a reload would start the mock backend afresh.
async function theme(page: Page, name: "Clair" | "Sombre") {
  await page.getByRole("button", { name: /^Thème/ }).click();
  await page.getByRole("menuitem", { name }).click();
  await expect(page.locator("html")).toHaveAttribute("data-theme", name === "Clair" ? "light" : "dark");
  await page.waitForTimeout(500);
}

async function bothThemes(page: Page, where: string) {
  await loaded(page);
  await expect(page.locator("html")).toHaveAttribute("lang", "fr");
  await theme(page, "Clair");
  await scan(page, `${where}, light`);
  await theme(page, "Sombre");
  await scan(page, `${where}, dark`);
  await theme(page, "Clair");
}

async function openActions(page: Page) {
  await page.getByRole("button", { name: "Actions", exact: true }).click();
  const menu = page.getByRole("menu");
  await expect(menu).toBeVisible();
  return menu;
}

test("the deployments pages speak French, and English again once chosen", async ({ page }) => {
  test.setTimeout(240_000);
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/deployments");
  await expect(page.getByRole("heading", { name: "Deployments", level: 1 })).toBeVisible();

  await page.getByRole("button", { name: /^Account/ }).click();
  await page.getByRole("menuitem", { name: "Français" }).click();
  await expect(page.locator("html")).toHaveAttribute("lang", "fr");
  await expect(page.getByRole("heading", { name: "Déploiements", level: 1 })).toBeVisible();
  await expect(page.getByText(/^\d+ déploiements · \d+ VM, \d+ allumées/)).toBeVisible();
  await expect(page.getByLabel("Les miens seulement")).toBeVisible();
  await expect(page.getByRole("link", { name: "Nouvelle demande" })).toBeVisible();
  await bothThemes(page, "deployments");

  await page.getByRole("link", { name: "web-dev-api", exact: true }).click();
  await expect(page.getByRole("heading", { name: "web-dev-api", level: 1 })).toBeVisible();
  await expect(page.getByText(/^Propriétaire\s:\sThomas Nguyen$/)).toBeVisible();
  await expect(page.getByRole("tab", { name: "Vue d'ensemble" })).toHaveAttribute("aria-selected", "true");
  await expect(page.getByRole("region", { name: "Machines virtuelles" })).toBeVisible();
  const lease = page.getByRole("region", { name: "Bail", exact: true });
  await expect(lease.getByText(/1 prolongation sur 2 utilisée\s; jusqu'à 90 jours sans approbation/)).toBeVisible();
  await expect(lease.getByRole("button", { name: "Prolonger le bail" })).toBeVisible();
  await bothThemes(page, "a deployment's overview");

  const menu = await openActions(page);
  await expect(menu.getByText("Cycle de vie")).toBeVisible();
  await expect(menu.getByRole("menuitem", { name: /^Allumer/ })).toContainText("The VM is already powered on.");
  await scan(page, "a deployment's Actions menu");
  await page.keyboard.press("Escape");
  await expect(menu).toBeHidden();

  for (const name of ["Clair", "Sombre"] as const) {
    await theme(page, name);
    await page.getByRole("button", { name: "Propriétés de web-dev-api004" }).click();
    const sheet = page.getByRole("dialog", { name: "web-dev-api004" });
    await expect(sheet.getByRole("region", { name: "Cartes réseau" })).toBeVisible();
    await expect(sheet).toContainText(/\d+ vCPU · \d+\sGo de mémoire/);
    await scan(page, `a VM's properties, ${name}`);
    await page.keyboard.press("Escape");
    await expect(sheet).toBeHidden();
  }
  await theme(page, "Clair");

  await page.getByRole("tab", { name: "Topologie" }).click();
  await expect(page.getByRole("region", { name: "Topologie de web-dev-api" })).toBeVisible();
  await bothThemes(page, "a deployment's topology");
  await page.getByRole("tab", { name: "Historique" }).click();
  await expect(page.getByRole("region", { name: "Historique", exact: true })).toBeVisible();
  await bothThemes(page, "a deployment's history");
  await page.getByRole("tab", { name: "Entrées" }).click();
  await expect(page.getByRole("tab", { name: "Entrées" })).toHaveAttribute("aria-selected", "true");
  await bothThemes(page, "a deployment's inputs");

  await page
    .getByRole("navigation", { name: "Principale" })
    .getByRole("link", { name: "Déploiements", exact: true })
    .click();
  await page.getByRole("link", { name: "web-tst-search", exact: true }).click();
  await expect(page.getByRole("heading", { name: "web-tst-search", level: 1 })).toBeVisible();
  await (await openActions(page)).getByRole("menuitem", { name: "Mettre à jour", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Mettre à jour web-tst-search", level: 1 })).toBeVisible();
  const summary = page.getByRole("region", { name: "Ce que fait la mise à jour" });
  await expect(summary.getByText("Changez un champ pour voir ce que fait la mise à jour.")).toBeVisible();
  await expect(summary.getByRole("button", { name: "Vérifier la mise à jour" })).toBeDisabled();
  await expect(summary).toContainText("Aucune approbation nécessaire");
  await bothThemes(page, "the update page");

  await page.getByRole("button", { name: /^Compte/ }).click();
  await page.getByRole("menuitem", { name: "English" }).click();
  await expect(page.locator("html")).toHaveAttribute("lang", "en");
  await expect(page.getByRole("heading", { name: "Update web-tst-search", level: 1 })).toBeVisible();
  await expect(page.getByRole("region", { name: "What the update does" })).toContainText("No approval needed");
  expect(errors).toEqual([]);
});
