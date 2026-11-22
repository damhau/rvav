import { expect, test, type Page } from "@playwright/test";
import { bothThemes, scan, theme } from "./axe";
import { choose, press, selectTab, tabTo } from "./keyboard";

// An item shared with chosen people (G1.2, ADR-0019), by keyboard on the
// mock backend: Léa shares Ubuntu in Web Platform with the rvav-web group
// only, the sheet saying who can request before she saves, and then she,
// a Web Platform member outside the group, no longer finds it there.

// A fading toast fails axe's contrast check.
async function noToast(page: Page) {
  await page.mouse.move(0, 0);
  await expect(page.locator("[data-sonner-toast]")).toHaveCount(0, { timeout: 10_000 });
}

const ubuntu = "Ubuntu 24.04 server";

test("an item shared with a group in a project, by keyboard", async ({ page }) => {
  test.setTimeout(90_000);
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/catalog");
  const main = page.getByRole("main");
  await expect(main.getByText(ubuntu).first()).toBeVisible();

  const nav = page.getByRole("navigation", { name: "Main" });
  await press(page, nav.getByRole("link", { name: "Catalog items" }));
  await press(page, page.getByRole("region", { name: "Items" }).getByRole("link", { name: ubuntu }));
  await expect(page.getByRole("heading", { name: ubuntu, level: 1 })).toBeVisible();
  await selectTab(page, "Sharing");
  const shares = page.getByRole("table", { name: "Shares" });
  await expect(shares.getByRole("row", { name: /Web Platform/ })).toContainText("All members");

  await press(page, page.getByRole("button", { name: "Share", exact: true }));
  const sheet = page.getByRole("dialog", { name: `Share ${ubuntu}` });
  const project = sheet.getByRole("combobox", { name: "Project" });
  await expect(project).toBeFocused();
  await choose(page, /Web Platform/, { typed: "Web" });
  await expect(project).toHaveText(/Web Platform/);
  await expect(sheet.getByText("2 people in Web Platform can request it.")).toBeVisible();

  await press(page, sheet.getByRole("radio", { name: /All members/ }), "ArrowDown");
  await expect(sheet.getByRole("radio", { name: /Chosen people/ })).toBeChecked();
  await tabTo(page, sheet.getByRole("textbox", { name: "OIDC group" }));
  await page.keyboard.type("rvav-web");
  await page.keyboard.press("Enter");
  await expect(sheet.getByRole("list", { name: "Groups" })).toContainText("rvav-web");
  const can = sheet.getByRole("list", { name: "Can request it" });
  await expect(can).toContainText("Thomas Nguyen · in rvav-web");
  await expect(can).not.toContainText("Léa Martin");
  await expect(sheet.getByText(/^Left out: .*Léa Martin/)).toBeVisible();
  await press(page, sheet.getByRole("button", { name: "Share", exact: true }));
  await expect(page.getByText("Shared in Web Platform")).toBeVisible();
  await expect(sheet).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Share", exact: true })).toBeFocused();
  await expect(shares.getByRole("row", { name: /Web Platform/ })).toContainText("group rvav-web");
  await expect(shares.getByRole("row", { name: /Data Engineering/ })).toContainText("All members");

  await noToast(page);
  await bothThemes(page, "an item's sharing");
  // The theme button is behind the modal sheet: the sheet is opened in each theme.
  for (const name of ["Light", "Dark"] as const) {
    await theme(page, name);
    await press(page, shares.getByRole("button", { name: "Actions for Web Platform" }));
    await page.getByRole("menuitem", { name: "Change who may request it" }).press("Enter");
    await expect(sheet.getByRole("list", { name: "Can request it" })).toContainText("Thomas Nguyen");
    await scan(page, `an item's share for a group, ${name.toLowerCase()}`);
    await page.keyboard.press("Escape");
    await expect(sheet).toHaveCount(0);
  }
  await theme(page, "Light");

  // Léa is a Web Platform member outside rvav-web: its catalog no longer has Ubuntu.
  await press(page, nav.getByRole("link", { name: "Catalog", exact: true }));
  await expect(main.getByText("RHEL 9 server").first()).toBeVisible();
  await expect(main.getByText(ubuntu)).toHaveCount(0);
  expect(errors).toEqual([]);
});
