import { expect, test, type Page } from "@playwright/test";
import { scan } from "./axe";

// The pages a requester and an approver use, in French (O1.3, ADR-0061):
// the catalog, an item and its request form, the requests, a request's page,
// the schedules, the approvals, the resources and the reports. Each passes
// axe in both themes with lang="fr", and switches language from the top bar
// without a reload.

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

const h1 = (page: Page, name: string | RegExp) => page.getByRole("heading", { name, level: 1 });

/** What the run's canvas would say in English, rather than the steps' own titles. */
const english =
  /\b(Done|Failed|Running|Not started|Skipped|Cancelled|Undone|Waiting|Runs first|Runs after|Undo by|Calls|Runs|Makes|For each|At a time|Catches|If it fails)\b/;

test.describe("a French browser", () => {
  test.use({ locale: "fr-CH" });

  test("the catalog, an item and its request form", async ({ page }) => {
    test.setTimeout(120_000);
    await page.goto("/catalog");
    await expect(h1(page, "Catalogue")).toBeVisible();
    await expect(page.getByText(/^\d+ éléments$/)).toBeVisible();
    await bothThemes(page, "catalog");

    await page.goto("/catalog/rhel9-server");
    await expect(h1(page, "RHEL 9 server")).toBeVisible();
    await expect(page.getByText("Ce qu'il construit")).toBeVisible();
    await expect(page.getByRole("link", { name: "Demander", exact: true })).toBeVisible();
    await bothThemes(page, "catalog item");

    await page.goto("/catalog/rhel9-server/request");
    await expect(h1(page, "Demander RHEL 9 server")).toBeVisible();
    await expect(page.getByRole("radiogroup", { name: "Environnement" })).toBeVisible();
    await expect(page.getByRole("group", { name: "Bail et motif" })).toBeVisible();
    await expect(page.getByRole("button", { name: "Enregistrer comme brouillon" })).toBeVisible();
    await expect(page.getByRole("button", { name: /^Soumettre/ })).toBeVisible();
    await bothThemes(page, "request form");
  });

  test("the requests, a request's page and the schedules", async ({ page }) => {
    test.setTimeout(120_000);
    await page.goto("/requests");
    await expect(h1(page, "Demandes")).toBeVisible();
    await expect(page.getByRole("table", { name: "Demandes" })).toBeVisible();
    await expect(page.getByRole("link", { name: "Nouvelle demande" })).toBeVisible();
    await bothThemes(page, "requests");

    await page.goto("/requests/REQ-10461");
    await expect(page.getByText(/^Les \d+ étapes terminées/)).toBeVisible();
    await expect(page.getByRole("region", { name: "Ce qui a été demandé" })).toBeVisible();
    await expect(page.getByRole("link", { name: "Ouvrir le déploiement" })).toBeVisible();
    // rvav's own steps are worded by key (W1).
    const steps = page.getByRole("region", { name: "Étapes", exact: true });
    await expect(steps).toContainText("Soumise");
    await expect(steps).not.toContainText(/\b(Submitted|Waiting for approval|Ready)\b/);
    await bothThemes(page, "a request");

    // A composite's run, failed at a step: its graph and its timeline.
    await page.goto("/requests/REQ-10484");
    const run = page.getByRole("application", { name: "Exécution de REQ-10484" });
    await expect(run.getByRole("button", { name: /\. Échouée\./ }).first()).toBeVisible();
    await expect(run.getByRole("button", { name: /\. Terminée\./ }).first()).toBeVisible();
    await expect(page.getByRole("button", { name: "Réessayer", exact: true })).toBeVisible();
    const words = await run
      .locator(".react-flow__node, .react-flow__edge")
      .evaluateAll((drawn) => drawn.map((d) => d.getAttribute("aria-label") ?? ""));
    expect(words.filter((w) => english.test(w))).toEqual([]);
    await bothThemes(page, "a composite's run");
    await page.getByRole("button", { name: "Chronologie" }).click();
    await expect(page.getByRole("region", { name: "Étapes", exact: true })).toContainText("Soumise");
    await bothThemes(page, "a composite's timeline");

    await page.goto("/schedules");
    await expect(h1(page, "Planifications")).toBeVisible();
    await expect(page.getByRole("table", { name: "Planifications" })).toContainText(/Toutes les|Tous les|\(/);
    await expect(page.getByRole("link", { name: "Planifier une demande" })).toBeVisible();
    await bothThemes(page, "schedules");
  });

  test("the approvals, the resources and the reports", async ({ page }) => {
    test.setTimeout(120_000);
    await page.goto("/approvals");
    await expect(h1(page, "Approbations")).toBeVisible();
    await expect(page.getByRole("table", { name: "En attente de vous" })).toBeVisible();
    await bothThemes(page, "approvals");

    await page.goto("/resources");
    await expect(h1(page, "Ressources")).toBeVisible();
    await expect(page.getByRole("table", { name: "VM" })).toBeVisible();
    await expect(page.getByRole("button", { name: "Disques" })).toBeVisible();
    await bothThemes(page, "resources");

    await page.goto("/reports");
    await expect(h1(page, "Rapports")).toBeVisible();
    await expect(page.getByText("Afficher sous forme de tableau")).toBeVisible();
    await bothThemes(page, "reports");
  });
});

// From the top bar: the page shows French at once, and English again; a
// mark left on the window shows it wasn't reloaded.
for (const [path, english, french] of [
  ["/catalog", "Catalog", "Catalogue"],
  ["/catalog/rhel9-server/request", "Request RHEL 9 server", "Demander RHEL 9 server"],
  ["/requests", "Requests", "Demandes"],
  ["/requests/REQ-10477", "Requested by Thomas Nguyen", "Demandée par Thomas Nguyen"],
  ["/schedules", "Schedules", "Planifications"],
  ["/approvals", "Approvals", "Approbations"],
  ["/resources", "Resources", "Ressources"],
  ["/reports", "Reports", "Rapports"],
] as const) {
  test(`the language switched in the top bar, without a reload: ${path}`, async ({ page }) => {
    const errors: string[] = [];
    page.on("pageerror", (e) => errors.push(e.message));
    const shows = (text: string) => page.getByRole("main").getByText(text, { exact: true }).first();
    await page.goto(path);
    await expect(shows(english)).toBeVisible();
    await loaded(page);
    await page.evaluate(() => ((window as unknown as { kept: boolean }).kept = true));

    await page.getByRole("button", { name: /^Account/ }).click();
    await page.getByRole("menuitem", { name: "Français" }).click();
    await expect(page.locator("html")).toHaveAttribute("lang", "fr");
    await expect(shows(french)).toBeVisible();
    await expect(page.getByRole("main")).not.toContainText(/\b(Loading|Requested by|Submitted|Load more)\b/);

    await page.getByRole("button", { name: /^Compte/ }).click();
    await page.getByRole("menuitem", { name: "English" }).click();
    await expect(page.locator("html")).toHaveAttribute("lang", "en");
    await expect(shows(english)).toBeVisible();
    expect(await page.evaluate(() => (window as unknown as { kept?: boolean }).kept)).toBe(true);
    expect(errors).toEqual([]);
  });
}
