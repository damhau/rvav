import { expect, type Locator, type Page, test } from "@playwright/test";
import { bothThemes, scan, theme } from "./axe";

// Audit forwarding (O1.4, ADR-0058) against the mock backend: an
// administrator sees how delivery to each syslog server and webhook goes,
// tests one, adds a syslog server over TLS and removes a webhook; the audit
// log shows the failed sign-ins it forwards.

async function pick(page: Page, scope: Locator, label: string, option: string) {
  await scope.getByRole("combobox", { name: label }).click();
  await page.getByRole("option", { name: option, exact: true }).click();
}

async function quiet(page: Page) {
  await page.mouse.move(0, 0);
  await expect(page.locator("[data-sonner-toast]")).toHaveCount(0, { timeout: 10_000 });
}

/** Opens a dialog in each theme, scans it, and closes it. */
async function inBothThemes(page: Page, open: () => Promise<Locator>, where: string) {
  for (const name of ["Light", "Dark"] as const) {
    await theme(page, name);
    const dialog = await open();
    await expect(dialog).toBeVisible();
    await quiet(page);
    await scan(page, `${where}, ${name.toLowerCase()}`);
    await page.keyboard.press("Escape");
    await expect(page.getByRole("dialog")).toHaveCount(0);
    await expect(page.getByRole("alertdialog")).toHaveCount(0);
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

test("an administrator forwards the audit log to a syslog server and tests it", async ({ page }) => {
  test.slow();
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/");
  await page.getByRole("navigation", { name: "Main" }).getByRole("link", { name: "Audit forwarding" }).click();
  await expect(page.getByRole("heading", { name: "Audit forwarding", exact: true })).toBeVisible();
  const destinations = page.getByRole("region", { name: "Destinations" });
  const soc = destinations.getByRole("row", { name: /SOC sign-ins/ });
  await expect(soc).toContainText("Failing");
  await expect(soc).toContainText("37 waiting");
  await expect(destinations.getByRole("row", { name: /Corporate SIEM/ })).toContainText("Up to date");
  await bothThemes(page, "Audit forwarding");

  const sendTest = async (name: string) => {
    await destinations.getByRole("button", { name: `Actions for ${name}` }).click();
    await page.getByRole("menuitem", { name: "Send a test event" }).click();
    return page.getByRole("dialog", { name: `Test of ${name}` });
  };
  await expect(await sendTest("Corporate SIEM")).toContainText("Delivered in 84 ms");
  await page.keyboard.press("Escape");
  await inBothThemes(page, () => sendTest("SOC sign-ins"), "A failed test");

  const open = async () => {
    await page.getByRole("button", { name: "New destination" }).click();
    return page.getByRole("dialog", { name: "New destination" });
  };
  await inBothThemes(page, open, "New destination");
  const sheet = await open();
  await sheet.getByRole("textbox", { name: "Name", exact: true }).fill("Lab syslog");
  await sheet.getByRole("textbox", { name: "Host" }).fill("not a host");
  await sheet.getByRole("textbox", { name: "Client certificate" }).fill("-----BEGIN CERTIFICATE-----\nMIIC\n-----END CERTIFICATE-----"); // prettier-ignore
  await sheet.getByRole("button", { name: "Save destination" }).click();
  await expect(sheet.getByText("A host name or an IP address, such as siem.corp.example.")).toBeVisible();
  await expect(sheet.getByText("Give the client certificate with the stored secret that holds its key.")).toBeVisible();
  await quiet(page);
  await scan(page, "New destination, refused");

  await sheet.getByRole("textbox", { name: "Host" }).fill("syslog.lab.example");
  await pick(page, sheet, "Facility", "audit");
  await pick(page, sheet, "Client key", "webhook-signing");
  await sheet.getByRole("checkbox", { name: "Sign-ins and sign-outs" }).check();
  await sheet.getByRole("button", { name: "Save destination" }).click();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  const lab = destinations.getByRole("row", { name: /Lab syslog/ });
  await expect(lab).toContainText("syslog.lab.example:6514");
  await expect(lab).toContainText("TLS · audit · rvav · client certificate");
  await expect(lab).toContainText("Nothing delivered yet");

  const remove = async () => {
    await soc.getByRole("button", { name: "Actions for SOC sign-ins" }).click();
    await page.getByRole("menuitem", { name: "Remove" }).click();
    return page.getByRole("alertdialog", { name: "Stop sending audit events to SOC sign-ins?" });
  };
  await inBothThemes(page, remove, "Remove a destination");
  const confirm = await remove();
  await expect(confirm).toContainText("The 37 events waiting for it are dropped.");
  await confirm.getByRole("button", { name: "Remove destination" }).click();
  await expect(soc).toHaveCount(0);

  await page.getByRole("navigation", { name: "Main" }).getByRole("link", { name: "Audit log" }).click();
  await page.getByRole("combobox", { name: "What" }).click();
  await page.getByRole("option", { name: "Failed sign-ins" }).click();
  const failed = page.getByRole("table", { name: "Audit events" }).getByRole("row", { name: /auth\.sign_in_failed/ });
  await expect(failed).toContainText("Unknown");
  await expect(failed).toContainText("from 10.20.4.17");
  expect(errors).toEqual([]);
});

test.describe("reflow at 320 px", () => {
  test.use({ viewport: { width: 320, height: 640 } });

  test("the destinations and a destination's sheet", async ({ page }) => {
    await page.goto("/admin/audit-forwarding");
    await expect(page.getByRole("region", { name: "Destinations" }).getByRole("row", { name: /SOC/ })).toBeVisible();
    await noSideways(page, "Audit forwarding");
    await page.getByRole("region", { name: "Destinations" }).getByRole("button", { name: "Actions for SOC sign-ins" }).click(); // prettier-ignore
    await page.getByRole("menuitem", { name: "Change" }).click();
    await expect(page.getByRole("dialog", { name: "Change SOC sign-ins" })).toBeVisible();
    await noSideways(page, "Change SOC sign-ins");
  });
});
