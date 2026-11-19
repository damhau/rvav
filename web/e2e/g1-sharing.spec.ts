import { expect, type Locator, type Page, test } from "@playwright/test";
import { bothThemes, scan, theme } from "./axe";

// G1.3 on the mock backend, by keyboard (ADR-0045): Web Platform's project
// admin shares its deployments with their owners only and writes a soft
// day-2 policy of its own; a member then sees only what they own, is
// warned before powering a VM off, and goes on. The mock signs in one
// person: the "member" persona's seed carries what the project admin did.

/** Tab, or Shift+Tab when it comes before the focus, until `target` has the focus. */
async function tabTo(page: Page, target: Locator) {
  await expect(target).toBeVisible();
  const at = () => target.evaluate((e) => e === document.activeElement);
  const back = await target.evaluate((e) => {
    const now = document.activeElement;
    return !!now && now !== document.body && !!(e.compareDocumentPosition(now) & Node.DOCUMENT_POSITION_FOLLOWING);
  });
  for (let i = 0; i < 150 && !(await at()); i++) await page.keyboard.press(back ? "Shift+Tab" : "Tab");
  await expect(target).toBeFocused();
}

async function press(page: Page, target: Locator, key = "Enter") {
  await tabTo(page, target);
  await page.keyboard.press(key);
}

async function typeInto(page: Page, field: Locator, text: string) {
  await tabTo(page, field);
  await page.keyboard.press("Control+A");
  await page.keyboard.press("Delete");
  await page.keyboard.type(text);
}

/** Opens the list with Enter, then the arrow keys to an option, then Enter. */
async function choose(page: Page, select: Locator, name: string) {
  await tabTo(page, select);
  await page.keyboard.press("Enter");
  const option = page.getByRole("option", { name, exact: true });
  await expect(page.getByRole("listbox")).toBeVisible();
  for (let i = 0; i < 20; i++) {
    if (await option.evaluate((e) => e === document.activeElement || e.hasAttribute("data-highlighted"))) {
      await page.keyboard.press("Enter");
      await expect(page.getByRole("listbox")).toHaveCount(0);
      return;
    }
    await page.keyboard.press("ArrowDown");
  }
  throw new Error(`no option ${name}`);
}

/** The tabs are one Tab stop: from the one selected, the arrow keys to `name`. */
async function selectTab(page: Page, name: string) {
  const target = page.getByRole("tab", { name, exact: true });
  const selected = page.getByRole("tab", { selected: true });
  await tabTo(page, selected);
  for (let i = 0; i < 8 && (await target.getAttribute("aria-selected")) !== "true"; i++) {
    const was = (await selected.textContent()) ?? "";
    await page.keyboard.press("ArrowRight");
    // Each key waits for the tab it selects: a key read before it lands goes one too far.
    await expect(selected).not.toHaveText(was);
  }
  await expect(target).toHaveAttribute("aria-selected", "true");
}

/** In an open menu, the arrow keys down to `item`, then Enter. */
async function pick(page: Page, item: Locator) {
  await expect(page.getByRole("menu")).toBeVisible();
  for (let i = 0; i < 30 && !(await item.evaluate((e) => e === document.activeElement)); i++)
    await page.keyboard.press("ArrowDown");
  await expect(item).toBeFocused();
  await page.keyboard.press("Enter");
}

// A fading toast fails axe's contrast check.
async function noToast(page: Page) {
  await page.mouse.move(0, 0);
  await expect(page.locator("[data-sonner-toast]")).toHaveCount(0, { timeout: 10_000 });
}

const nav = (page: Page, name: string) =>
  page.getByRole("navigation", { name: "Main" }).getByRole("link", { name, exact: true });

const reason = "Web Platform's VMs serve the checkout: tell the on-call team before you power one off.";

async function fillPolicy(page: Page, sheet: Locator) {
  await typeInto(page, sheet.getByRole("textbox", { name: "Name" }), "Powering off web VMs");
  await typeInto(page, sheet.getByRole("spinbutton", { name: "Priority" }), "30");
  await choose(page, sheet.getByRole("combobox", { name: "Effect" }), "Deny");
  await choose(
    page,
    sheet.getByRole("combobox", { name: "Enforcement" }),
    "Soft: it warns, and the requester may go on",
  );
  await typeInto(page, sheet.getByRole("textbox", { name: "Actions" }), "power_off");
  await typeInto(page, sheet.getByRole("textbox", { name: "Reason people see" }), reason);
  await expect(sheet.getByRole("combobox", { name: "Project" })).toHaveText(/Web Platform/);
}

test("a project admin shares Web Platform's deployments with their owners, and writes a soft day-2 policy", async ({
  page,
}) => {
  test.setTimeout(120_000);
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.addInitScript(() => localStorage.setItem("rvav.mock.as", "project-admin"));
  await page.goto("/");

  await press(page, nav(page, "Projects"));
  await expect(page.getByRole("heading", { name: "Projects", level: 1 })).toBeVisible();
  await press(page, page.getByRole("region", { name: "Projects" }).getByRole("link", { name: "Web Platform" }));
  await expect(page.getByRole("heading", { name: "Web Platform", level: 1 })).toBeVisible();
  const sharing = page.getByRole("region", { name: "Deployment sharing" });
  await choose(page, sharing.getByRole("combobox", { name: "Who sees them" }), "Owners only");
  await expect(sharing).toContainText(
    "Project admins, supervisors, auditors and administrators still see all of them.",
  );
  await press(page, sharing.getByRole("button", { name: "Save sharing" }));
  await expect(page.getByText("Web Platform's deployments: owners only")).toBeVisible();
  await expect(sharing.getByRole("button", { name: "Save sharing" })).toBeDisabled();
  await noToast(page);
  await bothThemes(page, "a project's deployment sharing");

  await press(page, nav(page, "Policies"));
  await expect(page.getByRole("heading", { name: "Policies", level: 1 })).toBeVisible();
  await expect(page.getByRole("tab", { name: "Naming" })).toHaveCount(0);
  await selectTab(page, "Day-2");
  const region = page.getByRole("region", { name: "Day-2 policies" });
  await expect(region.getByRole("row", { name: /Members operate their VMs/ })).toBeVisible();
  await expect(region.getByRole("button", { name: /^Actions for/ })).toHaveCount(0);
  await bothThemes(page, "a project admin's day-2 policies");

  // The theme button is behind the modal sheet: it is opened in each theme.
  const sheet = page.getByRole("dialog", { name: "New day-2 policy" });
  await theme(page, "Dark");
  await press(page, region.getByRole("button", { name: "New day-2 policy" }));
  await fillPolicy(page, sheet);
  await scan(page, "a project admin's new day-2 policy, dark");
  await page.keyboard.press("Escape");
  await expect(sheet).toHaveCount(0);
  await theme(page, "Light");
  await press(page, region.getByRole("button", { name: "New day-2 policy" }));
  await fillPolicy(page, sheet);
  await scan(page, "a project admin's new day-2 policy, light");
  await press(page, sheet.getByRole("button", { name: "Save policy" }));
  await expect(sheet).toHaveCount(0);

  const row = region.getByRole("row", { name: /Powering off web VMs/ });
  await expect(row).toContainText("Soft: warns");
  await expect(row.getByRole("button", { name: "Actions for Powering off web VMs" })).toBeVisible();
  await noToast(page);
  await bothThemes(page, "a project admin's soft day-2 policy");
  expect(errors).toEqual([]);
});

test("a member sees only what they own, is warned before powering a VM off, and goes on", async ({ page }) => {
  test.setTimeout(120_000);
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.addInitScript(() => localStorage.setItem("rvav.mock.as", "member"));
  await page.goto("/deployments");
  await expect(page.getByRole("heading", { name: "Deployments", level: 1 })).toBeVisible();
  await expect(page.getByRole("link", { name: "web-tst-search", exact: true })).toBeVisible();
  await expect(page.getByRole("link", { name: "web-dev-api", exact: true })).toHaveCount(0);
  await bothThemes(page, "the deployments a member owns");

  await press(page, page.getByRole("link", { name: "web-tst-search", exact: true }));
  await expect(page.getByRole("heading", { name: "web-tst-search", level: 1 })).toBeVisible();
  const vm = "web-tst-search001";
  const dialog = page.getByRole("dialog", { name: `Power off · ${vm}` });
  for (const name of ["Dark", "Light"] as const) {
    await theme(page, name);
    await press(page, page.getByRole("button", { name: `More actions for ${vm}` }));
    const off = page.getByRole("menuitem", { name: /^Power off/ });
    await expect(off).toContainText("warns");
    await pick(page, off);
    await expect(dialog.getByRole("note", { name: "Policy warning" })).toContainText(reason);
    await expect(dialog.getByRole("note", { name: "Policy warning" })).toContainText(
      "You may go on: the request keeps the warning.",
    );
    await scan(page, `a day-2 action that warns, ${name.toLowerCase()}`);
    if (name === "Dark") {
      await page.keyboard.press("Escape");
      await expect(dialog).toHaveCount(0);
    }
  }
  await press(page, dialog.getByRole("button", { name: "Power off anyway" }));
  await expect(dialog).toHaveCount(0);
  await noToast(page);

  await selectTab(page, "History");
  await press(page, page.getByRole("link", { name: "Power off", exact: true }));
  const warned = page.getByRole("region", { name: "Policy warnings" });
  await expect(warned).toContainText(reason);
  await expect(warned).toContainText("The requester went on despite them.");
  await bothThemes(page, "a request that went on despite a warning");
  expect(errors).toEqual([]);
});
