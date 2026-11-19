import { expect, test } from "@playwright/test";

// Day-2 actions against the mock backend: each runs as a request, the VM
// shows the result, and the deployment's history lists it.

test("power off a VM, snapshot it, then delete the deployment", async ({ page }) => {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));

  await page.goto("/deployments");
  await page.getByRole("link", { name: "web-dev-api", exact: true }).click();
  await expect(page.getByRole("heading", { name: "web-dev-api" })).toBeVisible();

  // Power off from the VM's menu: a confirmation first.
  await page.getByRole("button", { name: "More actions for web-dev-api004" }).click();
  await page.getByRole("menuitem", { name: /^Power off/ }).click();
  const dialog = page.getByRole("dialog", { name: "Power off · web-dev-api004" });
  await expect(dialog.getByText(/like pulling the plug/)).toBeVisible();
  await dialog.getByRole("button", { name: "Power off" }).click();
  await expect(dialog).toBeHidden();
  await expect(page.getByText(/^REQ-\d+ has started\.$/)).toBeVisible();
  await expect(page.getByText(/vm-\d+ · off/)).toBeVisible({ timeout: 15_000 });
  // Now it can be powered on, not off.
  await page.getByRole("button", { name: "More actions for web-dev-api004" }).click();
  await expect(page.getByRole("menuitem", { name: /^Power on/ })).toBeEnabled();
  await expect(page.getByRole("menuitem", { name: /^Power off/ })).toHaveAttribute("data-disabled", "");
  await page.keyboard.press("Escape");

  // A snapshot, from the deployment's Actions menu, with its form.
  await page.getByRole("button", { name: "Actions", exact: true }).click();
  await page.getByRole("menuitem", { name: /^Take a snapshot/ }).click();
  const snap = page.getByRole("dialog", { name: "Take a snapshot · web-dev-api004" });
  await snap.getByRole("textbox", { name: "Name" }).fill("before-cleanup");
  await snap.getByRole("button", { name: "Take a snapshot" }).click();
  await expect(snap).toBeHidden();

  // The history lists both, and the creating request.
  const sections = page.getByRole("tablist", { name: "Deployment sections" });
  await sections.getByRole("tab", { name: "History" }).click();
  await expect(page).toHaveURL(/\?tab=history$/);
  const history = page.getByRole("region", { name: "History" });
  await expect(history.getByRole("link", { name: "Power off" })).toBeVisible();
  await expect(history.getByRole("link", { name: "Take a snapshot" })).toBeVisible();
  await expect(history.getByText("Succeeded").first()).toBeVisible({ timeout: 15_000 });

  // Delete asks for the name.
  await sections.getByRole("tab", { name: "Overview" }).click();
  await page.getByRole("button", { name: "Actions", exact: true }).click();
  await page.getByRole("menuitem", { name: "Delete", exact: true }).click();
  const del = page.getByRole("dialog", { name: "Delete · web-dev-api" });
  const confirm = del.getByRole("button", { name: "Delete" });
  await expect(confirm).toBeDisabled();
  await del.getByRole("textbox", { name: /to confirm/ }).fill("web-dev-api");
  await confirm.click();
  await expect(page.getByText("Deleted", { exact: true }).first()).toBeVisible({ timeout: 15_000 });
  expect(errors).toEqual([]);
});

test("shut down the guests of two deployments at once", async ({ page }) => {
  await page.goto("/deployments");
  await page.getByRole("checkbox", { name: "Select web-dev-api" }).check();
  await page.getByRole("checkbox", { name: "Select web-prd-cache" }).check();
  const bar = page.getByRole("region", { name: "Bulk actions" });
  await expect(bar.getByText("2 deployments selected")).toBeVisible();
  await bar.getByRole("button", { name: "Shut down guest" }).click();
  const dialog = page.getByRole("dialog", { name: "Shut down guest · 2 deployments" });
  await dialog.getByRole("button", { name: "Shut down guest" }).click();
  const results = dialog.getByRole("list", { name: "Results" });
  await expect(results.getByText(/REQ-\d+ started/)).toHaveCount(3);
  await dialog.getByRole("button", { name: "Close", exact: true }).last().click();
  await expect(bar).toBeHidden();
});

test("extend a lease, and see that a longer one needs approval", async ({ page }) => {
  await page.goto("/deployments");
  await page.getByRole("link", { name: "web-dev-api", exact: true }).click();
  const lease = page.getByRole("region", { name: "Lease" });
  await expect(lease.getByText(/1 of 2 extensions used; up to 90 days without approval/)).toBeVisible();
  await lease.getByRole("button", { name: "Extend the lease" }).click();
  const dialog = page.getByRole("dialog", { name: "Extend the lease · web-dev-api" });
  const days = dialog.getByRole("spinbutton", { name: "Days from now" });
  await expect(days).toHaveValue("30");
  await days.fill("45");
  await dialog.getByRole("button", { name: "Extend the lease" }).click();
  await expect(dialog).toBeHidden();
  await expect(lease.getByText(/2 of 2 extensions used/)).toBeVisible({ timeout: 15_000 });
  // Both used: the next one asks the project admins.
  await lease.getByRole("button", { name: "Extend the lease" }).click();
  const next = page.getByRole("dialog", { name: "Extend the lease · web-dev-api" });
  await expect(next.getByText(/Needs approval: All 2 extensions without approval are used/)).toBeVisible();
  await expect(next.getByRole("button", { name: "Ask for approval" })).toBeVisible();
});

test("run an AWX action on a VM", async ({ page }) => {
  await page.goto("/deployments");
  await page.getByRole("link", { name: "web-dev-api", exact: true }).click();
  await page.getByRole("button", { name: "More actions for web-dev-api004" }).click();
  await expect(page.getByText("Automation", { exact: true })).toBeVisible();
  await page.getByRole("menuitem", { name: /^Install monitoring agent/ }).click();
  const dialog = page.getByRole("dialog", { name: "Install monitoring agent · web-dev-api004" });
  await expect(dialog.getByRole("spinbutton", { name: "Port" })).toHaveValue("9100");
  await dialog.getByRole("button", { name: "Install monitoring agent" }).click();
  await expect(dialog).toBeHidden();
  await expect(page.getByText(/^REQ-\d+ has started\.$/)).toBeVisible();
});

test("a VM changed outside rvav: what differs, then accepted (S13.2)", async ({ page }) => {
  await page.goto("/deployments");
  await page.getByRole("link", { name: "web-dev-legacy", exact: true }).click();
  await expect(page.getByRole("heading", { name: "web-dev-legacy" })).toBeVisible();
  await expect(page.getByText("Changed outside rvav", { exact: true }).first()).toBeVisible();

  const drift = page.getByRole("region", { name: "Changed outside rvav" });
  const table = drift.getByRole("table", { name: "How web-dev-legacy001 differs" });
  await expect(table.getByRole("row", { name: "Memory 16 GB 32 GB" })).toBeVisible();
  await expect(table.getByRole("row", { name: "Hard disk 2 none 200 GB" })).toBeVisible();

  // No form and nothing lost: it runs at once, as a request.
  await drift.getByRole("button", { name: "Accept the changes" }).click();
  await expect(page.getByText(/^REQ-\d+ has started\.$/)).toBeVisible();
  await expect(drift).toBeHidden({ timeout: 15_000 });
  await expect(page.getByText("Changed outside rvav", { exact: true })).toHaveCount(0);
});

test("suspend a VM and resume it, then add a disk and remove it (X1.3)", async ({ page }) => {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/deployments");
  await page.getByRole("link", { name: "web-dev-api", exact: true }).click();
  await expect(page.getByRole("heading", { name: "web-dev-api" })).toBeVisible();
  const more = page.getByRole("button", { name: "More actions for web-dev-api004" });

  // Suspended, its hardware can't change; power on resumes it.
  await more.click();
  await page.getByRole("menuitem", { name: /^Suspend/ }).click();
  const suspend = page.getByRole("dialog", { name: "Suspend · web-dev-api004" });
  await expect(suspend.getByText(/Power on resumes it/)).toBeVisible();
  await suspend.getByRole("button", { name: "Suspend" }).click();
  await expect(page.getByText(/vm-\d+ · suspended/)).toBeVisible({ timeout: 15_000 });
  await more.click();
  await expect(page.getByRole("menuitem", { name: /^Resize/ })).toHaveAttribute("data-disabled", "");
  await expect(page.getByText("The VM is suspended: power it on or off first.").first()).toBeVisible();
  await page.getByRole("menuitem", { name: /^Power on/ }).click();
  await expect(page.getByText(/vm-\d+ · on/)).toBeVisible({ timeout: 15_000 });

  // A disk added, then removed; the boot disk isn't offered.
  await page.goto("/deployments");
  await page.getByRole("link", { name: "web-dev-legacy", exact: true }).click();
  await expect(page.getByText(/· 320 GB$/)).toBeVisible();
  const legacy = page.getByRole("button", { name: "More actions for web-dev-legacy001" });
  await legacy.click();
  await page.getByRole("menuitem", { name: /^Add a disk/ }).click();
  const add = page.getByRole("dialog", { name: "Add a disk · web-dev-legacy001" });
  await add.getByRole("combobox", { name: "Datastore" }).click();
  await page.getByRole("option", { name: /^nfs-gva-bronze-01/ }).click();
  await add.getByRole("button", { name: "Add a disk" }).click();
  await expect(add).toBeHidden();
  await expect(page.getByText(/· 370 GB$/)).toBeVisible({ timeout: 15_000 });

  await legacy.click();
  await page.getByRole("menuitem", { name: /^Remove a disk/ }).click();
  const remove = page.getByRole("dialog", { name: "Remove a disk · web-dev-legacy001" });
  await expect(remove.getByText(/deleted from the datastore/)).toBeVisible();
  await remove.getByRole("combobox", { name: "Disk" }).click();
  await expect(page.getByRole("option", { name: /Hard disk 1/ })).toHaveCount(0);
  const third = page.getByRole("option", { name: "Hard disk 3", exact: true });
  await expect(third).toContainText("50 GB on nfs-gva-bronze-01");
  await third.click();
  await remove.getByRole("button", { name: "Remove a disk" }).click();
  await expect(remove).toBeHidden();
  await expect(page.getByText(/· 320 GB$/)).toBeVisible({ timeout: 15_000 });
  expect(errors).toEqual([]);
});

test("edit a VM's vSphere tags (X1.3)", async ({ page }) => {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/deployments");
  await page.getByRole("link", { name: "web-dev-api", exact: true }).click();
  await expect(page.getByRole("heading", { name: "web-dev-api" })).toBeVisible();
  await page.getByRole("button", { name: "More actions for web-dev-api004" }).click();
  await page.getByRole("menuitem", { name: /^Edit tags/ }).click();
  const edit = page.getByRole("dialog", { name: "Edit tags · web-dev-api004" });
  await expect(edit.getByText("Tags of other categories stay as they are.")).toBeVisible();
  await expect(edit.getByRole("checkbox", { name: "backup/daily" })).not.toBeChecked();
  await edit.getByRole("checkbox", { name: "backup/weekly" }).check();
  await edit.getByRole("button", { name: "Edit tags" }).click();
  await expect(edit).toBeHidden();

  await page.getByRole("button", { name: "web-dev-api004 properties" }).click();
  const props = page.getByRole("dialog", { name: "web-dev-api004" });
  await expect(props.getByRole("region", { name: "vSphere tags" })).toHaveText(/backup\/weekly/, { timeout: 15_000 });
  expect(errors).toEqual([]);
});

test("unregister a VM, then its deployment (X1.3)", async ({ page }) => {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/deployments");
  await page.getByRole("link", { name: "web-prd-cache", exact: true }).click();
  await expect(page.getByRole("heading", { name: "web-prd-cache" })).toBeVisible();

  await page.getByRole("button", { name: "More actions for web-prd-cache001" }).click();
  await page.getByRole("menuitem", { name: /^Unregister/ }).click();
  const vm = page.getByRole("dialog", { name: "Unregister · web-prd-cache001" });
  await expect(vm.getByText(/it stays in vCenter as it is/)).toBeVisible();
  await vm.getByRole("button", { name: "Unregister" }).click();
  await expect(vm).toBeHidden();
  await expect(page.getByRole("button", { name: "More actions for web-prd-cache001" })).toHaveCount(0, {
    timeout: 15_000,
  });

  // Its only VM now goes with the deployment.
  await page.getByRole("button", { name: "More actions for web-prd-cache002" }).click();
  await expect(page.getByRole("menuitem", { name: /^Unregister/ })).toHaveCount(0);
  await page.keyboard.press("Escape");
  await page.getByRole("button", { name: "Actions", exact: true }).click();
  await page.getByRole("menuitem", { name: /^Unregister/ }).click();
  const dep = page.getByRole("dialog", { name: "Unregister · web-prd-cache" });
  await expect(dep.getByText(/its VMs stay in vCenter as they are/)).toBeVisible();
  await dep.getByRole("button", { name: "Unregister" }).click();
  await expect(page.getByText("Deleted", { exact: true }).first()).toBeVisible({ timeout: 15_000 });
  expect(errors).toEqual([]);
});

test("an auditor is offered no action on a VM, not even its console", async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem("rvav.mock.as", "auditor"));
  await page.goto("/deployments");
  await page.getByRole("link", { name: "web-dev-api", exact: true }).click();
  await expect(page.getByRole("heading", { name: "web-dev-api" })).toBeVisible();
  await expect(page.getByText(/vm-\d+ · on/).first()).toBeVisible();
  await expect(page.getByRole("button", { name: "Power off web-dev-api004" })).toBeDisabled();
  await expect(page.getByRole("button", { name: "Open the console of web-dev-api004" })).toBeDisabled();
  await expect(page.getByRole("link", { name: "Open the console of web-dev-api004" })).toHaveCount(0);
  await page.getByRole("button", { name: "More actions for web-dev-api004" }).click();
  await expect(page.getByRole("menuitem", { name: "Open the console" })).toHaveAttribute("data-disabled", "");
});
