import { expect, test } from "@playwright/test";

// The notification centre and preferences against the mock backend (S11.5, ADR-0023).

test("the bell: unread notifications, opened and marked read", async ({ page }) => {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/");
  const bell = page.getByRole("button", { name: "Notifications, 3 unread" });
  await expect(bell).toBeVisible();
  await bell.click();
  await page.getByRole("menuitem", { name: /Approval needed: RHEL 9 server web-prd-front04/ }).click();
  await expect(page).toHaveURL(/\/approvals\?id=apr_/);
  await expect(page.getByRole("button", { name: "Notifications, 2 unread" })).toBeVisible();

  await page.getByRole("button", { name: "Notifications, 2 unread" }).click();
  await page.getByRole("menuitem", { name: "All notifications and preferences" }).click();
  await expect(page.getByRole("heading", { name: "Notifications", exact: true })).toBeVisible();
  const list = page.getByRole("list", { name: "Notifications" });
  await expect(list.getByRole("listitem")).toHaveCount(4);
  await page.getByRole("checkbox", { name: "Unread only" }).click();
  await expect(list.getByRole("listitem")).toHaveCount(2);
  await page.getByRole("button", { name: "Mark all read" }).click();
  await expect(page.getByText("Nothing unread")).toBeVisible();
  await expect(page.getByRole("button", { name: "Notifications", exact: true })).toBeVisible();
  expect(errors).toEqual([]);
});

test("preferences: channels per kind, what can reach you, a test", async ({ page }) => {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/notifications");
  await page.getByRole("tab", { name: "Preferences" }).click();
  await expect(page.getByText("is not in the tenant's directory")).toBeVisible();
  await expect(page.getByText("Slack is not set up in rvav")).toBeVisible();
  const submitted = page.getByRole("checkbox", { name: "Request submitted by Email" });
  await expect(submitted).not.toBeChecked();
  await expect(page.getByRole("checkbox", { name: "Approval needed by Email" })).toBeChecked();
  await expect(page.getByRole("checkbox", { name: "Approval needed by Teams" })).toBeDisabled();
  await expect(page.getByRole("checkbox", { name: "Request submitted in the app" })).toBeDisabled();

  await submitted.click();
  await page.getByRole("checkbox", { name: "Request done by Email" }).click();
  await page.getByRole("combobox", { name: "Language of what is sent" }).click();
  await page.getByRole("option", { name: "Français" }).click();
  await page.getByRole("button", { name: "Save preferences" }).click();
  await expect(page.getByText("Notification preferences saved")).toBeVisible();
  // The mock keeps them in the page: away and back shows what the server saved.
  await page.getByRole("tab", { name: "Notifications" }).click();
  await page.getByRole("tab", { name: "Preferences" }).click();
  await expect(page.getByRole("checkbox", { name: "Request submitted by Email" })).toBeChecked();
  await expect(page.getByRole("checkbox", { name: "Request done by Email" })).not.toBeChecked();
  await expect(page.getByRole("combobox", { name: "Language of what is sent" })).toContainText("Français");

  await page.getByRole("button", { name: "Send me a test on Email" }).click();
  await expect(page.getByText(/Sent to .*@corp\.example/)).toBeVisible();
  expect(errors).toEqual([]);
});
