import { expect, test } from "@playwright/test";
import { bothThemes } from "./axe";

// A custom day-2 action's designed form (E1.3, ADR-0050): an administrator
// lays it out in the catalog's designer and turns it on; the action's
// dialog then asks for it.
test.use({ reducedMotion: "reduce" });

test("design an AWX action's form, turn it on, and run the action with it", async ({ page }) => {
  await page.goto("/admin/day2-actions");
  const row = page
    .getByRole("table", { name: "AWX day-2 actions" })
    .getByRole("row", { name: /Install monitoring agent/ });
  await row.getByRole("button", { name: "Actions for Install monitoring agent" }).click();
  await page.getByRole("menuitem", { name: "Design the form" }).click();
  await expect(page.getByRole("heading", { name: "Install monitoring agent form" })).toBeVisible();
  await expect(page.getByText(/Not designed yet: this is the AWX survey/)).toBeVisible();

  const preview = page.getByRole("region", { name: "Preview" });
  await expect(preview.getByRole("spinbutton", { name: "Port" })).toHaveValue("9100");
  await page.getByRole("region", { name: "Fields" }).getByRole("button", { name: /^Port/ }).click();
  await page.getByRole("region", { name: "Properties" }).getByRole("textbox", { name: "Label" }).fill("Listen port");
  await expect(page.getByText(/^Saved as a draft: people get the AWX survey/)).toBeVisible();
  await expect(preview.getByRole("spinbutton", { name: "Listen port" })).toHaveValue("9100");

  await page.getByRole("button", { name: "Turn on" }).click();
  await expect(page.getByText("Install monitoring agent asks for this form from its next run")).toBeVisible();
  await expect(page.getByText("Saved: people get this form from their next run.")).toBeVisible();
  await bothThemes(page, "/admin/day2-actions/awx.install-monitoring-agent/form");

  // In the app: a reload would start the mock backend afresh, without the form.
  await page.getByRole("navigation", { name: "Main" }).getByRole("link", { name: "Deployments", exact: true }).click();
  await page.getByRole("link", { name: "web-dev-api", exact: true }).click();
  await page.getByRole("button", { name: "More actions for web-dev-api004" }).click();
  await page.getByRole("menuitem", { name: /^Install monitoring agent/ }).click();
  const dialog = page.getByRole("dialog", { name: "Install monitoring agent · web-dev-api004" });
  await expect(dialog.getByRole("spinbutton", { name: "Listen port" })).toHaveValue("9100");
  await dialog.getByRole("button", { name: "Install monitoring agent" }).click();
  await expect(dialog).toBeHidden();
  await expect(page.getByText(/^REQ-\d+ has started\.$/)).toBeVisible();
});
