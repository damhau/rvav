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
  "/catalog/three-tier/request",
  "/requests",
  "/deployments",
  "/approvals",
  "/notifications",
  "/reports",
  "/tokens",
  "/admin/setup",
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
  "/admin/system",
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

    // Before the app: the sign-in page, the local administrator's
    // password change, and a trial sign-in's window (ADR-0030).
    for (const path of ["/sign-in", "/change-password", "/auth/trial"]) {
      test(`axe: ${path}`, async ({ page }) => {
        await page.goto(path);
        await expect(page.getByRole("main").getByRole("heading", { level: 1 })).toBeVisible();
        await scan(page, path);
      });
    }

    // A connection's saved settings applying, then applied (ADR-0030).
    test("axe: a connection's settings applying", async ({ page }) => {
      await page.goto("/admin/integrations");
      await loaded(page);
      await page.getByRole("main").getByRole("tab", { name: "AWX" }).click();
      const awx = page.getByRole("region", { name: "AWX connection" });
      await awx.getByRole("textbox", { name: "Organisation" }).fill("rvav-test");
      await awx.getByRole("button", { name: "Save settings" }).click();
      const applying = page.getByRole("region", { name: "Applying the settings" });
      await expect(applying.getByText("Finishing its running work").first()).toBeVisible();
      await scan(page, "/admin/integrations › AWX (applying)");
      await expect(applying.getByRole("status")).toHaveText("Applied: every process runs with these settings.");
      await scan(page, "/admin/integrations › AWX (applied)");
    });

    test("axe: an approval open in the inbox", async ({ page }) => {
      await page.goto("/approvals");
      await loaded(page);
      await page.getByRole("main").locator("[aria-pressed=false]").first().click();
      await scanAll(page, "/approvals (an approval open)");
    });

    // The canvas (D1, ADR-0032): a composite's steps in its draft's
    // Definition tab, a step selected with all it binds; a composite
    // request's run, failed, then running again once retried.
    test("axe: a composite draft's steps, a step selected", async ({ page }) => {
      await page.goto("/admin/catalog-items");
      await loaded(page);
      await page.getByRole("region", { name: "Items" }).getByRole("link", { name: "PostgreSQL 16 cluster" }).click();
      await page.getByRole("button", { name: "New draft" }).click();
      await page.getByRole("tab", { name: "Definition" }).click();
      await loaded(page);
      await page
        .getByRole("region", { name: "Steps of PostgreSQL 16 cluster" })
        .getByRole("button", { name: /^AWX Backups\./ })
        .click();
      await expect(page.getByRole("region", { name: "Selected: Backups" }).getByRole("combobox").first()).toBeVisible();
      await scan(page, "/admin/catalog-items/postgres-cluster › Definition (a step selected)");
    });

    // A REST call and its undo (D2.2), their fields and a problem on the step.
    test("axe: a composite draft's REST call step selected", async ({ page }) => {
      await page.goto("/admin/catalog-items");
      await loaded(page);
      await page.getByRole("region", { name: "Items" }).getByRole("link", { name: "PostgreSQL 16 cluster" }).click();
      await page.getByRole("button", { name: "New draft" }).click();
      await page.getByRole("tab", { name: "Definition" }).click();
      await loaded(page);
      await page
        .getByRole("region", { name: "Steps of PostgreSQL 16 cluster" })
        .getByRole("button", { name: /^AWX Backups\./ })
        .click();
      const details = page.getByRole("region", { name: "Selected: Backups" });
      await details.getByRole("combobox", { name: "Runs" }).click();
      await page.getByRole("option", { name: "A REST call" }).click();
      await expect(details.getByRole("group", { name: "REST call" })).toBeVisible();
      await details.getByRole("combobox", { name: "Undone by" }).click();
      await page.getByRole("option", { name: "A REST call" }).click();
      await expect(details.getByRole("textbox", { name: "Undo's URL" })).toBeVisible();
      await loaded(page);
      await scan(page, "/admin/catalog-items/postgres-cluster › Definition (a REST call selected)");
    });

    // A wait for a person (D2.3): its fields with a question and a problem on
    // the step, then the form its answerer gets on the request's page.
    test("axe: a composite draft's wait for a person, and answering it", async ({ page }) => {
      test.slow();
      await page.goto("/admin/catalog-items");
      await loaded(page);
      await page.getByRole("button", { name: "New item" }).click();
      const dialog = page.getByRole("dialog", { name: "New item" });
      await dialog.getByRole("textbox", { name: "Name" }).fill("Owner check");
      await dialog.getByRole("textbox", { name: "Slug" }).fill("owner-check");
      await dialog.getByRole("textbox", { name: "Summary" }).fill("Waits for a person.");
      await dialog.getByRole("combobox", { name: "Type" }).click();
      await page.getByRole("option", { name: /composite/i }).click();
      await dialog.getByRole("button", { name: "Create" }).click();
      await page.getByRole("tab", { name: "Definition" }).click();
      await page.getByRole("button", { name: "Add a step" }).click();
      const add = page.getByRole("dialog", { name: "Add a step" });
      await add.getByRole("combobox", { name: "What it runs" }).click();
      await page.getByRole("option", { name: "A person" }).click();
      await add.getByRole("textbox", { name: "Key" }).fill("owner_ok");
      await add.getByRole("textbox", { name: "Title" }).fill("Owner's answer");
      await add.getByRole("button", { name: "Add step" }).click();
      const details = page.getByRole("region", { name: "Selected: Owner's answer" });
      await details.getByRole("button", { name: "Add a question" }).click();
      const q = details.getByRole("group", { name: "Question 1" });
      await q.getByRole("combobox", { name: "Type" }).click();
      await page.getByRole("option", { name: "A choice" }).click();
      await details.getByRole("textbox", { name: "Timeout" }).fill("2h");
      await details.getByRole("textbox", { name: "Timeout" }).press("Tab");
      await details.getByRole("combobox", { name: "Then" }).click();
      await page.getByRole("option", { name: "It escalates" }).click();
      await expect(details.getByRole("list", { name: "Problems with this step" })).toContainText("Say who is asked");
      await loaded(page);
      await scan(page, "/admin/catalog-items/owner-check › Definition (a wait for a person selected)");
      const shownAs = page
        .getByRole("region", { name: "Steps of Owner check" })
        .getByRole("group", { name: "Show as" });
      await shownAs.getByRole("button", { name: "List" }).click();
      await expect(shownAs.getByRole("button", { name: "List" })).toHaveAttribute("aria-pressed", "true");
      await scan(page, "/admin/catalog-items/owner-check › Definition (a wait for a person, as a list)");
      await shownAs.getByRole("button", { name: "Graph" }).click();

      // Filled in, it can be published, requested and answered.
      await details.getByRole("checkbox", { name: "The requester" }).click();
      await q.getByRole("textbox", { name: "Question" }).fill("Keep it?");
      await q.getByRole("textbox", { name: "Question" }).press("Tab");
      await q.getByRole("textbox", { name: "Options" }).fill("keep, delete");
      await q.getByRole("textbox", { name: "Options" }).press("Tab");
      await q.getByRole("checkbox", { name: "An answer is required" }).click();
      await details.getByRole("textbox", { name: "Escalate to OIDC group" }).fill("chiefs");
      await details.getByRole("textbox", { name: "Escalate to OIDC group" }).press("Tab");
      await expect(details.getByRole("list", { name: "Problems with this step" })).toHaveCount(0);
      await details.getByRole("button", { name: "Add a question" }).click();
      const q2 = details.getByRole("group", { name: "Question 2" });
      await q2.getByRole("textbox", { name: "Question" }).fill("Are you sure?");
      await q2.getByRole("textbox", { name: "Question" }).press("Tab");
      await q2.getByRole("combobox", { name: "Type" }).click();
      await page.getByRole("option", { name: "Yes or no" }).click();
      await page.getByRole("button", { name: "Save draft" }).click();
      await expect(page.getByText(/^Draft [\d.]+ saved$/)).toBeVisible();
      await page.getByRole("tab", { name: "Entitlements" }).click();
      await page.getByRole("radio", { name: /Chosen projects/ }).check();
      await page.getByRole("checkbox", { name: "Web Platform" }).click();
      await page.getByRole("button", { name: "Save entitlements" }).click();
      await expect(page.getByText("Entitlements saved")).toBeVisible();
      await page.getByRole("tab", { name: "Versions" }).click();
      await page.getByRole("button", { name: "Publish" }).click();
      await expect(page.getByText(/^[\d.]+ published$/)).toBeVisible();
      await page.getByRole("navigation").getByRole("link", { name: "Catalog", exact: true }).click();
      await page.getByRole("link", { name: /^Owner check/ }).click();
      await page.getByRole("textbox", { name: "Name" }).fill("web-01");
      // What a requester's form says of it. A toast that is fading reads as too faint.
      await expect(page.locator("[data-sonner-toast]")).toHaveCount(0, { timeout: 25_000 });
      await loaded(page);
      await scan(page, "/catalog/owner-check (a wait for a person, masked)");
      await page.getByRole("button", { name: "Submit request" }).click();
      const answer = page.getByRole("form", { name: "Your answer: Owner's answer" });
      await expect(answer).toBeVisible({ timeout: 20_000 });
      await answer.getByRole("button", { name: "Send answer" }).click();
      await expect(answer).toContainText("Answer this.");
      await loaded(page);
      await scan(page, "/requests/REQ (a wait's form, a problem on a question)");

      await answer.getByRole("combobox", { name: "Keep it?" }).click();
      await page.getByRole("option", { name: "delete" }).click();
      await answer.getByRole("button", { name: "Send answer" }).click();
      await expect(page.getByRole("list", { name: "Steps that wait for a person" })).toContainText("Answered by");
      // Its toasts gone: one that is fading in reads as too faint.
      await expect(page.locator("[data-sonner-toast]")).toHaveCount(0, { timeout: 25_000 });
      await loaded(page);
      await scan(page, "/requests/REQ (a wait answered)");
      await page.getByRole("button", { name: "Timeline" }).click();
      await expect(page.getByRole("region", { name: "Steps", exact: true })).toContainText("Answered by");
      await scan(page, "/requests/REQ › Timeline (a wait answered)");
    });

    test("axe: a composite request's run, failed then running", async ({ page }) => {
      await page.goto("/requests/REQ-10484");
      await expect(page.getByRole("region", { name: "Selected: The database's owner" })).toBeVisible();
      await expect(page.getByRole("log", { name: /^Output of / })).toContainText("PLAY RECAP");
      await loaded(page);
      await scan(page, "/requests/REQ-10484 (failed)");
      await page.getByRole("button", { name: "Retry", exact: true }).click();
      const run = page.getByRole("application", { name: "Run of REQ-10484" });
      await expect(run.getByRole("button", { name: /\. Running\./ }).first()).toBeVisible();
      await scan(page, "/requests/REQ-10484 (running)");
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
