import AxeBuilder from "@axe-core/playwright";
import { expect, type Page, test } from "@playwright/test";

// The accessibility audit's automated part (S14.8): axe on every page
// (WCAG 2.2 A and AA, in each of the page's tabs, in light and dark themes;
// detail pages reached from their lists, as a person would), each page's
// structure as a screen reader reads it, reflow at 400% zoom, and a request
// made with the keyboard only.
const pages = [
  "/",
  "/catalog",
  "/catalog/rhel9-server/request",
  "/catalog/web-app/request",
  "/requests",
  "/deployments",
  "/approvals",
  "/notifications",
  "/reports",
  "/tokens",
  "/admin/catalog-items",
  "/admin/projects",
  "/admin/policies",
  "/admin/integrations",
  "/admin/infrastructure",
  "/admin/day2-actions",
  "/admin/workflows",
  "/admin/onboarding",
  "/admin/vra-import",
  "/admin/subscriptions",
  "/admin/audit",
];

// A list page and the links on it that open a detail page.
const details: [string, RegExp][] = [
  ["/requests", /^\/requests\/[^/]+$/],
  ["/deployments", /^\/deployments\/[^/]+$/],
  ["/admin/catalog-items", /^\/admin\/catalog-items\/[^/]+$/],
  ["/admin/projects", /^\/admin\/projects\/[^/]+$/],
  ["/admin/subscriptions", /^\/admin\/subscriptions\/[^/]+$/],
];

async function scan(page: Page, where: string) {
  const result = await new AxeBuilder({ page })
    .withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa"])
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

// Loaded: the main region shows, with no placeholder left.
async function loaded(page: Page) {
  await expect(page.getByRole("main")).toBeVisible();
  await expect(page.locator(".animate-pulse, [role=status][aria-label=Loading]")).toHaveCount(0);
  await page.waitForTimeout(300);
}

// The page and each of its tabs.
async function scanAll(page: Page, path: string) {
  await loaded(page);
  await scan(page, path);
  const tabs = page.getByRole("main").getByRole("tab");
  for (let i = 0; i < (await tabs.count()); i++) {
    const tab = tabs.nth(i);
    if ((await tab.getAttribute("aria-selected")) === "true") continue;
    await tab.click();
    await loaded(page);
    await scan(page, `${path} › ${(await tab.innerText()).trim()}`);
  }
}

for (const theme of ["light", "dark"] as const) {
  test.describe(`${theme} theme`, () => {
    test.beforeEach(async ({ page }) => {
      await page.addInitScript((t) => localStorage.setItem("rvav.theme", t), theme);
    });

    for (const path of pages) {
      test(`axe: ${path}`, async ({ page }) => {
        await page.goto(path);
        await scanAll(page, path);
      });
    }

    test("axe: an approval open in the inbox", async ({ page }) => {
      await page.goto("/approvals");
      await loaded(page);
      await page.getByRole("main").locator("[aria-pressed=false]").first().click();
      await scanAll(page, "/approvals (an approval open)");
    });

    for (const [list, link] of details) {
      test(`axe: a detail page from ${list}`, async ({ page }) => {
        await page.goto(list);
        await loaded(page);
        const hrefs = await page
          .getByRole("main")
          .locator("a[href]")
          .evaluateAll((as) => as.map((a) => a.getAttribute("href") ?? ""));
        const href = hrefs.find((h) => link.test(h));
        expect(href, `no link to a detail page on ${list}`).toBeTruthy();
        await page.goto(href!);
        await scanAll(page, href!);
      });
    }
  });
}

// What a screen reader navigates by: landmarks, one h1, headings that
// don't skip a level.
test.describe("structure", () => {
  for (const path of pages) {
    test(`landmarks and headings: ${path}`, async ({ page }) => {
      await page.goto(path);
      await loaded(page);
      await expect(page.getByRole("banner")).toHaveCount(1);
      await expect(page.getByRole("navigation").first()).toBeVisible();
      await expect(page.getByRole("main")).toHaveCount(1);
      const levels = await page
        .getByRole("main")
        .locator("h1, h2, h3, h4, h5, h6")
        .evaluateAll((hs) => hs.filter((h) => h.getClientRects().length > 0).map((h) => Number(h.tagName[1])));
      expect(
        levels.filter((l) => l === 1),
        `${path}: h1s`,
      ).toHaveLength(1);
      expect(levels[0], `${path}: the first heading`).toBe(1);
      for (let i = 1; i < levels.length; i++) {
        expect(levels[i]! - levels[i - 1]!, `${path}: headings ${levels.join(" ")}`).toBeLessThanOrEqual(1);
      }
    });
  }
});

// 400% zoom of a 1280 px window is 320 CSS pixels (WCAG 1.4.10): the page
// reflows without scrolling sideways; only wide tables scroll within
// themselves.
test.describe("reflow at 320 px", () => {
  test.use({ viewport: { width: 320, height: 640 } });
  for (const path of pages) {
    test(`no sideways scrolling: ${path}`, async ({ page }) => {
      await page.goto(path);
      await loaded(page);
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

// A request made with the keyboard only: the skip link, the catalog, the
// form's fields, submitting, and the request's page.
test("a VM requested with the keyboard only", async ({ page }) => {
  await page.goto("/catalog");
  await loaded(page);
  await page.keyboard.press("Tab");
  const skip = page.getByRole("link", { name: "Skip to content" });
  await expect(skip).toBeFocused();
  await page.keyboard.press("Enter");
  // Tab to the RHEL 9 server's request link and open it.
  for (let i = 0; i < 60; i++) {
    await page.keyboard.press("Tab");
    const name = await page.evaluate(() => document.activeElement?.textContent ?? "");
    const href = await page.evaluate(() => document.activeElement?.getAttribute("href") ?? "");
    if (href === "/catalog/rhel9-server/request" || (/rhel 9/i.test(name) && href.includes("rhel9-server"))) break;
  }
  await page.keyboard.press("Enter");
  await expect(page).toHaveURL(/\/catalog\/rhel9-server/);
  await loaded(page);
  // Every field of the form is reached by Tab, and nothing traps focus.
  const reached = new Set<string>();
  for (let i = 0; i < 80; i++) {
    await page.keyboard.press("Tab");
    const id = await page.evaluate(() => {
      const e = document.activeElement as HTMLElement | null;
      return e
        ? `${e.tagName.toLowerCase()}#${e.id}|${e.getAttribute("aria-label") ?? e.textContent?.trim().slice(0, 30) ?? ""}`
        : "";
    });
    reached.add(id);
    if (id.startsWith("input#f-role")) await page.keyboard.type("kbd");
    // The focused element is visible: the focus ring has somewhere to show.
    const visible = await page.evaluate(() => {
      const e = document.activeElement as HTMLElement | null;
      if (!e || e === document.body) return true;
      const r = e.getBoundingClientRect();
      return r.width > 0 && r.height > 0;
    });
    expect(visible, `focus on an invisible element: ${id}`).toBe(true);
    if (/submit request|request vm/i.test(id)) break;
  }
  const ids = [...reached].join("\n");
  for (const field of [
    "#f-role",
    "|Small",
    "#f-cluster",
    "#f-datastore",
    "#f-network",
    "#justification",
    "|Submit request",
  ]) {
    expect(ids, `${field} is never focused:\n${ids}`).toContain(field);
  }
  // Submit is focused: Enter sends the request, and its page opens.
  await page.keyboard.press("Enter");
  await expect(page).toHaveURL(/\/requests\/REQ-/, { timeout: 15_000 });
  await expect(page.getByRole("heading", { level: 1 })).toContainText("web-dev-kbd");
});

// A dialog takes the focus, keeps it, and gives it back when closed with
// Escape.
test("a dialog with the keyboard", async ({ page }) => {
  await page.goto("/admin/infrastructure");
  await loaded(page);
  await page.getByRole("tab", { name: "Networks" }).click();
  const open = page.getByRole("button", { name: "Map a network" });
  await open.focus();
  await page.keyboard.press("Enter");
  const dialog = page.getByRole("dialog", { name: "Map a network" });
  await expect(dialog).toBeVisible();
  for (let i = 0; i < 15; i++) {
    await page.keyboard.press("Tab");
    expect(await dialog.evaluate((d) => d.contains(document.activeElement)), "focus left the dialog").toBe(true);
  }
  await page.keyboard.press("Escape");
  await expect(dialog).toBeHidden();
  await expect(open).toBeFocused();
});
