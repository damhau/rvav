import { expect, test, type Page } from "@playwright/test";

// Projects, placement, quotas, policies, templates, sizes and networks on
// the mock backend (S9.9, ADR-0020). A reload resets the mock database, so
// each flow moves around in the app.

async function open(page: Page, link: string, heading: string) {
  await page.goto("/");
  await page.getByRole("navigation").getByRole("link", { name: link }).click();
  await expect(page.getByRole("heading", { name: heading, level: 1 })).toBeVisible();
}

async function choose(page: Page, combobox: string, option: string) {
  await page.getByRole("combobox", { name: combobox, exact: true }).click();
  await page.getByRole("option", { name: option, exact: true }).click();
}

test("set a project up: create it, add a member, place it and give it quotas", async ({ page }) => {
  await open(page, "Projects", "Projects");
  await page.getByRole("button", { name: "New project" }).click();
  const dialog = page.getByRole("dialog", { name: "New project" });
  await dialog.getByRole("textbox", { name: "Code" }).fill("Pay");
  await dialog.getByRole("textbox", { name: "Name" }).fill("Payments");
  await dialog.getByRole("checkbox", { name: "Test" }).check();
  await dialog.getByRole("button", { name: "Create" }).click();
  await expect(dialog.getByText("2 to 8 lower-case letters")).toBeVisible();
  await dialog.getByRole("textbox", { name: "Code" }).fill("pay");
  await dialog.getByRole("textbox", { name: "Project admins" }).fill("Sophie");
  await dialog.getByRole("button", { name: /Sophie Brunner/ }).click();
  await expect(dialog.getByRole("list", { name: "Chosen project admins" })).toContainText("Sophie Brunner");
  await dialog.getByRole("button", { name: "Create" }).click();
  await expect(page.getByRole("heading", { name: "Payments", level: 1 })).toBeVisible();

  // Members: the only project admin can't be removed.
  const members = page.getByRole("region", { name: "Members" });
  await expect(members.getByRole("row", { name: /Sophie Brunner/ })).toBeVisible();
  await members.getByRole("textbox", { name: "Add someone" }).fill("Thomas");
  await members.getByRole("button", { name: /Thomas Nguyen/ }).click();
  await expect(page.getByText("Thomas Nguyen is a member")).toBeVisible();
  await members.getByRole("button", { name: "Remove Sophie Brunner" }).click();
  await expect(page.getByText(/would have no project admin/)).toBeVisible();

  // Placement: nothing until a rule names networks, then the preview fills.
  await page.getByRole("tab", { name: "Placement" }).click();
  await expect(page.getByText("No placement rule for Payments in dev")).toBeVisible();
  await page.getByRole("button", { name: "New rule" }).click();
  const rule = page.getByRole("dialog", { name: /New placement rule/ });
  await rule.getByRole("button", { name: "Save rule" }).click();
  await expect(rule.getByText("Name the networks VMs may connect to.")).toBeVisible();
  await rule.getByRole("textbox", { name: "Networks" }).fill("dvpg-*-dev-*");
  await rule.getByRole("button", { name: "Save rule" }).click();
  await expect(page.getByText("Placement rule saved")).toBeVisible();
  const preview = page.getByRole("region", { name: "What Payments gets in dev" });
  await expect(preview).toContainText("GVA-DEV-CL01");
  await expect(preview).toContainText("dvpg-app-dev-vl310 10.31.10.0/24");

  // Quotas.
  await page.getByRole("tab", { name: "Quotas" }).click();
  await page.getByRole("button", { name: "Add a limit" }).click();
  await page.getByRole("spinbutton", { name: "Limit 1: value" }).fill("16");
  await choose(page, "Limit 1: environment", "dev");
  await page.getByRole("button", { name: "Save quotas" }).click();
  await expect(page.getByText("Quotas saved")).toBeVisible();

  // Archive it: the list hides it until archived ones are asked for.
  page.on("dialog", (d) => void d.accept());
  await page.getByRole("tab", { name: "Details" }).click();
  await page.getByRole("button", { name: "Archive" }).click();
  await expect(page.getByText("Payments archived")).toBeVisible();
  await page.getByRole("link", { name: "Projects" }).first().click();
  const list = page.getByRole("region", { name: "Projects" });
  await expect(list.getByRole("link", { name: "Web Platform" })).toBeVisible();
  await expect(list.getByRole("link", { name: "Payments" })).toBeHidden();
  await list.getByRole("checkbox", { name: "Archived ones too" }).check();
  await expect(list.getByRole("link", { name: "Payments" })).toBeVisible();
});

test("environments and OIDC group mappings", async ({ page }) => {
  await open(page, "Projects", "Projects");
  const envs = page.getByRole("region", { name: "Environments" });
  await envs.getByRole("textbox", { name: "New environment key" }).fill("qa");
  await envs.getByRole("textbox", { name: "New environment name" }).fill("Quality");
  await envs.getByRole("button", { name: "Add environment" }).click();
  await expect(envs.getByRole("row", { name: /Quality/ })).toBeVisible();
  await expect(envs.getByRole("button", { name: "Remove Production" })).toBeDisabled();
  await envs.getByRole("button", { name: "Remove Quality" }).click();
  await expect(envs.getByRole("row", { name: /Quality/ })).toBeHidden();

  const groups = page.getByRole("region", { name: "OIDC group mappings" });
  await groups.getByRole("textbox", { name: "OIDC group" }).fill("rvav-auditors");
  await choose(page, "Role", "Auditor");
  await groups.getByRole("button", { name: "Map group" }).click();
  await expect(groups.getByRole("cell", { name: "rvav-auditors", exact: true })).toBeVisible();
  await groups.getByRole("button", { name: "Remove the mapping of rvav-admins" }).click();
  await expect(page.getByText(/Nobody would be an administrator/)).toBeVisible();
});

test("approval, day-2, lease and naming policies, and what applies", async ({ page }) => {
  page.on("dialog", (d) => void d.accept());
  await open(page, "Policies", "Policies");

  // Approval: a broken condition is caught when saved, under its field; so is
  // escalating to nobody. Moving policies works on tied priorities too.
  await page.getByRole("button", { name: "New approval policy" }).click();
  const ap = page.getByRole("dialog", { name: "New approval policy" });
  await ap.getByRole("textbox", { name: "Name" }).fill("Big VMs");
  await ap.getByRole("textbox", { name: "Applies when (CEL)" }).fill("inputs.cpu_count >");
  await ap.getByRole("textbox", { name: "OIDC group" }).fill("rvav-capacity");
  await ap.getByRole("button", { name: "Save policy" }).click();
  await expect(ap.getByText(/not valid CEL/)).toBeVisible();
  await expect(ap.getByRole("textbox", { name: "Applies when (CEL)" })).toHaveAttribute("aria-invalid", "true");
  await ap.getByRole("textbox", { name: "Applies when (CEL)" }).fill("inputs.cpu_count > 8");
  await expect(ap.getByRole("combobox", { name: "Then" })).toBeDisabled();
  await ap.getByRole("textbox", { name: "Time-out" }).fill("48h");
  await choose(page, "Then", "Escalate");
  await ap.getByRole("button", { name: "Save policy" }).click();
  await expect(ap.getByText(/Say whom to escalate to/)).toBeVisible();
  await ap.getByRole("textbox", { name: "Escalate to OIDC group" }).fill("rvav-infra-heads");
  await ap.getByRole("button", { name: "Save policy" }).click();
  await expect(page.getByText("Big VMs saved")).toBeVisible();
  const approvals = page.getByRole("region", { name: "Approval policies" });
  // Both at priority 100: moving the first down still swaps them.
  await expect(approvals.getByRole("row").nth(1)).toContainText("Production VMs");
  await approvals.getByRole("button", { name: "Move Production VMs down" }).click();
  await expect(approvals.getByRole("row").nth(1)).toContainText("Big VMs");
  await expect(approvals.getByRole("row").nth(2)).toContainText("Production VMs");
  await approvals.getByRole("button", { name: "Move Production VMs up" }).click();
  await expect(approvals.getByRole("row").nth(1)).toContainText("Production VMs");

  // Day-2: the first policy that matches decides.
  await page.getByRole("tab", { name: "Day-2" }).click();
  const day2 = page.getByRole("region", { name: "Day-2 policies" });
  await expect(day2.getByRole("row").nth(1)).toContainText("Administrators");
  await day2.getByRole("button", { name: "Move Members operate their VMs up" }).click();
  await expect(day2.getByRole("row").nth(2)).toContainText("Members operate their VMs");

  // Lease: a project override, then removed.
  await page.getByRole("tab", { name: "Lease" }).click();
  await page.getByRole("button", { name: "New lease policy" }).click();
  const lease = page.getByRole("dialog", { name: "New lease policy" });
  await choose(page, "Project", "Web Platform");
  await lease.getByRole("spinbutton", { name: "Lease, in days" }).fill("120");
  await lease.getByRole("button", { name: "Save lease policy" }).click();
  await expect(lease.getByText("The default can't be longer than the maximum.")).toBeVisible();
  await lease.getByRole("spinbutton", { name: "Lease, in days" }).fill("14");
  await lease.getByRole("button", { name: "Save lease policy" }).click();
  await expect(page.getByText("Lease policy saved")).toBeVisible();
  const leases = page.getByRole("region", { name: "Lease policies" });
  await expect(leases.getByRole("button", { name: "Remove the lease in dev", exact: true })).toBeHidden();
  await leases.getByRole("button", { name: "Remove the lease in dev, Web Platform" }).click();
  await expect(leases.getByRole("row", { name: /Web Platform/ })).toBeHidden();

  // Naming: preview the next names, then save.
  await page.getByRole("tab", { name: "Naming" }).click();
  await page.getByRole("button", { name: "New naming policy" }).click();
  const naming = page.getByRole("dialog", { name: "New naming policy" });
  await naming.getByRole("textbox", { name: "Name" }).fill("Web names");
  await naming.getByRole("button", { name: "Preview names" }).click();
  await expect(naming.getByText("web-dev-app002, web-dev-app003, web-dev-app004")).toBeVisible();
  await expect(naming.getByText("taken in vcenter")).toBeVisible();
  await naming.getByRole("button", { name: "Add an offer" }).click();
  await naming.getByRole("button", { name: "Save naming policy" }).click();
  await expect(
    page.getByRole("region", { name: "Naming policies" }).getByRole("cell", { name: "Web names", exact: true }),
  ).toBeVisible();

  // What applies to a delete in production.
  await page.getByRole("tab", { name: "What applies here" }).click();
  await choose(page, "Environment", "Production");
  await choose(page, "Day-2 action", "delete");
  await page.getByRole("button", { name: "Explain" }).click();
  // An administrator: the first policy that matches allows it.
  await expect(page.getByRole("region", { name: "Day-2" })).toContainText("delete: allow");
  await expect(page.getByRole("region", { name: "Day-2" })).toContainText("Decided by: Administrators");
  await expect(page.getByRole("region", { name: "Naming" })).toContainText("Web names");
});

test("templates, sizes and network mappings", async ({ page }) => {
  await open(page, "Templates and networks", "Templates, sizes and networks");
  await page.getByRole("button", { name: "New template" }).click();
  const tpl = page.getByRole("dialog", { name: "New VM template" });
  await tpl.getByRole("textbox", { name: "Key" }).fill("ubuntu24");
  await tpl.getByRole("textbox", { name: "Label" }).fill("Ubuntu 24.04");
  await tpl.getByRole("textbox", { name: "vCenter template" }).fill("library:ubuntu-24.04");
  await tpl.getByRole("button", { name: "Save template" }).click();
  await expect(tpl.getByText(/its library in Content library/)).toBeVisible();
  // An item of a content library (S12.5).
  await tpl.getByRole("textbox", { name: "vCenter template" }).fill("ubuntu-24.04");
  await tpl.getByRole("textbox", { name: "Content library" }).fill("rvav-templates");
  await tpl.getByRole("button", { name: "Save template" }).click();
  const templates = page.getByRole("region", { name: "VM templates" });
  await expect(templates.getByRole("row", { name: /Ubuntu 24.04/ })).toContainText("rvav-templates / ubuntu-24.04");

  await page.getByRole("tab", { name: "Sizes" }).click();
  await page.getByRole("button", { name: "Change Medium" }).click();
  const size = page.getByRole("dialog", { name: "Change Medium" });
  await size.getByRole("spinbutton", { name: "Memory (GB)" }).fill("12");
  await size.getByRole("button", { name: "Save size" }).click();
  await expect(page.getByRole("region", { name: "Sizes" })).toContainText("4 vCPU, 12 GB, 80 GB");

  await page.getByRole("tab", { name: "Networks" }).click();
  await page.getByRole("button", { name: "Map a network" }).click();
  const map = page.getByRole("dialog", { name: "Map a network" });
  await map.getByRole("textbox", { name: "vCenter network" }).fill("dvpg-lab-dev-vl999");
  await map.getByRole("textbox", { name: "Subnet" }).fill("10.99.0");
  await map.getByRole("textbox", { name: "DNS zone" }).fill("lab.corp.example");
  await map.getByRole("button", { name: "Save mapping" }).click();
  await expect(map.getByText("A subnet such as 10.31.10.0/24.")).toBeVisible();
  await map.getByRole("textbox", { name: "Subnet" }).fill("10.99.0.0/24");
  await map.getByRole("textbox", { name: "Gateway" }).fill("10.99.0.1");
  await map.getByRole("button", { name: "Save mapping" }).click();
  const networks = page.getByRole("region", { name: "Network mappings" });
  await expect(networks.getByRole("row", { name: /dvpg-lab-dev-vl999/ })).toContainText("By hand");
  await networks.getByRole("button", { name: "Remove the mapping of dvpg-lab-dev-vl999" }).click();
  await expect(networks.getByRole("row", { name: /dvpg-lab-dev-vl999/ })).toBeHidden();
});

test("the administration pages work from the keyboard", async ({ page }) => {
  await open(page, "Policies", "Policies");
  const tabs = page.getByRole("tablist", { name: "Kinds of policy" });
  await tabs.getByRole("tab", { name: "Approval" }).focus();
  await page.keyboard.press("ArrowRight");
  await expect(tabs.getByRole("tab", { name: "Day-2" })).toBeFocused();
  await expect(page.getByRole("region", { name: "Day-2 policies" })).toBeVisible();
  await page.getByRole("button", { name: "New day-2 policy" }).focus();
  await page.keyboard.press("Enter");
  const dialog = page.getByRole("dialog", { name: "New day-2 policy" });
  await expect(dialog.getByRole("button", { name: "Close" })).toBeFocused();
  await page.keyboard.press("Tab");
  await expect(dialog.getByRole("textbox", { name: "Name" })).toBeFocused();
  await page.keyboard.type("Snapshots");
  await page.keyboard.press("Escape");
  await expect(dialog).toBeHidden();
  await expect(page.getByRole("button", { name: "New day-2 policy" })).toBeFocused();
});

test("integration settings: locked fields, the write-only secret, test connection and the restart notice", async ({
  page,
}) => {
  await open(page, "Integrations", "Integrations");
  const vcenter = page.getByRole("region", { name: "vCenter connection" });
  await expect(vcenter.getByRole("textbox", { name: "URL" })).toBeDisabled();
  await expect(vcenter.getByText("Set by RVAV_VCENTER_URL")).toBeVisible();
  await expect(vcenter.getByText("Set; type a new one to replace it")).toBeVisible();
  await vcenter.getByRole("textbox", { name: "User" }).fill("svc-rvav2@vsphere.local");
  await vcenter.getByLabel("Password").fill("n3w-secret");
  await vcenter.getByRole("button", { name: "Test connection" }).click();
  const test = page.getByRole("region", { name: "vCenter test" });
  await expect(test).toContainText("Every check passed.");
  await expect(test).toContainText("Tagging service");
  const put = page.waitForRequest((r) => r.method() === "PUT" && r.url().endsWith("/admin/integrations/vcenter"));
  await vcenter.getByRole("button", { name: "Save settings" }).click();
  // The new password is sent once, with the save.
  expect(((await put).postDataJSON() as { secret?: string }).secret).toBe("n3w-secret");
  await expect(page.getByText("vCenter settings saved")).toBeVisible();
  await expect(vcenter.getByRole("status")).toContainText("apply when rvav's processes (API and worker) restart");
  await expect(vcenter.getByLabel("Password")).toHaveValue("");
  await expect(vcenter.getByRole("textbox", { name: "User" })).toHaveValue("svc-rvav2@vsphere.local");

  await page.getByRole("tab", { name: "AWX" }).click();
  const awx = page.getByRole("region", { name: "AWX connection" });
  await awx.getByRole("textbox", { name: "URL" }).fill("https://unreachable.corp.example");
  // The saved token isn't sent to another address: it is asked for again.
  await awx.getByRole("button", { name: "Test connection" }).click();
  await expect(awx.getByText(/needs the password or token again/)).toBeVisible();
  await awx.getByLabel("Token").fill("another-token");
  await awx.getByRole("button", { name: "Test connection" }).click();
  await expect(page.getByRole("region", { name: "AWX test" })).toContainText("A check failed.");
  await expect(page.getByRole("region", { name: "AWX test" })).toContainText("no such host");
  await awx.getByRole("textbox", { name: "URL" }).fill("awx.corp.example");
  await awx.getByRole("button", { name: "Save settings" }).click();
  await expect(awx.getByText("An http or https URL.")).toBeVisible();

  await expect(page.getByRole("button", { name: "Run the check" })).toBeVisible();
});

test("what a project admin and an auditor see", async ({ page }) => {
  // A project admin of Web Platform, not an administrator.
  await page.addInitScript(() => localStorage.setItem("rvav.mock.as", "project-admin"));
  await page.goto("/");
  const nav = page.getByRole("navigation", { name: "Main" });
  await expect(nav.getByRole("link", { name: "Projects", exact: true })).toBeVisible();
  await expect(nav.getByRole("link", { name: "Audit log" })).toBeVisible();
  for (const hidden of ["Policies", "Integrations", "Templates and networks"])
    await expect(nav.getByRole("link", { name: hidden })).toBeHidden();
  await nav.getByRole("link", { name: "Projects", exact: true }).click();
  await page.getByRole("region", { name: "Your projects" }).getByRole("link", { name: "Web Platform" }).click();
  await expect(page.getByRole("textbox", { name: "Add someone" })).toBeVisible();
  await page.getByRole("tab", { name: "Placement" }).click();
  await expect(page.getByRole("region", { name: "What Web Platform gets in dev" })).toContainText("GVA-DEV-CL01");
  await expect(page.getByRole("button", { name: "New rule" })).toBeHidden();
  await page.getByRole("tab", { name: "Quotas" }).click();
  await expect(page.getByRole("button", { name: "Add a limit" })).toBeHidden();
  await page.getByRole("tab", { name: "Details" }).click();
  await expect(page.getByRole("checkbox", { name: "Production" })).toBeDisabled();
});

test("an auditor reads the administration pages without changing them", async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem("rvav.mock.as", "auditor"));
  await open(page, "Policies", "Policies");
  await expect(page.getByRole("region", { name: "Approval policies" })).toContainText("Production VMs");
  await expect(page.getByRole("button", { name: "New approval policy" })).toBeHidden();
  await expect(page.getByRole("navigation", { name: "Main" }).getByRole("link", { name: "Integrations" })).toBeHidden();
  await page.getByRole("navigation", { name: "Main" }).getByRole("link", { name: "Projects", exact: true }).click();
  await expect(
    page.getByRole("region", { name: "Projects" }).getByRole("link", { name: "Web Platform" }),
  ).toBeVisible();
  await expect(page.getByRole("button", { name: "New project" })).toBeHidden();
  const envs = page.getByRole("region", { name: "Environments" });
  await expect(envs.getByRole("checkbox", { name: "Production is production" })).toBeDisabled();
  await expect(envs.getByRole("button", { name: "Add environment" })).toBeHidden();
  await expect(
    page.getByRole("region", { name: "OIDC group mappings" }).getByRole("button", { name: "Map group" }),
  ).toBeHidden();
  await page.getByRole("region", { name: "Projects" }).getByRole("link", { name: "Web Platform" }).click();
  await expect(page.getByRole("textbox", { name: "Add someone" })).toBeHidden();
  await expect(page.getByRole("combobox", { name: /Role of/ })).toHaveCount(0);
});
