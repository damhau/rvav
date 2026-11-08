import { expect, type Page, test } from "@playwright/test";

// A composite's branch and message (D2.5, ADR-0033) on the mock backend: added
// in the designer from the keyboard, each edit one step of the YAML's history;
// on the graph and the list view; saved, published, requested. A requester's
// form masks what a case compares, and who a message names and what it says. On
// a run's graph the side taken can be followed, the others skipped; a case that
// can't be read fails its branch, masked. In the mocks a case reads the form, a
// person's answer or a REST call's status, other outputs as null;
// d2-acceptance.spec.ts branches on an answer.

/** In a list opened from the keyboard, the arrow keys to an option, then Enter. */
async function choose(page: Page, name: string | RegExp) {
  await page.keyboard.press("Enter");
  const option = page.getByRole("option", { name });
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

/** Types into the focused field and leaves it, which commits it, for the next one. */
async function typeAndTab(page: Page, text: string) {
  await page.keyboard.type(text);
  await page.keyboard.press("Tab");
}

/** A new composite item, its Definition tab open: a new one needs no approval, so it runs on its own. */
async function newItem(page: Page, name: string, slug: string) {
  await page.goto("/admin/catalog-items");
  await page.getByRole("button", { name: "New item" }).click();
  const dialog = page.getByRole("dialog", { name: "New item" });
  await dialog.getByRole("textbox", { name: "Name" }).fill(name);
  await dialog.getByRole("textbox", { name: "Slug" }).fill(slug);
  await dialog.getByRole("textbox", { name: "Summary" }).fill("Branches.");
  await dialog.getByRole("combobox", { name: "Type" }).click();
  await page.getByRole("option", { name: /composite/i }).click();
  await dialog.getByRole("button", { name: "Create" }).click();
  await expect(page.getByRole("heading", { name })).toBeVisible();
  await page.getByRole("tab", { name: "Definition" }).click();
}

const aBranch = /^A branch/;
const aMessage = "A message to people";

/** The dialog that's open: what it runs, its key and title, from the keyboard. Its details take the focus. */
async function fillAdd(page: Page, runs: string | RegExp, key: string, title: string) {
  const add = page.getByRole("dialog", { name: "Add a step" });
  await expect(add.getByRole("combobox", { name: "What it runs" })).toBeFocused();
  await choose(page, runs);
  await add.getByRole("textbox", { name: "Key" }).fill(key);
  await add.getByRole("textbox", { name: "Title" }).fill(title);
  await add.getByRole("button", { name: "Add step" }).focus();
  await page.keyboard.press("Enter");
  const details = page.getByRole("region", { name: `Selected: ${title}` });
  await expect(details.getByRole("heading", { name: title })).toBeFocused();
  return details;
}

async function addStep(page: Page, runs: string | RegExp, key: string, title: string) {
  await page.getByRole("button", { name: "Add a step", exact: true }).click();
  return fillAdd(page, runs, key, title);
}

/** Back to the branch from a step on one of its sides. */
async function backTo(page: Page, key: string, title: string) {
  await page.getByRole("button", { name: `Select the branch ${key}` }).focus();
  await page.keyboard.press("Enter");
  const details = page.getByRole("region", { name: `Selected: ${title}` });
  await expect(details.getByRole("heading", { name: title })).toBeFocused();
  return details;
}

/** Fills in a text field and leaves it, which commits it. */
async function commit(where: ReturnType<Page["getByRole"]>, label: string, value: string) {
  const field = where.getByRole("textbox", { name: label, exact: true });
  await field.fill(value);
  await field.press("Tab");
}

/** Saves, offers to Web Platform, publishes, then opens its form from the catalog, by clicks: the mocks live in memory. */
async function publishAndOpen(page: Page, name: string) {
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
  await page.getByRole("link", { name: new RegExp(`^${name}`) }).click();
}

async function submit(page: Page, name = "web-01") {
  await page.getByRole("textbox", { name: "Name" }).fill(name);
  await page.getByRole("button", { name: "Submit request" }).click();
  await expect(page).toHaveURL(/\/requests\/REQ-\d+$/);
}

const yes = "Named web-01";
const yesWhen = "inputs.name == 'web-01'";

/** A branch on the form's name, a message on its first side, a wait on its second, a REST call otherwise. */
async function buildBranch(page: Page) {
  const editor = page.getByRole("textbox", { name: /^Definition of Which way/ });
  const branch = await addStep(page, aBranch, "way", "The way");
  await expect(branch.getByRole("list", { name: "Problems with this step" })).toContainText(
    "Say when this case is taken",
  );
  // A branch's conditions are its cases'; it has no inputs and nothing to undo.
  await expect(branch.getByRole("group", { name: "Inputs" })).toHaveCount(0);
  await expect(branch.getByRole("group", { name: "Undo" })).toHaveCount(0);
  await expect(branch.getByRole("textbox", { name: "When", exact: true })).toHaveCount(1);

  // Its first case, in the order they are tabbed through, then a step on its side.
  const first = branch.getByRole("group", { name: "Case 1" });
  await first.getByRole("textbox", { name: "Title" }).focus();
  await typeAndTab(page, yes);
  await expect(first.getByRole("textbox", { name: "When" })).toBeFocused();
  await typeAndTab(page, yesWhen);
  await expect(editor).toContainText(yesWhen);
  await expect(branch.getByRole("list", { name: "Problems with this step" })).toHaveCount(0);
  await expect(first.getByRole("button", { name: `Add a step to ${yes}` })).toBeFocused();
  await page.keyboard.press("Enter");
  const message = await fillAdd(page, aMessage, "told", "Tell them");
  await expect(message).toContainText("On a side of");
  const problems = message.getByRole("list", { name: "Problems with this step" });
  await expect(problems).toContainText("Say who is told");
  await expect(problems).toContainText("Say what it says.");
  // Who it tells, then what it says, in the order they are tabbed through.
  await message.getByRole("checkbox", { name: "The requester" }).focus();
  await page.keyboard.press("Space");
  await expect(editor).toContainText("requester: true");
  await page.keyboard.press("Tab");
  await expect(message.getByRole("checkbox", { name: "The owner" })).toBeFocused();
  await page.keyboard.press("Tab");
  await expect(message.getByRole("textbox", { name: "OIDC groups" })).toBeFocused();
  await typeAndTab(page, "grp-dba-secret");
  await expect(editor).toContainText("grp-dba-secret");
  await expect(message.getByRole("textbox", { name: "People" })).toBeFocused();
  await page.keyboard.press("Tab");
  await expect(message.getByRole("textbox", { name: "Message" })).toBeFocused();
  await typeAndTab(page, "Ready, secret text: ${inputs.name}");
  await expect(editor).toContainText("Ready, secret text: ${inputs.name}");
  await expect(problems).toHaveCount(0);

  // A case with its own side, added from the branch: the focus goes to its title.
  let again = await backTo(page, "way", "The way");
  await expect(again.getByRole("group", { name: "Case 1" })).toContainText("Tell them");
  await again.getByRole("button", { name: "Add a case" }).focus();
  await page.keyboard.press("Enter");
  const second = again.getByRole("group", { name: "Case 2" });
  await expect(second.getByRole("textbox", { name: "Title" })).toBeFocused();
  await page.keyboard.press("Tab");
  await typeAndTab(page, "inputs.name == 'db-01'");
  await expect(second.getByRole("button", { name: "Add a step to Case 2" })).toBeFocused();
  await page.keyboard.press("Enter");
  const hold = await fillAdd(page, "A wait for a time", "hold", "Hold it");
  await commit(hold, "For", "72h");

  // What runs otherwise.
  again = await backTo(page, "way", "The way");
  await again.getByRole("button", { name: "Add a step to Otherwise" }).focus();
  await page.keyboard.press("Enter");
  const log = await fillAdd(page, "A REST call", "log", "Log it");
  await commit(log, "URL", "https://cmdb.corp.example/log");

  // It moves within its side only, and can't run with the step before.
  await expect(log.getByRole("button", { name: "Run with the step before" })).toHaveCount(0);
  await expect(log.locator("#cd-earlier")).toBeDisabled();
  await expect(log.locator("#cd-later")).toBeDisabled();

  // A step after the branch: at the same time as a branch can't be chosen.
  again = await backTo(page, "way", "The way");
  await page.getByRole("button", { name: "Add a step", exact: true }).click();
  const add = page.getByRole("dialog", { name: "Add a step" });
  await expect(add.getByRole("radio", { name: "After The way" })).toBeVisible();
  await expect(add.getByRole("radio", { name: "At the same time as The way" })).toHaveCount(0);
  const done = await fillAdd(page, aMessage, "bye", "Say it's done");
  await commit(done, "Message", "All done.");
  await done.getByRole("checkbox", { name: "The owner" }).click();
  await expect(done).toContainText("steps.way.case");
  return editor;
}

test("a branch and its messages, filled in from the keyboard, published, requested, and its side followed", async ({
  page,
}) => {
  test.slow();
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await newItem(page, "Which way", "which-way");
  const editor = await buildBranch(page);

  // Each edit is one step of the YAML's history.
  await page.getByRole("button", { name: "Undo", exact: true }).click();
  await expect(editor).not.toContainText("owner: true");
  await page.getByRole("button", { name: "Redo", exact: true }).click();
  await expect(editor).toContainText("owner: true");

  // On the graph, each side's way labelled; on the list, its steps under its cases.
  const steps = page.getByRole("region", { name: "Steps of Which way" });
  await expect(
    steps.getByRole("button", { name: /^Branch The way\. way\. Named web-01: inputs\.name == / }),
  ).toBeVisible();
  await expect(
    steps.getByRole("button", { name: /^Message Tell them\. told\. Tells: the requester, grp-dba-secret/ }),
  ).toBeVisible();
  await expect(steps.locator(".react-flow__edge-text", { hasText: yes })).toBeVisible();
  await steps.getByRole("group", { name: "Show as" }).getByRole("button", { name: "List" }).click();
  await expect(steps).toContainText(yesWhen);
  await expect(steps).toContainText("Otherwise");
  await expect(steps).toContainText("On the side Otherwise of The way");
  await steps.getByRole("group", { name: "Show as" }).getByRole("button", { name: "Graph" }).click();

  await publishAndOpen(page, "Which way");

  // A requester's form: the way it goes, not what it compares, who it names or what it says.
  const form = page.getByRole("region", { name: "Steps of Which way" });
  await expect(form.getByRole("button", { name: /^Branch The way\. way\./ })).toBeVisible();
  await expect(form.getByRole("button", { name: /^Message Tell them\. told\. Tells: the requester/ })).toBeVisible();
  await expect(form).not.toContainText("1 problem");
  const main = page.getByRole("main");
  for (const hidden of ["grp-dba-secret", "secret text", "'web-01'", "cmdb.corp.example"])
    await expect(main).not.toContainText(hidden);
  await submit(page);

  // The side taken can be followed, the others are skipped; so is what its steps said.
  const run = page.getByRole("application", { name: /^Run of REQ-\d+$/ });
  await expect(run.getByRole("button", { name: /^Message Say it's done\. Done\./ })).toBeVisible({ timeout: 30_000 });
  await expect(run.getByRole("button", { name: /^Branch The way\. Done\./ })).toBeVisible();
  await expect(run.getByRole("button", { name: /^Message Tell them\. Done\./ })).toBeVisible();
  await expect(run.getByRole("button", { name: /^Wait Hold it\. Skipped\./ })).toBeVisible();
  await expect(run.getByRole("button", { name: /^REST Log it\. Skipped\./ })).toBeVisible();
  await expect(run.getByText(`${yes}: taken`)).toBeVisible();
  await expect(run.getByText("Case 2: not taken")).toBeVisible();
  await expect(run.getByText("Otherwise: not taken")).toBeVisible();
  await page.getByRole("button", { name: "Timeline" }).click();
  const timeline = page.getByRole("region", { name: "Steps", exact: true });
  await expect(timeline).toContainText(`Took: ${yes}`);
  await expect(timeline).toContainText("Message sent.");
  await expect(timeline).toContainText("Its side of the branch wasn't taken.");
  await expect(timeline).not.toContainText("secret text");
  await expect(timeline).not.toContainText("grp-dba-secret");
  // A message to the requester reaches the notification centre.
  await page.getByRole("button", { name: /^Notifications/ }).click();
  await expect(
    page
      .getByRole("dialog", { name: /Notifications/ })
      .or(page.getByRole("region", { name: /Notifications/ }))
      .first(),
  ).toContainText("Ready, secret text: web-01");
  expect(errors).toEqual([]);
});

test("no case holds: what runs otherwise, and a later step is offered what a side gives", async ({ page }) => {
  test.slow();
  await newItem(page, "Otherwise", "otherwise");
  const branch = await addStep(page, aBranch, "way", "The way");
  await commit(branch.getByRole("group", { name: "Case 1" }), "When", "inputs.name == 'db-01'");
  await branch.getByRole("button", { name: "Add a step to Otherwise" }).click();
  const log = await fillAdd(page, "A REST call", "log", "Log it");
  await commit(log, "URL", "https://cmdb.corp.example/log");
  // A later step is offered what a side's step gives; a side, another side's isn't.
  await backTo(page, "way", "The way");
  const after = await addStep(page, "A REST call", "after", "After it");
  await expect(after).toContainText("steps.log.status");
  await commit(after, "URL", "https://cmdb.corp.example/after");

  await publishAndOpen(page, "Otherwise");
  await submit(page);
  const run = page.getByRole("application", { name: /^Run of REQ-\d+$/ });
  await expect(run.getByRole("button", { name: /^REST After it\. Done\./ })).toBeVisible({ timeout: 30_000 });
  await expect(run.getByRole("button", { name: /^REST Log it\. Done\./ })).toBeVisible();
  await expect(run.getByText("Otherwise: taken")).toBeVisible();
  await expect(run.getByText("Case 1: not taken")).toBeVisible();
  await page.getByRole("button", { name: "Timeline" }).click();
  await expect(page.getByRole("region", { name: "Steps", exact: true })).toContainText("Took: otherwise");
});

test("with no otherwise, and no case that holds, it goes on and says so", async ({ page }) => {
  test.slow();
  await newItem(page, "Nothing here", "nothing-here");
  const none = await addStep(page, aBranch, "way", "The way");
  const first = none.getByRole("group", { name: "Case 1" });
  await commit(first, "When", "inputs.name == 'db-01'");
  await first.getByRole("button", { name: "Add a step to Case 1" }).click();
  const skipped = await fillAdd(page, "A wait for a time", "hold", "Hold it");
  await commit(skipped, "For", "72h");
  await publishAndOpen(page, "Nothing here");
  await submit(page);
  const empty = page.getByRole("application", { name: /^Run of REQ-\d+$/ });
  await expect(empty.getByRole("button", { name: /^Wait Hold it\. Skipped\./ })).toBeVisible({ timeout: 30_000 });
  await expect(empty.getByText("Otherwise: taken")).toBeVisible();
  await expect(page.getByRole("heading", { name: /Nothing here/ })).toBeVisible();
  await expect(page.getByText("Succeeded", { exact: true })).toBeVisible({ timeout: 30_000 });
  await page.getByRole("button", { name: "Timeline" }).click();
  await expect(page.getByRole("region", { name: "Steps", exact: true })).toContainText(
    "No case held: nothing ran here.",
  );
});

test("a message that can't be filled in fails its step, masked, and a Retry keeps the side chosen", async ({
  page,
}) => {
  test.slow();
  await newItem(page, "Bad message", "bad-message");
  const branch = await addStep(page, aBranch, "way", "The way");
  const first = branch.getByRole("group", { name: "Case 1" });
  await commit(first, "When", "inputs.name == 'web-01'");
  await first.getByRole("button", { name: "Add a step to Case 1" }).click();
  const bad = await fillAdd(page, aMessage, "told", "Tell them");
  await bad.getByRole("checkbox", { name: "The requester" }).click();
  await commit(bad, "Message", "See ${[inputs.name, 'lit-n0te-secret']}");
  await publishAndOpen(page, "Bad message");
  await submit(page);
  const run = page.getByRole("application", { name: /^Run of REQ-\d+$/ });
  const failed = run.getByRole("button", { name: /^Message Tell them\. Failed\./ });
  await expect(failed).toBeVisible({ timeout: 30_000 });
  const main = page.getByRole("main");
  await expect(main).toContainText("The message wasn't sent");
  await expect(main).toContainText("it isn't a single value");
  await expect(main).not.toContainText("lit-n0te-secret");
  await page.getByRole("button", { name: "Retry from the failed step" }).click();
  await expect(page.getByRole("button", { name: "Retry from the failed step" })).toHaveCount(0);
  await expect(failed).toBeVisible({ timeout: 30_000 });
  await expect(run.getByRole("button", { name: /^Branch The way\. Done\./ })).toBeVisible();
  await expect(run.getByText("Case 1: taken")).toBeVisible();
});

test("a case that can't be read fails its branch, masked, its side not started, and a Retry reads it again", async ({
  page,
}) => {
  test.slow();
  await newItem(page, "Unread case", "unread-case");
  const branch = await addStep(page, aBranch, "way", "The way");
  const first = branch.getByRole("group", { name: "Case 1" });
  await commit(first, "When", "inputs.absent == 'lit-c4se-secret'");
  await first.getByRole("button", { name: "Add a step to Case 1" }).click();
  const hold = await fillAdd(page, "A wait for a time", "hold", "Hold it");
  await commit(hold, "For", "72h");
  await publishAndOpen(page, "Unread case");
  await submit(page);
  const run = page.getByRole("application", { name: /^Run of REQ-\d+$/ });
  const failed = run.getByRole("button", { name: /^Branch The way\. Failed\./ });
  await expect(failed).toBeVisible({ timeout: 30_000 });
  await expect(run.getByRole("button", { name: /^Wait Hold it\. Not started\./ })).toBeVisible();
  const main = page.getByRole("main");
  await expect(main).toContainText("Its side couldn't be chosen");
  await expect(main).toContainText("what it reads isn't there");
  await expect(main).not.toContainText("lit-c4se-secret");
  // What it reads still isn't there: it fails again, its side still not started.
  await page.getByRole("button", { name: "Retry from the failed step" }).click();
  await expect(page.getByRole("button", { name: "Retry from the failed step" })).toHaveCount(0);
  await expect(failed).toBeVisible({ timeout: 30_000 });
  await expect(run.getByRole("button", { name: /^Wait Hold it\. Not started\./ })).toBeVisible();
  await page.getByRole("button", { name: "Timeline" }).click();
  const timeline = page.getByRole("region", { name: "Steps", exact: true });
  await expect(timeline).toContainText("Its side couldn't be chosen");
  await expect(timeline).not.toContainText("lit-c4se-secret");
});

test("a step on a side can't read another side, and neither a branch nor a VM can be on one", async ({ page }) => {
  test.slow();
  await newItem(page, "Refused", "refused");
  const branch = await addStep(page, aBranch, "way", "The way");
  await commit(branch.getByRole("group", { name: "Case 1" }), "When", "inputs.name == 'a'");
  await branch.getByRole("button", { name: "Add a step to Case 1" }).click();
  const one = await fillAdd(page, "A REST call", "one", "One");
  await commit(one, "URL", "https://cmdb.corp.example/one");
  const again = await backTo(page, "way", "The way");
  await again.getByRole("button", { name: "Add a step to Otherwise" }).click();
  const other = await fillAdd(page, "A REST call", "other", "Other");
  // Not offered, and refused when written.
  await expect(other).not.toContainText("steps.one.status");
  await commit(other, "URL", "https://cmdb.corp.example/${steps.one.status}");
  await expect(other.getByRole("list", { name: "Problems with this step" })).toContainText("steps.one");
  await commit(other, "URL", "https://cmdb.corp.example/other");
  await expect(other.getByRole("list", { name: "Problems with this step" })).toHaveCount(0);

  // A branch can't go on a side, nor in a parallel group: it isn't offered.
  const back = await backTo(page, "way", "The way");
  await back.getByRole("button", { name: "Add a step to Otherwise" }).click();
  const add = page.getByRole("dialog", { name: "Add a step" });
  await expect(add.getByRole("combobox", { name: "What it runs" })).toBeFocused();
  await page.keyboard.press("Enter");
  await expect(page.getByRole("option", { name: aMessage })).toBeVisible();
  await expect(page.getByRole("option", { name: aBranch })).toHaveCount(0);
  // A VM is planned at submission: it can't wait for a branch.
  await page.getByRole("option", { name: /^RHEL 9 server/ }).click();
  await add.getByRole("textbox", { name: "Key" }).fill("vm");
  await add.getByRole("button", { name: "Add step" }).click();
  const vm = page.getByRole("region", { name: /^Selected: / });
  await expect(vm.getByRole("list", { name: "Problems with this step" })).toContainText("can't be on a side of one");
});
