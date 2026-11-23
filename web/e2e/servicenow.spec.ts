import { expect, test, type Page } from "@playwright/test";
import { scan } from "./axe";

// ServiceNow on the mock backend (I1.5, ADR-0065): the integration's page
// with the token ServiceNow requests with and the CMDB report, then a request
// ordered in ServiceNow and the deployment it built, each naming the
// requested item. In both themes, on a desktop and on a phone.

const sizes = { desktop: { width: 1440, height: 1000 }, phone: { width: 390, height: 844 } };
const RITM = "https://corp.service-now.com/sc_req_item.do?sys_id=9d385017c611228701d22104cc95c371";

for (const theme of ["light", "dark"] as const) {
  for (const [device, viewport] of Object.entries(sizes)) {
    test(`ServiceNow: token, CMDB report, a request ordered there, ${theme} theme, ${device}`, async ({ page }) => {
      const errors: string[] = [];
      page.on("pageerror", (e) => errors.push(e.message));
      await page.setViewportSize(viewport);
      await page.addInitScript((t) => localStorage.setItem("rvav.theme", t), theme);
      await serviceNowFlow(page, async (where) => {
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

async function serviceNowFlow(page: Page, check: (where: string) => Promise<void>) {
  await page.goto("/admin/integrations/servicenow");
  await expect(page.getByRole("heading", { name: "ServiceNow", level: 1 })).toBeVisible();
  const token = page.getByRole("region", { name: "ServiceNow's token", exact: true });
  await expect(token).toContainText("ServiceNow last called rvav");
  const cmdb = page.getByRole("region", { name: "CMDB", exact: true });
  await expect(cmdb.getByRole("table", { name: "Deployments ServiceNow refused" })).toContainText(
    "Insufficient rights",
  );
  await expect(cmdb.getByRole("table", { name: "CIs rvav no longer has" })).toContainText("web-dev-old001");
  await check("ServiceNow's page");

  await token.getByRole("button", { name: "Create a new token" }).click();
  const renew = page.getByRole("alertdialog", { name: "Create a new token?" });
  await expect(renew).toContainText("The token it has now is revoked at once.");
  await check("asking before a new token");
  await renew.getByRole("button", { name: "Create a new token" }).click();
  const made = token.getByRole("region", { name: "ServiceNow's token made" });
  await expect(made).toContainText("The token made before is revoked.");
  await check("ServiceNow's token made");

  await token.getByRole("button", { name: "Revoke" }).click();
  const ask = page.getByRole("alertdialog", { name: "Revoke ServiceNow's token?" });
  await check("revoking ServiceNow's token");
  await ask.getByRole("button", { name: "Revoke token" }).click();
  await expect(token).toContainText("None made: ServiceNow can't call rvav.");
  // Revoke went with the token: the focus goes to the button that makes the next one.
  await expect(token.getByRole("button", { name: "Create a token" })).toBeFocused();
  await expect(cmdb).toContainText("0 deployments; 1 refused, to be tried again");

  await cmdb.getByRole("button", { name: "Reconcile now" }).click();
  await expect(page.getByText("rvav reconciles within seconds")).toBeVisible();
  await check("ServiceNow reconciled");

  await page.goto("/requests/REQ-10461");
  const header = page.getByRole("heading", { level: 1, name: /web-tst-search002/ }).locator("xpath=ancestor::header");
  await expect(header.getByRole("link", { name: "RITM0010032 (opens in a new tab)" })).toHaveAttribute("href", RITM);
  await expect(header).toContainText("ordered by Claire Dubois for Léa Martin");
  await check("a request ordered in ServiceNow");

  await header.getByRole("link", { name: "Open deployment" }).click();
  const details = page.getByRole("region", { name: "Details", exact: true });
  await expect(details.getByRole("link", { name: "RITM0010032 (opens in a new tab)" })).toHaveAttribute("href", RITM);
  await check("its deployment");
}
