import { expect, test, type Page } from "@playwright/test";

// Screenshots go to test-results; UPDATE_SCREENSHOTS=1 refreshes the
// committed ones in e2e/__screenshots__.
const shots = process.env.UPDATE_SCREENSHOTS ? "e2e/__screenshots__" : "test-results/screenshots";

async function choose(page: Page, combobox: string, option: string) {
  await page.getByRole("combobox", { name: combobox, exact: true }).click();
  await page.getByRole("option", { name: option, exact: true }).click();
}

/** Back to the checklist, from the sidebar (the mock's state lasts the page load). */
async function checklist(page: Page) {
  await page.getByRole("navigation").getByRole("link", { name: "Setup" }).click();
  await expect(page.getByRole("heading", { name: "Set up rvav", level: 1 })).toBeVisible();
}

function step(page: Page, name: string) {
  return page.getByRole("list", { name: "Steps" }).getByRole("listitem", { name: new RegExp(`^${name}: `) });
}

// A new install (U1.5, ADR-0030), against the mocks with rvav.mock.fresh: no
// environments, projects, items or integration settings. The administrator
// lands on the setup checklist and follows its steps' links.
test.beforeEach(async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem("rvav.mock.fresh", "1"));
});

test("a new install opens on the setup checklist, whose steps are followed", async ({ page }) => {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));

  await page.goto("/");
  await expect(page).toHaveURL(/\/admin\/setup$/);
  await expect(page.getByRole("heading", { name: "Set up rvav", level: 1 })).toBeVisible();
  await expect(page.getByText("0 of 8 done")).toBeVisible();
  await expect(step(page, "Add environments and projects")).toContainText("0 environments, 0 projects");
  await page.screenshot({ path: `${shots}/setup-checklist.png`, fullPage: true });

  // The identity provider, in its tab.
  await step(page, "Set the identity provider").getByRole("link", { name: "Identity provider" }).click();
  await expect(page.getByRole("tab", { name: "Identity provider" })).toHaveAttribute("aria-selected", "true");
  const idp = page.getByRole("region", { name: "Identity provider" });
  await idp.getByRole("textbox", { name: "Issuer" }).fill("https://login.corp.example/realms/rvav");
  await idp.getByRole("textbox", { name: "Client ID" }).fill("rvav");
  await idp.getByRole("button", { name: "Save settings" }).click();
  await expect(page.getByText("Identity provider saved")).toBeVisible();
  await checklist(page);
  await expect(step(page, "Set the identity provider")).toHaveAccessibleName(/: done$/);
  await expect(step(page, "Set the identity provider")).toContainText("Done: https://login.corp.example/realms/rvav");

  // A group mapped to admin, an environment and a project, on the Projects page.
  await step(page, "Map a group to admin").getByRole("link", { name: "OIDC group mappings" }).click();
  const groups = page.getByRole("region", { name: "OIDC group mappings" });
  await groups.getByRole("textbox", { name: "OIDC group" }).fill("rvav-admins");
  await choose(page, "Role", "Administrator");
  await groups.getByRole("button", { name: "Map group" }).click();
  await expect(groups.getByRole("cell", { name: "rvav-admins", exact: true })).toBeVisible();
  const envs = page.getByRole("region", { name: "Environments" });
  await envs.getByRole("textbox", { name: "New environment key" }).fill("dev");
  await envs.getByRole("textbox", { name: "New environment name" }).fill("Development");
  await envs.getByRole("button", { name: "Add environment" }).click();
  await expect(envs.getByRole("row", { name: /Development/ })).toBeVisible();
  await page.getByRole("button", { name: "New project" }).click();
  const dialog = page.getByRole("dialog", { name: "New project" });
  await dialog.getByRole("textbox", { name: "Code" }).fill("web");
  await dialog.getByRole("textbox", { name: "Name" }).fill("Web");
  await dialog.getByRole("checkbox", { name: "Development" }).check();
  await dialog.getByRole("button", { name: "Create" }).click();
  await expect(page.getByRole("heading", { name: "Web", level: 1 })).toBeVisible();

  // vCenter, in its tab: nothing set by the environment here.
  await checklist(page);
  await expect(step(page, "Map a group to admin")).toContainText("Done: rvav-admins");
  await expect(step(page, "Add environments and projects")).toContainText("Done: 1 environment, 1 project");
  await step(page, "Connect vCenter").getByRole("link", { name: "vCenter" }).click();
  await expect(page.getByRole("tab", { name: "vCenter" })).toHaveAttribute("aria-selected", "true");
  const vcenter = page.getByRole("region", { name: "vCenter connection" });
  await vcenter.getByRole("textbox", { name: "URL" }).fill("https://vc01.corp.example/sdk");
  await vcenter.getByRole("textbox", { name: "User" }).fill("svc-rvav@vsphere.local");
  await vcenter.getByLabel("Password").fill("s3cret");
  await vcenter.getByRole("button", { name: "Save settings" }).click();
  await expect(page.getByText("vCenter settings saved")).toBeVisible();

  await checklist(page);
  await expect(page.getByText("4 of 8 done")).toBeVisible();
  await expect(page.getByRole("progressbar", { name: "Steps done" })).toHaveAttribute("aria-valuenow", "4");
  await expect(step(page, "Connect EfficientIP")).toHaveAccessibleName(/: to do$/);

  // Kept from opening at sign-in; the Overview still offers it.
  await page.getByRole("button", { name: "Don't open it at sign-in" }).click();
  await expect(page.getByText("The checklist won't open at sign-in")).toBeVisible();
  await expect(page.getByRole("button", { name: "Open it at sign-in again" })).toBeVisible();
  await page.screenshot({ path: `${shots}/setup-checklist-progress.png`, fullPage: true });
  expect(errors).toEqual([]);
});
