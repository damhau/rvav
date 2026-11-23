import { expect, test, type Page } from "@playwright/test";
import { bothThemes, scan, theme } from "./axe";
import { chooseIn, press, tabTo } from "./keyboard";

// Supervisors, and approvers by role or manager (G1.1, ADR-0041), by
// keyboard on the mock backend: the Supervisor given in a project, approval
// and day-2 levels that ask project roles and the requester's manager, and
// a request's page saying whom a level asks.

// A fading toast fails axe's contrast check.
async function noToast(page: Page) {
  await page.mouse.move(0, 0);
  await expect(page.locator("[data-sonner-toast]")).toHaveCount(0, { timeout: 10_000 });
}

test("an approval level asks project roles and the requester's manager", async ({ page }) => {
  test.setTimeout(90_000);
  await page.goto("/admin/policies");
  const region = page.getByRole("region", { name: "Approval policies" });
  await press(page, region.getByRole("button", { name: "New approval policy" }));
  const sheet = page.getByRole("dialog", { name: "New approval policy" });
  await expect(sheet.getByRole("textbox", { name: "Name" })).toBeFocused();
  await page.keyboard.type("Nobody");
  await press(page, sheet.getByRole("button", { name: "Create policy" }));
  await expect(
    sheet.getByText("Give an OIDC group, approvers, project roles or the requester's manager."),
  ).toBeVisible();

  await tabTo(page, sheet.getByRole("textbox", { name: "Name" }));
  await page.keyboard.press("Control+A");
  await page.keyboard.type("Supervised");
  const roles = sheet.getByRole("group", { name: "Project roles" });
  await expect(roles).toHaveAccessibleDescription(/Whoever holds one in the request's project/);
  await expect(roles.getByRole("checkbox", { name: "Administrator" })).toHaveCount(0);
  await press(page, roles.getByRole("checkbox", { name: "Supervisor", exact: true }), "Space");
  await expect(roles.getByRole("checkbox", { name: "Supervisor", exact: true })).toBeChecked();
  const manager = sheet.getByRole("checkbox", { name: "The requester's manager" });
  await expect(manager).toHaveAccessibleDescription(/Without one, the level asks only its other approvers/);
  await press(page, manager, "Space");
  await expect(manager).toBeChecked();
  await press(page, sheet.getByRole("button", { name: "Create policy" }));
  await expect(page.getByText("Supervised saved")).toBeVisible();
  await expect(region.getByRole("row", { name: /Supervised/ })).toContainText("Supervisor, the requester's manager");

  // The theme button is behind the modal sheet: the sheet is opened in each theme.
  await noToast(page);
  const change = page.getByRole("dialog", { name: "Change Supervised" });
  for (const name of ["Light", "Dark"] as const) {
    await theme(page, name);
    await press(page, region.getByRole("button", { name: "Actions for Supervised" }));
    await page.getByRole("menuitem", { name: "Change" }).press("Enter");
    await expect(change.getByRole("checkbox", { name: "Supervisor", exact: true })).toBeChecked();
    await expect(change.getByRole("checkbox", { name: "The requester's manager" })).toBeChecked();
    await scan(page, `an approval level asking roles and the manager, ${name.toLowerCase()}`);
    await page.keyboard.press("Escape");
    await expect(change).toHaveCount(0);
  }
  await theme(page, "Light");

  // A day-2 approval level asks them the same way.
  await press(page, page.getByRole("tab", { name: "Approval" }), "ArrowRight");
  await expect(page.getByRole("tab", { name: "Day-2" })).toBeFocused();
  const day2 = page.getByRole("region", { name: "Day-2 policies" });
  await press(page, day2.getByRole("button", { name: "Actions for Deleting in production" }));
  await page.getByRole("menuitem", { name: "Change" }).press("Enter");
  const d2 = page.getByRole("dialog", { name: "Change Deleting in production" });
  const asked = d2.getByRole("group", { name: "Project roles" });
  await press(page, asked.getByRole("checkbox", { name: "Project admin", exact: true }), "Space");
  await press(page, d2.getByRole("button", { name: "Save policy" }));
  await expect(page.getByText("Deleting in production saved")).toBeVisible();
});

test("the Supervisor is given in a project, and a request says whom its level asks", async ({ page }) => {
  test.setTimeout(90_000);
  await page.goto("/admin/projects");
  await press(page, page.getByRole("region", { name: "Projects" }).getByRole("link", { name: "Web Platform" }));
  await expect(page.getByRole("heading", { name: "Web Platform", level: 1 })).toBeVisible();
  const members = page.getByRole("region", { name: "Members" });
  await chooseIn(page, page.getByRole("combobox", { name: "As", exact: true }), "Supervisor", { typed: "Sup" });
  await expect(page.getByRole("combobox", { name: "As", exact: true })).toHaveText(/Supervisor/);
  await press(page, members.getByRole("textbox", { name: "Add someone" }), "C");
  await page.keyboard.type("laire");
  await press(page, members.getByRole("button", { name: /Claire Dubois/ }));
  await expect(page.getByText("Claire Dubois added as Supervisor")).toBeVisible();
  await expect(members.getByRole("list", { name: "Roles of Claire Dubois" })).toHaveText("Supervisor given here");
  await noToast(page);
  await bothThemes(page, "project members, a supervisor");

  await page.goto("/requests/REQ-10471");
  const levels = page.getByRole("list", { name: "Approval levels" });
  const security = levels.getByRole("listitem").filter({ hasText: /^Network security/ });
  await expect(security).toContainText("Asks whoever holds Supervisor in the project and the requester's manager");
  await expect(security).toContainText("Waiting for Marc Keller");
  await bothThemes(page, "request, a level asking roles and the manager");
});
