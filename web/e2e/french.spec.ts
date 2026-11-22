import { expect, test, type Page } from "@playwright/test";
import { scan } from "./axe";

// The web app in French (O1.3, ADR-0061): chosen in the top bar, it shows at
// once and stays from page to page; a French browser gets it before anything
// is chosen. The pages pass axe in both themes with lang="fr", and reflow at
// 320 px with the longer French words.

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

const nav = (page: Page) => page.getByRole("navigation", { name: "Principale" });

test("French chosen in the top bar shows at once, on every page, until English is chosen again", async ({ page }) => {
  test.setTimeout(150_000);
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/");
  await expect(page.locator("html")).toHaveAttribute("lang", "en");
  await expect(page.getByRole("heading", { name: "Overview", level: 1 })).toBeVisible();

  await page.getByRole("button", { name: /^Account/ }).click();
  await page.getByRole("menuitem", { name: "Français" }).click();
  await expect(page.locator("html")).toHaveAttribute("lang", "fr");
  await expect(page.getByRole("heading", { name: "Vue d'ensemble", level: 1 })).toBeVisible();
  await expect(page.getByRole("region", { name: "Requiert votre attention" })).toBeVisible();
  await expect(nav(page).getByRole("link", { name: "Catalogue", exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Notifications, 3 non lues" })).toBeVisible();
  await bothThemes(page, "overview");

  await page.getByRole("button", { name: /^Compte/ }).click();
  await expect(page.getByRole("menuitem", { name: "Français" })).toHaveAttribute("lang", "fr");
  await expect(page.getByRole("menuitem", { name: "English" })).toHaveAttribute("lang", "en");
  await page.getByRole("menuitem", { name: "Jetons d'API" }).click();
  await expect(page.getByRole("heading", { name: "Jetons d'API", level: 1 })).toBeVisible();
  await expect(page.getByRole("textbox", { name: "À quoi il sert" })).toBeVisible();
  const token = page.getByRole("row", { name: /GitLab pipeline/ });
  await expect(token).toContainText("créé il y a 20 jours");
  await expect(token).toContainText("il y a 42 minutes");
  await expect(token).toContainText("dans 2 mois");
  await bothThemes(page, "API tokens");

  await page.getByRole("button", { name: "Notifications, 3 non lues" }).click();
  await page.getByRole("menuitem", { name: "Toutes les notifications et les préférences" }).click();
  await expect(page.getByRole("heading", { name: "Notifications", level: 1 })).toBeVisible();
  await expect(page.getByRole("button", { name: "Tout marquer comme lu" })).toBeVisible();
  await page.getByRole("tab", { name: "Préférences" }).click();
  await expect(page.getByRole("combobox", { name: "Langue" })).toContainText("Français");
  await expect(page.getByRole("checkbox", { name: "Approbation requise par E-mail" })).toBeChecked();
  await bothThemes(page, "notification preferences");

  // A request form: the renderer's own words, a failed source and a refused submit.
  await nav(page).getByRole("link", { name: "Catalogue", exact: true }).click();
  const open = page.locator('a[href="/catalog/rhel9-server/request"]').first();
  await expect(open).toBeVisible();
  // Three calls: the first and the client's two retries.
  await page.evaluate(() => (window as unknown as MockWindow).__rvavMocks.faults.failFieldValues("cluster", 3));
  await open.click();
  await expect(page.getByText("Impossible de charger les options.")).toBeVisible({ timeout: 15_000 });
  await expect(page.getByRole("combobox", { name: "Cluster" })).toHaveText("Non chargé");
  // The request page's own words are translated with features/requests.
  await page.getByRole("button", { name: /^Soumettre/ }).click();
  await expect(page.getByText("2 champs demandent votre attention.")).toBeVisible();
  await expect(page.getByRole("button", { name: "Réessayer" })).toBeFocused();
  await bothThemes(page, "request form refused");

  await page.getByRole("button", { name: /^Compte/ }).click();
  await page.getByRole("menuitem", { name: "English" }).click();
  await expect(page.locator("html")).toHaveAttribute("lang", "en");
  await expect(page.getByText("2 inputs need attention.")).toBeVisible();
  await expect(page.getByRole("button", { name: "Try again" })).toBeVisible();
  expect(errors).toEqual([]);
});

test.describe("a French browser", () => {
  test.use({ locale: "fr-CH" });

  test("shows French before a language is chosen", async ({ page }) => {
    await page.goto("/");
    await expect(page.locator("html")).toHaveAttribute("lang", "fr");
    await expect(page.getByRole("heading", { name: "Vue d'ensemble", level: 1 })).toBeVisible();
    await expect(page.getByRole("link", { name: "Aller au contenu" })).toBeAttached();
    await page.goto("/notifications?tab=preferences");
    await expect(page.getByRole("combobox", { name: "Langue" })).toContainText("Français");
  });

  test.describe("reflow at 320 px", () => {
    test.use({ viewport: { width: 320, height: 640 } });
    for (const path of [
      "/",
      "/tokens",
      "/notifications",
      "/notifications?tab=preferences",
      "/catalog/rhel9-server/request",
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
});

interface MockWindow {
  __rvavMocks: { faults: { failFieldValues: (field: string, times: number) => void } };
}
