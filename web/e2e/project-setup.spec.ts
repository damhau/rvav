import { expect, test, type Page } from "@playwright/test";

// The acceptance of slice 9 on the mock backend: a project set up end to
// end from the UI (members, placement, quota, naming, a template and the
// catalog entitlement) requests a VM, without any SQL or YAML. The mock
// follows the placement rules, naming policy and quota set here.

async function nav(page: Page, link: string) {
  await page.getByRole("navigation", { name: "Main" }).getByRole("link", { name: link, exact: true }).click();
}

async function choose(page: Page, combobox: string, option: string) {
  await page.getByRole("combobox", { name: combobox, exact: true }).click();
  await page.getByRole("option", { name: option, exact: true }).click();
}

test("a project set up from the UI requests a VM", async ({ page }) => {
  test.setTimeout(90_000);
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/");

  // The project, with Léa as its admin, and Thomas as a member.
  await nav(page, "Projects");
  await page.getByRole("button", { name: "New project" }).click();
  const create = page.getByRole("dialog", { name: "New project" });
  await create.getByRole("textbox", { name: "Code" }).fill("pay");
  await create.getByRole("textbox", { name: "Name" }).fill("Payments");
  await create.getByRole("textbox", { name: "Cost centre" }).fill("CC-4400");
  await create.getByRole("textbox", { name: "Project admins" }).fill("Léa");
  await create.getByRole("button", { name: /Léa Martin/ }).click();
  await create.getByRole("button", { name: "Create" }).click();
  await expect(page.getByRole("heading", { name: "Payments", level: 1 })).toBeVisible();
  const members = page.getByRole("region", { name: "Members" });
  await members.getByRole("textbox", { name: "Add someone" }).fill("Thomas");
  await members.getByRole("button", { name: /Thomas Nguyen/ }).click();
  await expect(members.getByRole("row", { name: /Thomas Nguyen/ })).toBeVisible();

  // Placement: bronze clusters by vCenter tag, the vSAN datastores, the dev app networks.
  await page.getByRole("tab", { name: "Placement" }).click();
  await page.getByRole("button", { name: "New rule" }).click();
  const rule = page.getByRole("dialog", { name: "New placement rule for Payments in dev" });
  await rule.getByRole("textbox", { name: "Clusters" }).fill("");
  await rule.getByRole("textbox", { name: "Cluster tags" }).fill("rvav-tier/bronze");
  await rule.getByRole("textbox", { name: "Datastores" }).fill("vsan-*");
  await rule.getByRole("textbox", { name: "Networks" }).fill("dvpg-app-dev-*");
  await rule.getByRole("button", { name: "Save rule" }).click();
  const preview = page.getByRole("region", { name: "What Payments gets in dev" });
  await expect(preview).toContainText("Clusters: GVA-DEV-CL01");
  await expect(preview).toContainText("Datastores: vsan-gva-dev-cl01");

  // A quota of one VM.
  await page.getByRole("tab", { name: "Quotas" }).click();
  await page.getByRole("button", { name: "Add a limit" }).click();
  await choose(page, "Limit 1: resource", "VMs");
  await page.getByRole("spinbutton", { name: "Limit 1: value" }).fill("1");
  await page.getByRole("button", { name: "Save quotas" }).click();
  await expect(page.getByText("Quotas saved")).toBeVisible();

  // Its own naming policy.
  await nav(page, "Policies");
  await page.getByRole("tab", { name: "Naming" }).click();
  await page.getByRole("button", { name: "New naming policy" }).click();
  const naming = page.getByRole("dialog", { name: "New naming policy" });
  await naming.getByRole("textbox", { name: "Name" }).fill("Payments names");
  await naming.getByRole("textbox", { name: "Template" }).fill("{project}{environment}{role}{seq:02}");
  await naming.getByRole("button", { name: "Add an offer" }).click();
  await choose(page, "Offer 1: project", "Payments");
  await naming.getByRole("button", { name: "Save naming policy" }).click();
  await expect(page.getByText("Payments names saved")).toBeVisible();

  // A template offered to it.
  await nav(page, "Templates and networks");
  await page.getByRole("button", { name: "New template" }).click();
  const tpl = page.getByRole("dialog", { name: "New VM template" });
  await tpl.getByRole("textbox", { name: "Key" }).fill("ubuntu");
  await tpl.getByRole("textbox", { name: "Label" }).fill("Ubuntu 24.04");
  await tpl.getByRole("textbox", { name: "vCenter template" }).fill("tpl-ubuntu24.04-2026.10");
  await choose(page, "Offer 1: project", "Payments");
  await tpl.getByRole("button", { name: "Save template" }).click();
  await expect(
    page.getByRole("region", { name: "VM templates" }).getByRole("row", { name: /Ubuntu 24.04/ }),
  ).toContainText("Payments");

  // The RHEL 9 server item, open to Payments too.
  await nav(page, "Catalog items");
  await page.getByRole("link", { name: "RHEL 9 server", exact: true }).click();
  await page.getByRole("tab", { name: "Entitlements" }).click();
  await page.getByRole("radio", { name: /Chosen projects/ }).check();
  await page.getByRole("checkbox", { name: "Payments" }).click();
  await page.getByRole("button", { name: "Save entitlements" }).click();
  await expect(page.getByText("Entitlements saved")).toBeVisible();

  // In Payments: the form offers only what the rule allows, and the VM is named by the policy.
  await page.getByRole("button", { name: /^Project/ }).click();
  await page.getByRole("menuitem", { name: /Payments/ }).click();
  await nav(page, "Catalog");
  await page.getByRole("link", { name: "RHEL 9 server", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Request RHEL 9 server" })).toBeVisible();
  await page.getByRole("textbox", { name: "Role" }).fill("api");
  await expect(page.locator("#f-vm_name")).toHaveText(/paydevapi01/);
  await page.getByRole("combobox", { name: "Cluster" }).click();
  await expect(page.getByRole("option")).toHaveText([/GVA-DEV-CL01/]);
  await page.keyboard.press("Escape");
  await page.getByRole("button", { name: "Submit request" }).click();

  await expect(page).toHaveURL(/\/requests\/REQ-\d+$/);
  await expect(page.getByRole("heading", { name: /RHEL 9 server · paydevapi01/ })).toBeVisible();
  await expect(page.getByText("Succeeded", { exact: true }).first()).toBeVisible({ timeout: 30_000 });

  // A second VM is over the quota.
  await nav(page, "Catalog");
  await page.getByRole("link", { name: "RHEL 9 server", exact: true }).click();
  await page.getByRole("textbox", { name: "Role" }).fill("web");
  await expect(page.locator("#f-vm_name")).toHaveText(/paydevweb01/);
  await page.getByRole("button", { name: "Submit request" }).click();
  await expect(page.getByText(/more vm count than Payments has left/).first()).toBeVisible();
  await expect(page).not.toHaveURL(/\/requests\/REQ-/);
  expect(errors).toEqual([]);
});
