import { expect, test, type Locator, type Page } from "@playwright/test";
import { scan } from "./axe";
import { press, skipToContent } from "./keyboard";

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

  await open(page, "Éléments du catalogue");
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
  await details.getByRole("combobox", { name: "Compensée par" }).click();
  await page.getByRole("option", { name: "Un appel REST" }).click();
  await expect(details.getByRole("textbox", { name: "URL de la compensation" })).toBeVisible();
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

/** A composite that waits for the requester, branches on their answer, then waits for a signal that never comes. */
const composite = `form_schema:
  type: object
  properties: {}
ui_schema:
  layout:
    kind: page
    sections: [{key: main, title: Principal, columns: 1, fields: []}]
  fields: {}
initial_values: {}
blueprint:
  kind: composite
  steps:
    - key: accord
      title: Accord du demandeur
      ask: {requester: true, timeout: 48h, on_timeout: continue}
    - key: voie
      title: Quelle voie
      branch:
        cases:
          - title: Approuvée
            when: steps.accord.answered
            steps:
              - key: dire
                title: Le dire
                notify: {requester: true, message: "En route"}
        else:
          - key: rien
            title: Ne rien dire
            notify: {requester: true, message: "Rien"}
    - key: feu
      title: Feu vert
      signal: {name: feu-vert, timeout: 1m, on_timeout: continue}
`;

/** What the canvas would say in English, rather than the steps' own titles. */
const english =
  /\b(Person|Branch|Asks|Wants|Waits for|Timeout|then it|the requester|an approval|Tells|Says|Runs first|Runs after|Otherwise|Case \d|On the side|Request|Done|Running|Skipped|Not started|taken)\b/;

/** The names of a canvas's nodes and edges that hold an English word. */
async function inEnglish(canvas: Locator) {
  const names = await canvas
    .locator(".react-flow__node, .react-flow__edge")
    .evaluateAll((drawn) => drawn.map((d) => d.getAttribute("aria-label") ?? ""));
  return names.filter((n) => english.test(n));
}

test("a composite with a person, a branch and a signal, on the canvas and in its run, in French", async ({ page }) => {
  test.setTimeout(180_000);
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await inFrench(page);

  await open(page, "Éléments du catalogue");
  await page.getByRole("button", { name: "Nouvel élément" }).click();
  const dialog = page.getByRole("dialog", { name: "Nouvel élément" });
  await dialog.getByRole("textbox", { name: "Nom" }).fill("Mise en service");
  await dialog.getByRole("textbox", { name: "Slug" }).fill("mise-en-service");
  await dialog.getByRole("textbox", { name: "Résumé" }).fill("Attend le demandeur, puis un signal.");
  await dialog.getByRole("combobox", { name: "Type" }).click();
  await page.getByRole("option", { name: /composite/i }).click();
  await dialog.getByRole("button", { name: "Créer" }).click();
  await expect(page.getByRole("heading", { name: "Mise en service", level: 1 })).toBeVisible();
  await tab(page, "Définition");
  const editor = page.getByRole("textbox", { name: /^Définition de Mise en service/ });
  await expect(editor).toContainText("kind: composite");
  await editor.click();
  await page.keyboard.press("Control+A");
  await page.keyboard.insertText(composite);

  const steps = page.getByRole("region", { name: "Étapes de Mise en service" });
  const node = (name: RegExp) => steps.getByRole("button", { name });
  await expect(
    node(
      /^Personne Accord du demandeur\. accord\. Demande à\s:\sle demandeur\. Attend\s:\sune approbation ou un refus\. Délai\s:\s48h, puis elle continue/,
    ),
  ).toBeVisible();
  await expect(node(/^Branche Quelle voie\. voie\./)).toBeVisible();
  await expect(node(/^Message Le dire\. dire\. Prévient\s:\sle demandeur/)).toBeVisible();
  await expect(
    node(/^Signal Feu vert\. feu\. Attend\s:\sle signal feu-vert\. Délai\s:\s1m, puis elle continue/),
  ).toBeVisible();
  expect(await inEnglish(steps)).toEqual([]);
  await bothThemes(page, "a composite with a person, a branch and a signal");

  await page.getByRole("button", { name: "Enregistrer le brouillon" }).click();
  await expect(page.getByText(/^Brouillon [\d.]+ enregistré$/)).toBeVisible();
  await tab(page, "Partage");
  await page.getByRole("radio", { name: /Projets choisis/ }).check();
  await page.getByRole("checkbox", { name: "Web Platform" }).check();
  await page.getByRole("button", { name: "Enregistrer les projets" }).click();
  await expect(page.getByText("Projets enregistrés")).toBeVisible();
  await tab(page, "Versions");
  await page.getByRole("button", { name: /^Publier/ }).click();
  await expect(page.getByRole("main").getByText(/^[\d.]+ publiée$/)).toBeVisible();
  await nav(page).getByRole("link", { name: "Catalogue", exact: true }).click();
  await page.getByRole("link", { name: /^Demander Mise en service/ }).click();
  await page.getByRole("button", { name: "Soumettre la demande" }).click();
  await expect(page).toHaveURL(/\/requests\/REQ-\d+$/);

  // The run waits for the requester, who approves from the keyboard; the branch takes its case and the signal's wait ends.
  const run = page.getByRole("application", { name: /^Exécution de REQ-\d+$/ });
  await expect(run.getByRole("button", { name: /^Personne Accord du demandeur\. En cours\./ })).toBeVisible({
    timeout: 20_000,
  });
  const answer = page.getByRole("form", { name: /^Votre réponse\s:\sAccord du demandeur$/ });
  await skipToContent(page, "Aller au contenu");
  await press(page, answer.getByRole("button", { name: "Approuver" }));
  await expect(answer).toHaveCount(0);
  await expect(run.getByRole("button", { name: /^Signal Feu vert\. Terminée\./ })).toBeVisible({ timeout: 40_000 });
  await expect(run.getByRole("button", { name: /^Personne Accord du demandeur\. Terminée\./ })).toBeVisible();
  await expect(run.getByRole("button", { name: /^Branche Quelle voie\. Terminée\./ })).toBeVisible();
  await expect(run.getByRole("button", { name: /^Message Le dire\. Terminée\./ })).toBeVisible();
  await expect(run.getByRole("button", { name: /^Message Ne rien dire\. Ignorée\./ })).toBeVisible();
  expect(await inEnglish(run)).toEqual([]);
  await bothThemes(page, "a composite's run");
  await page.getByRole("button", { name: "Chronologie" }).click();
  await expect(page.getByRole("region", { name: "Étapes", exact: true })).toContainText("Soumise");
  await bothThemes(page, "a composite's timeline");
  expect(errors).toEqual([]);
});

test("categories, secrets, variables, property groups and the form library, in French", async ({ page }) => {
  test.setTimeout(180_000);
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await inFrench(page);

  await open(page, "Éléments du catalogue");
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
