import { expect, test, type Page } from "@playwright/test";
import { oidc, signIn } from "./auth";

test.skip(!oidc, "needs Keycloak sign-in, to act as two people (set RVAV_STACK_AUTH=oidc)");

// A1 acceptance on the real stack (ADR-0034): Marc, an administrator, makes
// a VM operator and gives it to Thomas in Infra Core, where Thomas holds no
// other role; Thomas powers Marc's VM there off and on through Temporal and
// vcsim, and is offered nothing else. Marc then gives him an auditor of his
// own making for all of rvav, and Thomas reads the administration without a
// way to change it. Marc takes both away, deletes the roles and the VM.

async function nav(page: Page, link: string, heading = link) {
  await page.getByRole("navigation", { name: "Main" }).getByRole("link", { name: link, exact: true }).click();
  await expect(page.getByRole("heading", { name: heading, level: 1 })).toBeVisible();
}

async function choose(page: Page, combobox: string, option: string) {
  await page.getByRole("combobox", { name: combobox, exact: true }).click();
  await page.getByRole("option", { name: option, exact: true }).click();
}

async function signInAs(page: Page, username: string) {
  await page.getByRole("button", { name: "Account" }).click();
  await page.getByRole("menuitem", { name: "Sign out" }).click();
  await signIn(page, username);
}

async function openRole(page: Page, name: string) {
  await nav(page, "Roles");
  await page.getByRole("region", { name: "Roles" }).getByRole("link", { name, exact: true }).click();
  await expect(page.getByRole("heading", { name, level: 1 })).toBeVisible();
}

async function openInfraCore(page: Page) {
  await nav(page, "Projects");
  await page.getByRole("region", { name: "Projects" }).getByRole("link", { name: "Infra Core" }).click();
  await expect(page.getByRole("heading", { name: "Infra Core", level: 1 })).toBeVisible();
}

/** The items of an open menu that can be picked. */
const enabledItems = (page: Page) => page.getByRole("menuitem").and(page.locator(":not([data-disabled])"));

test("an operator who only powers VMs, and an auditor who changes nothing", async ({ page }) => {
  test.setTimeout(300_000);
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.on("dialog", (d) => void d.accept());
  // Roles and a VM of its own, so the test can run again on the same stack.
  const suffix = Array.from({ length: 5 }, () => String.fromCharCode(97 + Math.floor(Math.random() * 26))).join("");
  const operator = `VM operator ${suffix}`;
  const auditor = `Compliance auditor ${suffix}`;
  const role = `op${suffix}`;

  // Keycloak gives Thomas a new subject whenever it imports the realm again,
  // and rvav a record per subject: the pickers list the one seen last first.
  await page.goto("/");
  await signIn(page, "thomas");
  await expect(page.getByRole("button", { name: /^Account: Thomas Nguyen/ })).toBeVisible();
  await signInAs(page, "marc");
  await page.goto("/admin/roles");
  await expect(page.getByRole("heading", { name: "Roles", level: 1 })).toBeVisible();
  await page.getByRole("link", { name: "New role" }).click();
  await page.getByRole("textbox", { name: "Key" }).fill(`op-${suffix}`);
  await page.getByRole("textbox", { name: "Name" }).fill(operator);
  for (const box of ["project.see", "Power on", "Power off"])
    await page.getByRole("checkbox", { name: box, exact: true }).check();
  await page.getByRole("button", { name: "Create role" }).click();
  await expect(page.getByRole("heading", { name: operator, level: 1 })).toBeVisible();

  // A VM of Marc's in Infra Core, built for real.
  await page.getByRole("button", { name: /^Project/ }).click();
  await page.getByRole("menuitem", { name: /^Infra Core/ }).click();
  await expect(page.getByRole("button", { name: /^Project/ })).toContainText("Infra Core");
  await nav(page, "Catalog");
  await page.getByRole("link", { name: "RHEL 9 server", exact: true }).click();
  await page.getByRole("link", { name: "Request", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Request RHEL 9 server" })).toBeVisible();
  await page.getByRole("textbox", { name: "Role" }).fill(role);
  const vm = `core-dev-${role}001`;
  await expect(page.locator("#f-vm_name")).toHaveText(new RegExp(vm));
  await page.getByRole("button", { name: "Submit request" }).click();
  await expect(page).toHaveURL(/\/requests\/REQ-\d+$/);
  await expect(page.getByText("Succeeded", { exact: true }).first()).toBeVisible({ timeout: 90_000 });
  await page.getByRole("link", { name: "Open deployment" }).click();
  await expect(page.getByText(vm, { exact: true }).first()).toBeVisible();
  const deploymentUrl = page.url();
  const deployment = (await page.getByRole("heading", { level: 1 }).innerText()).trim();

  // Thomas gets the operator there.
  await openInfraCore(page);
  const members = page.getByRole("region", { name: "Members" });
  await choose(page, "As", operator);
  await members.getByRole("textbox", { name: "Add someone" }).fill("Thomas");
  await members
    .getByRole("list", { name: "Add someone: people found" })
    .getByRole("button", { name: /^Thomas Nguyen/ })
    .first()
    .click();
  await expect(members.getByRole("list", { name: "Roles of Thomas Nguyen" })).toContainText(`${operator} given here`);

  // Thomas powers it off and on, and nothing else.
  await signInAs(page, "thomas");
  await page.goto(deploymentUrl);
  await expect(page.getByText(vm, { exact: true }).first()).toBeVisible();
  await page.getByRole("button", { name: `More actions for ${vm}` }).click();
  await expect(enabledItems(page)).toHaveText([/^Power off/]);
  await expect(page.getByRole("menuitem", { name: /^Reset/ })).toContainText(
    `Your role in Infra Core does not allow Reset.`,
  );
  await page.getByRole("menuitem", { name: /^Power off/ }).click();
  const dialog = page.getByRole("dialog", { name: `Power off · ${vm}` });
  await dialog.getByRole("button", { name: "Power off" }).click();
  await expect(dialog).toBeHidden();
  await expect(page.getByText(/vm-\d+ · off/)).toBeVisible({ timeout: 60_000 });
  await expect(page.getByRole("button", { name: `Power on ${vm}` })).toBeEnabled();
  await page.getByRole("button", { name: `Power on ${vm}` }).click();
  await expect(page.getByText(/vm-\d+ · on/)).toBeVisible({ timeout: 60_000 });
  await expect(page.getByRole("button", { name: `Open the console of ${vm}` })).toBeDisabled();
  await expect(page.getByRole("region", { name: "Actions" }).getByRole("button", { disabled: false })).toHaveCount(0);
  await page.getByRole("button", { name: `More actions for ${vm}` }).click();
  await expect(enabledItems(page)).toHaveText([/^Power off/]);
  await page.keyboard.press("Escape");

  // Marc makes an auditor from the built-in one and gives it to Thomas for all of rvav.
  await signInAs(page, "marc");
  await openRole(page, "Auditor");
  await page.getByRole("link", { name: "New role from it" }).click();
  await expect(page.getByText("Starting from what Auditor allows.")).toBeVisible();
  await page.getByRole("textbox", { name: "Key" }).fill(`aud-${suffix}`);
  await page.getByRole("textbox", { name: "Name" }).fill(auditor);
  await page.getByRole("button", { name: "Create role" }).click();
  await expect(page.getByRole("heading", { name: auditor, level: 1 })).toBeVisible();
  await nav(page, "Roles");
  await page.getByRole("tab", { name: "For all of rvav" }).click();
  const rvav = page.getByRole("region", { name: "Given for all of rvav" });
  await choose(page, "The role", auditor);
  await rvav.getByRole("textbox", { name: "Give someone" }).fill("Thomas");
  await rvav
    .getByRole("list", { name: "Give someone: people found" })
    .getByRole("button", { name: /^Thomas Nguyen/ })
    .first()
    .click();
  await expect(rvav.getByRole("button", { name: `Actions for ${auditor} of Thomas Nguyen` })).toBeVisible();

  // Thomas reads the administration, read only.
  await signInAs(page, "thomas");
  await nav(page, "Policies");
  await expect(page.getByRole("region", { name: "Approval policies" })).toContainText("Production VMs");
  await expect(page.getByRole("button", { name: "New approval policy" })).toBeHidden();
  await expect(page.getByRole("navigation", { name: "Main" }).getByRole("link", { name: "Integrations" })).toBeHidden();
  await nav(page, "Projects");
  await expect(page.getByRole("button", { name: "New project" })).toBeHidden();
  await page.getByRole("tab", { name: "Environments" }).click();
  const envs = page.getByRole("region", { name: "Environments" });
  await expect(envs.getByRole("row", { name: /^Production/ }).first()).toBeVisible();
  await expect(envs.getByRole("button", { name: "Actions for Production" })).toBeHidden();
  await expect(envs.getByRole("button", { name: "New environment" })).toBeHidden();
  await page.getByRole("tab", { name: "Identity provider groups" }).click();
  await expect(
    page.getByRole("region", { name: "Identity provider groups" }).getByRole("button", { name: "Map a group" }),
  ).toBeHidden();
  await page.getByRole("tab", { name: "Projects", exact: true }).click();
  await page.getByRole("region", { name: "Projects" }).getByRole("link", { name: "Infra Core" }).click();
  const theirs = page.getByRole("region", { name: "Members" });
  await expect(theirs.getByRole("list", { name: "Roles of Thomas Nguyen" })).toContainText(operator);
  await expect(theirs.getByRole("textbox", { name: "Add someone" })).toBeHidden();
  await expect(theirs.getByRole("button", { name: /^Actions for/ })).toHaveCount(0);
  await openRole(page, auditor);
  await expect(page.getByRole("list", { name: "Its permissions" })).toContainText("audit.read");
  await expect(page.getByRole("checkbox")).toHaveCount(0);
  await expect(page.getByRole("button", { name: `More actions for ${auditor}` })).toBeHidden();
  await nav(page, "Roles");
  await expect(page.getByRole("link", { name: "New role" })).toBeHidden();
  await nav(page, "Audit log");

  // Marc takes both away, deletes the roles, then the VM with its day-2 Delete.
  await signInAs(page, "marc");
  await nav(page, "Roles");
  await page.getByRole("tab", { name: "For all of rvav" }).click();
  await page.getByRole("button", { name: `Actions for ${auditor} of Thomas Nguyen` }).click();
  await page.getByRole("menuitem", { name: "Take it away" }).click();
  await page
    .getByRole("alertdialog", { name: `Take ${auditor} away from Thomas Nguyen?` })
    .getByRole("button", { name: "Take it away" })
    .click();
  await expect(page.getByText(`${auditor} taken away from Thomas Nguyen`)).toBeVisible();
  await openInfraCore(page);
  await page
    .getByRole("region", { name: "Members" })
    .getByRole("button", { name: "Actions for Thomas Nguyen" })
    .click();
  await page.getByRole("menuitem", { name: "Remove" }).click();
  await page
    .getByRole("alertdialog", { name: "Remove Thomas Nguyen?" })
    .getByRole("button", { name: "Remove member" })
    .click();
  await expect(page.getByRole("region", { name: "Members" }).getByRole("row", { name: /Thomas Nguyen/ })).toHaveCount(
    0,
  );
  for (const name of [operator, auditor]) {
    await openRole(page, name);
    await page.getByRole("button", { name: `More actions for ${name}` }).click();
    await page.getByRole("menuitem", { name: "Delete role" }).click();
    await page
      .getByRole("alertdialog", { name: `Delete ${name}?` })
      .getByRole("button", { name: "Delete role" })
      .click();
    await expect(page.getByText(`${name} deleted`)).toBeVisible();
  }
  await page.goto(deploymentUrl);
  await page.getByRole("button", { name: "Actions", exact: true }).click();
  await page.getByRole("menuitem", { name: "Delete", exact: true }).click();
  const del = page.getByRole("dialog", { name: `Delete · ${deployment}` });
  await del.getByRole("textbox", { name: /to confirm/ }).fill(deployment);
  await del.getByRole("button", { name: "Delete" }).click();
  await expect(page.getByText("Deleted", { exact: true }).first()).toBeVisible({ timeout: 90_000 });
  expect(errors).toEqual([]);
});
