import { expect, test } from "@playwright/test";
import { bothThemes, scan, theme } from "./axe";
import { choose, tabTo } from "./keyboard";

// Shared variables (E1.5, ADR-0057) on the mock backend, from the keyboard
// only: a catalog admin adds a list, changes it and removes it; a name that
// says it holds a password is refused next to the field. Scanned in either
// theme, the sheet too; at 320 px the page reflows without scrolling
// sideways.

test("a catalog admin keeps a shared variable from the keyboard", async ({ page }) => {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/admin/variables");
  await expect(page.getByRole("heading", { name: "Variables", exact: true })).toBeVisible();
  await expect(page.getByRole("row", { name: /vars\.network\.dns_servers/ })).toContainText("10.20.0.53, 10.20.0.54");
  await bothThemes(page, "Variables");

  await tabTo(page, page.getByRole("button", { name: "New variable", exact: true }));
  await page.keyboard.press("Enter");
  const sheet = page.getByRole("dialog", { name: "New variable", exact: true });
  await expect(sheet).toBeVisible();
  await tabTo(page, page.locator("#variable-group"));
  await page.keyboard.type("monitoring");
  await page.keyboard.press("Tab");
  await page.keyboard.type("targets");
  await expect(sheet).toContainText("Steps and conditions read it as vars.monitoring.targets.");
  await page.keyboard.press("Tab");
  await expect(sheet.getByRole("combobox", { name: "Type", exact: true })).toBeFocused();
  await choose(page, "List or object (JSON)");
  await expect(sheet.getByRole("combobox", { name: "Type", exact: true })).toHaveText("List or object (JSON)");
  await tabTo(page, page.locator("#variable-value"));
  await page.keyboard.type('["web", "db"]');
  await page.keyboard.press("Tab");
  await page.keyboard.type("The hosts the agent watches.");
  await page.keyboard.press("Enter");
  await expect(page.getByText("vars.monitoring.targets added")).toBeVisible();
  const row = page.getByRole("row", { name: /vars\.monitoring\.targets/ });
  await expect(row).toContainText("web, db");
  await expect(row).toContainText("List");

  await tabTo(page, page.getByRole("button", { name: "Actions for vars.monitoring.targets", exact: true }));
  await page.keyboard.press("Enter");
  await expect(page.getByRole("menuitem", { name: "Change the value", exact: true })).toBeFocused();
  await page.keyboard.press("Enter");
  const change = page.getByRole("dialog", { name: "Change vars.monitoring.targets", exact: true });
  await expect(change).toBeVisible();
  await expect(change.getByRole("textbox", { name: "Group", exact: true })).toBeDisabled();
  await tabTo(page, page.locator("#variable-value"));
  await page.keyboard.press("ControlOrMeta+A");
  await page.keyboard.type('["web"');
  await page.keyboard.press("Tab");
  await page.keyboard.press("Enter");
  await expect(change.getByText(/^Not JSON/)).toBeVisible();
  await scan(page, "A variable's value that isn't JSON");
  await tabTo(page, page.locator("#variable-value"));
  await page.keyboard.press("ControlOrMeta+End");
  await page.keyboard.type("]");
  await page.keyboard.press("Tab");
  await page.keyboard.press("Enter");
  await expect(page.getByText("vars.monitoring.targets saved")).toBeVisible();
  await expect(row.getByRole("cell").nth(1)).toHaveText("webList");

  await tabTo(page, page.getByRole("button", { name: "Actions for vars.monitoring.targets", exact: true }));
  await page.keyboard.press("Enter");
  await page.keyboard.press("End");
  await expect(page.getByRole("menuitem", { name: "Remove", exact: true })).toBeFocused();
  await page.keyboard.press("Enter");
  const confirm = page.getByRole("alertdialog", { name: "Remove vars.monitoring.targets?", exact: true });
  await expect(confirm).toContainText("No item reads it today.");
  await tabTo(page, confirm.getByRole("button", { name: "Remove variable", exact: true }));
  await page.keyboard.press("Enter");
  await expect(page.getByText("vars.monitoring.targets removed")).toBeVisible();
  await expect(row).toHaveCount(0);

  // A password belongs in a secret: the name says why next to it, in either theme.
  for (const name of ["Light", "Dark"] as const) {
    await theme(page, name);
    await tabTo(page, page.getByRole("button", { name: "New variable", exact: true }));
    await page.keyboard.press("Enter");
    await tabTo(page, page.locator("#variable-group"));
    await page.keyboard.type("cmdb");
    await page.keyboard.press("Tab");
    await page.keyboard.type("admin_password");
    await tabTo(page, page.locator("#variable-value"));
    await page.keyboard.type("hunter2");
    await page.keyboard.press("Enter");
    // The sheet's description says so too: only the note under the name is the refusal.
    await expect(sheet.getByText(/^A variable isn't masked anywhere: store a password/)).toBeVisible();
    await scan(page, `A refused variable, ${name.toLowerCase()}`);
    await page.keyboard.press("Escape");
    await expect(sheet).toHaveCount(0);
  }
  await theme(page, "Light");

  await page.setViewportSize({ width: 320, height: 640 });
  await expect(page.getByRole("row", { name: /vars\.network\.dns_servers/ })).toBeVisible();
  const overflow = await page.evaluate(() => {
    const main = document.getElementById("main")!;
    return {
      page: document.documentElement.scrollWidth - window.innerWidth,
      main: main.scrollWidth - main.clientWidth,
    };
  });
  expect(overflow).toEqual({ page: 0, main: 0 });
  expect(errors).toEqual([]);
});
