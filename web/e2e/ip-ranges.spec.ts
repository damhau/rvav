import { expect, test, type Page } from "@playwright/test";
import { scan } from "./axe";

// rvav's own IP ranges on the mock backend (I1.2, ADR-0063): a range with
// who holds each address, one added after the refusals of its fields, the
// network it serves, and the reconcile report giving back an address no
// deployment holds. In both themes, on a desktop and on a phone.

const sizes = { desktop: { width: 1440, height: 1000 }, phone: { width: 390, height: 844 } };

for (const theme of ["light", "dark"] as const) {
  for (const [device, viewport] of Object.entries(sizes)) {
    test(`IP ranges: addresses, a range added, its network, the reconcile report, ${theme} theme, ${device}`, async ({
      page,
    }) => {
      const errors: string[] = [];
      page.on("pageerror", (e) => errors.push(e.message));
      await page.setViewportSize(viewport);
      await page.addInitScript((t) => localStorage.setItem("rvav.theme", t), theme);
      await rangesFlow(page, async (where) => {
        await scan(page, `${where}, ${theme}, ${device}`);
        expect(await overflow(page), where).toEqual({ page: 0, main: 0 });
      });
      expect(errors).toEqual([]);
    });
  }
}

async function overflow(page: Page) {
  return page.evaluate(() => {
    const main = document.getElementById("main")!;
    return {
      page: document.documentElement.scrollWidth - window.innerWidth,
      main: main.scrollWidth - main.clientWidth,
    };
  });
}

// A toast covers the rows' menus at the bottom of the page. It stays while
// the pointer is on it, where the sheet's Save button was.
async function toastsGone(page: Page) {
  await page.mouse.move(0, 0);
  await expect(page.locator("[data-sonner-toast]")).toHaveCount(0, { timeout: 15_000 });
}

async function rangesFlow(page: Page, check: (where: string) => Promise<void>) {
  await page.goto("/admin/infrastructure?tab=ip-ranges");
  await expect(page.getByRole("heading", { name: "Templates and networks", level: 1 })).toBeVisible();
  const ranges = page.getByRole("region", { name: "IP ranges", exact: true });
  const lab = ranges.getByRole("row", { name: /Test lab/ });
  await expect(lab).toContainText("29 free");
  await expect(lab).toContainText("10.41.10.200 – 10.41.10.239");
  await check("the IP ranges");

  await ranges.getByRole("button", { name: "Actions for Test lab" }).click();
  await page.getByRole("menuitem", { name: "Addresses given" }).click();
  const given = page.getByRole("dialog", { name: "Addresses of Test lab" });
  await expect(given.getByRole("row", { name: /10\.41\.10\.202/ })).toContainText("adapter backup");
  await expect(given.getByRole("row", { name: /10\.41\.10\.205/ })).toContainText("Gone");
  await check("a range's addresses");
  await page.keyboard.press("Escape");
  await expect(given).toBeHidden();

  await ranges.getByRole("button", { name: "New IP range" }).click();
  const add = page.getByRole("dialog", { name: "New IP range" });
  await expect(add).toContainText("rvav writes no DNS records for them.");
  await add.getByRole("textbox", { name: "Name" }).fill("Prod lab");
  await add.getByRole("combobox", { name: "Network" }).click();
  await page.getByRole("option", { name: "dvpg-db-prd-vl230", exact: true }).click();
  await add.getByRole("textbox", { name: "Subnet" }).fill("10.22.30.0/25");
  await add.getByRole("textbox", { name: "Gateway" }).fill("10.22.30.1");
  await add.getByRole("textbox", { name: "First address" }).fill("10.22.30.200");
  await add.getByRole("textbox", { name: "Last address" }).fill("10.22.30.120");
  await add.getByRole("button", { name: "Save range" }).click();
  await expect(add.getByText("The start must be in the subnet 10.22.30.0/25.")).toBeVisible();
  await toastsGone(page);
  await check("a range refused");
  await add.getByRole("textbox", { name: "First address" }).fill("10.22.30.100");
  await add.getByRole("textbox", { name: "Excluded addresses" }).fill("10.22.30.110");
  await add.getByRole("button", { name: "Save range" }).click();
  await expect(add).toBeHidden();
  await expect(ranges.getByRole("row", { name: /Prod lab/ })).toContainText("20 free");

  await page.getByRole("tab", { name: "Networks" }).click();
  const networks = page.getByRole("region", { name: "Network mappings" });
  const ranged = networks.getByRole("row", { name: /dvpg-db-prd-vl230.*rvav IP ranges/ });
  await expect(ranged).toContainText("No DNS records");
  await expect(networks.getByRole("row", { name: /dvpg-db-prd-vl230.*EfficientIP/ })).toContainText(
    "Not used in vc01.corp.example: rvav's IP ranges give its addresses there.",
  );
  await toastsGone(page);
  await check("the networks with their source");
  await ranged.getByRole("link", { name: "rvav IP ranges" }).click();
  await expect(ranges).toBeVisible();

  const reconcile = page.getByRole("region", { name: "Reconcile the IP ranges" });
  await reconcile.getByRole("button", { name: "Run the check" }).click();
  const orphans = reconcile.getByRole("region", { name: "Addresses of VMs rvav no longer has" });
  await expect(orphans.getByRole("row", { name: /10\.41\.10\.205/ })).toContainText("web-tst-lab009");
  const strays = reconcile.getByRole("region", { name: "VMs on an address their range didn't give" });
  await expect(strays.getByRole("row", { name: /web-tst-old01/ })).toContainText("web-tst-lab002");
  // Said as it ends, the check's button keeping the focus.
  const again = reconcile.getByRole("button", { name: "Check again" });
  await expect(reconcile.getByRole("status")).toHaveText("Checked: 3 to reconcile.");
  await expect(again).toBeFocused();
  await check("the reconcile report");
  await orphans.getByRole("button", { name: "Actions for 10.41.10.205" }).click();
  await page.getByRole("menuitem", { name: "Give back" }).click();
  const confirm = page.getByRole("alertdialog", { name: "Give back 10.41.10.205?" });
  await check("an address given back");
  await confirm.getByRole("button", { name: "Give back address" }).click();
  await expect(page.getByText("10.41.10.205 given back")).toBeVisible();
  await expect(orphans).toBeHidden();
  // Its list gone with it, the focus goes back to the check.
  await expect(again).toBeFocused();
  await expect(reconcile.getByRole("status")).toHaveText("Checked: 2 to reconcile.");
  await expect(lab).toContainText("30 free");

  await toastsGone(page);
  await strays.getByRole("button", { name: "Actions for lab-tst-build01" }).click();
  await page.getByRole("menuitem", { name: "Exclude from Test lab" }).click();
  await expect(page.getByText("10.41.10.230 excluded from Test lab")).toBeVisible();
  await expect(strays.getByRole("row", { name: /lab-tst-build01/ })).toBeHidden();
  // The row now at its place has nothing to focus: the list takes the focus, not the page.
  await expect(strays.locator("[data-table]")).toBeFocused();
  await expect(lab).toContainText("30 free");
  await expect(lab).toContainText("3 excluded");
}
