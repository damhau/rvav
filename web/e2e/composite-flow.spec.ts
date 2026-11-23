import { expect, type Locator, type Page, test } from "@playwright/test";
import { bothThemes } from "./axe";
import { choose, typeAndTab } from "./keyboard";

// A composite's loop, catch path, error handler and wait for a signal (E1.5,
// ADR-0057) on the mock backend: each added and filled in from the keyboard in
// the designer, drawn on the graph, checked with axe in both themes and at
// 320 px; then saved, published and requested. In the mocks no signal comes
// and nothing fails: the wait reaches its deadline and goes on, and the catch
// path and the error handler are skipped.

/** Focuses a control and presses Enter on it. */
async function press(page: Page, control: Locator) {
  await control.focus();
  await page.keyboard.press("Enter");
}

/** Fills in the open Add a step dialog from the keyboard: what it runs, its key and title; its details take the focus. */
async function fillAdd(page: Page, runs: string | RegExp, key: string, title: string) {
  const add = page.getByRole("dialog", { name: "Add a step" });
  await expect(add.getByRole("combobox", { name: "What it runs" })).toBeFocused();
  await choose(page, runs);
  await page.keyboard.press("Tab");
  await expect(add.getByRole("textbox", { name: "Key" })).toBeFocused();
  await typeAndTab(page, key, true);
  await expect(add.getByRole("textbox", { name: "Title" })).toBeFocused();
  await page.keyboard.type(title);
  await page.keyboard.press("Enter");
  const details = page.getByRole("region", { name: `Selected: ${title}` });
  await expect(details.getByRole("heading", { name: title })).toBeFocused();
  return details;
}

/** The definition of an item whose steps are `steps`, and its error handler `onError`. */
const definition = (steps: string, onError = "") => `form_schema:
  type: object
  properties: {}
ui_schema:
  layout:
    kind: page
    sections: [{key: main, title: Main, columns: 1, fields: []}]
  fields: {}
initial_values: {}
blueprint:
  kind: composite
  steps:
${steps}${onError}`;

const rest = (key: string, title: string, indent = "    ") =>
  `${indent}- key: ${key}\n${indent}  title: ${title}\n${indent}  rest: {method: POST, url: "https://cmdb.corp.example/api/${key}", body: "{}"}\n`;

/** Every node of the graph lies inside its pane. */
async function allInPane(steps: Locator) {
  await expect
    .poll(
      () =>
        steps.locator(".react-flow").evaluate((pane) => {
          const box = pane.getBoundingClientRect();
          return [...pane.querySelectorAll<HTMLElement>(".react-flow__node")]
            .filter((n) => {
              const r = n.getBoundingClientRect();
              return (
                r.top < box.top - 1 || r.bottom > box.bottom + 1 || r.left < box.left - 1 || r.right > box.right + 1
              );
            })
            .map((n) => n.dataset.id);
        }),
      { message: "nodes outside the pane" },
    )
    .toEqual([]);
}

test("the graph is fitted again when typing adds a catch path or an error handler", async ({ page }) => {
  await page.goto("/admin/catalog-items");
  await page.getByRole("button", { name: "New item" }).click();
  const dialog = page.getByRole("dialog", { name: "New item" });
  await dialog.getByRole("textbox", { name: "Name" }).fill("Fitted steps");
  await dialog.getByRole("textbox", { name: "Slug" }).fill("fitted-steps");
  await dialog.getByRole("textbox", { name: "Summary" }).fill("Steps, then their catch paths.");
  await dialog.getByRole("combobox", { name: "Type" }).click();
  await page.getByRole("option", { name: /composite/i }).click();
  await dialog.getByRole("combobox", { name: "Category" }).click();
  await page.getByRole("option", { name: "Compute" }).click();
  await dialog.getByRole("button", { name: "Create item" }).click();
  await page.getByRole("tab", { name: "Definition" }).click();
  const editor = page.getByRole("textbox", { name: /^Definition of Fitted steps/ });
  await expect(editor).toContainText("kind: composite");
  const steps = page.getByRole("region", { name: "Steps of Fitted steps" });
  const node = (name: RegExp) => steps.getByRole("button", { name });
  const write = async (yaml: string) => {
    await editor.click();
    await page.keyboard.press("Control+A");
    await page.keyboard.insertText(yaml);
  };

  await write(
    definition(
      `${rest("make", "Make things")}      catch:\n${rest("fix", "Fix things", "        ")}`,
      `  on_error:\n${rest("incident", "Open an incident")}`,
    ),
  );
  await expect(node(/^REST Open an incident\. incident\. Catches: the request\./)).toBeVisible();
  await allInPane(steps);

  await write(definition(rest("one", "Step one") + rest("two", "Step two") + rest("three", "Step three")));
  await expect(node(/^REST Step three\./)).toBeVisible();
  await allInPane(steps);

  await write(
    definition(
      `${rest("one", "Step one")}${rest("two", "Step two")}      catch:\n${rest("two_fix", "Fix two", "        ")}${rest("three", "Step three")}`,
    ),
  );
  await expect(node(/^REST Fix two\. two_fix\. Catches: Step two\./)).toBeVisible();
  await allInPane(steps);
  await expect(node(/^Request\./)).toBeInViewport();

  // Zoomed in, then fitted from the button: every node again.
  for (let i = 0; i < 4; i++) await steps.getByRole("button", { name: "Zoom in" }).click();
  await steps.getByRole("button", { name: "Fit to view" }).click();
  await allInPane(steps);
});

test("a publish refused for shared variables that don't exist says each on its step and its line", async ({ page }) => {
  await page.goto("/admin/catalog-items");
  await page.getByRole("button", { name: "New item" }).click();
  const dialog = page.getByRole("dialog", { name: "New item" });
  await dialog.getByRole("textbox", { name: "Name" }).fill("Shared reads");
  await dialog.getByRole("textbox", { name: "Slug" }).fill("shared-reads");
  await dialog.getByRole("textbox", { name: "Summary" }).fill("Reads variables nobody added.");
  await dialog.getByRole("combobox", { name: "Type" }).click();
  await page.getByRole("option", { name: /composite/i }).click();
  await dialog.getByRole("combobox", { name: "Category" }).click();
  await page.getByRole("option", { name: "Compute" }).click();
  await dialog.getByRole("button", { name: "Create item" }).click();
  await page.getByRole("tab", { name: "Definition" }).click();
  const editor = page.getByRole("textbox", { name: /^Definition of Shared reads/ });
  await expect(editor).toContainText("kind: composite");
  const steps = page.getByRole("region", { name: "Steps of Shared reads" });
  const node = (name: RegExp) => steps.getByRole("button", { name });
  const reads = (key: string, title: string, expr: string, indent = "    ") =>
    rest(key, title, indent).replace('body: "{}"', `body: "{'v': ${expr}}"`);
  await editor.click();
  await page.keyboard.press("Control+A");
  await page.keyboard.insertText(
    definition(
      `${reads("make", "Make things", "vars.nope.x")}      catch:\n${reads("fix", "Fix things", "vars.ghost.y", "        ")}`,
      `  on_error:\n${reads("incident", "Open an incident", "vars.nope.z")}`,
    ),
  );
  const missing = (v: string) =>
    `There is no variable ${v}: add it under Administration > Variables.`.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  // The designer knows the variables: it says so before the server does.
  await expect(node(/^REST Fix things\./)).toHaveAccessibleName(new RegExp(`1 problem: ${missing("vars.ghost.y")}`));

  await page.getByRole("button", { name: "Save draft" }).click();
  await expect(page.getByText(/^Draft [\d.]+ saved$/)).toBeVisible();
  await page.getByRole("tab", { name: "Versions" }).click();
  await page.getByRole("button", { name: "Publish" }).click();
  await expect(page.getByText(/^[\d.]+ not published$/)).toBeVisible();
  await expect(page.getByText("3 problems in its definition, shown on their lines and steps.")).toBeVisible();
  await expect(page.getByRole("tab", { name: "Definition" })).toHaveAttribute("aria-selected", "true");
  const problems = page.getByRole("list", { name: "Problems in the definition" });
  await expect(problems).toBeFocused();
  await expect(problems.getByRole("listitem")).toHaveText([
    new RegExp(`^Line \\d+: ${missing("vars.nope.x")}$`),
    new RegExp(`^Line \\d+: ${missing("vars.ghost.y")}$`),
    new RegExp(`^Line \\d+: ${missing("vars.nope.z")}$`),
  ]);
  // Once on each step: the server's word is the designer's.
  await expect(node(/^REST Make things\./)).toHaveAccessibleName(new RegExp(`1 problem: ${missing("vars.nope.x")}`));
  await expect(node(/^REST Fix things\./)).toHaveAccessibleName(/1 problem: /);
  await expect(node(/^REST Open an incident\./)).toHaveAccessibleName(
    new RegExp(`1 problem: ${missing("vars.nope.z")}`),
  );
});

test("a loop, a catch path, the error handler and a wait for a signal, from the keyboard, to the run", async ({
  page,
}) => {
  test.slow();
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/admin/catalog-items");
  await page.getByRole("button", { name: "New item" }).click();
  const dialog = page.getByRole("dialog", { name: "New item" });
  await dialog.getByRole("textbox", { name: "Name" }).fill("Register hosts");
  await dialog.getByRole("textbox", { name: "Slug" }).fill("register-hosts");
  await dialog.getByRole("textbox", { name: "Summary" }).fill("Waits for the board, then registers.");
  await dialog.getByRole("combobox", { name: "Type" }).click();
  await page.getByRole("option", { name: /composite/i }).click();
  await dialog.getByRole("combobox", { name: "Category" }).click();
  await page.getByRole("option", { name: "Compute" }).click();
  await dialog.getByRole("button", { name: "Create item" }).click();
  await expect(page.getByRole("heading", { name: "Register hosts" })).toBeVisible();
  await page.getByRole("tab", { name: "Definition" }).click();
  const editor = page.getByRole("textbox", { name: /^Definition of Register hosts/ });
  const steps = page.getByRole("region", { name: "Steps of Register hosts" });
  const node = (name: RegExp) => steps.getByRole("button", { name });
  const addAStep = page.getByRole("button", { name: "Add a step", exact: true });

  // A wait for a signal: its name, its deadline, and what then.
  await press(page, addAStep);
  const cab = await fillAdd(page, "A wait for a signal or an event", "cab", "The change board");
  await expect(cab.getByRole("group", { name: "Inputs" })).toHaveCount(0);
  await expect(cab.getByRole("group", { name: "Undo" })).toHaveCount(0);
  await expect(cab.getByRole("group", { name: "Loop" })).toHaveCount(0);
  await cab.getByRole("textbox", { name: "Signal name" }).focus();
  await typeAndTab(page, "cab-approved", true);
  await expect(editor).toContainText("name: cab-approved");
  await expect(cab.getByRole("textbox", { name: "Timeout" })).toBeFocused();
  await typeAndTab(page, "1m", true);
  await expect(cab.getByRole("combobox", { name: "Then" })).toBeFocused();
  await choose(page, "It goes on");
  await expect(editor).toContainText("on_timeout: continue");
  await expect(
    node(/^Signal The change board\. cab\. Waits for: the signal cab-approved\. Timeout: 1m, then it goes on/),
  ).toBeVisible();

  // A REST call after it, repeated for each host, two at a time.
  await press(page, addAStep);
  const dns = await fillAdd(page, "A REST call", "dns", "DNS records");
  await dns.getByRole("textbox", { name: "URL" }).focus();
  await typeAndTab(page, "https://ipam.corp.example/api/records/${each}");
  await press(page, dns.getByRole("button", { name: "Repeat it for each item of a list" }));
  const loop = dns.getByRole("group", { name: "Loop" });
  await expect(loop.getByRole("textbox", { name: "For each item of" })).toBeFocused();
  await typeAndTab(page, "['a.corp.example', 'b.corp.example']");
  await expect(loop.getByRole("textbox", { name: "At a time" })).toBeFocused();
  await typeAndTab(page, "2", true);
  await expect(editor).toContainText("parallel: 2");
  await expect(dns.getByRole("list", { name: "Problems with this step" })).toHaveCount(0);
  await expect(dns.getByRole("group", { name: "Undo" })).toHaveCount(0);

  // Its catch path: a message, which reads what failed.
  await press(page, dns.getByRole("button", { name: "Add a step to the catch path" }));
  await expect(page.getByRole("dialog", { name: "Add a step" })).toContainText("It runs on the catch path");
  const ticket = await fillAdd(page, "A message to people", "dns_ticket", "Open a ticket");
  await expect(ticket).toContainText("On the catch path of dns: it runs only if that step fails");
  await ticket.getByRole("checkbox", { name: "The requester" }).focus();
  await page.keyboard.press("Space");
  await ticket.getByRole("textbox", { name: "Message" }).focus();
  await typeAndTab(page, "DNS failed: ${error.message}");
  await expect(ticket.getByRole("list", { name: "Problems with this step" })).toHaveCount(0);
  await expect(node(/^Message Open a ticket\. dns_ticket\. Catches: DNS records\./)).toBeVisible();

  // The error handler, from Done's details: the graph is one Tab stop, on the
  // Request until a node is visited; End goes to its last node.
  await node(/^Request\./).focus();
  await page.keyboard.press("End");
  await expect(node(/^Done/)).toBeFocused();
  await page.keyboard.press("Enter");
  const done = page.getByRole("region", { name: "Selected: Done" });
  await press(page, done.getByRole("button", { name: "Add a step to the error handler" }));
  const incident = await fillAdd(page, "A REST call", "incident", "Open an incident");
  await incident.getByRole("textbox", { name: "URL" }).focus();
  await typeAndTab(page, "https://itsm.corp.example/api/incidents?step=${error.step}");
  await expect(incident.getByRole("list", { name: "Problems with this step" })).toHaveCount(0);
  await expect(editor).toContainText("on_error:");
  await expect(node(/^REST Open an incident\. incident\. Catches: the request\./)).toBeVisible();
  await expect(
    node(/^REST DNS records\. dns\. .*For each: \['a\.corp\.example', 'b\.corp\.example'\]\. At a time: 2/),
  ).toBeVisible();
  await expect(steps).not.toContainText("problem");

  await bothThemes(page, "the designer, a step of the error handler selected");

  // As a list: each catch step says what it catches and when it runs.
  const shownAs = steps.getByRole("group", { name: "Show as" });
  await press(page, shownAs.getByRole("button", { name: "List" }));
  await expect(shownAs.getByRole("button", { name: "List" })).toHaveAttribute("aria-pressed", "true");
  const listed = (title: string) =>
    steps.getByRole("listitem").filter({ has: page.getByRole("button", { name: title }) });
  await expect(listed("Open a ticket")).toContainText(/Catches\s*DNS records/);
  await expect(listed("Open a ticket")).toContainText("Runs if DNS records fails");
  await expect(listed("Open an incident")).toContainText(/Catches\s*the request/);
  await expect(listed("DNS records")).toContainText(/At a time\s*2/);
  await bothThemes(page, "the designer as a list, a step of the error handler selected");
  await press(page, shownAs.getByRole("button", { name: "Graph" }));

  await page.setViewportSize({ width: 320, height: 640 });
  await expect(incident).toBeVisible();
  const overflow = await page.evaluate(() => {
    const main = document.getElementById("main")!;
    return {
      page: document.documentElement.scrollWidth - window.innerWidth,
      main: main.scrollWidth - main.clientWidth,
    };
  });
  expect(overflow).toEqual({ page: 0, main: 0 });
  await page.setViewportSize({ width: 1280, height: 720 });

  // Published and requested: no signal comes, and nothing fails.
  await page.getByRole("button", { name: "Save draft" }).click();
  await expect(page.getByText(/^Draft [\d.]+ saved$/)).toBeVisible();
  await page.getByRole("tab", { name: "Sharing" }).click();
  await page.getByRole("radio", { name: /Chosen projects/ }).check();
  await page.getByRole("checkbox", { name: "Web Platform" }).click();
  await page.getByRole("button", { name: "Save projects" }).click();
  await expect(page.getByText("Projects saved")).toBeVisible();
  await page.getByRole("tab", { name: "Versions" }).click();
  await page.getByRole("button", { name: "Publish" }).click();
  await expect(page.getByText(/^[\d.]+ published$/)).toBeVisible();
  await page.getByRole("navigation", { name: "Main" }).getByRole("link", { name: "Catalog", exact: true }).click();
  await page.getByRole("link", { name: /^Request Register hosts/ }).click();
  const form = page.getByRole("region", { name: "Steps of Register hosts" });
  await expect(
    form.getByRole("button", { name: /^Message Open a ticket\. dns_ticket\. Catches: DNS records\./ }),
  ).toBeVisible();
  await expect(page.getByRole("main")).not.toContainText("ipam.corp.example");
  await page.getByRole("textbox", { name: "Name" }).fill("web-01");
  await page.getByRole("button", { name: "Submit request" }).click();
  await expect(page).toHaveURL(/\/requests\/REQ-\d+$/);

  const run = page.getByRole("application", { name: /^Run of REQ-\d+$/ });
  await expect(run.getByRole("button", { name: /^REST DNS records\. Done\./ })).toBeVisible({ timeout: 40_000 });
  await expect(run.getByRole("button", { name: /^Signal The change board\. Done\./ })).toBeVisible();
  await expect(run.getByRole("button", { name: /^Message Open a ticket\. Skipped\./ })).toBeVisible();
  await expect(run.getByRole("button", { name: /^REST Open an incident\. Skipped\./ })).toBeVisible();
  await page.getByRole("button", { name: "Timeline" }).click();
  const timeline = page.getByRole("region", { name: "Steps", exact: true });
  await expect(timeline).toContainText("No signal cab-approved came in 1m, so it went on.");
  await expect(timeline).toContainText("Nothing failed before it.");
  expect(errors).toEqual([]);
});
