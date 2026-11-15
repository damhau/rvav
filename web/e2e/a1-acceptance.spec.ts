import { expect, test, type Page } from "@playwright/test";

// A1's acceptance on the mock backend (A1.4, ADR-0034): an administrator
// makes an operator who may only power VMs on and off in a project, and an
// auditor who sees everything and changes nothing, and gives them. The
// mock signs in one person, so what their holders see is told by the
// personas holding the same roles (rvav.mock.as "vm-operator" and
// "compliance-auditor").

async function choose(page: Page, combobox: string, option: string) {
  await page.getByRole("combobox", { name: combobox, exact: true }).click();
  await page.getByRole("option", { name: option, exact: true }).click();
}

async function nav(page: Page, link: string, heading = link) {
  await page.getByRole("navigation", { name: "Main" }).getByRole("link", { name: link, exact: true }).click();
  await expect(page.getByRole("heading", { name: heading, level: 1 })).toBeVisible();
}

async function openDeployment(page: Page, name: string) {
  await page.goto("/deployments");
  await page.getByRole("link", { name, exact: true }).click();
  await expect(page.getByRole("heading", { name })).toBeVisible();
}

/** The items of a VM's menu that can be picked. */
const enabledItems = (page: Page) => page.getByRole("menuitem").and(page.locator(":not([data-disabled])"));

test("an administrator makes the operator and the auditor, and gives them", async ({ page }) => {
  test.slow();
  await page.goto("/");

  await nav(page, "Roles");
  await page.getByRole("link", { name: "New role" }).click();
  await page.getByRole("textbox", { name: "Key" }).fill("vm-operator");
  await page.getByRole("textbox", { name: "Name" }).fill("VM operator");
  for (const box of ["project.see", "Power on", "Power off"])
    await page.getByRole("checkbox", { name: box, exact: true }).check();
  await expect(page.getByRole("region", { name: "What it allows" })).toContainText(
    "Run the power day-2 actions Power on and Power off.",
  );
  await page.getByRole("button", { name: "Make role" }).click();
  await expect(page.getByRole("heading", { name: "VM operator", level: 1 })).toBeVisible();

  // Given in Web Platform: to Claire, and to whoever is in rvav-operators.
  await nav(page, "Projects");
  await page.getByRole("region", { name: "Projects" }).getByRole("link", { name: "Web Platform" }).click();
  await expect(page.getByRole("heading", { name: "Web Platform", level: 1 })).toBeVisible();
  const members = page.getByRole("region", { name: "Members" });
  await choose(page, "As", "VM operator");
  await members.getByRole("textbox", { name: "Add someone" }).fill("Claire");
  await members.getByRole("button", { name: /Claire Dubois/ }).click();
  await expect(members.getByRole("list", { name: "Roles of Claire Dubois" })).toHaveText("VM operator given here");
  await nav(page, "Projects");
  const groups = page.getByRole("region", { name: "OIDC group mappings" });
  await groups.getByRole("textbox", { name: "OIDC group" }).fill("rvav-operators");
  await choose(page, "In project", "Web Platform");
  await choose(page, "Role", "VM operator");
  await groups.getByRole("button", { name: "Map group" }).click();
  await expect(groups.getByRole("row", { name: /rvav-operators/ })).toContainText("VM operator in Web Platform");

  // The auditor: the built-in auditor's permissions, given for all of rvav.
  await nav(page, "Roles");
  await page.getByRole("region", { name: "Roles" }).getByRole("link", { name: "Auditor" }).click();
  await page.getByRole("link", { name: "Make a role from it" }).click();
  await expect(page.getByText("Starting from what Auditor allows.")).toBeVisible();
  await page.getByRole("textbox", { name: "Key" }).fill("compliance-auditor");
  await page.getByRole("textbox", { name: "Name" }).fill("Compliance auditor");
  await page.getByRole("button", { name: "Make role" }).click();
  await expect(page.getByRole("heading", { name: "Compliance auditor", level: 1 })).toBeVisible();
  await expect(page.getByRole("checkbox", { name: "audit.read" })).toBeChecked();
  await expect(page.getByRole("checkbox", { name: "policies.manage" })).not.toBeChecked();
  await nav(page, "Roles");
  await page.getByRole("tab", { name: "For all of rvav" }).click();
  const rvav = page.getByRole("region", { name: "Given for all of rvav" });
  await choose(page, "The role", "Compliance auditor");
  await rvav.getByRole("textbox", { name: "Give someone" }).fill("Alain");
  await rvav.getByRole("button", { name: /Alain Favre/ }).click();
  await expect(rvav.getByRole("row", { name: /Alain Favre/ })).toContainText("Compliance auditor");

  // What each then may do.
  await page.getByRole("tab", { name: "What someone may do" }).click();
  const may = page.getByRole("region", { name: "What someone may do" });
  await may.getByRole("textbox", { name: "Who" }).fill("Claire");
  await may.getByRole("button", { name: /Claire Dubois/ }).click();
  await choose(page, "Where", "Web Platform");
  const claire = may.getByRole("table", { name: "What Claire Dubois may do in Web Platform" });
  await expect(claire.getByRole("row", { name: /day2\.power:power_off/ })).toContainText(
    "VM operator, given in Web Platform",
  );
  await expect(claire.getByRole("row", { name: /request\.submit/ })).toContainText(
    "No role they hold in Web Platform grants it.",
  );
  await may.getByRole("textbox", { name: "Who" }).fill("Alain");
  await may.getByRole("button", { name: /Alain Favre/ }).click();
  await choose(page, "Where", "For all of rvav");
  const alain = may.getByRole("table", { name: "What Alain Favre may do for all of rvav" });
  await expect(alain.getByRole("row", { name: /\baudit\.read\b/ })).toContainText("Compliance auditor");
  await expect(alain.getByRole("row", { name: /policies\.manage/ })).toContainText(
    "No role they hold for all of rvav grants it.",
  );
});

test("the operator powers a VM off and on, and is offered nothing else", async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem("rvav.mock.as", "vm-operator"));
  await page.goto("/deployments");
  await expect(page.getByRole("link", { name: "web-dev-api", exact: true })).toBeVisible();
  await page.getByRole("button", { name: /^Project/ }).click();
  await expect(page.getByRole("menuitem")).toHaveText([/Web Platform.*VM operator/]);
  await page.keyboard.press("Escape");
  await openDeployment(page, "web-dev-api");
  await expect(page.getByText("Owner Thomas Nguyen")).toBeVisible();
  await expect(page.getByText(/vm-\d+ · on/)).toBeVisible();

  const vm = "web-dev-api004";
  await expect(page.getByRole("button", { name: `Open the console of ${vm}` })).toBeDisabled();
  await expect(page.getByRole("region", { name: "Actions" }).getByRole("button", { disabled: false })).toHaveCount(0);
  await page.getByRole("button", { name: `More actions for ${vm}` }).click();
  await expect(enabledItems(page)).toHaveText([/^Power off/]);
  await expect(page.getByRole("menuitem", { name: /^Reset/ })).toContainText(
    "Your role in Web Platform does not allow Reset.",
  );
  await page.getByRole("menuitem", { name: /^Power off/ }).click();
  const dialog = page.getByRole("dialog", { name: `Power off · ${vm}` });
  await dialog.getByRole("button", { name: "Power off" }).click();
  await expect(dialog).toBeHidden();
  await expect(page.getByText(/vm-\d+ · off/)).toBeVisible({ timeout: 15_000 });

  await page.getByRole("button", { name: `More actions for ${vm}` }).click();
  await expect(enabledItems(page)).toHaveText([/^Power on/]);
  await page.keyboard.press("Escape");
  await page.getByRole("button", { name: `Power on ${vm}` }).click();
  await expect(page.getByText(/vm-\d+ · on/)).toBeVisible({ timeout: 15_000 });
  await page.getByRole("button", { name: `More actions for ${vm}` }).click();
  await expect(enabledItems(page)).toHaveText([/^Power off/]);
});

test("the compliance auditor reads the administration and can change nothing", async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem("rvav.mock.as", "compliance-auditor"));
  await page.goto("/");
  await nav(page, "Policies");
  await expect(page.getByRole("region", { name: "Approval policies" })).toContainText("Production VMs");
  await expect(page.getByRole("button", { name: "New approval policy" })).toBeHidden();
  const main = page.getByRole("navigation", { name: "Main" });
  await expect(main.getByRole("link", { name: "Integrations" })).toBeHidden();

  await nav(page, "Projects");
  await expect(page.getByRole("button", { name: "New project" })).toBeHidden();
  const envs = page.getByRole("region", { name: "Environments" });
  await expect(envs.getByRole("checkbox", { name: "Production is production" })).toBeDisabled();
  await expect(envs.getByRole("button", { name: "Add environment" })).toBeHidden();
  await expect(
    page.getByRole("region", { name: "OIDC group mappings" }).getByRole("button", { name: "Map group" }),
  ).toBeHidden();
  await page.getByRole("region", { name: "Projects" }).getByRole("link", { name: "Web Platform" }).click();
  const members = page.getByRole("region", { name: "Members" });
  await expect(members.getByRole("list", { name: "Roles of Thomas Nguyen" })).toContainText("Member");
  await expect(members.getByRole("textbox", { name: "Add someone" })).toBeHidden();
  await expect(members.getByRole("button", { name: /^Change roles/ })).toHaveCount(0);
  await expect(members.getByRole("button", { name: /^Remove/ })).toHaveCount(0);

  await nav(page, "Roles");
  await expect(
    page.getByRole("region", { name: "Roles" }).getByRole("link", { name: "Compliance auditor" }),
  ).toBeVisible();
  await expect(page.getByRole("link", { name: "New role" })).toBeHidden();
  await page.getByRole("region", { name: "Roles" }).getByRole("link", { name: "Compliance auditor" }).click();
  await expect(page.getByRole("list", { name: "Its permissions" })).toContainText("audit.read");
  await expect(page.getByRole("checkbox")).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Save role" })).toBeHidden();
  await expect(page.getByRole("button", { name: "Delete role" })).toBeHidden();
  await nav(page, "Roles");
  await page.getByRole("tab", { name: "For all of rvav" }).click();
  const rvav = page.getByRole("region", { name: "Given for all of rvav" });
  await expect(rvav.getByRole("row", { name: /Léa Martin/ })).toContainText("Compliance auditor");
  await expect(rvav.getByRole("textbox", { name: "Give someone" })).toBeHidden();
  await expect(rvav.getByRole("button", { name: /^Take / })).toHaveCount(0);

  await nav(page, "Audit log");
  await openDeployment(page, "web-dev-api");
  await expect(page.getByRole("button", { name: "Power off web-dev-api004" })).toBeDisabled();
  await expect(page.getByRole("button", { name: "Open the console of web-dev-api004" })).toBeDisabled();
  await page.getByRole("button", { name: "More actions for web-dev-api004" }).click();
  await expect(enabledItems(page)).toHaveCount(0);
});
