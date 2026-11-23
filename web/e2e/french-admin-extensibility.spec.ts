import { expect, test, type Locator, type Page } from "@playwright/test";
import { scan } from "./axe";

// Administration's subscriptions, resource types, custom workflows and
// onboarding plans in French (O1.3, ADR-0061): chosen once in the top bar,
// then walked in the app (a reload would restart the mock backend). Each page
// passes axe in both themes with lang="fr".

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
  await expect(page.locator("[data-sonner-toast]")).toHaveCount(0, { timeout: 10_000 });
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

async function pick(page: Page, scope: Locator, label: string, option: string) {
  await scope.getByRole("combobox", { name: label }).click();
  await page.getByRole("option", { name: option, exact: true }).click();
}

async function inFrench(page: Page) {
  await page.goto("/");
  await expect(page.getByRole("heading", { name: "Overview", level: 1 })).toBeVisible();
  await page.getByRole("button", { name: /^Account/ }).click();
  await page.getByRole("menuitem", { name: "Français" }).click();
  await expect(page.locator("html")).toHaveAttribute("lang", "fr");
  await expect(page.getByRole("heading", { name: "Vue d'ensemble", level: 1 })).toBeVisible();
}

test("subscriptions and a subscription's runs, in French", async ({ page }) => {
  test.setTimeout(150_000);
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await inFrench(page);

  await open(page, "Abonnements");
  const list = page.getByRole("table", { name: "Abonnements" });
  await expect(list.getByRole("row", { name: /CMDB pre-check/ }).getByText("Bloquant")).toBeVisible();
  await bothThemes(page, "French subscriptions");

  await page.getByRole("button", { name: "Nouvel abonnement" }).click();
  const sheet = page.getByRole("dialog", { name: "Nouvel abonnement" });
  await pick(page, sheet, "Sujet", "network.configure");
  await expect(sheet.getByRole("checkbox", { name: /^Bloquant/ })).toBeEnabled();
  await expect(sheet.getByRole("button", { name: "Créer l'abonnement" })).toBeVisible();
  await scan(page, "French new subscription");
  await sheet.getByRole("button", { name: "Annuler" }).click();
  await expect(sheet).toBeHidden();

  await list.getByRole("link", { name: "CMDB naming" }).click();
  await expect(page.getByRole("heading", { name: "CMDB naming", level: 1 })).toBeVisible();
  await expect(page.getByRole("button", { name: "Modifier l'abonnement" })).toBeVisible();
  const runs = page.getByRole("table", { name: "Exécutions" });
  await expect(runs.getByText("Reprise").first()).toBeVisible();
  await bothThemes(page, "a French subscription");

  await runs
    .getByRole("button", { name: /^Ouvrir l'exécution lancée/ })
    .first()
    .click();
  const run = page.getByRole("dialog", { name: "Exécution" });
  await expect(run.getByText("renommée web-dev-app742", { exact: false })).toBeVisible();
  await scan(page, "a French run");
  await page.keyboard.press("Escape");
  await expect(run).toBeHidden();
  expect(errors).toEqual([]);
});

test("resource types and custom workflows, in French, and back to English without a reload", async ({ page }) => {
  test.setTimeout(150_000);
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await inFrench(page);

  await open(page, "Types de ressources");
  const types = page.getByRole("table", { name: "Types de ressources" });
  await expect(types.getByRole("row", { name: /Load balancer/ })).toContainText("AWX · create-load-balancer");
  await bothThemes(page, "French resource types");

  await page.getByRole("button", { name: "Nouveau type de ressource" }).first().click();
  const sheet = page.getByRole("dialog", { name: "Nouveau type de ressource" });
  await expect(sheet.getByRole("group", { name: "Le crée" })).toBeVisible();
  await expect(sheet.getByRole("button", { name: "Créer le type de ressource" })).toBeVisible();
  await scan(page, "French new resource type");
  await sheet.getByRole("button", { name: "Annuler" }).click();
  await expect(sheet).toBeHidden();

  await open(page, "Workflows personnalisés");
  await expect(page.getByRole("table", { name: "Workflows déclarés" })).toBeVisible();
  await bothThemes(page, "French custom workflows");
  await page.getByRole("button", { name: "Ajouter un workflow" }).first().click();
  const register = page.getByRole("dialog", { name: "Ajouter un workflow" });
  await expect(register.getByRole("button", { name: "Ajouter le workflow" })).toBeVisible();
  await scan(page, "French workflow registration");
  await register.getByRole("button", { name: "Annuler" }).click();
  await expect(register).toBeHidden();

  await page.getByRole("button", { name: /^Compte/ }).click();
  await page.getByRole("menuitem", { name: "English" }).click();
  await expect(page.locator("html")).toHaveAttribute("lang", "en");
  await expect(page.getByRole("heading", { name: "Custom workflows", level: 1 })).toBeVisible();
  await expect(page.getByRole("button", { name: "Add a workflow" }).first()).toBeVisible();
  expect(errors).toEqual([]);
});

test("an onboarding plan saved, run and shown, in French", async ({ page }) => {
  test.setTimeout(150_000);
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await inFrench(page);

  await nav(page).getByRole("link", { name: "Intégration de l'existant", exact: true }).click();
  await page.getByRole("tab", { name: "Plans", exact: true }).click();
  await expect(page.getByText("Aucun plan pour l'instant")).toBeVisible();
  await page.getByRole("button", { name: "Nouveau plan" }).click();
  const sheet = page.getByRole("dialog", { name: "Nouveau plan" });
  await sheet.getByRole("textbox", { name: "Nom", exact: true }).fill("ERP servers");
  await sheet.getByRole("textbox", { name: /^Tag/ }).fill("app/erp");
  await expect(sheet.getByText("12 VM que rvav ne gère pas correspondent maintenant.")).toBeVisible();
  await pick(page, sheet, "Projet", "Web Platform");
  await pick(page, sheet, "Où elles vont", "Toutes dans un nouveau déploiement");
  await pick(page, sheet, "Environnement", "Production");
  await pick(page, sheet, "Propriétaire", "Thomas Nguyen");
  await pick(page, sheet, "Élément de catalogue", "RHEL 9 server");
  await sheet.getByRole("textbox", { name: "Nom du déploiement", exact: true }).fill("erp-prd");
  await scan(page, "a French plan's sheet");
  await sheet.getByRole("button", { name: "Créer le plan" }).click();

  await expect(page.getByRole("heading", { name: "ERP servers", level: 1 })).toBeVisible();
  await expect(page.getByText("Dans erp-prd, un seul déploiement, dans Web Platform, en Production")).toBeVisible();
  await expect(page.getByText("Jamais lancé")).toBeVisible();
  await bothThemes(page, "a French onboarding plan");

  await page.getByRole("button", { name: "Lancer maintenant" }).click();
  const runs = page.getByRole("table", { name: "Exécutions" });
  await expect(runs.getByText("Réussie")).toBeVisible({ timeout: 15_000 });
  await expect(runs.getByText("Intégrées : 12, ignorées : 0, en échec : 0")).toBeVisible();
  await bothThemes(page, "a French onboarding plan's run");
  expect(errors).toEqual([]);
});
