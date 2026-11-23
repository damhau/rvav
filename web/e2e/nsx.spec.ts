import AxeBuilder from "@axe-core/playwright";
import { expect, test, type Page } from "@playwright/test";
import { scan } from "./axe";

// NSX on the mock backend (ADR-0028): a placement rule's segments made for
// deployments, a deployment's segment and security groups, and a VM put back
// in its group after its tag was removed in NSX. A reload resets the mock
// database, so each flow moves around in the app.

async function open(page: Page, link: string, heading: string) {
  await page.goto("/");
  await page.getByRole("navigation", { name: "Main" }).getByRole("link", { name: link }).click();
  await expect(page.getByRole("heading", { name: heading, level: 1 })).toBeVisible();
}

test("a placement rule makes NSX segments: checked, saved and shown", async ({ page }) => {
  await open(page, "Projects", "Projects");
  await page.getByRole("region", { name: "Projects" }).getByRole("link", { name: "Web Platform" }).click();
  await page.getByRole("tab", { name: "Placement" }).click();
  await page
    .getByRole("button", { name: /^Actions for the rule of priority/ })
    .first()
    .click();
  await page.getByRole("menuitem", { name: "Change" }).click();
  const rule = page.getByRole("dialog", { name: /Change placement rule/ });
  await rule.getByRole("checkbox", { name: "NSX segments made for deployments" }).check();
  await rule.getByRole("textbox", { name: "Tier-1 gateway" }).fill("t1-nope");
  await rule.getByRole("textbox", { name: "EfficientIP block" }).fill("nsx-gva-dev");
  await rule.getByRole("textbox", { name: "DNS zone" }).fill("corp.example");
  await rule.getByRole("textbox", { name: "DNS servers" }).fill("10.10.0.53");
  await rule.getByRole("button", { name: "Save rule" }).click();
  await expect(rule.getByText("NSX has no Tier-1 t1-nope.")).toBeVisible();

  await rule.getByRole("textbox", { name: "Tier-1 gateway" }).fill("t1-gva-dev");
  await rule.getByRole("button", { name: "Save rule" }).click();
  await expect(page.getByText("Placement rule saved")).toBeVisible();
  await expect(page.getByText("NSX t1-gva-dev, nsx-gva-dev /26, corp.example")).toBeVisible();

  // Opened again, it still makes them.
  await page
    .getByRole("button", { name: /^Actions for the rule of priority/ })
    .first()
    .click();
  await page.getByRole("menuitem", { name: "Change" }).click();
  await expect(rule.getByRole("checkbox", { name: "NSX segments made for deployments" })).toBeChecked();
  await expect(rule.getByRole("textbox", { name: "Tier-1 gateway" })).toHaveValue("t1-gva-dev");
});

test("a deployment's segment and security groups; a VM put back in its group (N1.3)", async ({ page }) => {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/deployments");
  await page.getByRole("link", { name: "web-tst-search", exact: true }).click();
  await expect(page.getByRole("heading", { name: "web-tst-search" })).toBeVisible();

  // The segment with the addresses, the group with the rules into it.
  await expect(page.getByRole("region", { name: "Network and addresses" })).toContainText(
    "NSX segment · t1-gva-prd · 10.41.10.0/26",
  );
  const groups = page.getByRole("region", { name: "Security groups" });
  await expect(groups).toContainText("Allows HTTPS from anywhere");
  await expect(groups).toContainText("Allows SSH from grp-backup-clients");
  await expect(groups.getByRole("status")).toContainText("Its tags for grp-web-servers were removed in NSX");
  // In both themes, as the accessibility sweep checks the other pages
  // (loaded again in each: the mock database is the same once reloaded).
  for (const scheme of ["dark", "light"] as const) {
    await page.emulateMedia({ colorScheme: scheme });
    await page.reload();
    await expect(groups.getByRole("status")).toBeVisible();
    const axe = await new AxeBuilder({ page })
      .withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa"])
      .analyze();
    expect(
      axe.violations.map((v) => `${scheme}: ${v.id} ${v.help} ${v.nodes.map((n) => n.html.slice(0, 120)).join(" | ")}`),
    ).toEqual([]);
  }

  // Added to the group again: its tags restored.
  await page.getByRole("button", { name: "More actions for web-tst-search002" }).click();
  await page.getByRole("menuitem", { name: /^Add to a security group/ }).click();
  const add = page.getByRole("dialog", { name: "Add to a security group · web-tst-search002" });
  await add.getByRole("combobox", { name: "Group", exact: true }).click();
  await page.getByRole("option", { name: "grp-web-servers", exact: true }).click();
  await add.getByRole("button", { name: "Add to a security group" }).click();
  await expect(add).toBeHidden();
  await expect(page.getByText(/^REQ-\d+ has started\.$/)).toBeVisible();
  await expect(groups.getByRole("status")).toBeHidden({ timeout: 15_000 });
  expect(errors).toEqual([]);
});

test("a composite's request form says what its VM steps build, with their security groups (N2.1)", async ({ page }) => {
  await page.goto("/catalog/postgres-cluster/request");
  await expect(page.getByRole("heading", { name: /Request PostgreSQL 16 cluster/ })).toBeVisible();
  const builds = page.getByRole("group", { name: "What it builds" });
  await expect(builds).toContainText("What it builds · 3 VMs");
  const nodes = builds.getByRole("listitem", { name: "Database nodes · PostgreSQL nodes" });
  await expect(nodes).toContainText("On separate hosts");
  await expect(nodes).toContainText("Security groups Database servers (new), grp-backup-clients");
  for (const scheme of ["dark", "light"] as const) {
    await page.emulateMedia({ colorScheme: scheme });
    await page.reload();
    await expect(builds).toBeVisible();
    const axe = await new AxeBuilder({ page })
      .withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa"])
      .analyze();
    expect(axe.violations.map((v) => `${scheme}: ${v.id} ${v.help}`)).toEqual([]);
  }
});

for (const theme of ["light", "dark"] as const) {
  test(`several NSX Managers: one added for a vCenter, tested, linked and removed, ${theme} theme (I1.1)`, async ({
    page,
  }) => {
    await page.addInitScript((t) => localStorage.setItem("rvav.theme", t), theme);
    await managersFlow(page, (where) => scan(page, `${where}, ${theme}`));
  });
}

// A toast covers the rows' menus at the bottom of the page. It stays while
// the pointer is on it, where the sheet's Save button was.
async function toastsGone(page: Page) {
  await page.mouse.move(0, 0);
  await expect(page.locator("[data-sonner-toast]")).toHaveCount(0, { timeout: 15_000 });
}

async function managersFlow(page: Page, check: (where: string) => Promise<void>) {
  await open(page, "Integrations", "Integrations");
  await page.getByRole("table", { name: "Connections" }).getByRole("link", { name: "NSX", exact: true }).click();
  await expect(page.getByRole("heading", { name: "NSX", level: 1 })).toBeVisible();
  const managers = page.getByRole("region", { name: "NSX Managers" });
  const primary = managers.getByRole("row", { name: /nsx\.corp\.example The connection above/ });
  await expect(primary).toContainText("vc01.corp.example");

  await managers.getByRole("button", { name: "Add an NSX Manager" }).click();
  const add = page.getByRole("dialog", { name: "Add an NSX Manager" });
  await add.getByRole("textbox", { name: "Name" }).fill("nsx02.corp.example");
  await add.getByRole("textbox", { name: "Address" }).fill("http://nsx02.corp.example");
  await add.getByRole("textbox", { name: "Account" }).fill("svc-rvav");
  await add.getByRole("button", { name: "Add NSX Manager" }).click();
  await expect(add.getByText("Its address over HTTPS, for example https://nsx02.corp.example.")).toBeVisible();
  await expect(add.getByText("Give the account's password.")).toBeVisible();
  await add.getByRole("textbox", { name: "Address" }).fill("https://nsx02.corp.example");
  await add.getByLabel("Password").fill("s3cret-nsx-pass");
  const vc01 = add.getByRole("checkbox", { name: /vc01\.corp\.example/ });
  await vc01.check();
  await expect(add.getByText("Moves here from nsx.corp.example")).toBeVisible();
  await add.getByRole("button", { name: "Test connection" }).click();
  await expect(add.getByRole("list", { name: "Test" })).toContainText("Tier-1 gateways");
  await toastsGone(page);
  await check("an NSX Manager added, tested");
  await add.getByRole("button", { name: "Add NSX Manager" }).click();
  await expect(add).toBeHidden();
  const added = managers.getByRole("row", { name: /nsx02\.corp\.example/ });
  await expect(added).toContainText("vc01.corp.example");
  await expect(primary).toContainText("None yet");

  // The integration's: its vCenters only, back to it.
  const menu = managers.getByRole("button", { name: "Actions for nsx.corp.example" });
  await toastsGone(page);
  await menu.click();
  await expect(page.getByRole("menuitem", { name: "Remove" })).toHaveCount(0);
  await page.getByRole("menuitem", { name: "Link vCenters" }).click();
  const link = page.getByRole("dialog", { name: "vCenters nsx.corp.example serves" });
  await expect(link.getByText("Served by nsx02.corp.example")).toBeVisible();
  await check("an NSX Manager's vCenters");
  await link.getByRole("checkbox", { name: /vc01\.corp\.example/ }).check();
  await link.getByRole("button", { name: "Save vCenters" }).click();
  await expect(link).toBeHidden();
  await expect(menu).toBeFocused();
  await expect(primary).toContainText("vc01.corp.example");
  await expect(added).toContainText("None yet");

  await toastsGone(page);
  await managers.getByRole("button", { name: "Actions for nsx02.corp.example" }).click();
  await page.getByRole("menuitem", { name: "Remove" }).click();
  const confirm = page.getByRole("alertdialog", { name: "Stop managing nsx02.corp.example?" });
  await expect(confirm).toContainText("What it has stays in NSX.");
  await confirm.getByRole("button", { name: "Stop managing it" }).click();
  await expect(added).toBeHidden();
  await expect(page.getByText("nsx02.corp.example removed")).toBeVisible();
  // Its row gone, the focus goes to the row now last, not to the page.
  await expect(menu).toBeFocused();
}
