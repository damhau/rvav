import AxeBuilder from "@axe-core/playwright";
import { expect, type Page } from "@playwright/test";

/** Runs axe on the page once it has stopped loading, failing on any WCAG 2.2 AA violation. */
export async function scan(page: Page, where: string) {
  await expect(page.locator(".animate-pulse, [role=status][aria-label=Loading]")).toHaveCount(0);
  await page.waitForTimeout(300);
  // axe can't run inside a frame whose sandbox forbids scripts (the email
  // preview, ADR-0059): Playwright would wait forever for its context.
  const result = await new AxeBuilder({ page })
    .withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa"])
    .exclude('iframe[sandbox=""]')
    .analyze();
  const found = result.violations.map(
    (v) =>
      `${where}: ${v.id} (${v.impact}) ${v.help}\n    ` +
      v.nodes
        .slice(0, 5)
        .map((n) => `${n.target.join(" ")}  ${n.html.slice(0, 160)}`)
        .join("\n    "),
  );
  expect(found, found.join("\n")).toEqual([]);
}

// In the app: a reload would start the mock backend afresh.
export async function theme(page: Page, name: "Light" | "Dark") {
  await page.getByRole("button", { name: /^Theme/ }).click();
  await page.getByRole("menuitem", { name }).click();
  await expect(page.locator("html")).toHaveAttribute("data-theme", name.toLowerCase());
  await page.waitForTimeout(500);
}

/** Scans the page in the light theme, then the dark one, and leaves it light. */
export async function bothThemes(page: Page, where: string) {
  await theme(page, "Light");
  await scan(page, `${where}, light`);
  await theme(page, "Dark");
  await scan(page, `${where}, dark`);
  await theme(page, "Light");
}
