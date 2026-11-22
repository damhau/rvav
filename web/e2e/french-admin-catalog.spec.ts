import { expect, test, type Page } from "@playwright/test";
import { scan } from "./axe";

// Administration's catalog in French (O1.3, ADR-0061): its items, categories
// and secrets, an item's page with its form designer and composite designer,
// variables, property groups and the form library. Chosen once in the top
// bar, then walked in the app (a reload would restart the mock backend). Each
// page passes axe in both themes with lang="fr".

async function loaded(page: Page) {
  await expect(page.getByRole("main")).toBeVisible();
  await expect(page.locator(".animate-pulse, [role=status][aria-label=Chargement]")).toHaveCount(0);
  await page.waitForTimeout(300);
}

async function theme(page: Page, name: "Clair" | "Sombre") {
  await page.getByRole("button", { name: /^Thème/ }).click();
  await page.getByRole("menuitem", { name }).click();
  await expect(page.locator("html")).toHaveAttribute("data-theme", name === "Clair" ? "light" : "dark");
  await page.waitForTimeout(500);
}

async function bothThemes(page: Page, where: string) {
  await loaded(page);
  await expect(page.locator("html")).toHaveAttribute("lang", "fr");
  await page.mouse.move(0, 0);
  await theme(page, "Clair");
  await scan(page, `${where}, light`);
  await theme(page, "Sombre");
  await scan(page, `${where}, dark`);
  await theme(page, "Clair");
}

const nav = (page: Page) => page.getByRole("navigation", { name: "Principale" });

async function open(page: Page, link: string, heading = link) {
  await nav(page).getByRole("link", { name: link, exact: true }).click();
  await expect(page.getByRole("heading", { name: heading, level: 1 })).toBeVisible();
}

async function tab(page: Page, name: string) {
  await page.getByRole("tab", { name, exact: true }).click();
  await expect(page.getByRole("tab", { name, exact: true })).toHaveAttribute("aria-selected", "true");
}

async function inFrench(page: Page) {
  await page.goto("/");
  await expect(page.getByRole("heading", { name: "Overview", level: 1 })).toBeVisible();
  await page.getByRole("button", { name: /^Account/ }).click();
  await page.getByRole("menuitem", { name: "Français" }).click();
  await expect(page.locator("html")).toHaveAttribute("lang", "fr");
  await expect(page.getByRole("heading", { name: "Vue d'ensemble", level: 1 })).toBeVisible();
}

test("a catalog item, its form designer and composite designer, in French, then English without a reload", async ({
  page,
}) => {
  test.setTimeout(180_000);
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await inFrench(page);

  await open(page, "Éléments de catalogue");
  const items = page.getByRole("region", { name: "Éléments", exact: true });
  await expect(items.getByRole("columnheader", { name: "Ouvert à" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Nouvel élément" })).toBeVisible();
  await bothThemes(page, "catalog items");

  await items.getByRole("link", { name: "PostgreSQL 16 cluster" }).click();
  await expect(page.getByRole("heading", { name: "PostgreSQL 16 cluster", level: 1 })).toBeVisible();
  await expect(page.getByRole("tab", { name: "Versions", exact: true })).toHaveAttribute("aria-selected", "true");
  await expect(page.getByRole("columnheader", { name: "Notes de version" })).toBeVisible();
  await bothThemes(page, "an item's versions");

  await page.getByRole("button", { name: "Nouveau brouillon" }).click();
  await expect(page.getByRole("tab", { name: "Concepteur de formulaire", exact: true })).toBeVisible();
  await tab(page, "Concepteur de formulaire");
  await expect(page.getByRole("region", { name: "Champs", exact: true })).toBeVisible();
  await expect(page.getByRole("region", { name: "Aperçu", exact: true })).toBeVisible();
  await expect(page.getByRole("combobox", { name: "Ajouter un champ" })).toBeVisible();
  await bothThemes(page, "the form designer");

  await tab(page, "Définition");
  await loaded(page);
  await expect(page.getByRole("button", { name: "Ajouter une étape", exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Défaire", exact: true })).toBeVisible();
  await page
    .getByRole("region", { name: "Étapes de PostgreSQL 16 cluster" })
    .getByRole("button", { name: /^AWX Backups\./ })
    .click();
  const details = page.getByRole("region", { name: /^Sélection\s:\sBackups$/ });
  await details.getByRole("combobox", { name: "Exécute" }).click();
  await page.getByRole("option", { name: "Un appel REST" }).click();
  await expect(details.getByRole("group", { name: "Appel REST" })).toBeVisible();
  await details.getByRole("combobox", { name: "Retour arrière par" }).click();
  await page.getByRole("option", { name: "Un appel REST" }).click();
  await expect(details.getByRole("textbox", { name: "URL du retour arrière" })).toBeVisible();
  await expect(details.getByRole("button", { name: "Retirer l'étape" })).toBeVisible();
  await bothThemes(page, "the composite designer, a REST call selected");

  await page.getByRole("button", { name: /^Compte/ }).click();
  await page.getByRole("menuitem", { name: "English" }).click();
  await expect(page.locator("html")).toHaveAttribute("lang", "en");
  await expect(page.getByRole("tab", { name: "Definition", exact: true })).toHaveAttribute("aria-selected", "true");
  await expect(page.getByRole("button", { name: "Add a step", exact: true })).toBeVisible();
  await expect(
    page.getByRole("region", { name: "Selected: Backups" }).getByRole("group", { name: "REST call" }),
  ).toBeVisible();
  expect(errors).toEqual([]);
});

test("categories, secrets, variables, property groups and the form library, in French", async ({ page }) => {
  test.setTimeout(180_000);
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await inFrench(page);

  await open(page, "Éléments de catalogue");
  await tab(page, "Catégories");
  await expect(page.getByRole("button", { name: "Nouvelle catégorie" })).toBeVisible();
  await expect(page.getByRole("table", { name: "Catégories" })).toBeVisible();
  await bothThemes(page, "catalog categories");
  await tab(page, "Secrets");
  await expect(page.getByRole("button", { name: "Nouveau secret" })).toBeVisible();
  await expect(page.getByRole("table", { name: "Secrets" })).toBeVisible();
  await bothThemes(page, "catalog secrets");

  await open(page, "Variables");
  await expect(
    page.getByRole("table", { name: "Variables" }).getByRole("columnheader", { name: "Lue par" }),
  ).toBeVisible();
  await bothThemes(page, "variables");
  await page.getByRole("button", { name: "Nouvelle variable" }).first().click();
  const sheet = page.getByRole("dialog", { name: "Nouvelle variable" });
  await expect(sheet.getByRole("textbox", { name: "Groupe" })).toBeVisible();
  await expect(sheet.getByRole("button", { name: "Ajouter" })).toBeVisible();
  // The sheet is modal: the theme menu is out of reach until it closes.
  await scan(page, "a new variable, light");
  await page.keyboard.press("Escape");
  await expect(sheet).toBeHidden();

  await open(page, "Groupes de propriétés");
  await expect(page.getByRole("button", { name: "Nouveau groupe de propriétés" })).toBeVisible();
  await expect(
    page.getByRole("table", { name: "Groupes de propriétés" }).getByRole("columnheader", { name: "Dernière version" }),
  ).toBeVisible();
  await bothThemes(page, "property groups");

  await open(page, "Bibliothèque de formulaires");
  await expect(page.getByRole("button", { name: "Nouvelle entrée" })).toBeVisible();
  await expect(
    page
      .getByRole("table", { name: "Entrées de la bibliothèque de formulaires" })
      .getByRole("columnheader", { name: "Rôle" }),
  ).toBeVisible();
  await bothThemes(page, "the form library");
  expect(errors).toEqual([]);
});
