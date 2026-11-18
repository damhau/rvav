import { expect, type Page, test } from "@playwright/test";
import { bothThemes, scan, theme } from "./axe";

// A deployment's topology and its VMs' properties (X1.1, ADR-0032): the
// Topology tab draws what the deployment holds, and a VM's node, or its
// name on the Overview, opens its properties in a side panel.

async function sheetInBothThemes(page: Page, open: () => Promise<void>, where: string) {
  for (const name of ["Light", "Dark"] as const) {
    await theme(page, name);
    await open();
    await expect(page.getByRole("dialog")).toBeVisible();
    await scan(page, `${where}, ${name.toLowerCase()}`);
    await page.keyboard.press("Escape");
    await expect(page.getByRole("dialog")).toHaveCount(0);
  }
  await theme(page, "Light");
}

test("a deployment's topology opens a VM's properties, by click and by keyboard", async ({ page }) => {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/deployments");
  await page.getByRole("link", { name: "web-tst-search", exact: true }).click();
  await page.getByRole("tab", { name: "Topology" }).click();
  await expect(page).toHaveURL(/\?tab=topology$/);

  // What it holds: its VMs, the segment they are on, the groups they are in and the rules into those.
  const topology = page.getByRole("region", { name: "Topology of web-tst-search" });
  const node = (name: RegExp) => topology.getByRole("button", { name });
  await expect(node(/^Segment rvav-.*-app\. Subnet: 10\.41\.10\.0\/26\./)).toBeVisible();
  await expect(node(/^web-tst-search001\. Power: On\. Address: 10\.41\.10\.\d+\./)).toBeVisible();
  await expect(node(/^web-tst-search002\..*1 problem: Its NSX tags changed outside rvav\./)).toBeVisible();
  await expect(node(/^Group Search\..*Allows HTTPS from Any\. Allows SSH from grp-backup-clients$/)).toBeVisible();
  await bothThemes(page, "Topology tab");

  // By click: its properties, with its link to the vSphere Client.
  await node(/^web-tst-search002\./).click();
  const sheet = page.getByRole("dialog", { name: "web-tst-search002" });
  await expect(sheet.getByRole("region", { name: "Placement" })).toContainText("rvav/tst");
  await expect(sheet.getByRole("link", { name: /^Open web-tst-search002 in the vSphere Client/ })).toHaveAttribute(
    "href",
    /^https:\/\/vc01\.corp\.example\/ui\/app\/vm;nav=h\/urn:vmomi:VirtualMachine:vm-\d+:[0-9a-f-]+\/summary$/,
  );
  await page.keyboard.press("Escape");
  await expect(page.getByRole("region", { name: "Selected: web-tst-search002" })).toBeVisible();

  // By keyboard: the node last visited is the graph's Tab stop, Home goes to the first, Enter opens it.
  await node(/^web-tst-search002\./).focus();
  await page.keyboard.press("Home");
  const first = node(/^web-tst-search001\./);
  await expect(first).toBeFocused();
  await page.keyboard.press("Enter");
  await expect(page.getByRole("dialog", { name: "web-tst-search001" })).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(first).toBeFocused();

  await sheetInBothThemes(
    page,
    async () => {
      await first.focus();
      await page.keyboard.press("Enter");
    },
    "VM properties from the topology",
  );

  // From the Overview's list of VMs, by keyboard.
  await page.getByRole("tab", { name: "Overview" }).click();
  const name = page.getByRole("button", { name: "web-tst-search001 properties" });
  await name.focus();
  await page.keyboard.press("Enter");
  await expect(page.getByRole("dialog", { name: "web-tst-search001" })).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(name).toBeFocused();
  expect(errors).toEqual([]);
});
