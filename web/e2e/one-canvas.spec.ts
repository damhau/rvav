import { expect, type Locator, type Page, test } from "@playwright/test";

// D1's acceptance in the browser (D1.5, ADR-0032), on the mock backend and
// with the keyboard only: the seeded PostgreSQL cluster gets a step on the
// canvas (added after the nodes, its inputs bound, a condition set), is
// saved, published and requested, and the request is followed on the same
// graph. The mocks can't approve a request as someone else, so it is
// followed as far as it goes, waiting for approval; the failed REQ-10484 is
// retried and followed to its end. TestD1Acceptance follows a request of
// the same edit to its end through the API. The mouse's path is
// composite-designer.spec.ts and request-run.spec.ts.

type Way = "Tab" | "Shift+Tab";

/**
 * Tab until `target` has the focus: Shift+Tab when it comes before what has
 * the focus (the page has no positive tabindex, so Tab follows the DOM).
 * When nothing has (what had it went away, a button gone or disabled), Tab
 * goes on from where it was: `gone` is the way to the target from there, as
 * the reader knows it (else forward, then back: a walk to the page's end).
 */
async function tabTo(page: Page, target: Locator, gone?: Way) {
  await expect(target).toBeVisible();
  const at = () => target.evaluate((e) => e === document.activeElement);
  const before = await target.evaluate((e) => {
    const now = document.activeElement;
    if (!now || now === document.body) return undefined;
    return !!(e.compareDocumentPosition(now) & Node.DOCUMENT_POSITION_FOLLOWING);
  });
  const back = (w: Way): Way => (w === "Tab" ? "Shift+Tab" : "Tab");
  const ways: Way[] = before === undefined ? [gone ?? "Tab", back(gone ?? "Tab")] : [before ? "Shift+Tab" : "Tab"];
  for (const key of ways) {
    for (let i = 0; i < 150; i++) {
      if (await at()) return;
      await page.keyboard.press(key);
    }
  }
  if (!(await at())) throw new Error(`${ways.join(" then ")} never reached ${target.toString()}`);
}

/** The page's skip link, then Enter: Tab goes on from the page's content, past the navigation. */
async function skipToContent(page: Page) {
  // The page's first Tab stop: the first Tab of a page just opened.
  await tabTo(page, page.getByRole("link", { name: "Skip to content" }), "Tab");
  await page.keyboard.press("Enter");
}

/** In a list opened from the keyboard, the arrow keys to an option, then Enter. */
async function choose(page: Page, name: string | RegExp) {
  const option = page.getByRole("option", { name, exact: typeof name === "string" });
  await expect(page.getByRole("listbox")).toBeVisible();
  for (let i = 0; i < 40; i++) {
    if (await option.evaluate((e) => e === document.activeElement || e.hasAttribute("data-highlighted"))) {
      await page.keyboard.press("Enter");
      return;
    }
    await page.keyboard.press("ArrowDown");
  }
  throw new Error(`no option ${name.toString()}`);
}

/** Drawn: React has rendered what a key changed, and run its effects. */
const drawn = (page: Page) =>
  page.evaluate(() => new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r))));

/**
 * The tabs are one Tab stop, the arrow keys between them, each tab shown as
 * it is reached: from the one selected to `name`, a key at a time, back on
 * the tabs (above their panel) if what one showed took the focus.
 */
async function selectTab(page: Page, name: string, key: "ArrowRight" | "ArrowLeft") {
  const target = page.getByRole("tab", { name, exact: true });
  const selected = page.getByRole("tab", { selected: true });
  for (let i = 0; i < 8 && (await target.getAttribute("aria-selected")) !== "true"; i++) {
    await tabTo(page, selected, "Shift+Tab");
    const was = (await selected.textContent()) ?? "";
    await page.keyboard.press(key);
    await expect(selected).not.toHaveText(was);
    await drawn(page);
  }
  await expect(target).toHaveAttribute("aria-selected", "true");
}

/**
 * On a graph, the arrow keys from the focused node to `target`: along each
 * row, then down to the next.
 */
async function arrowTo(page: Page, target: Locator) {
  const at = () => target.evaluate((e) => e === document.activeElement);
  const now = () => page.evaluate(() => document.activeElement?.getAttribute("aria-label") ?? "");
  const press = async (key: string) => {
    const before = await now();
    await page.keyboard.press(key);
    await drawn(page);
    return (await now()) !== before;
  };
  for (let row = 0; row < 12; row++) {
    if (await at()) return;
    for (const key of ["ArrowRight", "ArrowLeft"]) {
      while (await press(key)) if (await at()) return;
    }
    if (!(await press("ArrowDown"))) break;
  }
  if (!(await at())) throw new Error(`the arrow keys never reached ${target.toString()}`);
}

test("a composite's step added, published, requested and followed, with the keyboard only", async ({ page }) => {
  // Some 170 keys, each awaited and checked: over 30 s on a slow CI runner.
  test.slow();
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/admin/catalog-items");
  await skipToContent(page);
  await tabTo(page, page.getByRole("region", { name: "Items" }).getByRole("link", { name: "PostgreSQL 16 cluster" }));
  await page.keyboard.press("Enter");
  await tabTo(page, page.getByRole("button", { name: "New draft" }));
  await page.keyboard.press("Enter");
  await expect(page.getByText(/^Draft [\d.]+ made from the published version/)).toBeVisible();
  // The tabs, above the button gone.
  await tabTo(page, page.getByRole("tab", { name: "Versions" }), "Shift+Tab");
  await selectTab(page, "Definition", "ArrowRight");
  const editor = page.getByRole("textbox", { name: /^Definition of PostgreSQL 16 cluster/ });
  const steps = page.getByRole("region", { name: "Steps of PostgreSQL 16 cluster" });
  const node = (name: RegExp) => steps.getByRole("button", { name });

  // The nodes' step: the graph is one Tab stop, the arrow keys move, Enter selects.
  await tabTo(page, node(/^Request\./));
  await arrowTo(page, node(/^Item Database nodes\./));
  await page.keyboard.press("Enter");
  await expect(page.getByRole("region", { name: "Selected: Database nodes" })).toBeVisible();

  // A step after it, from the dialog; what it runs picked from its list.
  await tabTo(page, page.getByRole("button", { name: "Add a step" }));
  await page.keyboard.press("Enter");
  const dialog = page.getByRole("dialog", { name: "Add a step" });
  await expect(dialog.getByRole("combobox", { name: "What it runs" })).toBeFocused();
  await page.keyboard.press("Enter");
  await choose(page, "Monitoring agent");
  await tabTo(page, dialog.getByRole("textbox", { name: "Key" }));
  await page.keyboard.type("monitor");
  await expect(dialog.getByRole("radio", { name: "After Database nodes" })).toBeChecked();
  await page.keyboard.press("Enter");
  await expect(dialog).toBeHidden();
  const details = page.getByRole("region", { name: "Selected: Monitoring agent" });
  await expect(details.getByRole("heading", { name: /Monitoring agent/ })).toBeFocused();
  await expect(node(/^AWX Monitoring agent\. monitor\..*Runs after Database nodes/)).toBeVisible();

  // Its inputs: the nodes' first FQDN, and a port written out.
  await tabTo(page, details.getByRole("combobox", { name: /^Agent/ }));
  await page.keyboard.press("Enter");
  await choose(page, "Database nodes: The first VM's FQDN");
  await tabTo(page, details.getByRole("combobox", { name: /^Port/ }));
  await page.keyboard.press("Enter");
  await choose(page, "A value or CEL expression");
  await expect(details.getByRole("textbox", { name: "Port: value or CEL expression" })).toBeFocused();
  await page.keyboard.type("9187");
  await page.keyboard.press("Enter");
  // A condition.
  await tabTo(page, details.getByRole("textbox", { name: "When" }));
  await page.keyboard.type("inputs.backup_retention_days >= 30");
  await page.keyboard.press("Enter");
  await expect(editor).toContainText("when: inputs.backup_retention_days >= 30");
  await expect(editor).toContainText("monitoring_agent: steps.nodes.fqdn");
  await expect(editor).toContainText("listen_port: 9187");
  await expect(page.getByRole("list", { name: "Problems in the steps" })).toHaveCount(0);

  // Saved and published.
  await tabTo(page, page.getByRole("button", { name: "Save draft" }));
  await page.keyboard.press("Enter");
  await expect(page.getByText(/^Draft [\d.]+ saved$/)).toBeVisible();
  // Save draft, disabled once saved, lets the focus go: the tabs are above it.
  await tabTo(page, page.getByRole("tab", { name: "Definition" }), "Shift+Tab");
  await page.keyboard.press("Home");
  await expect(page.getByRole("tab", { name: "Versions" })).toHaveAttribute("aria-selected", "true");
  await tabTo(page, page.getByRole("button", { name: "Publish" }));
  await page.keyboard.press("Enter");
  await expect(page.getByText(/^[\d.]+ published$/)).toBeVisible();

  // Requested: the navigation is above the button gone.
  await tabTo(page, page.getByRole("link", { name: "Catalog", exact: true }), "Shift+Tab");
  await page.keyboard.press("Enter");
  await skipToContent(page);
  await tabTo(page, page.getByRole("link", { name: /^PostgreSQL 16 cluster/ }));
  await page.keyboard.press("Enter");
  await tabTo(page, page.getByRole("textbox", { name: "Database name" }));
  await page.keyboard.type("ledger");
  await tabTo(page, page.getByRole("button", { name: "Submit for approval" }));
  await page.keyboard.press("Enter");
  await expect(page).toHaveURL(/\/requests\/REQ-\d+$/);

  // Followed on the version's graph: the step added, between the nodes and the owner, not started yet.
  const run = page.getByRole("application", { name: /^Run of REQ-\d+$/ });
  const step = (name: RegExp) => run.getByRole("button", { name });
  await expect(step(/^Request\. Running\./)).toBeVisible();
  await tabTo(page, step(/^Request\./));
  await expect(step(/^Item The database's owner\. Not started\..*Runs after Monitoring agent/)).toBeVisible();
  await arrowTo(page, step(/^AWX Monitoring agent\. Not started\..*Runs after Database nodes/));
  await page.keyboard.press("Enter");
  await expect(step(/^AWX Monitoring agent/)).toHaveAttribute("aria-pressed", "true");
  await expect(page.getByRole("region", { name: "Selected: Monitoring agent" })).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(page.getByRole("region", { name: "Selected: Monitoring agent" })).toHaveCount(0);
  await tabTo(page, page.getByRole("button", { name: "Timeline" }));
  await page.keyboard.press("Enter");
  await expect(page.getByRole("region", { name: "Steps", exact: true })).toContainText(
    /Waiting for approval[\s\S]*Database nodes[\s\S]*Monitoring agent · monitoring-agent[\s\S]*The database's owner/,
  );
  expect(errors).toEqual([]);
});

test("a failed composite request retried and followed to its end, with the keyboard only", async ({ page }) => {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/requests");
  await skipToContent(page);
  await tabTo(page, page.getByRole("link", { name: /PostgreSQL 16 cluster · orders/ }));
  await page.keyboard.press("Enter");
  await expect(page).toHaveURL(/\/requests\/REQ-10484$/);
  const run = page.getByRole("application", { name: "Run of REQ-10484" });
  const node = (name: RegExp) => run.getByRole("button", { name });
  await expect(node(/^Item The database's owner\. Failed\./)).toHaveAttribute("aria-pressed", "true");

  // Another node, from the keyboard: its steps; the failure stays under the graph.
  await tabTo(page, node(/^Request\./));
  await arrowTo(page, node(/^AWX Backups\. Undone\./));
  await page.keyboard.press("Enter");
  await expect(page.getByRole("region", { name: "Selected: Backups" })).toContainText(
    "Undo Backups · pgbackrest-stanza-delete",
  );

  // Retried, and followed to the end.
  await tabTo(page, page.getByRole("button", { name: "Retry", exact: true }));
  await page.keyboard.press("Enter");
  await expect(node(/^Done\. Done\./)).toBeVisible({ timeout: 15_000 });
  for (const name of [/^Item Database nodes\. Done\./, /^Item The database's owner\. Done\./, /^AWX Backups\. Done\./])
    await expect(node(name)).toBeVisible();
  // Its last step's details, from the keyboard: back up to the graph from Retry, gone, where it was left, End, then Enter.
  await tabTo(page, node(/^AWX Backups/), "Shift+Tab");
  await page.keyboard.press("End");
  await expect(node(/^Done\. Done\./)).toBeFocused();
  await page.keyboard.press("Enter");
  await expect(page.getByRole("region", { name: "Selected: Done" })).toContainText("Ready");
  expect(errors).toEqual([]);
});
