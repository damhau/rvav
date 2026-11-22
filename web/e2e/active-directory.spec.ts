import { expect, test, type Page } from "@playwright/test";
import { scan } from "./axe";

// Active Directory on the mock backend (ADR-0064): the integration tested
// over TLS with plain LDAP warned about, and the OUs a project and every
// project's environments give computer objects. A reload resets the mock
// database, so the flow moves around in the app.

async function open(page: Page, link: string, heading: string) {
  await page.goto("/");
  await page.getByRole("navigation", { name: "Main" }).getByRole("link", { name: link }).click();
  await expect(page.getByRole("heading", { name: heading, level: 1 })).toBeVisible();
}

async function toastsGone(page: Page) {
  await page.mouse.move(0, 0);
  await expect(page.locator("[data-sonner-toast]")).toHaveCount(0, { timeout: 15_000 });
}

for (const theme of ["light", "dark"] as const) {
  test(`Active Directory: tested, warned about plain LDAP, OUs given to a project and an environment, ${theme} theme (I1.3)`, async ({
    page,
  }) => {
    await page.addInitScript((t) => localStorage.setItem("rvav.theme", t), theme);
    const check = (where: string) => scan(page, `${where}, ${theme}`);

    await open(page, "Integrations", "Integrations");
    await page.getByRole("table", { name: "Connections" }).getByRole("link", { name: "Active Directory" }).click();
    await expect(page.getByRole("heading", { name: "Active Directory", level: 1 })).toBeVisible();
    await page.getByRole("button", { name: "Test connection" }).click();
    await expect(page.getByText("Bound to ldaps://dc01.corp.example as svc-rvav@corp.example")).toBeVisible();
    await page.getByRole("checkbox", { name: "Allow plain LDAP, without StartTLS" }).check();
    await expect(page.getByRole("status").filter({ hasText: "go over the network in the clear" })).toBeVisible();
    await toastsGone(page);
    await check("the Active Directory integration, plain LDAP ticked");

    await open(page, "Projects", "Projects");
    await page.getByRole("region", { name: "Projects" }).getByRole("link", { name: "Web Platform" }).click();
    await page.getByRole("tab", { name: "Placement" }).click();
    const directory = page.getByRole("region", { name: "Active Directory" });
    await expect(directory.getByRole("row", { name: /^Any environment/ })).toContainText("OU=Web,OU=Servers");
    await directory.getByRole("button", { name: "New OU" }).click();
    const sheet = page.getByRole("dialog", { name: "New Active Directory OU for Web Platform" });
    await sheet.getByRole("combobox", { name: "Environment" }).click();
    await page.getByRole("option", { name: "dev" }).click();
    await sheet.getByRole("textbox", { name: "OU", exact: true }).fill("Servers");
    await sheet.getByRole("button", { name: "Save OU" }).click();
    await expect(sheet.getByText(/"Servers" is not a DN/)).toBeVisible();
    await toastsGone(page);
    await check("a project's new OU, refused");
    await sheet.getByRole("textbox", { name: "OU", exact: true }).fill("OU=Dev,OU=Web");
    await sheet.getByRole("textbox", { name: "Groups" }).fill("CN=Web servers,OU=Groups\nCN=Dev,OU=Groups");
    await sheet.getByRole("button", { name: "Save OU" }).click();
    await expect(sheet).toBeHidden();
    await expect(directory.getByRole("row", { name: /^In dev/ })).toContainText("CN=Dev,OU=Groups");
    await toastsGone(page);
    await check("a project's OUs");

    await page.getByRole("navigation", { name: "Main" }).getByRole("link", { name: "Projects" }).click();
    await page.getByRole("tab", { name: "Environments" }).click();
    const shared = page.getByRole("region", { name: "Active Directory" });
    await expect(shared.getByRole("row", { name: /^Every project in prd/ })).toContainText("OU=Production,OU=Servers");
    await check("every project's OUs");
  });
}
