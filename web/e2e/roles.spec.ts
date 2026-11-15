import { expect, test, type Page } from "@playwright/test";

// Administration › Roles on the mock backend (A1.3, ADR-0034): making,
// changing, refusing and deleting a custom role. A reload resets the mock
// database, so each flow moves around in the app.

async function openRolesFromNav(page: Page) {
  await page.getByRole("navigation").getByRole("link", { name: "Roles", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Roles", level: 1 })).toBeVisible();
}

async function openRoles(page: Page) {
  await page.goto("/");
  await openRolesFromNav(page);
}

test("make a custom role, change it and delete it", async ({ page }) => {
  test.slow();
  await openRoles(page);
  await page.getByRole("link", { name: "New role" }).click();
  await expect(page.getByRole("heading", { name: "New role", level: 1 })).toBeVisible();
  await page.getByRole("textbox", { name: "Key" }).fill("auditor");
  await page.getByRole("textbox", { name: "Name" }).fill("VM operator");
  await page.getByRole("checkbox", { name: "Power on" }).check();
  await page.getByRole("checkbox", { name: "Power off" }).check();
  const allows = page.getByRole("region", { name: "What it allows" });
  await expect(allows).toContainText("Run the power day-2 actions Power on and Power off.");

  // Refused where the problem is: the key, then the permissions.
  await page.getByRole("button", { name: "Make role" }).click();
  await expect(page.getByRole("textbox", { name: "Key" })).toHaveAccessibleDescription(/not a built-in role's key/);
  await page.getByRole("textbox", { name: "Key" }).fill("vm-operator");
  await page.getByRole("button", { name: "Make role" }).click();
  await expect(page.getByRole("region", { name: "Permissions" }).getByRole("alert")).toContainText("Add project.see");
  await page.getByRole("checkbox", { name: "project.see" }).check();
  await page.getByRole("button", { name: "Make role" }).click();
  await expect(page.getByRole("heading", { name: "VM operator", level: 1 })).toBeVisible();
  await expect(page.getByRole("textbox", { name: "Key" })).toBeDisabled();

  // A whole group; access.manage says what it makes of whoever holds it.
  await page.getByRole("checkbox", { name: "day2.snapshots" }).check();
  await expect(page.getByRole("checkbox", { name: "Take a snapshot" })).toBeDisabled();
  await expect(allows).toContainText("Run every snapshots day-2 action");
  await page.getByRole("checkbox", { name: "access.manage" }).check();
  await expect(allows).toContainText("an administrator in all but name");
  await expect(allows).toContainText("Given for all of rvav only: access.manage is a permission for all of rvav only.");
  await page.getByRole("checkbox", { name: "access.manage" }).uncheck();
  await page.getByRole("textbox", { name: "Name" }).fill("Power operator");
  await page.getByRole("button", { name: "Save role" }).click();
  await expect(page.getByText("Power operator saved")).toBeVisible();
  await expect(page.getByRole("heading", { name: "Power operator", level: 1 })).toBeVisible();

  // A key is taken once.
  await openRolesFromNav(page);
  await page.getByRole("link", { name: "New role" }).click();
  await page.getByRole("textbox", { name: "Key" }).fill("vm-operator");
  await page.getByRole("textbox", { name: "Name" }).fill("Again");
  await page.getByRole("checkbox", { name: "project.see" }).check();
  await page.getByRole("button", { name: "Make role" }).click();
  await expect(page.getByRole("textbox", { name: "Key" })).toHaveAccessibleDescription(
    "A role has the key vm-operator.",
  );

  await openRolesFromNav(page);
  const roles = page.getByRole("region", { name: "Roles" });
  await expect(roles.getByRole("row", { name: /Power operator/ })).toContainText("Nobody");
  await roles.getByRole("link", { name: "Power operator" }).click();
  page.once("dialog", (d) => void d.accept());
  await page.getByRole("button", { name: "Delete role" }).click();
  await expect(page.getByText("Power operator deleted")).toBeVisible();
  await expect(page.getByRole("heading", { name: "Roles", level: 1 })).toBeVisible();
  await expect(roles.getByRole("link", { name: "Power operator" })).toHaveCount(0);
});

test("a built-in role can't be changed: a role of your own starts from it", async ({ page }) => {
  await openRoles(page);
  await page.getByRole("region", { name: "Roles" }).getByRole("link", { name: "Auditor" }).click();
  await expect(page.getByRole("heading", { name: "Auditor", level: 1 })).toBeVisible();
  await expect(page.getByText("Built in: it can't be changed or deleted.")).toBeVisible();
  await expect(page.getByRole("checkbox")).toHaveCount(0);
  await expect(page.getByRole("list", { name: "Its permissions" })).toContainText("audit.read");
  await page.getByRole("link", { name: "Make a role from it" }).click();
  await expect(page.getByRole("heading", { name: "New role", level: 1 })).toBeVisible();
  await expect(page.getByText("Starting from what Auditor allows.")).toBeVisible();
  await expect(page.getByRole("checkbox", { name: "audit.read" })).toBeChecked();
  await page.getByRole("checkbox", { name: "access.read" }).uncheck();
  await page.getByRole("textbox", { name: "Key" }).fill("reader");
  await page.getByRole("textbox", { name: "Name" }).fill("Reader");
  await page.getByRole("button", { name: "Make role" }).click();
  await expect(page.getByRole("heading", { name: "Reader", level: 1 })).toBeVisible();
  await expect(page.getByRole("checkbox", { name: "access.read" })).not.toBeChecked();
});
