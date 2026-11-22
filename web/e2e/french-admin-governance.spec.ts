import { expect, test, type Page } from "@playwright/test";
import { scan } from "./axe";

// Administration's governance, access and notification pages in French
// (O1.3, ADR-0061): chosen once in the top bar, then walked in the app (a
// reload would restart the mock backend). Each page passes axe in both
// themes with lang="fr", and reflows at 320 px in a French browser.

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

test("roles and what someone may do, in French", async ({ page }) => {
  test.setTimeout(150_000);
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await inFrench(page);

  await open(page, "Rôles");
  await expect(page.getByRole("link", { name: "Nouveau rôle" })).toBeVisible();
  const roles = page.getByRole("table", { name: "Rôles" });
  await expect(roles.getByRole("columnheader", { name: "Donné à" })).toBeVisible();
  await expect(roles.getByText("Intégré").first()).toBeVisible();
  await bothThemes(page, "roles");

  await roles.getByRole("link").first().click();
  await expect(page.getByRole("link", { name: "Créer un rôle à partir de celui-ci" })).toBeVisible();
  await expect(page.getByRole("region", { name: "Ce qu'il permet" })).toBeVisible();
  await bothThemes(page, "a built-in role");

  await page.getByRole("link", { name: "Créer un rôle à partir de celui-ci" }).click();
  await expect(page.getByRole("heading", { name: "Nouveau rôle", level: 1 })).toBeVisible();
  await expect(page.getByRole("button", { name: "Créer le rôle" })).toBeVisible();
  await expect(page.getByRole("textbox", { name: "Clé" })).toBeVisible();
  await bothThemes(page, "new role");

  await open(page, "Rôles");
  await tab(page, "Pour l'ensemble de rvav");
  await expect(page.getByRole("table", { name: "Donnés pour l'ensemble de rvav" })).toBeVisible();
  await expect(page.getByRole("link", { name: "Projets › Groupes du fournisseur d'identité" })).toBeVisible();
  await bothThemes(page, "roles for all of rvav");

  await tab(page, "Ce que quelqu'un peut faire");
  const explain = page.getByRole("region", { name: "Ce que quelqu'un peut faire" });
  await expect(explain.getByRole("table", { name: "Ce que vous pouvez faire pour l'ensemble de rvav" })).toBeVisible();
  await expect(explain.getByRole("combobox", { name: "Afficher" })).toContainText("Toutes les permissions");
  await bothThemes(page, "what someone may do");

  await page.getByRole("button", { name: /^Compte/ }).click();
  await page.getByRole("menuitem", { name: "Ce que je peux faire" }).click();
  await expect(page.getByRole("heading", { name: "Ce que je peux faire", level: 1 })).toBeVisible();
  await expect(page.getByRole("region", { name: "Vos permissions" })).toBeVisible();
  await bothThemes(page, "what I may do");
  expect(errors).toEqual([]);
});

test("notification templates, in French, and back to English without a reload", async ({ page }) => {
  test.setTimeout(120_000);
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await inFrench(page);

  await open(page, "Modèles de notification");
  const list = page.getByRole("table", { name: "Modèles de notification" });
  await expect(list.getByText("Modèles intégrés").first()).toBeVisible();
  await bothThemes(page, "notification templates");

  await list.getByRole("link").first().click();
  await expect(page.getByRole("button", { name: "Enregistrer le modèle" })).toBeVisible();
  await expect(page.getByRole("textbox", { name: "Objet" })).toBeVisible();
  await expect(page.getByRole("tab", { name: "HTML" })).toBeVisible();
  await bothThemes(page, "an email template");
  await page.getByRole("button", { name: "Teams" }).click();
  await expect(page.getByRole("tab", { name: "Carte" })).toBeVisible();
  await expect(page.getByRole("textbox", { name: "Titre" })).toBeVisible();
  await bothThemes(page, "a Teams template");

  await page.getByRole("button", { name: /^Compte/ }).click();
  await page.getByRole("menuitem", { name: "English" }).click();
  await expect(page.locator("html")).toHaveAttribute("lang", "en");
  await expect(page.getByRole("button", { name: "Save template" })).toBeVisible();
  await expect(page.getByRole("tab", { name: "Card" })).toBeVisible();
  expect(errors).toEqual([]);
});

test.describe("a French browser, at 320 px", () => {
  test.use({ locale: "fr-CH", viewport: { width: 320, height: 640 } });
  for (const path of [
    "/admin/roles",
    "/admin/roles/auditor",
    "/admin/roles?tab=explain",
    "/admin/notification-templates",
    "/admin/notification-templates/approval_needed",
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
