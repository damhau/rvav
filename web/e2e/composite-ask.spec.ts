import { expect, type Page, test } from "@playwright/test";

// A composite's wait for a person (D2.3, ADR-0033) on the mock backend, from
// the keyboard: added in the designer, its fields filled in tab order, the
// YAML following; on the graph and the list view; saved, published, requested.
// A requester's form says that it waits for a person, not what it asks of whom.
// Those it names answer on the request's page, and the run goes on; a reject
// fails it, a Retry asks again, and a cancel takes the question away.

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
  await dialog.getByRole("textbox", { name: "Summary" }).fill("Waits for a person.");
  await dialog.getByRole("combobox", { name: "Type" }).click();
  await page.getByRole("option", { name: /composite/i }).click();
  await dialog.getByRole("button", { name: "Create" }).click();
  await expect(page.getByRole("heading", { name })).toBeVisible();
  await page.getByRole("tab", { name: "Definition" }).click();
}

/** Adds a step that waits for a person; its details take the focus. */
async function addAsk(page: Page, key: string, title: string) {
  await page.getByRole("button", { name: "Add a step" }).click();
  const add = page.getByRole("dialog", { name: "Add a step" });
  await expect(add.getByRole("combobox", { name: "What it runs" })).toBeFocused();
  await choose(page, "A person");
  await add.getByRole("textbox", { name: "Key" }).fill(key);
  await add.getByRole("textbox", { name: "Title" }).fill(title);
  await add.getByRole("button", { name: "Add step" }).click();
  const details = page.getByRole("region", { name: `Selected: ${title}` });
  await expect(details.getByRole("heading", { name: new RegExp(title) })).toBeFocused();
  return details;
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

test("a wait with a form, filled in from the keyboard, published, requested and answered", async ({ page }) => {
  test.slow();
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await newItem(page, "Decommission check", "decommission-check");
  const editor = page.getByRole("textbox", { name: /^Definition of Decommission check/ });
  const steps = page.getByRole("region", { name: "Steps of Decommission check" });
  const details = await addAsk(page, "owner_ok", "The owner's decision");

  // Until it says who is asked, the step says so; it has no inputs or undo.
  const problems = details.getByRole("list", { name: "Problems with this step" });
  await expect(problems).toContainText("Say who is asked");
  await expect(details.getByRole("group", { name: "Inputs" })).toHaveCount(0);
  await expect(details.getByRole("group", { name: "Undo" })).toHaveCount(0);

  // Its fields, in the order they are tabbed through.
  await details.getByRole("checkbox", { name: "The requester" }).focus();
  await page.keyboard.press("Space");
  await expect(editor).toContainText("requester: true");
  await expect(problems).toHaveCount(0);
  await details.getByRole("textbox", { name: "Message" }).focus();
  await typeAndTab(page, "Check it is unused first.");
  await expect(editor).toContainText("message: Check it is unused first.");
  await expect(details.getByRole("button", { name: "Add a question" })).toBeFocused();
  await page.keyboard.press("Enter");
  const q1 = details.getByRole("group", { name: "Question 1" });
  await expect(q1.getByRole("textbox", { name: "Key" })).toBeFocused();
  await page.keyboard.press("Control+a");
  await typeAndTab(page, "decision");
  await expect(q1.getByRole("textbox", { name: "Question" })).toBeFocused();
  await typeAndTab(page, "Keep or delete?");
  await expect(q1.getByRole("combobox", { name: "Type" })).toBeFocused();
  await choose(page, "A choice");
  await expect(editor).toContainText("type: choice");
  await expect(problems).toContainText("List what it may be.");
  await q1.getByRole("combobox", { name: "Type" }).focus();
  await page.keyboard.press("Tab");
  await expect(q1.getByRole("textbox", { name: "Options" })).toBeFocused();
  await typeAndTab(page, "keep, delete");
  await expect(editor).toContainText("options: [keep, delete]");
  await expect(problems).toHaveCount(0);
  await expect(q1.getByRole("textbox", { name: "Default" })).toBeFocused();
  await page.keyboard.press("Tab");
  await expect(q1.getByRole("checkbox", { name: "An answer is required" })).toBeFocused();
  await page.keyboard.press("Space");
  await expect(editor).toContainText("required: true");

  const timeout = details.getByRole("textbox", { name: "Timeout" });
  await timeout.focus();
  await typeAndTab(page, "soon");
  await expect(problems).toContainText("A duration such as 48h.");
  await timeout.focus();
  await page.keyboard.press("Control+a");
  await typeAndTab(page, "48h");
  await expect(problems).toHaveCount(0);
  await expect(details.getByRole("combobox", { name: "Then" })).toBeFocused();
  await choose(page, "It goes on with no answer");
  await expect(editor).toContainText("on_timeout: continue");

  // Undone and redone in one step each, with the YAML.
  await page.getByRole("button", { name: "Undo", exact: true }).click();
  await expect(editor).not.toContainText("on_timeout");
  await page.getByRole("button", { name: "Redo", exact: true }).click();
  await expect(editor).toContainText("on_timeout: continue");

  // On the graph and the list view: who it asks, what for and how long.
  const drawn =
    /^Person The owner's decision\. owner_ok\. Asks: the requester\. Wants: 1 answer\. Timeout: 48h, then it goes on/;
  await expect(steps.getByRole("button", { name: drawn })).toBeVisible();
  await steps.getByRole("group", { name: "Show as" }).getByRole("button", { name: "List" }).click();
  await expect(steps.getByRole("button", { name: /The owner's decision/ })).toBeVisible();
  await expect(steps).toContainText("48h, then it goes on");
  await steps.getByRole("group", { name: "Show as" }).getByRole("button", { name: "Graph" }).click();

  // A later step is told what it can read of the answer.
  await page.getByRole("button", { name: "Add a step" }).click();
  const add = page.getByRole("dialog", { name: "Add a step" });
  await choose(page, "A REST call");
  await add.getByRole("textbox", { name: "Key" }).fill("cmdb");
  await add.getByRole("button", { name: "Add step" }).click();
  const call = page.getByRole("region", { name: "Selected: cmdb" }).getByRole("group", { name: "REST call" });
  await expect(call).toContainText("steps.owner_ok.answer.decision");
  await call.getByRole("textbox", { name: "URL" }).fill("https://cmdb.corp.example/${steps.owner_ok.answer.decision}");
  await call.getByRole("textbox", { name: "URL" }).press("Tab");

  await publishAndOpen(page, "Decommission check");

  // A requester's form says that it waits, for whom by role only: not what it says or offers.
  const form = page.getByRole("region", { name: "Steps of Decommission check" });
  await expect(form.getByRole("button", { name: drawn })).toBeVisible();
  await expect(page.getByRole("main")).not.toContainText("Check it is unused first");
  await expect(page.getByRole("main")).not.toContainText("Keep or delete?");
  await expect(form).not.toContainText("1 problem");
  await submit(page);

  // Asked on the request's page, from the run's node to the form.
  const run = page.getByRole("application", { name: /^Run of REQ-\d+$/ });
  await expect(run.getByRole("button", { name: /^Person The owner's decision\. Running\./ })).toBeVisible({
    timeout: 20_000,
  });
  const answer = page.getByRole("form", { name: "Your answer: The owner's decision" });
  await expect(answer).toContainText("Check it is unused first.");
  await expect(page.getByRole("button", { name: "Reject" })).toHaveCount(0);

  // What it requires is said on the question, which takes the focus.
  await answer.getByRole("textbox", { name: "Comment" }).focus();
  await page.keyboard.type("Nobody uses it.");
  await page.keyboard.press("Tab");
  await expect(answer.getByRole("button", { name: "Send answer" })).toBeFocused();
  await page.keyboard.press("Enter");
  const decision = answer.getByRole("combobox", { name: "Keep or delete?" });
  await expect(answer).toContainText("Answer this.");
  await expect(decision).toBeFocused();
  await choose(page, "delete");
  await decision.focus();
  await answer.getByRole("button", { name: "Send answer" }).focus();
  await page.keyboard.press("Enter");

  // Answered: it says who, and what; the question is gone and the run goes on to its end.
  await expect(answer).toHaveCount(0);
  const asked = page.getByRole("list", { name: "Steps that wait for a person" });
  await expect(asked).toContainText("Answered by Léa Martin");
  await expect(asked).toContainText("Keep or delete?");
  await expect(asked).toContainText("delete");
  await expect(asked).toContainText("“Nobody uses it.”");
  await expect(run.getByRole("button", { name: /^Person The owner's decision\. Done\./ })).toBeVisible();
  await expect(run.getByRole("button", { name: /^REST cmdb\. Done\./ })).toBeVisible({ timeout: 30_000 });
  await page.getByRole("button", { name: "Timeline" }).click();
  await expect(page.getByRole("region", { name: "Steps", exact: true })).toContainText("Answered by Léa Martin");
  expect(errors).toEqual([]);
});

test("approve or reject: a reject fails it, a Retry asks again, a cancel takes the question away", async ({ page }) => {
  test.slow();
  await newItem(page, "Large disk", "large-disk");
  const details = await addAsk(page, "manager", "A manager's OK");
  await details.getByRole("checkbox", { name: "The owner" }).click();
  await expect(details.getByText("No questions: they approve or reject")).toBeVisible();
  await publishAndOpen(page, "Large disk");
  await submit(page);

  const answer = page.getByRole("form", { name: "Your answer: A manager's OK" });
  await expect(answer).toBeVisible({ timeout: 20_000 });

  // A reject says why, and fails the step.
  await answer.getByRole("button", { name: "Reject" }).click();
  await expect(answer).toContainText("Say why you reject it");
  await expect(answer.getByRole("textbox", { name: "Comment" })).toBeFocused();
  await page.keyboard.type("Too big.");
  await answer.getByRole("button", { name: "Reject" }).click();
  const run = page.getByRole("application", { name: /^Run of REQ-\d+$/ });
  await expect(run.getByRole("button", { name: /^Person A manager's OK\. Failed\./ })).toBeVisible();
  const asked = page.getByRole("list", { name: "Steps that wait for a person" });
  await expect(asked).toContainText("Rejected by Léa Martin");
  await expect(asked).toContainText("“Too big.”");
  await expect(answer).toHaveCount(0);

  // A Retry asks again, the first answer staying as history.
  await page.getByRole("button", { name: "Retry from the failed step" }).click();
  await expect(answer).toBeVisible({ timeout: 20_000 });
  await expect(asked).toContainText("Rejected by Léa Martin");

  // The inbox has it too, and sends it to the request's page.
  await page
    .getByRole("navigation")
    .getByRole("link", { name: /^Approvals/ })
    .click();
  const row = page.getByRole("button", { name: /A step asks: A manager's OK/ });
  await row.click();
  await page.getByRole("link", { name: "Answer on the request's page" }).click();
  await expect(answer).toBeVisible();

  // A cancel stops the wait and takes the question away.
  await page.getByRole("button", { name: "Cancel request" }).click();
  await page.getByRole("dialog").getByRole("button", { name: "Cancel request" }).click();
  await expect(answer).toHaveCount(0);
  await expect(asked.getByText("Cancelled")).toBeVisible();
  await expect(run.getByRole("button", { name: /^Person A manager's OK\. Cancelled\./ })).toBeVisible();
});

test("a wait for someone else: its names stay off the form, and only they get the question", async ({ page }) => {
  test.slow();
  await newItem(page, "Someone's OK", "someones-ok");
  const steps = page.getByRole("region", { name: "Steps of Someone's OK" });
  const details = await addAsk(page, "marc", "Marc's OK");
  await details.getByRole("textbox", { name: "Approvers" }).fill("usr_mkeller");
  await details.getByRole("textbox", { name: "Message" }).fill("Is this for your team?");
  await details.getByRole("textbox", { name: "Message" }).press("Tab");
  await expect(steps.getByRole("button", { name: /^Person Marc's OK\. marc\. Asks: 1 person\./ })).toBeVisible();

  // What happens on time-out: whom it escalates to is asked once it does.
  await details.getByRole("textbox", { name: "Timeout" }).fill("2h");
  await details.getByRole("textbox", { name: "Timeout" }).press("Tab");
  await expect(details.getByRole("combobox", { name: "Then" })).toBeFocused();
  await choose(page, "It escalates");
  const problems = details.getByRole("list", { name: "Problems with this step" });
  await expect(problems).toContainText("Say whom to escalate to");
  await details.getByRole("textbox", { name: "Escalate to OIDC group" }).fill("chiefs");
  await details.getByRole("textbox", { name: "Escalate to OIDC group" }).press("Tab");
  await expect(problems).toHaveCount(0);
  await expect(page.getByRole("textbox", { name: /^Definition of Someone's OK/ })).toContainText("group: chiefs");

  await publishAndOpen(page, "Someone's OK");
  const form = page.getByRole("region", { name: "Steps of Someone's OK" });
  await expect(form.getByRole("button", { name: /^Person Marc's OK\. marc\. Asks: a person\./ })).toBeVisible();
  await expect(page.getByRole("main")).not.toContainText("usr_mkeller");
  await expect(page.getByRole("main")).not.toContainText("chiefs");
  await expect(page.getByRole("main")).not.toContainText("Is this for your team?");
  await submit(page);

  // The requester sees whom it waits for, as for an approval: not what it asks, and no form.
  const asked = page.getByRole("list", { name: "Steps that wait for a person" });
  await expect(asked).toContainText("Waiting for Marc Keller", { timeout: 20_000 });
  await expect(page.getByRole("main")).not.toContainText("Is this for your team?");
  await expect(page.getByRole("form", { name: /^Your answer/ })).toHaveCount(0);
});
