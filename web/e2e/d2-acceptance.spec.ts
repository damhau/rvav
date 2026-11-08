import AxeBuilder from "@axe-core/playwright";
import { expect, type Locator, type Page, test } from "@playwright/test";

// D2's acceptance in the browser (D2.6, ADR-0033), on the mock backend. The
// vRO workflow std-ask-requester, which rvav vro-triage -standard puts in group
// A (a REST call, a wait for a person), rebuilt as a composite on the canvas
// with the keyboard only: the requester is asked which backup policy the VM
// needs, the answer is recorded in the CMDB, and a branch on it warns the owner
// when there will be none. Published, requested, answered as the requester,
// and followed on the run's graph, the side taken included. Then axe on the
// designer, the run waiting for the answer and the run that ended, in both
// themes. The route is in three tests, each under a minute at 6x CPU
// throttling: the later ones start from the YAML the earlier ones built, as
// the mocks reset when a page is opened. TestD2Acceptance runs the same steps
// through the real server and Temporal, the CMDB receiving the answer.

type Way = "Tab" | "Shift+Tab";

/**
 * Tab until `target` has the focus: Shift+Tab when it comes before what has
 * the focus. When nothing has (what had it went away), `gone` is the way to
 * the target from there (else forward, then back).
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

/** The page's skip link, then Enter: Tab goes on from the page's content. */
async function skipToContent(page: Page) {
  await tabTo(page, page.getByRole("link", { name: "Skip to content" }), "Tab");
  await page.keyboard.press("Enter");
}

/** Opens the focused list with Enter, then the arrow keys to an option, then Enter. */
async function choose(page: Page, name: string | RegExp) {
  await page.keyboard.press("Enter");
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

/** The tabs are one Tab stop: from the one selected, the arrow keys to `name`. */
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

/** On a graph, the arrow keys from the focused node to `target`: along each row, then down. */
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

/** In the Add a step dialog, its focus on what it runs: that, a key and a title, then Enter. Its details take the focus. */
async function addStep(page: Page, runs: string | RegExp, key: string, title: string) {
  const dialog = page.getByRole("dialog", { name: "Add a step" });
  await expect(dialog.getByRole("combobox", { name: "What it runs" })).toBeFocused();
  await choose(page, runs);
  await tabTo(page, dialog.getByRole("textbox", { name: "Key" }), "Tab");
  await page.keyboard.type(key);
  await page.keyboard.press("Tab");
  await expect(dialog.getByRole("textbox", { name: "Title" })).toBeFocused();
  await page.keyboard.type(title);
  await page.keyboard.press("Enter");
  await expect(dialog).toBeHidden();
  const details = page.getByRole("region", { name: `Selected: ${title}` });
  await expect(details.getByRole("heading", { name: title })).toBeFocused();
  return details;
}

const item = "Ask the requester";
const message = "Which backup policy does this VM need?";
const warning = "${inputs.name} has backup policy ${steps.detail.answer.backup}: nothing will back it up.";

/** The same composite, written into the YAML; `asked` is its first two steps. */
const definition = `form_schema:
  type: object
  required: [name]
  properties:
    name: {type: string, minLength: 1}
ui_schema:
  layout:
    kind: page
    sections: [{key: main, title: ${item}, columns: 1, fields: [name]}]
  fields:
    name: {widget: text, label: Name}
initial_values: {}
blueprint:
  kind: composite
  rollback_on_failure: true
  steps:
    - key: detail
      title: Ask for the backup policy
      ask:
        requester: true
        message: ${message}
        form:
          - {key: backup, label: Backup policy, type: choice, options: [daily, weekly, none], required: true}
    - key: record
      title: Record it in the CMDB
      rest:
        method: PATCH
        url: https://cmdb.corp.example/api/ci/\${inputs.name}
        body: "{'backup': steps.detail.answer.backup}"
    - key: way
      title: Backed up or not
      branch:
        cases:
          - title: No backup
            when: steps.detail.answer.backup == 'none'
            steps:
              - key: warn
                title: Warn the owner
                notify:
                  owner: true
                  message: "${warning}"
`;
const asked = definition.slice(0, definition.indexOf("    - key: way\n"));

/** A new composite item, its steps written into the YAML, its Definition tab open, by clicks. */
async function designed(page: Page, yaml = definition) {
  await page.goto("/admin/catalog-items");
  await page.getByRole("button", { name: "New item" }).click();
  const create = page.getByRole("dialog", { name: "New item" });
  await create.getByRole("textbox", { name: "Name" }).fill(item);
  await create.getByRole("textbox", { name: "Slug" }).fill("ask-requester");
  await create.getByRole("textbox", { name: "Summary" }).fill("Asks the requester which backup policy a VM needs.");
  await create.getByRole("combobox", { name: "Type" }).click();
  await page.getByRole("option", { name: "Composite" }).click();
  await create.getByRole("button", { name: "Create" }).click();
  await page.getByRole("tab", { name: "Definition" }).click();
  const editor = page.getByRole("textbox", { name: new RegExp(`^Definition of ${item}`) });
  await editor.click();
  await page.keyboard.press("Control+A");
  await page.keyboard.insertText(yaml);
  await expect(
    page
      .getByRole("region", { name: `Steps of ${item}` })
      .getByRole("button", { name: /^REST Record it in the CMDB\./ }),
  ).toBeVisible();
  await expect(page.getByRole("list", { name: "Problems in the steps" })).toHaveCount(0);
}

/** Saved, offered to Web Platform and published, by clicks: the mocks live in memory, so no page is opened anew. */
async function publish(page: Page) {
  await page.getByRole("button", { name: "Save draft" }).click();
  await expect(page.getByText(/^Draft [\d.]+ saved$/)).toBeVisible();
  await page.getByRole("tab", { name: "Entitlements" }).click();
  await page.getByRole("checkbox", { name: "Web Platform" }).click();
  await page.getByRole("button", { name: "Save entitlements" }).click();
  await expect(page.getByText("Entitlements saved")).toBeVisible();
  await page.getByRole("tab", { name: "Versions" }).click();
  await page.getByRole("button", { name: "Publish" }).click();
  await expect(page.getByText(/^[\d.]+ published$/)).toBeVisible();
}

async function scan(page: Page, where: string) {
  // A toast fading in reads as too faint; a placeholder isn't the page.
  await expect(page.locator("[data-sonner-toast]")).toHaveCount(0, { timeout: 25_000 });
  await expect(page.locator(".animate-pulse, [role=status][aria-label=Loading]")).toHaveCount(0);
  await page.waitForTimeout(300);
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

test("rebuilt on the canvas with the keyboard only: the requester asked, the answer recorded in the CMDB", async ({
  page,
}) => {
  // Some 90 keys, each awaited and checked.
  test.slow();
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));

  // A new composite item.
  await page.goto("/admin/catalog-items");
  await skipToContent(page);
  await tabTo(page, page.getByRole("button", { name: "New item" }));
  await page.keyboard.press("Enter");
  const create = page.getByRole("dialog", { name: "New item" });
  await tabTo(page, create.getByRole("textbox", { name: "Name" }), "Tab");
  await page.keyboard.type(item);
  await page.keyboard.press("Tab");
  await page.keyboard.type("ask-requester");
  await page.keyboard.press("Tab");
  await page.keyboard.type("Asks for a backup policy.");
  await tabTo(page, create.getByRole("combobox", { name: "Type" }));
  await choose(page, "Composite");
  await tabTo(page, create.getByRole("button", { name: "Create" }), "Tab");
  await page.keyboard.press("Enter");
  await expect(page.getByRole("heading", { name: item })).toBeVisible();
  await selectTab(page, "Definition", "ArrowRight");
  const editor = page.getByRole("textbox", { name: new RegExp(`^Definition of ${item}`) });
  const steps = page.getByRole("region", { name: `Steps of ${item}` });

  // The requester is asked: a choice they must make.
  await tabTo(page, page.getByRole("button", { name: "Add a step", exact: true }));
  await page.keyboard.press("Enter");
  const ask = await addStep(page, "A person", "detail", "Ask for the backup policy");
  await tabTo(page, ask.getByRole("checkbox", { name: "The requester" }));
  await page.keyboard.press("Space");
  await expect(editor).toContainText("requester: true");
  await tabTo(page, ask.getByRole("textbox", { name: "Message" }));
  await page.keyboard.type(message);
  await page.keyboard.press("Tab");
  await expect(ask.getByRole("button", { name: "Add a question" })).toBeFocused();
  await page.keyboard.press("Enter");
  const question = ask.getByRole("group", { name: "Question 1" });
  await expect(question.getByRole("textbox", { name: "Key" })).toBeFocused();
  await page.keyboard.press("Control+a");
  await page.keyboard.type("backup");
  await page.keyboard.press("Tab");
  await page.keyboard.type("Backup policy");
  await page.keyboard.press("Tab");
  await expect(question.getByRole("combobox", { name: "Type" })).toBeFocused();
  await choose(page, "A choice");
  await tabTo(page, question.getByRole("textbox", { name: "Options" }), "Tab");
  await page.keyboard.type("daily, weekly, none");
  await tabTo(page, question.getByRole("checkbox", { name: "An answer is required" }));
  await page.keyboard.press("Space");
  await expect(editor).toContainText("options: [daily, weekly, none]");
  await expect(editor).toContainText("required: true");

  // The answer recorded in the CMDB.
  await tabTo(page, page.getByRole("button", { name: "Add a step", exact: true }), "Shift+Tab");
  await page.keyboard.press("Enter");
  const record = await addStep(page, "A REST call", "record", "Record it in the CMDB");
  const call = record.getByRole("group", { name: "REST call" });
  await tabTo(page, call.getByRole("combobox", { name: "Method" }));
  await choose(page, "PATCH");
  await tabTo(page, call.getByRole("textbox", { name: "URL" }), "Tab");
  await page.keyboard.type("https://cmdb.corp.example/api/ci/${inputs.name}");
  await page.keyboard.press("Tab");
  await expect(call.getByRole("textbox", { name: "Body" })).toBeFocused();
  await page.keyboard.type("{'backup': steps.detail.answer.backup}");
  await page.keyboard.press("Tab");
  await expect(editor).toContainText("method: PATCH");
  await expect(editor).toContainText("body: \"{'backup': steps.detail.answer.backup}\"");
  await expect(
    steps.getByRole("button", { name: /^REST Record it in the CMDB\. record\. Calls: PATCH / }),
  ).toBeVisible();
  await expect(page.getByRole("list", { name: "Problems in the steps" })).toHaveCount(0);
  await tabTo(page, page.getByRole("button", { name: "Save draft" }), "Shift+Tab");
  await page.keyboard.press("Enter");
  await expect(page.getByText(/^Draft [\d.]+ saved$/)).toBeVisible();
  expect(errors).toEqual([]);
});

test("then a branch on the answer, its side telling the owner; saved, offered and published, with the keyboard only", async ({
  page,
}) => {
  test.slow();
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  // Its first two steps, as the test before built them.
  await designed(page, asked);
  const editor = page.getByRole("textbox", { name: new RegExp(`^Definition of ${item}`) });
  const steps = page.getByRole("region", { name: `Steps of ${item}` });

  // The answer decides, after the CMDB's step: no backup, the owner is told.
  await tabTo(page, page.getByRole("button", { name: "Add a step", exact: true }));
  await page.keyboard.press("Enter");
  const branch = await addStep(page, /^A branch/, "way", "Backed up or not");
  const none = branch.getByRole("group", { name: "Case 1" });
  await tabTo(page, none.getByRole("textbox", { name: "Title" }));
  await page.keyboard.type("No backup");
  await page.keyboard.press("Tab");
  await page.keyboard.type("steps.detail.answer.backup == 'none'");
  await page.keyboard.press("Tab");
  await expect(none.getByRole("button", { name: "Add a step to No backup" })).toBeFocused();
  await page.keyboard.press("Enter");
  const warn = await addStep(page, "A message to people", "warn", "Warn the owner");
  await tabTo(page, warn.getByRole("checkbox", { name: "The owner" }));
  await page.keyboard.press("Space");
  await tabTo(page, warn.getByRole("textbox", { name: "Message" }));
  await page.keyboard.type(warning);
  await page.keyboard.press("Tab");
  await expect(editor).toContainText(`when: "steps.detail.answer.backup == 'none'"`);
  await expect(editor).toContainText("owner: true");
  await expect(editor).toContainText("nothing will back it up");
  await expect(page.getByRole("list", { name: "Problems in the steps" })).toHaveCount(0);
  await expect(steps.getByRole("button", { name: /^Branch Backed up or not\. way\. No backup: / })).toBeVisible();
  await expect(steps.locator(".react-flow__edge-text", { hasText: "No backup" })).toBeVisible();
  await expect(editor).toContainText(/key: record[\s\S]*key: way/);

  // Saved, offered to Web Platform, published.
  await tabTo(page, page.getByRole("button", { name: "Save draft" }), "Shift+Tab");
  await page.keyboard.press("Enter");
  await expect(page.getByText(/^Draft [\d.]+ saved$/)).toBeVisible();
  await tabTo(page, page.getByRole("tab", { name: "Definition" }), "Shift+Tab");
  await selectTab(page, "Entitlements", "ArrowRight");
  await tabTo(page, page.getByRole("checkbox", { name: "Web Platform" }));
  await page.keyboard.press("Space");
  await tabTo(page, page.getByRole("button", { name: "Save entitlements" }));
  await page.keyboard.press("Enter");
  await expect(page.getByText("Entitlements saved")).toBeVisible();
  await tabTo(page, page.getByRole("tab", { name: "Entitlements" }), "Shift+Tab");
  await page.keyboard.press("Home");
  await expect(page.getByRole("tab", { name: "Versions" })).toHaveAttribute("aria-selected", "true");
  await tabTo(page, page.getByRole("button", { name: "Publish" }));
  await page.keyboard.press("Enter");
  await expect(page.getByText(/^[\d.]+ published$/)).toBeVisible();
  expect(errors).toEqual([]);
});

test("requested, answered as the requester and followed on the graph, the side taken, with the keyboard only", async ({
  page,
}) => {
  test.slow();
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await designed(page);
  await publish(page);

  // Requested: its form draws the steps, not what is asked, compared or said.
  await tabTo(page, page.getByRole("link", { name: "Catalog", exact: true }), "Shift+Tab");
  await page.keyboard.press("Enter");
  await skipToContent(page);
  await tabTo(page, page.getByRole("link", { name: new RegExp(`^${item}`) }));
  await page.keyboard.press("Enter");
  const form = page.getByRole("region", { name: `Steps of ${item}` });
  await expect(form.getByRole("button", { name: /^Branch Backed up or not\. way\./ })).toBeVisible();
  for (const hidden of [message, "'none'", "nothing will back it up"])
    await expect(page.getByRole("main")).not.toContainText(hidden);
  await tabTo(page, page.getByRole("textbox", { name: "Name" }));
  await page.keyboard.type("web-01");
  await tabTo(page, page.getByRole("button", { name: "Submit request" }));
  await page.keyboard.press("Enter");
  await expect(page).toHaveURL(/\/requests\/REQ-\d+$/);

  // The requester is asked on the request's page, and answers: no backup.
  const run = page.getByRole("application", { name: /^Run of REQ-\d+$/ });
  const node = (name: RegExp) => run.getByRole("button", { name });
  await expect(node(/^Person Ask for the backup policy\. Running\./)).toBeVisible({ timeout: 20_000 });
  const answer = page.getByRole("form", { name: "Your answer: Ask for the backup policy" });
  await expect(answer).toContainText(message);
  await tabTo(page, answer.getByRole("combobox", { name: "Backup policy" }));
  await choose(page, "none");
  await tabTo(page, answer.getByRole("button", { name: "Send answer" }), "Tab");
  await page.keyboard.press("Enter");
  await expect(answer).toHaveCount(0);
  await expect(page.getByRole("list", { name: "Steps that wait for a person" })).toContainText(
    "Answered by Léa Martin",
  );

  // The answer drove the branch: followed on the graph, the side taken.
  await expect(node(/^Done\. Done\./)).toBeVisible({ timeout: 30_000 });
  await expect(node(/^REST Record it in the CMDB\. Done\./)).toBeVisible();
  await expect(node(/^Branch Backed up or not\. Done\./)).toBeVisible();
  await expect(run.getByText("No backup: taken")).toBeVisible();
  await tabTo(page, node(/^Request\./), "Shift+Tab");
  await arrowTo(page, node(/^Branch Backed up or not\. Done\./));
  await page.keyboard.press("Enter");
  await expect(page.getByRole("region", { name: "Selected: Backed up or not" })).toContainText("Took: No backup");
  await arrowTo(page, node(/^Message Warn the owner\. Done\./));
  await page.keyboard.press("Enter");
  await expect(page.getByRole("region", { name: "Selected: Warn the owner" })).toContainText("Message sent.");
  await tabTo(page, page.getByRole("button", { name: "Timeline" }));
  await page.keyboard.press("Enter");
  await expect(page.getByRole("region", { name: "Steps", exact: true })).toContainText(
    /Answered by Léa Martin[\s\S]*PATCH answered 200[\s\S]*Took: No backup[\s\S]*Message sent\./,
  );
  expect(errors).toEqual([]);
});

for (const theme of ["light", "dark"] as const) {
  test(`axe, ${theme} theme: the designer, the run waiting for the answer and the run that took a side`, async ({
    page,
  }) => {
    test.slow();
    await page.addInitScript((t) => localStorage.setItem("rvav.theme", t), theme);
    await designed(page);
    await page
      .getByRole("region", { name: `Steps of ${item}` })
      .getByRole("button", { name: /^Branch Backed up or not\./ })
      .click();
    await expect(page.getByRole("region", { name: "Selected: Backed up or not" })).toBeVisible();
    await scan(page, `designer (${theme})`);

    await publish(page);
    await page.getByRole("navigation").getByRole("link", { name: "Catalog", exact: true }).click();
    await page.getByRole("link", { name: new RegExp(`^${item}`) }).click();
    await page.getByRole("textbox", { name: "Name" }).fill("web-01");
    await page.getByRole("button", { name: "Submit request" }).click();

    const run = page.getByRole("application", { name: /^Run of REQ-\d+$/ });
    const answer = page.getByRole("form", { name: "Your answer: Ask for the backup policy" });
    await expect(answer).toBeVisible({ timeout: 20_000 });
    await scan(page, `the run waiting for the answer (${theme})`);

    await answer.getByRole("combobox", { name: "Backup policy" }).click();
    await page.getByRole("option", { name: "none", exact: true }).click();
    await answer.getByRole("button", { name: "Send answer" }).click();
    await expect(run.getByRole("button", { name: /^Message Warn the owner\. Done\./ })).toBeVisible({
      timeout: 30_000,
    });
    await run.getByRole("button", { name: /^Message Warn the owner\./ }).click();
    await expect(page.getByRole("region", { name: "Selected: Warn the owner" })).toBeVisible();
    await scan(page, `the run that took a side (${theme})`);
    await page.getByRole("button", { name: "Timeline" }).click();
    await expect(page.getByRole("region", { name: "Steps", exact: true })).toContainText("Took: No backup");
    await scan(page, `its timeline (${theme})`);
  });
}
