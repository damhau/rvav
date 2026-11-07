import { expect, type Page, test } from "@playwright/test";

// A composite's waits for a time and for a condition (D2.4, ADR-0033) on the
// mock backend: added in the designer from the keyboard, their fields in tab
// order, the YAML following; on the graph and the list view; saved, published,
// requested. A requester's form masks what they read. A running one waits on
// the run's graph and says on the timeline what for and until when; a cancel
// stops it. The mocks count a tick as a minute of a wait, and a poll's condition
// holds at its third check: `72h` and `every: 6h` stay, `every: 1m` runs on.

/** Opens a select from the keyboard and picks an option by its text. */
async function choose(page: Page, option: string | RegExp) {
  await page.keyboard.press("Enter");
  await page.getByRole("option", { name: option }).click();
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
  await dialog.getByRole("textbox", { name: "Summary" }).fill("Waits.");
  await dialog.getByRole("combobox", { name: "Type" }).click();
  await page.getByRole("option", { name: /composite/i }).click();
  await dialog.getByRole("button", { name: "Create" }).click();
  await expect(page.getByRole("heading", { name })).toBeVisible();
  await page.getByRole("tab", { name: "Definition" }).click();
}

const aTime = "A wait for a time";
const aCondition = /^A wait for a condition/;

/** Adds a step; its details take the focus. */
async function addStep(page: Page, runs: string | RegExp, key: string, title: string) {
  await page.getByRole("button", { name: "Add a step" }).click();
  const add = page.getByRole("dialog", { name: "Add a step" });
  await expect(add.getByRole("combobox", { name: "What it runs" })).toBeFocused();
  await choose(page, runs);
  await add.getByRole("textbox", { name: "Key" }).fill(key);
  await add.getByRole("textbox", { name: "Title" }).fill(title);
  await add.getByRole("button", { name: "Add step" }).click();
  const details = page.getByRole("region", { name: `Selected: ${title}` });
  await expect(details.getByRole("heading", { name: title })).toBeFocused();
  return details;
}

/** Fills in a text field and leaves it, which commits it. */
async function commit(details: ReturnType<Page["getByRole"]>, label: string, value: string) {
  const field = details.getByRole("textbox", { name: label, exact: true });
  await field.fill(value);
  await field.press("Tab");
}

/** A poll's call and condition, for an item that isn't about the keyboard. */
async function fillPoll(details: ReturnType<Page["getByRole"]>, key: string) {
  await commit(details, "URL", "https://cmdb.corp.example/change/${inputs.name}");
  await commit(details, "Every", "1m");
  await commit(details, "Until", `steps.${key}.json == 'approved'`);
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

async function submit(page: Page) {
  await page.getByRole("textbox", { name: "Name" }).fill("web-01");
  await page.getByRole("button", { name: "Submit request" }).click();
  await expect(page).toHaveURL(/\/requests\/REQ-\d+$/);
}

const untilWhen = /Until \d{2} \w{3} \d{4}, \d{2}:\d{2}/;

test("a wait and a poll, filled in from the keyboard, published, requested, waiting and cancelled", async ({
  page,
}) => {
  test.slow();
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await newItem(page, "Keep a snapshot", "keep-a-snapshot");
  const editor = page.getByRole("textbox", { name: /^Definition of Keep a snapshot/ });
  const steps = page.getByRole("region", { name: "Steps of Keep a snapshot" });

  // Until it says how long, the step says so; it has no inputs or undo.
  const wait = await addStep(page, aTime, "keep", "Keep it three days");
  const waitProblems = wait.getByRole("list", { name: "Problems with this step" });
  await expect(waitProblems).toContainText("Say how long it waits, or until when.");
  await expect(wait.getByRole("group", { name: "Inputs" })).toHaveCount(0);
  await expect(wait.getByRole("group", { name: "Undo" })).toHaveCount(0);
  await wait.getByRole("textbox", { name: "For" }).focus();
  await typeAndTab(page, "soon");
  await expect(waitProblems).toContainText("A duration such as 72h.");
  await expect(wait.getByRole("textbox", { name: "Or until" })).toBeFocused();
  await wait.getByRole("textbox", { name: "For" }).focus();
  await page.keyboard.press("Control+a");
  await typeAndTab(page, "72h");
  await expect(editor).toContainText("for: 72h");
  await expect(waitProblems).toHaveCount(0);

  // A poll's fields, in the order they are tabbed through.
  const poll = await addStep(page, aCondition, "change", "The change is approved");
  const pollProblems = poll.getByRole("list", { name: "Problems with this step" });
  await expect(pollProblems).toContainText("Give the URL to call.");
  await expect(pollProblems).toContainText("Say what it waits for");
  await expect(poll.getByRole("group", { name: "Inputs" })).toHaveCount(0);
  await expect(poll.getByRole("group", { name: "Undo" })).toHaveCount(0);
  const call = poll.getByRole("group", { name: "The call it repeats" });
  await call.getByRole("combobox", { name: "Method" }).focus();
  await page.keyboard.press("Enter");
  await expect(page.getByRole("option")).toHaveText(["GET", "PUT", "DELETE"]);
  await page.keyboard.press("Escape");
  await call.getByRole("combobox", { name: "Method" }).focus();
  await page.keyboard.press("Tab");
  await expect(call.getByRole("textbox", { name: "URL" })).toBeFocused();
  await typeAndTab(page, "https://cmdb.corp.example/change/${inputs.name}");
  await expect(editor).toContainText("https://cmdb.corp.example/change/${inputs.name}");
  // A GET has no body, so the secret comes next, and what it accepts and keeps.
  await expect(call.getByRole("combobox", { name: "Secret" })).toBeFocused();
  await page.keyboard.press("Tab");
  await expect(call.getByRole("textbox", { name: "Accepted statuses" })).toBeFocused();
  await page.keyboard.press("Tab");
  await expect(call.getByRole("textbox", { name: "Keep" })).toBeFocused();
  await typeAndTab(page, "$.result.state");
  await expect(poll.getByRole("textbox", { name: "Every" })).toBeFocused();
  await page.keyboard.press("Control+a");
  await typeAndTab(page, "30s");
  await expect(pollProblems).toContainText("At least 1m");
  await expect(poll.getByRole("textbox", { name: "Until" })).toBeFocused();
  await typeAndTab(page, "inputs.name == 'x'");
  await expect(pollProblems).toContainText("Read the call's answer, as steps.change.json");
  await poll.getByRole("textbox", { name: "Every" }).focus();
  await page.keyboard.press("Control+a");
  await typeAndTab(page, "10m");
  await page.keyboard.press("Control+a");
  await typeAndTab(page, "steps.change.json == 'approved'");
  await expect(pollProblems).toHaveCount(0);
  await expect(poll.getByRole("textbox", { name: "Deadline" })).toBeFocused();
  await expect(poll.getByRole("textbox", { name: "Deadline" })).toHaveValue("72h");
  await page.keyboard.press("Tab");
  await expect(poll.getByRole("combobox", { name: "Then" })).toBeFocused();
  await choose(page, "It goes on");
  await expect(editor).toContainText("on_timeout: continue");

  // On the graph and the list, what they wait for and until when.
  const keepNode = /^Wait Keep it three days\. keep\. For: 72h/;
  const changeNode = /^Poll The change is approved\. change\. Calls: GET https:\/\/cmdb\.corp\.example/;
  await expect(steps.getByRole("button", { name: keepNode })).toBeVisible();
  await expect(steps.getByRole("button", { name: changeNode })).toBeVisible();
  await steps.getByRole("group", { name: "Show as" }).getByRole("button", { name: "List" }).click();
  await expect(steps).toContainText("steps.change.json == 'approved'");
  await expect(steps).toContainText("72h, then it goes on");
  await steps.getByRole("group", { name: "Show as" }).getByRole("button", { name: "Graph" }).click();

  await publishAndOpen(page, "Keep a snapshot");

  // A requester's form: that they wait and for how long, not what they call or compare.
  const form = page.getByRole("region", { name: "Steps of Keep a snapshot" });
  await expect(form.getByRole("button", { name: /^Wait Keep it three days\. keep\. For: 72h/ })).toBeVisible();
  await expect(form.getByRole("button", { name: /^Poll The change is approved/ })).toBeVisible();
  await expect(form).not.toContainText("1 problem");
  const main = page.getByRole("main");
  for (const hidden of ["cmdb.corp.example", "'approved'", "$.result.state"])
    await expect(main).not.toContainText(hidden);
  await submit(page);

  // It waits: on the graph, and on the timeline what for and until when.
  const run = page.getByRole("application", { name: /^Run of REQ-\d+$/ });
  await expect(run.getByRole("button", { name: /^Wait Keep it three days\. Waiting\./ })).toBeVisible({
    timeout: 20_000,
  });
  const selected = page.getByRole("region", { name: "Selected: Keep it three days" });
  await run.getByRole("button", { name: /^Wait Keep it three days/ }).click();
  await expect(selected).toContainText("Waiting for 72h");
  await expect(selected).toContainText(untilWhen);

  // A cancel stops it, whatever is left to wait.
  await page.getByRole("button", { name: "Cancel request" }).click();
  await page.getByRole("dialog").getByRole("button", { name: "Cancel request" }).click();
  await expect(run.getByRole("button", { name: /^Wait Keep it three days\. Cancelled\./ })).toBeVisible();
  await expect(run.getByRole("button", { name: /^Poll The change is approved\. Skipped\./ })).toBeVisible();
  await page.getByRole("button", { name: "Timeline" }).click();
  await expect(page.getByRole("region", { name: "Steps", exact: true })).not.toContainText(untilWhen);
  expect(errors).toEqual([]);
});

test("a poll checks and waits, a time passed goes on at once, and a deadline can go on, to the run's end", async ({
  page,
}) => {
  test.slow();
  await newItem(page, "Patch window", "patch-window");
  const passed = await addStep(page, aTime, "window", "The window");
  await commit(passed, "Or until", "'2020-01-02T03:04:05Z'");
  const poll = await addStep(page, aCondition, "change", "The change");
  await fillPoll(poll, "change");
  const late = await addStep(page, aCondition, "late", "The late one");
  await fillPoll(late, "late");
  await commit(late, "Deadline", "1m");
  await late.getByRole("combobox", { name: "Then" }).click();
  await page.getByRole("option", { name: "It goes on" }).click();
  const wait = await addStep(page, aTime, "soon", "A short wait");
  await commit(wait, "For", "2m");
  // What they give is offered to what comes after, and may be read.
  await page.getByRole("button", { name: "Add a step" }).click();
  const add = page.getByRole("dialog", { name: "Add a step" });
  await expect(add.getByRole("combobox", { name: "What it runs" })).toBeFocused();
  await choose(page, "A REST call");
  await add.getByRole("textbox", { name: "Key" }).fill("note");
  await add.getByRole("button", { name: "Add step" }).click();
  const note = page.getByRole("region", { name: "Selected: note" }).getByRole("group", { name: "REST call" });
  for (const offered of ["steps.window.until", "steps.change.status", "steps.change.json", "steps.late.met"])
    await expect(note).toContainText(offered);
  await commit(note, "URL", "https://cmdb.corp.example/${steps.late.met}");

  await publishAndOpen(page, "Patch window");
  await submit(page);

  const run = page.getByRole("application", { name: /^Run of REQ-\d+$/ });
  await expect(run.getByRole("button", { name: /^REST note\. Done\./ })).toBeVisible({ timeout: 40_000 });
  await expect(run.getByRole("button", { name: /^Poll The change\. Done\./ })).toBeVisible();
  await page.getByRole("button", { name: "Timeline" }).click();
  const timeline = page.getByRole("region", { name: "Steps", exact: true });
  await expect(timeline).toContainText("That time had already passed, so it went on at once.");
  await expect(timeline).toContainText("Its condition holds: GET answered 200.");
  await expect(timeline).toContainText("Its condition wasn't met within 1m, so it went on.");
  await expect(timeline).toContainText("Waiting for 2m");
  await expect(timeline).toContainText(untilWhen);
});

test("a poll that stays says its last check; its deadline fails it, and a time that can't be read too, masked", async ({
  page,
}) => {
  test.slow();
  await newItem(page, "Slow change", "slow-change");
  const stays = await addStep(page, aCondition, "stays", "It stays");
  await commit(stays, "URL", "https://cmdb.corp.example/change");
  await commit(stays, "Every", "6h");
  await commit(stays, "Until", "steps.stays.status == 200");
  await publishAndOpen(page, "Slow change");
  await submit(page);
  const run = page.getByRole("application", { name: /^Run of REQ-\d+$/ });
  const node = run.getByRole("button", { name: /^Poll It stays\. Waiting\./ });
  await expect(node).toBeVisible({ timeout: 20_000 });
  await node.click();
  const selected = page.getByRole("region", { name: "Selected: It stays" });
  await expect(selected).toContainText("Checks every 6h. Last check: GET answered 200, not yet.");
  await expect(selected).toContainText(untilWhen);
  await page.getByRole("button", { name: "Cancel request" }).click();
  await page.getByRole("dialog").getByRole("button", { name: "Cancel request" }).click();
  await expect(run.getByRole("button", { name: /^Poll It stays\. Cancelled\./ })).toBeVisible();

  // Its deadline fails it; a Retry starts it again, and the deadline comes again.
  await newItem(page, "Short deadline", "short-deadline");
  const short = await addStep(page, aCondition, "short", "A short deadline");
  await fillPoll(short, "short");
  await commit(short, "Deadline", "1m");
  await publishAndOpen(page, "Short deadline");
  await submit(page);
  const failed = page.getByRole("application", { name: /^Run of REQ-\d+$/ }).getByRole("button", {
    name: /^Poll A short deadline\. Failed\./,
  });
  await expect(failed).toBeVisible({ timeout: 20_000 });
  const main = page.getByRole("main");
  await expect(main).toContainText("Its condition wasn't met within 1m.");
  await page.getByRole("button", { name: "Retry from the failed step" }).click();
  await expect(page.getByRole("button", { name: "Retry from the failed step" })).toHaveCount(0);
  await expect(failed).toBeVisible({ timeout: 20_000 });

  // A time that can't be read fails it, and says so with its literals masked.
  await newItem(page, "Bad time", "bad-time");
  const bad = await addStep(page, aTime, "bad", "A bad time");
  await commit(bad, "Or until", "'lit-w4it-secret' + inputs.name");
  await publishAndOpen(page, "Bad time");
  await submit(page);
  await expect(
    page
      .getByRole("application", { name: /^Run of REQ-\d+$/ })
      .getByRole("button", { name: /^Wait A bad time\. Failed\./ }),
  ).toBeVisible({ timeout: 20_000 });
  await expect(main).toContainText("doesn't give a time");
  await expect(main).not.toContainText("lit-w4it-secret");
});
