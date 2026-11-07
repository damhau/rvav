import { expect, type Page, test } from "@playwright/test";

// A composite's REST call step (D2.2, ADR-0033) on the mock backend, from the
// keyboard: added in the designer, its fields and undo filled in tab order,
// the YAML following; on the graph and the list view; saved, published and
// requested. A requester's form shows its method and what it reads, masked;
// its run has a REST node. An item of only REST calls, which needs no approval,
// runs to its end, its timeline saying what the call answered.

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

test("a REST call step filled in from the keyboard, published, requested and run", async ({ page }) => {
  test.slow();
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/admin/catalog-items");
  await page.getByRole("region", { name: "Items" }).getByRole("link", { name: "PostgreSQL 16 cluster" }).click();
  await page.getByRole("button", { name: "New draft" }).click();
  await expect(page.getByText(/^Draft [\d.]+ made from the published version/)).toBeVisible();
  await page.getByRole("tab", { name: "Definition" }).click();
  const editor = page.getByRole("textbox", { name: /^Definition of PostgreSQL 16 cluster/ });
  const steps = page.getByRole("region", { name: "Steps of PostgreSQL 16 cluster" });

  // Added at the end of the steps, from the dialog.
  await steps.getByRole("button", { name: /^Request\./ }).focus();
  await page.keyboard.press("Enter");
  await page.getByRole("button", { name: "Add a step" }).click();
  const dialog = page.getByRole("dialog", { name: "Add a step" });
  await expect(dialog.getByRole("combobox", { name: "What it runs" })).toBeFocused();
  await choose(page, "A REST call");
  await dialog.getByRole("textbox", { name: "Key" }).fill("cmdb");
  await dialog.getByRole("textbox", { name: "Title" }).fill("Register in the CMDB");
  await dialog.getByRole("button", { name: "Add step" }).click();

  const details = page.getByRole("region", { name: "Selected: Register in the CMDB" });
  await expect(details.getByRole("heading", { name: /Register in the CMDB/ })).toBeFocused();
  await expect(details.getByRole("list", { name: "Problems with this step" })).toContainText("Give the URL to call.");
  await expect(details.getByRole("group", { name: "Inputs" })).toHaveCount(0);

  // Its fields, in the order they are tabbed through.
  const call = details.getByRole("group", { name: "REST call" });
  await call.getByRole("combobox", { name: "Method" }).focus();
  await choose(page, "POST");
  await expect(editor).toContainText("method: POST");
  await call.getByRole("combobox", { name: "Method" }).focus();
  await page.keyboard.press("Tab");
  await expect(call.getByRole("textbox", { name: "URL" })).toBeFocused();
  await typeAndTab(page, "https://cmdb.corp.example/api/ci/${inputs.role}?env=${environment}");
  await expect(call.getByRole("textbox", { name: "Body" })).toBeFocused();
  await typeAndTab(page, "{'name': inputs.role, 'token': 'abc-literal'}");
  await expect(call.getByRole("combobox", { name: "Secret" })).toBeFocused();
  await choose(page, /^webhook-signing/);
  await call.getByRole("combobox", { name: "Secret" }).focus();
  await page.keyboard.press("Tab");
  await expect(call.getByRole("textbox", { name: "Accepted statuses" })).toBeFocused();
  await typeAndTab(page, "200, 201");
  await expect(call.getByRole("textbox", { name: "Keep" })).toBeFocused();
  await typeAndTab(page, "$.result");
  await expect(editor).toContainText("accept: [200, 201]");
  await expect(editor).toContainText("keep: $.result");
  await expect(details.getByRole("list", { name: "Problems with this step" })).toHaveCount(0);

  // Its undo is another call.
  await details.getByRole("combobox", { name: "Undone by" }).focus();
  await choose(page, "A REST call");
  await expect(editor).toContainText("method: DELETE");
  await details.getByRole("textbox", { name: "Undo's URL" }).focus();
  await typeAndTab(page, "https://cmdb.corp.example/api/ci/${steps.cmdb.json.id}");
  await expect(details.getByRole("list", { name: "Problems with this step" })).toHaveCount(0);

  // Undone and redone in one step each, with the YAML.
  await page.getByRole("button", { name: "Undo", exact: true }).click();
  await expect(editor).not.toContainText("json.id");
  await page.getByRole("button", { name: "Redo", exact: true }).click();
  await expect(editor).toContainText("json.id");

  // On the graph and the list view, what it calls and what undoes it.
  const drawn = /^REST Register in the CMDB\. cmdb\. Calls: POST https:\/\/cmdb\.corp\.example\/api\/ci\//;
  await expect(steps.getByRole("button", { name: drawn })).toBeVisible();
  await steps.getByRole("group", { name: "Show as" }).getByRole("button", { name: "List" }).click();
  await expect(steps.getByRole("button", { name: /Register in the CMDB/ })).toBeVisible();
  await expect(steps).toContainText("POST https://cmdb.corp.example/api/ci/${inputs.role}?env=${environment}");
  await expect(steps).toContainText("DELETE https://cmdb.corp.example/api/ci/${steps.cmdb.json.id}");
  await steps.getByRole("group", { name: "Show as" }).getByRole("button", { name: "Graph" }).click();

  await page.getByRole("button", { name: "Save draft" }).click();
  await expect(page.getByText(/^Draft [\d.]+ saved$/)).toBeVisible();
  await page.getByRole("tab", { name: "Versions" }).click();
  await page.getByRole("button", { name: "Publish" }).click();
  await expect(page.getByText(/^[\d.]+ published$/)).toBeVisible();

  // A requester sees its method and what it reads: no host, path, literal or secret.
  await page.getByRole("link", { name: "Catalog", exact: true }).click();
  await page.getByRole("link", { name: /^PostgreSQL 16 cluster/ }).click();
  const form = page.getByRole("region", { name: "Steps of PostgreSQL 16 cluster" });
  const masked = form.getByRole("button", { name: /^REST Register in the CMDB\./ });
  await expect(masked).toBeVisible();
  await expect(masked).toHaveAccessibleName(/Calls: POST …\$\{inputs\.role\}…\$\{environment\}/);
  await expect(masked).toHaveAccessibleName(/Undo: DELETE …\$\{steps\.cmdb\.json\.id\}/);
  await expect(page.getByRole("main")).not.toContainText("cmdb.corp.example");
  await expect(page.getByRole("main")).not.toContainText("webhook-signing");

  await page.getByRole("textbox", { name: "Database name" }).fill("ledger");
  await page.getByRole("button", { name: "Submit for approval" }).click();
  await expect(page).toHaveURL(/\/requests\/REQ-\d+$/);

  // Its run has its node, masked as the form's; its timeline says who it is.
  const run = page.getByRole("application", { name: /^Run of REQ-\d+$/ });
  await expect(run.getByRole("button", { name: /^REST Register in the CMDB\. Not started\./ })).toBeVisible();
  await page.getByRole("button", { name: "Timeline" }).click();
  const timeline = page.getByRole("region", { name: "Steps", exact: true });
  await expect(timeline).toContainText(/Register in the CMDB[\s\S]*REST call · POST/);
  await expect(page.getByRole("main")).not.toContainText("cmdb.corp.example");
  expect(errors).toEqual([]);
});

test("a step made a REST call in place, and what the designer refuses in it", async ({ page }) => {
  await page.goto("/admin/catalog-items");
  await page.getByRole("region", { name: "Items" }).getByRole("link", { name: "PostgreSQL 16 cluster" }).click();
  await page.getByRole("button", { name: "New draft" }).click();
  await page.getByRole("tab", { name: "Definition" }).click();
  const editor = page.getByRole("textbox", { name: /^Definition of PostgreSQL 16 cluster/ });
  const steps = page.getByRole("region", { name: "Steps of PostgreSQL 16 cluster" });
  await steps.getByRole("button", { name: /^AWX Backups\./ }).click();
  const details = page.getByRole("region", { name: "Selected: Backups" });
  await details.getByRole("combobox", { name: "Runs" }).click();
  await page.getByRole("option", { name: "A REST call" }).click();
  await expect(editor).toContainText("method: GET");
  await expect(steps.getByRole("button", { name: /^REST Backups\./ })).toBeVisible();

  // An expression can't choose the host: said on the step.
  const problems = details.getByRole("list", { name: "Problems with this step" });
  await details.getByRole("textbox", { name: "URL" }).fill("https://${inputs.role}.corp.example/x");
  await page.keyboard.press("Enter");
  await expect(problems).toContainText("Expressions may fill the URL's path and query");
  await details.getByRole("textbox", { name: "URL" }).fill("https://cmdb.corp.example/x");
  await page.keyboard.press("Enter");
  await expect(problems).toHaveCount(0);

  // A long URL is whole on the list view, wrapped inside its card.
  const long = `https://cmdb.corp.example/api/now/table/cmdb_ci_server/${"x".repeat(120)}?sysparm_limit=10`;
  await details.getByRole("textbox", { name: "URL" }).fill(long);
  await page.keyboard.press("Enter");
  await steps.getByRole("group", { name: "Show as" }).getByRole("button", { name: "List" }).click();
  const line = steps.getByRole("definition").filter({ hasText: long });
  await expect(line).toBeVisible();
  const inside = (el: Element) => el.getBoundingClientRect().right <= el.closest("li")!.getBoundingClientRect().right;
  expect(await line.evaluate(inside)).toBe(true);
  await steps.getByRole("group", { name: "Show as" }).getByRole("button", { name: "Graph" }).click();

  // Back to a template: the call goes.
  await details.getByRole("combobox", { name: "Runs" }).click();
  await page.getByRole("option", { name: "Monitoring agent" }).click();
  await expect(editor).not.toContainText("cmdb.corp.example");
  await expect(details.getByRole("group", { name: "REST call" })).toHaveCount(0);
});

test("an item of only REST calls, requested and run to its end", async ({ page }) => {
  test.slow();
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/admin/catalog-items");
  await page.getByRole("button", { name: "New item" }).click();
  const dialog = page.getByRole("dialog", { name: "New item" });
  await dialog.getByRole("textbox", { name: "Name" }).fill("CMDB entry");
  await dialog.getByRole("textbox", { name: "Slug" }).fill("cmdb-entry");
  await dialog.getByRole("textbox", { name: "Summary" }).fill("A configuration item in the CMDB.");
  await dialog.getByRole("combobox", { name: "Type" }).click();
  await page.getByRole("option", { name: /composite/i }).click();
  await dialog.getByRole("button", { name: "Create" }).click();
  await expect(page.getByRole("heading", { name: "CMDB entry" })).toBeVisible();
  await page.getByRole("tab", { name: "Definition" }).click();

  await page.getByRole("button", { name: "Add a step" }).click();
  const add = page.getByRole("dialog", { name: "Add a step" });
  await add.getByRole("combobox", { name: "What it runs" }).click();
  await page.getByRole("option", { name: "A REST call" }).click();
  await add.getByRole("textbox", { name: "Key" }).fill("cmdb");
  await add.getByRole("textbox", { name: "Title" }).fill("Register in the CMDB");
  await add.getByRole("button", { name: "Add step" }).click();
  const details = page.getByRole("region", { name: "Selected: Register in the CMDB" });
  const call = details.getByRole("group", { name: "REST call" });
  await call.getByRole("combobox", { name: "Method" }).click();
  await page.getByRole("option", { name: "POST" }).click();
  await call.getByRole("textbox", { name: "URL" }).fill("https://cmdb.corp.example/api/ci/${inputs.name}");
  await call.getByRole("textbox", { name: "URL" }).press("Tab");
  await expect(details.getByRole("list", { name: "Problems with this step" })).toHaveCount(0);

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
  await page.getByRole("link", { name: /^CMDB entry/ }).click();
  await page.getByRole("textbox", { name: "Name" }).fill("web-01");
  await page.getByRole("button", { name: "Submit request" }).click();
  await expect(page).toHaveURL(/\/requests\/REQ-\d+$/);

  const run = page.getByRole("application", { name: /^Run of REQ-\d+$/ });
  await expect(run.getByRole("button", { name: /^REST Register in the CMDB\. Done\./ })).toBeVisible({
    timeout: 45_000,
  });
  // Selected, its step says what the call answered; its address stays hidden.
  await run.getByRole("button", { name: /^REST Register in the CMDB\./ }).click();
  await expect(page.getByRole("region", { name: "Selected: Register in the CMDB" })).toContainText("POST answered 201");
  await expect(run.getByRole("button", { name: /^REST Register in the CMDB\./ })).toHaveAccessibleName(
    /Calls: POST …\$\{inputs\.name\}/,
  );
  await page.getByRole("button", { name: "Timeline" }).click();
  await expect(page.getByRole("region", { name: "Steps", exact: true })).toContainText("POST answered 201");
  await expect(page.getByRole("main")).not.toContainText("cmdb.corp.example");
  expect(errors).toEqual([]);
});
