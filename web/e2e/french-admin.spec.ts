import { expect, test, type Page } from "@playwright/test";
import { scan } from "./axe";

// Administration's integrations, system and catalog pages in French (O1.3,
// ADR-0061): chosen once in the top bar, then walked in the app (a reload
// would restart the mock backend). Each page passes axe in both themes with
// lang="fr", and reflows at 320 px in a French browser.

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

async function integration(page: Page, name: string) {
  await open(page, "Intégrations");
  await page.getByRole("table", { name: "Connexions" }).getByRole("link", { name, exact: true }).click();
  await expect(page.getByRole("heading", { name, level: 1 })).toBeVisible();
}

test("integrations, in French, and back to English without a reload", async ({ page }) => {
  test.setTimeout(180_000);
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await inFrench(page);

  await open(page, "Intégrations");
  const connections = page.getByRole("table", { name: "Connexions" });
  await expect(connections.getByRole("columnheader", { name: /^État/ })).toBeVisible();
  await expect(connections.getByRole("link", { name: "Fournisseur d'identité" })).toBeVisible();
  await bothThemes(page, "integrations");
  await tab(page, "Vérifications");
  const reconcile = page.getByRole("region", { name: "Rapprochement EfficientIP" });
  await reconcile.getByRole("button", { name: "Lancer la vérification" }).click();
  await expect(reconcile.getByRole("table", { name: "Adresses qu'aucun déploiement ne possède" })).toBeVisible();
  await expect(reconcile.getByRole("button", { name: "Vérifier à nouveau" })).toBeVisible();
  await bothThemes(page, "the EfficientIP reconcile");

  await integration(page, "vCenter");
  const vcenter = page.getByRole("region", { name: "Connexion vCenter" });
  await vcenter.getByRole("button", { name: "Tester la connexion" }).click();
  await expect(vcenter.getByRole("region", { name: "Test vCenter" })).toBeVisible();
  await expect(page.getByRole("region", { name: "Console", exact: true })).toBeVisible();
  const vcenters = page.getByRole("region", { name: "vCenter", exact: true });
  await expect(vcenters.getByText("La connexion ci-dessus")).toBeVisible();
  await bothThemes(page, "vCenter");
  await vcenters.getByRole("button", { name: "Ajouter un vCenter" }).click();
  const sheet = page.getByRole("dialog", { name: "Ajouter un vCenter" });
  await expect(sheet.getByRole("button", { name: "Enregistrer le vCenter" })).toBeVisible();
  await expect(sheet.getByRole("textbox", { name: "Adresse du SDK" })).toBeVisible();
  await scan(page, "a new vCenter");
  await sheet.getByRole("button", { name: "Annuler" }).click();
  await expect(sheet).toBeHidden();

  await integration(page, "NSX");
  await expect(page.getByRole("region", { name: "NSX Manager", exact: true })).toBeVisible();
  await bothThemes(page, "NSX");

  await integration(page, "Fournisseur d'identité");
  const provider = page.getByRole("region", { name: "Paramètres du fournisseur" });
  await expect(provider.getByRole("button", { name: "Tester le fournisseur" })).toBeVisible();
  await expect(provider.getByText("URI de redirection, à enregistrer chez le fournisseur")).toBeVisible();
  await bothThemes(page, "the identity provider");

  await integration(page, "AWX");
  const token = page.getByRole("region", { name: "Jeton d'inventaire" });
  await expect(
    token.getByRole("button", { name: /^Créer (un|un nouveau) jeton et le placer dans AWX$/ }),
  ).toBeVisible();
  await bothThemes(page, "AWX");

  await integration(page, "ServiceNow");
  const snow = page.getByRole("region", { name: "Jeton de ServiceNow", exact: true });
  await expect(snow.getByRole("button", { name: "Créer un nouveau jeton" })).toBeVisible();
  const cmdb = page.getByRole("region", { name: "CMDB", exact: true });
  await expect(cmdb.getByRole("button", { name: "Rapprocher maintenant" })).toBeVisible();
  await expect(cmdb.getByRole("table", { name: "CI que rvav n'a plus" })).toBeVisible();
  await bothThemes(page, "ServiceNow");
  await snow.getByRole("button", { name: "Révoquer" }).click();
  const revoke = page.getByRole("alertdialog", { name: /^Révoquer le jeton de ServiceNow/ });
  await expect(revoke.getByRole("button", { name: "Révoquer le jeton" })).toBeVisible();
  await scan(page, "revoking ServiceNow's token");
  await revoke.getByRole("button", { name: "Annuler" }).click();
  await expect(revoke).toBeHidden();
  await integration(page, "AWX");

  await page.getByRole("button", { name: /^Compte/ }).click();
  await page.getByRole("menuitem", { name: "English" }).click();
  await expect(page.locator("html")).toHaveAttribute("lang", "en");
  await expect(page.getByRole("region", { name: "Inventory token" })).toBeVisible();
  await expect(
    page.getByRole("region", { name: "AWX connection" }).getByRole("button", { name: "Save settings" }),
  ).toBeVisible();
  expect(errors).toEqual([]);
});

test("setup, branding, the audit log and the system, in French", async ({ page }) => {
  test.setTimeout(180_000);
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await inFrench(page);

  await open(page, "Mise en route");
  await expect(page.getByRole("progressbar", { name: "Étapes faites" })).toBeVisible();
  await expect(page.getByRole("list", { name: "Étapes" }).getByRole("listitem")).toHaveCount(8);
  await bothThemes(page, "setup");

  await open(page, "Image de marque");
  await expect(page.getByRole("button", { name: "Enregistrer l'image de marque" })).toBeVisible();
  await expect(page.getByRole("region", { name: "Logos", exact: true })).toBeVisible();
  await bothThemes(page, "branding");

  await open(page, "Journal d'audit");
  const events = page.getByRole("table", { name: "Événements d'audit" });
  await expect(events.getByRole("columnheader", { name: "Quand" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Exporter en CSV" })).toBeVisible();
  await bothThemes(page, "the audit log");
  await events
    .getByRole("button", { name: /^Ouvrir l'événement du/ })
    .first()
    .click();
  const event = page.getByRole("dialog");
  await expect(event.getByRole("heading", { name: "Détail" })).toBeVisible();
  await scan(page, "an audit event");
  await page.keyboard.press("Escape");
  await expect(event).toBeHidden();

  await open(page, "Transfert de l'audit");
  const destinations = page.getByRole("region", { name: "Destinations" });
  await expect(destinations.getByRole("row", { name: /SOC sign-ins/ })).toContainText("En échec");
  await bothThemes(page, "audit forwarding");
  await page.getByRole("button", { name: "Nouvelle destination" }).click();
  const destination = page.getByRole("dialog", { name: "Nouvelle destination" });
  await expect(destination.getByRole("button", { name: "Enregistrer la destination" })).toBeVisible();
  await scan(page, "a new audit destination");
  await destination.getByRole("button", { name: "Annuler" }).click();
  await expect(destination).toBeHidden();

  await open(page, "Système");
  await expect(page.getByRole("region", { name: "Cette installation" })).toContainText("Mode FIPS 140-3");
  await expect(page.getByRole("table", { name: "Processus" })).toBeVisible();
  await expect(page.getByRole("main").getByRole("link", { name: "Intégrations", exact: true })).toBeVisible();
  await bothThemes(page, "system");

  await page.getByRole("button", { name: /^Compte/ }).click();
  await page.getByRole("menuitem", { name: "English" }).click();
  await expect(page.locator("html")).toHaveAttribute("lang", "en");
  await expect(page.getByRole("region", { name: "This install" })).toBeVisible();
  await expect(page.getByRole("table", { name: "Processes" })).toBeVisible();
  expect(errors).toEqual([]);
});

test("onboarding and AWX's templates, in French", async ({ page }) => {
  test.setTimeout(150_000);
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await inFrench(page);

  await open(page, "Intégration de l'existant");
  const vms = page.getByRole("table", { name: "VM à intégrer" });
  await expect(vms.getByRole("columnheader", { name: /^OS invité/ })).toBeVisible();
  await bothThemes(page, "onboarding");
  await page.getByRole("textbox", { name: "Rechercher" }).fill("app-old");
  await expect(page.getByText("8 VM", { exact: true })).toBeVisible();
  await vms.getByRole("checkbox", { name: "Sélectionner app-old01" }).check();
  await expect(page.getByRole("region", { name: "VM sélectionnées" })).toContainText("1 VM sélectionnée");
  await page.getByRole("button", { name: "Intégrer 1 VM…" }).click();
  const sheet = page.getByRole("dialog", { name: "Intégrer 1 VM" });
  await expect(sheet.getByRole("checkbox", { name: /^Protéger de la suppression/ })).toBeVisible();
  await scan(page, "adopting a VM");
  await sheet.getByRole("button", { name: "Annuler" }).click();
  await expect(sheet).toBeHidden();

  await open(page, "Éléments du catalogue");
  await tab(page, "Modèles AWX");
  const templates = page.getByRole("region", { name: "Importés d'AWX" });
  await expect(templates.getByRole("button", { name: "Synchroniser maintenant" })).toBeVisible();
  await expect(templates.getByRole("columnheader", { name: /^Élément de catalogue/ })).toBeVisible();
  await bothThemes(page, "AWX's templates");

  await page.getByRole("button", { name: /^Compte/ }).click();
  await page.getByRole("menuitem", { name: "English" }).click();
  await expect(page.locator("html")).toHaveAttribute("lang", "en");
  await expect(page.getByRole("heading", { name: "Catalog items", level: 1 })).toBeVisible();
  await expect(page.getByRole("tab", { name: "AWX templates" })).toHaveAttribute("aria-selected", "true");
  expect(errors).toEqual([]);
});

test("day-2 actions and a day-2 form, in French", async ({ page }) => {
  test.setTimeout(150_000);
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await inFrench(page);

  await open(page, "Actions de jour 2");
  const actions = page.getByRole("table", { name: "Actions de jour 2 d'AWX" });
  const row = actions.getByRole("row", { name: /Install monitoring agent/ });
  await expect(row).toContainText("Proposée");
  await expect(page.getByRole("table", { name: "Actions de jour 2 des workflows" })).toBeVisible();
  await bothThemes(page, "day-2 actions");
  await row.getByRole("button", { name: "Actions pour Install monitoring agent" }).click();
  await page.getByRole("menuitem", { name: "Modifier" }).click();
  const sheet = page.getByRole("dialog", { name: "Modifier Install monitoring agent" });
  await expect(sheet.getByRole("button", { name: "Enregistrer l'action" })).toBeVisible();
  await expect(sheet.getByRole("group", { name: "S'exécute sur" })).toBeVisible();
  await scan(page, "a day-2 action's sheet");
  await sheet.getByRole("button", { name: "Annuler" }).click();
  await expect(sheet).toBeHidden();

  await row.getByRole("button", { name: "Actions pour Install monitoring agent" }).click();
  await page.getByRole("menuitem", { name: "Concevoir le formulaire" }).click();
  await expect(page.getByRole("heading", { name: "Formulaire de Install monitoring agent", level: 1 })).toBeVisible();
  await expect(page.getByText(/^Pas encore conçu\s: voici le questionnaire AWX/)).toBeVisible();
  await expect(page.getByRole("region", { name: "Aperçu" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Activer" })).toBeVisible();
  await bothThemes(page, "a day-2 form");

  await page.getByRole("button", { name: /^Compte/ }).click();
  await page.getByRole("menuitem", { name: "English" }).click();
  await expect(page.locator("html")).toHaveAttribute("lang", "en");
  await expect(page.getByRole("heading", { name: "Install monitoring agent form", level: 1 })).toBeVisible();
  await expect(page.getByText(/^Not designed yet: this is the AWX survey/)).toBeVisible();
  expect(errors).toEqual([]);
});

test("the vRA import, in French", async ({ page }) => {
  test.setTimeout(150_000);
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await inFrench(page);

  await open(page, "Import depuis vRA");
  await expect(page.getByText("3 projets · 5 modèles · 4 déploiements · 7 workflows vRO")).toBeVisible();
  const steps = page.getByRole("list", { name: "Étapes" });
  await expect(steps.getByRole("listitem").first()).toContainText(/Lire vRA\s: terminée/);
  await expect(page.getByRole("tab", { name: "Projets", selected: true })).toBeVisible();
  const projects = page.getByRole("table", { name: "Projets vRA" });
  await expect(projects.getByRole("combobox", { name: "Projet rvav pour Web Platform" })).toHaveText(
    /^Par nom\s: Web Platform$/,
  );
  await bothThemes(page, "the vRA import's projects");

  await page.getByRole("tab", { name: /^Éléments du catalogue/ }).click();
  const templates = page.getByRole("table", { name: "Modèles de cloud" });
  await expect(templates.getByRole("row", { name: /RHEL 9 Server/ }).getByText("Inchangé")).toBeVisible();
  await bothThemes(page, "the vRA import's catalog items");

  await page.getByRole("tab", { name: /^vRO/ }).click();
  await expect(
    page.getByRole("table", { name: "Workflows et actions vRO" }).getByText("C · Workflow personnalisé"),
  ).toBeVisible();
  await page.getByRole("tab", { name: /^Bascule/ }).click();
  await expect(page.getByText("0 sur 3 projets basculés")).toBeVisible();
  await expect(
    page.getByRole("region", { name: "Legacy Apps" }).getByText("Basculera une fois associé à un projet rvav"),
  ).toBeVisible();
  await bothThemes(page, "the vRA import's cutover");

  await page.getByRole("button", { name: "Importer…" }).click();
  const dialog = page.getByRole("dialog", { name: "Importer depuis vRA" });
  await expect(dialog.getByText(/0 nouveau brouillon, rien de publié\s; 2 partages à ajouter/)).toBeVisible();
  await scan(page, "the vRA import's sheet");
  await dialog.getByRole("button", { name: "Annuler" }).click();
  await expect(dialog).toBeHidden();

  await page.getByRole("button", { name: /^Compte/ }).click();
  await page.getByRole("menuitem", { name: "English" }).click();
  await expect(page.locator("html")).toHaveAttribute("lang", "en");
  await expect(page.getByRole("heading", { name: "vRA import", level: 1 })).toBeVisible();
  await expect(page.getByText("0 of 3 projects moved")).toBeVisible();
  expect(errors).toEqual([]);
});

test.describe("a French browser, at 320 px", () => {
  test.use({ locale: "fr-CH", viewport: { width: 320, height: 640 } });
  for (const path of [
    "/admin/integrations",
    "/admin/integrations?tab=checks",
    "/admin/integrations/oidc",
    "/admin/integrations/vcenter",
    "/admin/integrations/nsx",
    "/admin/integrations/awx",
    "/admin/integrations/servicenow",
    "/admin/setup",
    "/admin/branding",
    "/admin/audit",
    "/admin/audit-forwarding",
    "/admin/system",
    "/admin/onboarding",
    "/admin/catalog-items?tab=awx",
    "/admin/day2-actions",
    "/admin/day2-actions/awx.install-monitoring-agent/form",
    "/admin/vra-import",
    "/admin/vra-import?tab=cutover",
  ]) {
    test(`no sideways scrolling: ${path}`, async ({ page }) => {
      await page.goto(path);
      await loaded(page);
      await expect(page.locator("html")).toHaveAttribute("lang", "fr");
      const overflow = await page.evaluate(() => {
        const main = document.getElementById("main")!;
        const wide = [...document.querySelectorAll("body *")]
          .filter((e) => {
            const r = e.getBoundingClientRect();
            return (
              r.right > window.innerWidth + 1 && r.width > 0 && !e.closest(".overflow-x-auto, .overflow-auto, table")
            );
          })
          .slice(0, 3)
          .map((e) => `${e.tagName.toLowerCase()}.${String(e.className).slice(0, 60)}`);
        return {
          page: document.documentElement.scrollWidth - window.innerWidth,
          main: main.scrollWidth - main.clientWidth,
          wide,
        };
      });
      expect(overflow, `${path}: ${overflow.wide.join(", ")}`).toEqual({ page: 0, main: 0, wide: [] });
    });
  }
});
