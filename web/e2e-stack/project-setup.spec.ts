import { expect, test, type Page } from "@playwright/test";
import { oidc, signIn } from "./auth";

// S9 acceptance on the real stack (ADR-0020): an administrator sets a new
// project up from the UI (members, placement by vCenter tag, a quota of one
// VM, its own naming policy, a template, the catalog entitlement), then
// requests a VM in it through Temporal, vcsim and the fakes. No SQL, no YAML.
// Marc is an administrator: with dev authentication through the dev user
// header, with Keycloak by signing in as him.
test.use(oidc ? {} : { extraHTTPHeaders: { "X-Rvav-Dev-User": "mkeller" } });

async function nav(page: Page, link: string) {
  await page.getByRole("navigation", { name: "Main" }).getByRole("link", { name: link, exact: true }).click();
}

async function choose(page: Page, combobox: string, option: string) {
  await page.getByRole("combobox", { name: combobox, exact: true }).click();
  await page.getByRole("option", { name: option, exact: true }).click();
}

test("a project set up in the browser requests a VM", async ({ page }) => {
  test.setTimeout(240_000);
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  // A project of its own, so the test can run again on the same stack.
  const suffix = Array.from({ length: 5 }, () => String.fromCharCode(97 + Math.floor(Math.random() * 26))).join("");
  const code = `p${suffix}`;
  const name = `Payments ${suffix}`;

  await page.goto("/admin/projects");
  await signIn(page, "marc");

  // The project, with Marc as its admin, and Thomas as a member.
  await page.getByRole("button", { name: "New project" }).click();
  const create = page.getByRole("dialog", { name: "New project" });
  await create.getByRole("textbox", { name: "Code" }).fill(code);
  await create.getByRole("textbox", { name: "Name" }).fill(name);
  await create.getByRole("textbox", { name: "Project admins" }).fill("Marc");
  await create.getByRole("button", { name: /Marc Keller/ }).click();
  await create.getByRole("button", { name: "Create" }).click();
  await expect(page.getByRole("heading", { name, level: 1 })).toBeVisible();
  const members = page.getByRole("region", { name: "Members" });
  await members.getByRole("textbox", { name: "Add someone" }).fill("Thomas");
  await members
    .getByRole("button", { name: /Thomas Nguyen/ })
    .first()
    .click();
  await expect(members.getByRole("row", { name: /Thomas Nguyen/ })).toBeVisible();

  // Placement: bronze clusters by vCenter tag, the vSAN datastores, the dev app networks.
  await page.getByRole("tab", { name: "Placement" }).click();
  await page.getByRole("button", { name: "New rule" }).click();
  const rule = page.getByRole("dialog", { name: `New placement rule for ${name} in dev` });
  await rule.getByRole("textbox", { name: "Clusters" }).fill("");
  await rule.getByRole("textbox", { name: "Cluster tags" }).fill("rvav-tier/bronze");
  await rule.getByRole("textbox", { name: "Datastores" }).fill("vsan-*");
  await rule.getByRole("textbox", { name: "Networks" }).fill("dvpg-app-dev-*");
  await rule.getByRole("button", { name: "Save rule" }).click();
  const preview = page.getByRole("region", { name: `What ${name} gets in dev` });
  await expect(preview).toContainText("Clusters: GVA-DEV-CL01");
  await expect(preview).toContainText("Datastores: vsan-gva-dev-cl01");
  await expect(preview).toContainText("dvpg-app-dev-vl310 10.31.10.0/24");

  // A quota of one VM in dev.
  await page.getByRole("tab", { name: "Quotas" }).click();
  await page.getByRole("button", { name: "Add a limit" }).click();
  await choose(page, "Limit 1: resource", "VMs");
  await choose(page, "Limit 1: environment", "dev");
  await page.getByRole("spinbutton", { name: "Limit 1: value" }).fill("1");
  await page.getByRole("button", { name: "Save quotas" }).click();
  await expect(page.getByText("Quotas saved")).toBeVisible();

  // Its own naming policy.
  await nav(page, "Policies");
  await page.getByRole("tab", { name: "Naming" }).click();
  await page.getByRole("button", { name: "New naming policy" }).click();
  const naming = page.getByRole("dialog", { name: "New naming policy" });
  await naming.getByRole("textbox", { name: "Name" }).fill(`${name} names`);
  await naming.getByRole("textbox", { name: "Template" }).fill("{project}{environment}{role}{seq:02}");
  await naming.getByRole("button", { name: "Add an offer" }).click();
  await choose(page, "Offer 1: project", name);
  await naming.getByRole("button", { name: "Save naming policy" }).click();
  await expect(page.getByText(`${name} names saved`)).toBeVisible();

  // A template offered to it, found in vCenter.
  await nav(page, "Templates and networks");
  await page.getByRole("button", { name: "New template" }).click();
  const tpl = page.getByRole("dialog", { name: "New VM template" });
  await tpl.getByRole("textbox", { name: "Key" }).fill(`ubuntu-${suffix}`);
  await tpl.getByRole("textbox", { name: "Label" }).fill(`Ubuntu 24.04 ${suffix}`);
  await tpl.getByRole("textbox", { name: "vCenter template" }).fill("tpl-ubuntu24.04-2026.10");
  await choose(page, "Offer 1: project", name);
  await tpl.getByRole("button", { name: "Save template" }).click();
  const row = page
    .getByRole("region", { name: "VM templates" })
    .getByRole("row", { name: new RegExp(`Ubuntu 24.04 ${suffix}`) });
  await expect(row).toContainText(name);
  await expect(row).not.toContainText("Not found");

  // The RHEL 9 server item, open to the project too.
  await nav(page, "Catalog items");
  await page.getByRole("region", { name: "Items" }).getByRole("link", { name: "RHEL 9 server", exact: true }).click();
  await page.getByRole("tab", { name: "Entitlements" }).click();
  await page.getByRole("radio", { name: /Chosen projects/ }).check();
  await page.getByRole("checkbox", { name, exact: true }).click();
  await page.getByRole("button", { name: "Save entitlements" }).click();
  await expect(page.getByText("Entitlements saved")).toBeVisible();

  // In the project: only what the rule allows, the VM named by the policy, built for real.
  await page.getByRole("button", { name: /^Project/ }).click();
  await page.getByRole("menuitem", { name: new RegExp(name) }).click();
  await nav(page, "Catalog");
  await page.getByRole("link", { name: "RHEL 9 server", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Request RHEL 9 server" })).toBeVisible();
  await page.getByRole("textbox", { name: "Role" }).fill("api");
  const vm = `${code}devapi01`;
  await expect(page.locator("#f-vm_name")).toHaveText(new RegExp(vm));
  await page.getByRole("combobox", { name: "Datastore" }).click();
  await expect(page.getByRole("option")).toHaveText([/vsan-gva-dev-cl01/]);
  await page.keyboard.press("Escape");
  await page.getByRole("button", { name: "Submit request" }).click();

  await expect(page).toHaveURL(/\/requests\/REQ-\d+$/);
  const steps = page.getByRole("region", { name: "Steps" });
  await expect(steps.getByText(/Reserved 10\.31\.10\.\d+/)).toBeVisible({ timeout: 30_000 });
  await expect(page.getByText("Succeeded", { exact: true }).first()).toBeVisible({ timeout: 90_000 });
  await page.getByRole("link", { name: "Open deployment" }).click();
  await expect(page.getByText(vm, { exact: true }).first()).toBeVisible();

  // A second VM is over the quota.
  await nav(page, "Catalog");
  await page.getByRole("link", { name: "RHEL 9 server", exact: true }).click();
  await page.getByRole("textbox", { name: "Role" }).fill("web");
  await expect(page.locator("#f-vm_name")).toHaveText(new RegExp(`${code}devweb01`));
  await page.getByRole("button", { name: "Submit request" }).click();
  await expect(page.getByText(new RegExp(`${name} in Development has 0 left`)).first()).toBeVisible();

  // What decided, in the policies' explain view.
  await nav(page, "Policies");
  await page.getByRole("tab", { name: "What applies here" }).click();
  await choose(page, "Project", name);
  await choose(page, "Environment", "Development");
  await page.getByRole("button", { name: "Explain" }).click();
  await expect(page.getByRole("region", { name: "Naming" })).toContainText(`${name} names`);
  expect(errors).toEqual([]);
});
