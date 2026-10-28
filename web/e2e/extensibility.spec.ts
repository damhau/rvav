import { expect, test, type Page } from "@playwright/test";

// Subscriptions, custom workflows and composite items against the mock
// backend (S10.8, ADR-0021 and ADR-0022).

async function pick(page: Page, scope: ReturnType<Page["getByRole"]>, label: string, option: string) {
  await scope.getByRole("combobox", { name: label }).click();
  await page.getByRole("option", { name: option, exact: true }).click();
}

test("a webhook subscription: checked, saved, tested; a failed run tried again", async ({ page }) => {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/");
  await page.getByRole("link", { name: "Subscriptions" }).click();
  await expect(page.getByRole("heading", { name: "Subscriptions", exact: true })).toBeVisible();
  await expect(page.getByRole("row", { name: /CMDB pre-check/ }).getByText("Blocking")).toBeVisible();

  await page.getByRole("button", { name: "New subscription" }).click();
  const dialog = page.getByRole("dialog", { name: "New subscription" });
  await dialog.getByRole("textbox", { name: "Name" }).fill("Inventory feed");
  await pick(page, dialog, "Topic", "provisioning.post");
  await expect(dialog.getByRole("checkbox", { name: /Blocking/ })).toBeDisabled();
  await dialog.getByRole("textbox", { name: "URL" }).fill("https://example.org/hook");
  await pick(page, dialog, "Signing secret", "webhook-signing");
  await dialog.getByRole("button", { name: "Save subscription" }).click();
  await expect(dialog.getByText("example.org is not an allowed webhook host")).toBeVisible();
  await dialog.getByRole("textbox", { name: "URL" }).fill("https://hooks.corp.example/rvav");
  await dialog.getByRole("button", { name: "Save subscription" }).click();
  await expect(page.getByText("Inventory feed saved")).toBeVisible();

  // Tested with the topic's sample event.
  await page.getByRole("button", { name: "Test Inventory feed" }).click();
  const tested = page.getByRole("dialog", { name: "Test delivery" });
  await expect(tested.getByText("Delivered")).toBeVisible();
  await expect(tested.getByText(/HTTP 200/)).toBeVisible();
  await page.keyboard.press("Escape");

  // A run that failed: its attempts, what was sent, tried again.
  await page.getByRole("link", { name: "ServiceNow change record" }).click();
  await expect(page.getByRole("heading", { name: "ServiceNow change record" })).toBeVisible();
  const failed = page.getByRole("row").filter({ hasText: "HTTP 503" });
  await failed.getByRole("button", { name: /Open the run/ }).click();
  const run = page.getByRole("dialog", { name: "Run" });
  await expect(run.getByText("Service Unavailable").first()).toBeVisible();
  await run.getByText("What was sent").click();
  await expect(run.getByText('"contract": "rvav.event.v1"')).toBeVisible();
  await run.getByRole("button", { name: "Try again" }).click();
  await expect(page.getByText("It will be delivered again")).toBeVisible();
  await expect(run.getByText("Delivered", { exact: true })).toBeVisible();
  expect(errors).toEqual([]);
});

test("register a custom workflow; one still running can't be removed", async ({ page }) => {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.on("dialog", (d) => void d.accept());
  await page.goto("/");
  await page.getByRole("link", { name: "Custom workflows" }).click();
  await expect(page.getByRole("heading", { name: "Custom workflows" })).toBeVisible();
  await expect(page.getByRole("row", { name: /Decommission an application/ })).toContainText("wf.decommission");

  await page.getByRole("button", { name: "Register a workflow" }).click();
  const dialog = page.getByRole("dialog", { name: "Register a workflow" });
  await dialog.getByRole("textbox", { name: "Key" }).fill("cmdb-sync");
  await dialog.getByRole("textbox", { name: "Title" }).fill("CMDB sync");
  await dialog.getByRole("textbox", { name: "Workflow type" }).fill("cmdbsync");
  await dialog.getByRole("textbox", { name: "Task queue" }).fill("platform-workflows");
  await dialog.getByRole("checkbox", { name: "Catalog item" }).click();
  await dialog.getByRole("checkbox", { name: "Subscription target" }).click();
  await dialog.getByRole("button", { name: "Save workflow" }).click();
  await expect(dialog.getByText("The type ends with its major version")).toBeVisible();
  await dialog.getByRole("textbox", { name: "Workflow type" }).fill("cmdb.sync.v1");
  await dialog.getByRole("button", { name: "Save workflow" }).click();
  await expect(page.getByText("CMDB sync saved")).toBeVisible();
  await expect(page.getByRole("row", { name: /CMDB sync/ })).toContainText("Subscription target");

  await page.getByRole("button", { name: "Remove Decommission an application" }).click();
  await expect(page.getByText("Runs of it haven't ended")).toBeVisible();
  await expect(page.getByRole("row", { name: /Decommission an application/ })).toBeVisible();
  expect(errors).toEqual([]);
});

const brokenSteps = `form_schema:
  type: object
  required: [name]
  properties:
    name: {type: string, minLength: 1}
ui_schema:
  layout:
    kind: page
    sections: [{key: main, title: Main, columns: 1, fields: [name]}]
  fields:
    name: {widget: text, label: Name}
initial_values: {}
blueprint:
  kind: composite
  rollback_on_failure: true
  steps:
    - key: db_user
      item: db-user
      with: {host: steps.vm.fqdn}
    - key: vm
      item: rhel9-server
`;

const goodSteps = brokenSteps.replace(
  `  steps:
    - key: db_user
      item: db-user
      with: {host: steps.vm.fqdn}
    - key: vm
      item: rhel9-server
`,
  `  steps:
    - key: vm
      item: rhel9-server
    - parallel:
        - key: db_user
          item: db-user
          with: {host: steps.vm.fqdn}
          undo: {job_template: pg-drop-role}
        - key: monitoring
          awx: {job_template: monitoring-agent}
          with: {target: steps.vm.fqdn}
`,
);

test("a composite item's steps, checked while edited and when saved", async ({ page }) => {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/admin/catalog-items");
  await page.getByRole("button", { name: "New item" }).click();
  const dialog = page.getByRole("dialog", { name: "New item" });
  await dialog.getByRole("textbox", { name: "Name" }).fill("VM with a database user");
  await dialog.getByRole("textbox", { name: "Slug" }).fill("vm-db-user");
  await dialog.getByRole("textbox", { name: "Summary" }).fill("A VM, its database user and monitoring.");
  await pick(page, dialog, "Type", "Composite");
  await dialog.getByRole("button", { name: "Create" }).click();

  await page.getByRole("tab", { name: "Definition" }).click();
  const editor = page.getByRole("textbox", { name: "Definition of VM with a database user 1.0" });
  await expect(editor).toContainText("kind: composite");
  const steps = page.getByRole("region", { name: "Composite steps" });
  await expect(steps).toContainText("No step yet");

  // A step that uses a later one: said while typing, and refused when saved.
  await editor.click();
  await page.keyboard.press("Control+A");
  await page.keyboard.insertText(brokenSteps);
  await expect(steps.getByRole("list", { name: "Problems in the steps" })).toContainText(
    "steps.vm is not an earlier step.",
  );
  await page.getByRole("button", { name: "Save draft" }).click();
  await expect(page.getByRole("list", { name: "Problems in the definition" })).toContainText(
    "steps.vm is not an earlier step.",
  );

  // The VM first, then the database user and monitoring at the same time.
  await editor.click();
  await page.keyboard.press("Control+A");
  await page.keyboard.insertText(goodSteps);
  await expect(steps.getByText("At the same time")).toBeVisible();
  await expect(steps).toContainText("Undone by pg-drop-role");
  await expect(steps.getByRole("list", { name: "Problems in the steps" })).toHaveCount(0);
  await page.getByRole("button", { name: "Save draft" }).click();
  await expect(page.getByText("Draft 1.0 saved")).toBeVisible();
  expect(errors).toEqual([]);
});
