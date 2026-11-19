import { expect, type Locator, type Page, test } from "@playwright/test";
import { bothThemes, scan, theme } from "./axe";

// Custom resource types (E1.4, ADR-0056) against the mock backend: an
// administrator defines one, and a deployment shows its load balancer like
// its VMs, runs the load balancer's day-2 action and draws it on its
// topology.

async function pick(page: Page, scope: Locator, label: string, option: string) {
  await scope.getByRole("combobox", { name: label }).click();
  await page.getByRole("option", { name: option, exact: true }).click();
}

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

async function noSideways(page: Page, where: string) {
  await expect(page.locator(".animate-pulse, [role=status][aria-label=Loading]")).toHaveCount(0);
  const overflow = await page.evaluate(() => {
    const main = document.getElementById("main")!;
    const wide = [...document.querySelectorAll("body *")]
      .filter((e) => {
        const r = e.getBoundingClientRect();
        return r.right > window.innerWidth + 1 && r.width > 0 && !e.closest(".overflow-x-auto, .overflow-auto, table");
      })
      .slice(0, 3)
      .map((e) => `${e.tagName.toLowerCase()}.${String(e.className).slice(0, 60)}`);
    return {
      page: document.documentElement.scrollWidth - window.innerWidth,
      main: main.scrollWidth - main.clientWidth,
      wide,
    };
  });
  expect(overflow, `${where}: ${overflow.wide.join(", ")}`).toEqual({ page: 0, main: 0, wide: [] });
}

test("an administrator defines a resource type; one deployments have can't be removed", async ({ page }) => {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/");
  await page.getByRole("link", { name: "Resource types" }).click();
  await expect(page.getByRole("heading", { name: "Resource types", exact: true })).toBeVisible();
  const lb = page.getByRole("row", { name: /Load balancer/ });
  await expect(lb).toContainText("AWX · create-load-balancer");
  await expect(lb).toContainText("wf.lb-drain");
  await bothThemes(page, "Resource types");

  await page.getByRole("button", { name: "New resource type" }).click();
  const dialog = page.getByRole("dialog", { name: "New resource type" });
  await dialog.getByRole("textbox", { name: "Key" }).fill("dns-record");
  await dialog.getByRole("button", { name: "Save resource type" }).click();
  await expect(dialog.getByText("Give it a title.")).toBeVisible();
  await expect(dialog.getByText("Name the custom workflow or the AWX template.")).toHaveCount(2);
  await scan(page, "A new resource type's field errors");
  await dialog.getByRole("textbox", { name: "Title" }).fill("DNS record");
  await pick(page, dialog.getByRole("group", { name: "Creates it" }), "Workflow", "DNS alias");
  const deletes = dialog.getByRole("group", { name: "Deletes it" });
  await pick(page, deletes, "Run", "AWX template");
  await pick(page, deletes, "Job template", "delete-load-balancer");
  await dialog.getByRole("checkbox", { name: /Drain/ }).click();
  await dialog.getByRole("button", { name: "Save resource type" }).click();
  await expect(page.getByText("DNS record saved")).toBeVisible();
  await expect(page.getByRole("row", { name: /DNS record/ })).toContainText("AWX · delete-load-balancer");
  // A toast under the pointer stays: Sonner pauses it while hovered.
  await page.mouse.move(0, 0);
  await expect(page.getByText("DNS record saved")).toHaveCount(0, { timeout: 10_000 });

  await sheetInBothThemes(
    page,
    async () => {
      await lb.getByRole("button", { name: "Actions for Load balancer" }).click();
      await page.getByRole("menuitem", { name: "Change" }).click();
      await expect(page.getByRole("dialog", { name: "Change Load balancer" })).toBeVisible();
    },
    "Change a resource type",
  );

  await lb.getByRole("button", { name: "Actions for Load balancer" }).click();
  await expect(page.getByRole("menuitem", { name: "Remove: deployments have 1 resource" })).toHaveAttribute(
    "aria-disabled",
    "true",
  );
  await page.keyboard.press("Escape");
  expect(errors).toEqual([]);
});

test("a deployment shows its load balancer, drains it and draws it on its topology", async ({ page }) => {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/deployments");
  await page.getByRole("link", { name: "web-prd-edge", exact: true }).click();
  const lb = page.getByRole("listitem", { name: "web-prd-edge-lb" });
  await expect(lb).toContainText("Load balancer");
  await expect(lb.getByRole("definition").first()).toHaveText("10.21.10.200");
  await expect(lb.getByRole("term").first()).toHaveText("Virtual IP");
  await bothThemes(page, "A deployment's custom resources");

  await lb.getByRole("button", { name: "More actions for web-prd-edge-lb" }).click();
  await page.getByRole("menuitem", { name: "Drain" }).click();
  await expect(page.getByText("Drain · web-prd-edge-lb")).toBeVisible();

  await page.getByRole("tab", { name: "Topology" }).click();
  const topology = page.getByRole("region", { name: "Topology of web-prd-edge" });
  const node = topology.getByRole("button", { name: /web-prd-edge-lb\..*Virtual IP: 10\.21\.10\.200/ });
  await expect(node).toBeVisible();
  await expect(page.getByText("Drain · web-prd-edge-lb")).toHaveCount(0, { timeout: 10_000 });
  await bothThemes(page, "A topology with a custom resource");
  expect(errors).toEqual([]);
});

test.describe("reflow at 320 px", () => {
  test.use({ viewport: { width: 320, height: 640 } });

  test("a resource type's sheet and a deployment's custom resources", async ({ page }) => {
    await page.goto("/admin/resource-types");
    await page.getByRole("button", { name: "New resource type" }).click();
    await expect(page.getByRole("dialog", { name: "New resource type" })).toBeVisible();
    await noSideways(page, "New resource type");
    await page.keyboard.press("Escape");

    await page.goto("/deployments");
    const href = await page.getByRole("link", { name: "web-prd-edge", exact: true }).getAttribute("href");
    await page.goto(href!);
    await expect(page.getByRole("listitem", { name: "web-prd-edge-lb" })).toBeVisible();
    await noSideways(page, href!);
  });
});
