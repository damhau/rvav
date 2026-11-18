import { expect, test, type Page } from "@playwright/test";

// Administration › Roles on the mock backend (A1.3, ADR-0034): making,
// changing, refusing and deleting a custom role, and giving roles in a
// project, to a group and for all of rvav. A reload resets the mock
// database, so each flow moves around in the app.

async function openRolesFromNav(page: Page) {
  await page.getByRole("navigation", { name: "Main" }).getByRole("link", { name: "Roles", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Roles", level: 1 })).toBeVisible();
}

async function openRoles(page: Page) {
  await page.goto("/");
  await openRolesFromNav(page);
}

async function choose(page: Page, combobox: string, option: string) {
  await page.getByRole("combobox", { name: combobox, exact: true }).click();
  await page.getByRole("option", { name: option, exact: true }).click();
}

/** Makes a role from the Roles page, its permissions by their checkboxes' names. */
async function makeRole(page: Page, key: string, name: string, boxes: string[]) {
  await openRolesFromNav(page);
  await page.getByRole("link", { name: "New role" }).click();
  await page.getByRole("textbox", { name: "Key" }).fill(key);
  await page.getByRole("textbox", { name: "Name" }).fill(name);
  for (const b of boxes) await page.getByRole("checkbox", { name: b, exact: true }).check();
  await page.getByRole("button", { name: "Make role" }).click();
  await expect(page.getByRole("heading", { name, level: 1 })).toBeVisible();
}

async function openProject(page: Page, name: string) {
  await page.getByRole("navigation", { name: "Main" }).getByRole("link", { name: "Projects", exact: true }).click();
  await page.getByRole("region", { name: "Projects" }).getByRole("link", { name }).click();
  await expect(page.getByRole("heading", { name, level: 1 })).toBeVisible();
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
  await page.getByRole("button", { name: "More actions for Power operator" }).click();
  await page.getByRole("menuitem", { name: "Delete role" }).click();
  const confirm = page.getByRole("alertdialog", { name: "Delete Power operator?" });
  await expect(confirm).toContainText("nobody loses a permission");
  await confirm.getByRole("button", { name: "Delete role" }).click();
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

test("give a custom role in a project and to a group; what it then refuses", async ({ page }) => {
  test.slow();
  await page.goto("/");
  await makeRole(page, "vm-operator", "VM operator", ["project.see", "Power on", "Power off"]);

  // Someone added with it; someone else holding it besides Member, given here and through a group.
  await openProject(page, "Web Platform");
  const members = page.getByRole("region", { name: "Members" });
  await choose(page, "As", "VM operator");
  await members.getByRole("textbox", { name: "Add someone" }).fill("Claire");
  await members.getByRole("button", { name: /Claire Dubois/ }).click();
  await expect(page.getByText("Claire Dubois added as VM operator")).toBeVisible();
  await expect(members.getByRole("list", { name: "Roles of Claire Dubois" })).toHaveText("VM operator given here");
  await members.getByRole("button", { name: "Actions for Thomas Nguyen" }).click();
  await page.getByRole("menuitem", { name: "Change roles" }).click();
  const dialog = page.getByRole("dialog", { name: "Roles of Thomas Nguyen" });
  await expect(dialog).toContainText("Through groups: Member (rvav-web).");
  await dialog.getByRole("checkbox", { name: "VM operator" }).check();
  await dialog.getByRole("button", { name: "Save roles" }).click();
  await expect(dialog).toBeHidden();
  const thomas = members.getByRole("list", { name: "Roles of Thomas Nguyen" });
  await expect(thomas).toContainText("Member given here and through rvav-web");
  await expect(thomas).toContainText("VM operator given here");

  // A group mapped to it in the project.
  await page.getByRole("navigation", { name: "Main" }).getByRole("link", { name: "Projects", exact: true }).click();
  await page.getByRole("tab", { name: "Identity provider groups" }).click();
  const groups = page.getByRole("region", { name: "Identity provider groups" });
  await groups.getByRole("button", { name: "Map a group" }).click();
  const map = page.getByRole("dialog", { name: "Map a group" });
  await map.getByRole("textbox", { name: "OIDC group" }).fill("rvav-operators");
  await choose(page, "In project", "Web Platform");
  await choose(page, "Role", "VM operator");
  await map.getByRole("button", { name: "Map group" }).click();
  await expect(groups.getByRole("row", { name: /rvav-operators/ })).toContainText("VM operator in Web Platform");

  // Given in a project, it takes no permission for all of rvav only, and isn't deleted.
  await openRolesFromNav(page);
  await page.getByRole("region", { name: "Roles" }).getByRole("link", { name: "VM operator" }).click();
  await expect(page.getByText("Given 3 times, to users or groups.")).toBeVisible();
  await page.getByRole("checkbox", { name: "policies.read" }).check();
  await page.getByRole("button", { name: "Save role" }).click();
  await expect(page.getByRole("region", { name: "Permissions" }).getByRole("alert")).toContainText(
    "VM operator is given in Web Platform: a permission for all of rvav only can't be added while it is.",
  );
  await page.getByRole("button", { name: "More actions for VM operator" }).click();
  await expect(page.getByRole("menuitem", { name: "Delete: given 3 times" })).toBeDisabled();
  await page.keyboard.press("Escape");
  await openRolesFromNav(page);
  await page.getByRole("region", { name: "Roles" }).getByRole("button", { name: "Actions for VM operator" }).click();
  await expect(page.getByRole("menuitem", { name: "Delete: given 3 times" })).toBeDisabled();
});

test("give a role for all of rvav, refused twice, then taken away", async ({ page }) => {
  await page.goto("/");
  await makeRole(page, "access-keeper", "Access keeper", ["access.manage"]);
  await openRolesFromNav(page);
  await page.getByRole("tab", { name: "For all of rvav" }).click();
  const rvav = page.getByRole("region", { name: "Given for all of rvav" });
  await expect(rvav.getByRole("row", { name: /Léa Martin/ })).toContainText("Set by rvav");
  await choose(page, "The role", "Access keeper");
  await expect(rvav).toContainText("whoever holds Access keeper is an administrator in all but name");
  for (let i = 0; i < 2; i++) {
    await rvav.getByRole("textbox", { name: "Give someone" }).fill("Alain");
    await rvav.getByRole("button", { name: /Alain Favre/ }).click();
  }
  await expect(rvav.getByRole("alert")).toHaveText("Alain Favre already holds Access keeper.");
  await expect(rvav.getByRole("row", { name: /Alain Favre/ })).toContainText("Access keeper");
  await expect(rvav.getByRole("button", { name: /^Actions for .* of Léa Martin$/ })).toHaveCount(0);
  await rvav.getByRole("button", { name: "Actions for Access keeper of Alain Favre" }).click();
  await page.getByRole("menuitem", { name: "Take it away" }).click();
  const confirm = page.getByRole("alertdialog", { name: "Take Access keeper away from Alain Favre?" });
  await confirm.getByRole("button", { name: "Take it away" }).click();
  await expect(page.getByText("Access keeper taken away from Alain Favre")).toBeVisible();
  await expect(rvav.getByRole("row", { name: /Alain Favre/ })).toHaveCount(0);
});

test("a project admin is offered only the roles they hold there", async ({ page }) => {
  // The persona's Web Platform has VM operator, given to Thomas Nguyen by an
  // administrator: its day-2 actions are not the project admin's.
  await page.addInitScript(() => localStorage.setItem("rvav.mock.as", "project-admin"));
  await page.goto("/");
  const nav = page.getByRole("navigation", { name: "Main" });
  await expect(nav.getByRole("link", { name: "Projects", exact: true })).toBeVisible();
  await expect(nav.getByRole("link", { name: "Roles", exact: true })).toBeHidden();
  await nav.getByRole("link", { name: "Projects", exact: true }).click();
  await page.getByRole("region", { name: "Your projects" }).getByRole("link", { name: "Web Platform" }).click();
  const members = page.getByRole("region", { name: "Members" });
  await expect(members.getByRole("list", { name: "Roles of Thomas Nguyen" })).toContainText("VM operator");
  await page.getByRole("combobox", { name: "As", exact: true }).click();
  for (const role of ["Project admin", "Member", "Viewer"])
    await expect(page.getByRole("option", { name: role, exact: true })).toBeVisible();
  await expect(page.getByRole("option")).toHaveCount(3);
});

test("what someone may do in a project, and why they can't", async ({ page }) => {
  await openRoles(page);
  await page.getByRole("tab", { name: "What someone may do" }).click();
  const panel = page.getByRole("region", { name: "What someone may do" });
  await panel.getByRole("textbox", { name: "Who" }).fill("Thomas");
  await panel.getByRole("button", { name: /Thomas Nguyen/ }).click();
  await choose(page, "Where", "Web Platform");
  const table = panel.getByRole("table", { name: "What Thomas Nguyen may do in Web Platform" });
  const see = table.getByRole("row", { name: /\bproject\.see\b/ });
  await expect(see).toContainText("Member, given in Web Platform");
  await expect(see).toContainText("Member, through rvav-web, in Web Platform");
  await expect(table.getByRole("row", { name: /project\.members\.manage/ })).toContainText(
    "No role they hold in Web Platform grants it.",
  );
  await choose(page, "Show", "Those not held");
  await expect(see).toHaveCount(0);
  await expect(table.getByRole("row", { name: /project\.members\.manage/ })).toBeVisible();
});

test("what I may do, from the account menu", async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem("rvav.mock.as", "project-admin"));
  await page.goto("/");
  await page.getByRole("button", { name: /^Account/ }).click();
  await page.getByRole("menuitem", { name: "What I may do" }).click();
  await expect(page.getByRole("heading", { name: "What I may do", level: 1 })).toBeVisible();
  const inWeb = page.getByRole("table", { name: "What you may do in Web Platform" });
  await expect(inWeb.getByRole("row", { name: /project\.members\.manage/ })).toContainText(
    "Project admin, given in Web Platform",
  );
  await expect(inWeb.getByRole("row", { name: /project\.update/ })).toContainText(
    "No role you hold in Web Platform grants it.",
  );
  await choose(page, "Where", "For all of rvav");
  const everywhere = page.getByRole("table", { name: "What you may do for all of rvav" });
  await expect(everywhere.getByRole("row", { name: /\bproject\.see\b/ })).toContainText(
    "No role you hold for all of rvav grants it.",
  );
});
