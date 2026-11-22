import { expect, type Page, test } from "@playwright/test";
import { bothThemes, scan, theme } from "./axe";

// Branding and notification templates (O1.5, ADR-0059) against the mock
// backend: an administrator edits the approval email and previews it on each
// channel, then gives the portal a name, an accent that reads in both themes
// and a logo, which the menu shows at once.

// Each test scans several pages in both themes.
test.describe.configure({ timeout: 90_000 });

const shots = process.env.RVAV_SHOTS;

async function shot(page: Page, name: string) {
  if (shots) await page.screenshot({ path: `${shots}/${name}.png`, fullPage: true });
}

async function quiet(page: Page) {
  await page.mouse.move(0, 0);
  await expect(page.locator("[data-sonner-toast]")).toHaveCount(0, { timeout: 10_000 });
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

const accent = (page: Page) =>
  page.evaluate(() => getComputedStyle(document.documentElement).getPropertyValue("--accent").trim());

const logo = `<svg xmlns="http://www.w3.org/2000/svg" width="96" height="24" viewBox="0 0 96 24">
<rect width="96" height="24" rx="4" fill="#806600"/><circle cx="12" cy="12" r="6" fill="#fff"/></svg>`;

test("an administrator edits the approval email and previews it on each channel", async ({ page }) => {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/");
  await page.getByRole("navigation", { name: "Main" }).getByRole("link", { name: "Notification templates" }).click();
  const list = page.getByRole("table", { name: "Notification templates" });
  await expect(list.getByRole("row", { name: /Approval needed/ })).toContainText("Built-in templates");
  await bothThemes(page, "Notification templates");
  await shot(page, "templates-list");

  await list.getByRole("link", { name: "Approval needed" }).click();
  await expect(page.getByRole("heading", { name: "Approval needed", level: 1 })).toBeVisible();
  const preview = page.getByRole("region", { name: "Preview" });
  const email = preview.locator('iframe[title="The email as HTML"]');
  await expect(email).toHaveAttribute("sandbox", "");
  await expect(email).toHaveAttribute("srcdoc", /<strong>REQ-10077<\/strong> waits for your decision/);
  await bothThemes(page, "Approval needed, email");
  await shot(page, "template-email");

  const subject = page.getByRole("textbox", { name: "Subject" });
  await subject.fill("{{.Deployment.Name}} waits");
  await expect(preview.getByRole("status")).toContainText("Line 1: .Deployment is not a variable of this kind.");
  await page.getByRole("button", { name: "Save template" }).click();
  await expect(subject).toHaveAttribute("aria-invalid", "true");
  await quiet(page);
  await scan(page, "Approval needed, refused");

  await subject.fill("Decide on {{.Request.Number}}: {{.Request.Title}}");
  await expect(preview).toContainText("Subject: Decide on REQ-10077: RHEL 9 server web-prd-front01");
  await page.getByRole("button", { name: "Save template" }).click();
  await expect(page.getByText("Template saved")).toBeVisible();
  await expect(page.getByText(/^Edited by .+, /)).toBeVisible();
  await preview.getByRole("tab", { name: "Text" }).click();
  await expect(preview.getByRole("region", { name: "The email as text" })).toContainText("REQ-10077");

  await page.getByRole("group", { name: "Channel" }).getByRole("button", { name: "Teams" }).click();
  await expect(page).toHaveURL(/channel=teams/);
  await expect(preview.getByRole("tab", { name: "Card" })).toHaveAttribute("aria-selected", "true");
  await expect(preview.getByRole("tabpanel")).toContainText("Open in rvav");
  await quiet(page);
  await bothThemes(page, "Approval needed, Teams");
  await shot(page, "template-teams");

  await page.getByRole("group", { name: "Channel" }).getByRole("button", { name: "Slack" }).click();
  await expect(preview.getByRole("tab", { name: "Message" })).toHaveAttribute("aria-selected", "true");
  await preview.getByRole("tab", { name: "JSON" }).click();
  await expect(preview.getByRole("region", { name: "The message's blocks" })).toContainText('"type": "header"');
  await bothThemes(page, "Approval needed, Slack");

  await page.getByRole("link", { name: "Notification templates" }).last().click();
  await expect(list.getByRole("row", { name: /Approval needed/ })).toContainText("Email, English");
  expect(errors).toEqual([]);
});

test("an administrator brands the portal with a name, an accent that reads and a logo", async ({ page }) => {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/");
  await expect(page).toHaveTitle("rvav");
  await page.getByRole("navigation", { name: "Main" }).getByRole("link", { name: "Branding" }).click();
  await expect(page.getByRole("heading", { name: "Branding", exact: true, level: 1 })).toBeVisible();
  await bothThemes(page, "Branding");

  await page.getByRole("textbox", { name: "Portal name" }).fill("Corp Cloud");
  const color = page.getByRole("textbox", { name: "Accent colour", exact: true });
  await color.fill("#ffcc00");
  await expect(page.getByText(/#ffcc00 has a contrast of 1\.\d+:1 in the light theme/)).toBeVisible();
  await bothThemes(page, "Branding, an accent that doesn't read");
  await page.getByRole("button", { name: /^Use #[0-9a-f]{6}$/ }).click();
  await expect(color).toHaveValue("#806600");
  await expect(page.getByText(/^Reads at \d+(\.\d+)?:1 in the light theme/)).toBeVisible();
  await page.getByRole("button", { name: "Save branding" }).click();
  await expect(page.getByText("Branding saved")).toBeVisible();
  await expect(page).toHaveTitle("Corp Cloud");
  const nav = page.getByRole("navigation", { name: "Main" });
  await expect(nav).toContainText("Corp Cloud");
  expect(await accent(page)).toBe("#806600");

  const chooser = page.waitForEvent("filechooser");
  await page.getByRole("button", { name: "Upload the light theme's logo" }).click();
  await (await chooser).setFiles({ name: "corp.svg", mimeType: "image/svg+xml", buffer: Buffer.from(logo) });
  await expect(page.getByText("Light theme's logo saved")).toBeVisible();
  await expect(page.getByRole("img", { name: "Light theme's logo" })).toBeVisible();
  await expect(nav.locator("img").first()).toBeVisible();

  const refused = page.waitForEvent("filechooser");
  await page.getByRole("button", { name: "Upload the dark theme's logo" }).click();
  await (
    await refused
  ).setFiles({
    name: "bad.svg",
    mimeType: "image/svg+xml",
    buffer: Buffer.from('<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>'),
  });
  await expect(page.getByText("The SVG has a <script> element, which a logo can't have.")).toBeVisible();
  await quiet(page);
  await bothThemes(page, "Branding, saved");
  await shot(page, "branding");

  await nav.getByRole("link", { name: "Overview" }).click();
  await expect(page.getByRole("heading", { level: 1 })).toBeVisible();
  await theme(page, "Dark");
  const dark = await accent(page);
  expect(dark).toMatch(/^#[0-9a-f]{6}$/);
  expect(dark).not.toBe("#806600");
  await theme(page, "Light");
  await bothThemes(page, "The overview with the portal's accent");
  await shot(page, "overview-branded");
  expect(errors).toEqual([]);
});

test.describe("reflow at 320 px", () => {
  test.use({ viewport: { width: 320, height: 640 } });

  test("the branding page and a template's page", async ({ page }) => {
    await page.goto("/admin/branding");
    await expect(page.getByRole("textbox", { name: "Portal name" })).toBeVisible();
    await noSideways(page, "Branding");
    await page.goto("/admin/notification-templates/approval_needed");
    await expect(page.locator('iframe[title="The email as HTML"]')).toBeVisible();
    await noSideways(page, "Approval needed");
    await page.goto("/admin/notification-templates/approval_needed?channel=teams");
    await expect(page.getByRole("region", { name: "Preview" }).getByRole("tabpanel")).toContainText("Open in rvav");
    await noSideways(page, "Approval needed, Teams");
  });
});
