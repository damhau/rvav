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

  await page.getByRole("button", { name: /^Compte/ }).click();
  await page.getByRole("menuitem", { name: "English" }).click();
  await expect(page.locator("html")).toHaveAttribute("lang", "en");
  await expect(page.getByRole("region", { name: "Inventory token" })).toBeVisible();
  await expect(
    page.getByRole("region", { name: "AWX connection" }).getByRole("button", { name: "Save settings" }),
  ).toBeVisible();
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
