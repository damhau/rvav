import { readFile } from "node:fs/promises";
import { expect, test } from "@playwright/test";
import { signIn } from "./auth";

// S8 acceptance on the real stack (ADR-0019): an item made from the RHEL
// item's YAML, with a dropdown from a REST service, previewed as a member of
// Web Platform, published, requested, changed and rolled back. The REST
// service is a static file served on the stack's network and allowed:
//
//   docker run -d --rm --name rvav-cmdb --network rvav_default \
//     -v "$PWD/deploy/cmdb:/usr/share/nginx/html:ro" nginx:1.27-alpine
//   RVAV_REST_SOURCE_HOSTS=http://rvav-cmdb make up
test.skip(!process.env.RVAV_STACK_CMDB, "needs the cmdb container (set RVAV_STACK_CMDB=1)");

test("an Ubuntu item with a REST dropdown, from YAML to roll back", async ({ page }) => {
  test.setTimeout(240_000);
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  // Names of its own, so the test can run again on the same stack.
  const suffix = Array.from({ length: 6 }, () => String.fromCharCode(97 + Math.floor(Math.random() * 26))).join("");
  const slug = `ubuntu-web-${suffix}`;
  const name = `Ubuntu web ${suffix}`;
  const secret = `backup-${suffix}`;

  await page.goto("/admin/catalog-items");
  await signIn(page);

  // The CMDB's token, stored.
  await page.getByRole("button", { name: "New secret" }).click();
  const secretDialog = page.getByRole("dialog", { name: "New secret" });
  await secretDialog.getByRole("textbox", { name: "Name" }).fill(secret);
  await secretDialog.getByLabel("Value").fill("backup-token");
  await secretDialog.getByRole("button", { name: "Store" }).click();
  await expect(page.getByText(`${secret} stored`)).toBeVisible();

  // The RHEL item exported, renamed and imported: a new item with a draft.
  await page.getByRole("region", { name: "Items" }).getByRole("link", { name: "RHEL 9 server" }).click();
  const download = page.waitForEvent("download");
  await page
    .getByRole("button", { name: /^Export .* as YAML$/ })
    .first()
    .click();
  const yaml = (await readFile(await (await download).path(), "utf8"))
    .replace(/^ {2}slug: .*$/m, `  slug: ${slug}`)
    .replace(/^ {2}name: .*$/m, `  name: ${name}`);
  await page.getByRole("navigation").getByRole("link", { name: "Catalog items" }).click();
  await page.getByRole("button", { name: "Import YAML" }).click();
  await page.getByRole("dialog", { name: "Import an item" }).getByRole("textbox", { name: "YAML" }).fill(yaml);
  await page.getByRole("button", { name: "Import", exact: true }).click();
  await expect(page.getByRole("heading", { name })).toBeVisible();
  const v1 = (await page
    .getByText(/^Draft \d+\.\d+$/)
    .first()
    .textContent())!.replace("Draft ", "");

  // A backup policy from the CMDB, tested as a member of Web Platform.
  await page.getByRole("tab", { name: "Form designer" }).click();
  const fields = page.getByRole("region", { name: "Fields" });
  const props = page.getByRole("region", { name: "Properties" });
  const preview = page.getByRole("region", { name: "Preview" });
  const saved = page.getByRole("status").filter({ hasText: `Draft ${v1}: saved` });
  await fields.getByRole("combobox", { name: "Add a field" }).click();
  await page.getByRole("option", { name: "Dropdown", exact: true }).click();
  const commit = async (label: string, value: string) => {
    const box = props.getByRole("textbox", { name: label, exact: true });
    await box.fill(value);
    await box.press("Enter");
  };
  await commit("Name", "backup");
  await props.getByRole("textbox", { name: "Label", exact: true }).fill("Backup policy");
  await props.getByRole("checkbox", { name: "Required" }).click();
  await props.getByRole("combobox", { name: "Value source" }).click();
  await page.getByRole("option", { name: "REST service" }).click();
  await commit("URL", "http://rvav-cmdb/policies.json?role=${inputs.role}");
  await commit("Secret", secret);
  await commit("Items (JSONPath)", "$.policies");
  await commit("Value (JSONPath in an item)", "$.id");
  await commit("Label (JSONPath in an item)", "$.name");
  await commit("Detail (JSONPath in an item)", "$.retention");
  await props.getByRole("checkbox", { name: "role" }).click();
  await expect(saved).toBeVisible();
  await preview.getByRole("combobox", { name: "Preview as" }).click();
  // A stack that had Keycloak sign-ins has more than one Thomas Nguyen (one per subject).
  await page
    .getByRole("option", { name: /^Thomas Nguyen/ })
    .first()
    .click();
  await props.getByRole("button", { name: "Test the source" }).click();
  await expect(props.getByRole("status")).toContainText("2 options: Daily, Weekly");

  // Open to Web Platform and published.
  await page.getByRole("tab", { name: "Entitlements" }).click();
  await page.getByRole("radio", { name: /Chosen projects/ }).check();
  await page.getByRole("checkbox", { name: "Web Platform" }).click();
  await page.getByRole("button", { name: "Save entitlements" }).click();
  await expect(page.getByText("Entitlements saved")).toBeVisible();
  await page.getByRole("tab", { name: "Versions" }).click();
  await page.getByRole("button", { name: "Publish" }).click();
  await expect(page.getByText(`${v1} published`)).toBeVisible();

  // Requested from the catalog, through Temporal and vcsim.
  const role =
    "ub" + Array.from({ length: 6 }, () => String.fromCharCode(97 + Math.floor(Math.random() * 26))).join("");
  await page.getByRole("navigation").getByRole("link", { name: "Catalog", exact: true }).click();
  await page.getByRole("link", { name, exact: true }).click();
  await expect(page.getByRole("heading", { name: `Request ${name}` })).toBeVisible();
  await page.getByRole("textbox", { name: "Role" }).fill(role);
  await page.getByRole("combobox", { name: "Backup policy" }).click();
  await page.getByRole("option", { name: /Weekly/ }).click();
  await page.getByRole("button", { name: "Submit request" }).click();
  await expect(page).toHaveURL(/\/requests\/REQ-\d+$/);
  await expect(page.getByText("Succeeded", { exact: true }).first()).toBeVisible({ timeout: 120_000 });

  // A new version renames the field; rolled back, the deployment stays on the first.
  await page.getByRole("navigation").getByRole("link", { name: "Catalog items" }).click();
  await page.getByRole("region", { name: "Items" }).getByRole("link", { name }).click();
  await page.getByRole("button", { name: "New draft" }).click();
  const draftToast = page.getByText(/^Draft (\d+\.\d+) made from the published version$/);
  await expect(draftToast).toBeVisible();
  const v2 = /Draft (\S+) made/.exec((await draftToast.textContent())!)![1]!;
  await page.getByRole("tab", { name: "Form designer" }).click();
  await fields.getByRole("button", { name: /^Backup policy/ }).click();
  await props.getByRole("textbox", { name: "Label", exact: true }).fill("Backup schedule");
  await expect(page.getByRole("status").filter({ hasText: `Draft ${v2}: saved` })).toBeVisible();
  await page.getByRole("tab", { name: "Versions" }).click();
  await page.getByRole("button", { name: "Publish" }).click();
  await expect(page.getByText(`${v2} published`)).toBeVisible();
  await page.getByRole("button", { name: `Roll back to ${v1}` }).click();
  await expect(page.getByText(`Rolled back to ${v1}`)).toBeVisible();
  const rows = page.getByRole("region", { name: "Versions" }).getByRole("row");
  await expect(rows.filter({ hasText: v1 }).filter({ hasText: "published" })).toContainText("1");
  await expect(rows.filter({ hasText: v2 }).filter({ hasText: "deprecated" })).toBeVisible();
  expect(errors).toEqual([]);
});
