import { expect, test } from "@playwright/test";

// The vRA importer (P2.5) against the mock backend: what an import would do,
// a vRA project mapped, the import, and the import run again.

test("see the dry run, map a project, import, and import again", async ({ page }) => {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/admin/vra-import");
  await expect(page.getByRole("heading", { name: "vRA import" })).toBeVisible();
  await expect(page.getByText("3 projects · 5 templates · 5 deployments · 7 vRO workflows")).toBeVisible();

  // The dry run: four items to make, one that doesn't map.
  const catalog = page.getByRole("region", { name: "Catalog" });
  await expect(catalog.getByText("to create")).toBeVisible();
  const templates = page.getByRole("table", { name: "Cloud templates" });
  const rhel = templates.getByRole("row", { name: /RHEL 9 Server/ });
  await expect(rhel.getByText("Create")).toBeVisible();
  await expect(rhel.getByText("Not Data Services, Legacy Apps: not mapped")).toBeVisible();
  await rhel.getByRole("button", { name: "1 to check" }).click();
  await expect(templates.getByText(/write an rvav placement rule/)).toBeVisible();
  await expect(templates.getByRole("row", { name: /Work in progress/ }).getByText("Not imported")).toBeVisible();

  // Data Services is Data Engineering.
  await page.getByRole("tab", { name: "Projects" }).click();
  const projects = page.getByRole("table", { name: "vRA projects" });
  await expect(projects.getByRole("combobox", { name: "rvav project for Web Platform" })).toHaveText(
    "By name: Web Platform",
  );
  await projects.getByRole("combobox", { name: "rvav project for Data Services" }).click();
  await page.getByRole("option", { name: "Data Engineering", exact: true }).click();
  await expect(projects.getByRole("combobox", { name: "rvav project for Data Services" })).toHaveText(
    "Data Engineering",
  );
  await page.getByRole("tab", { name: /Catalog items/ }).click();
  await expect(rhel.getByText("Adds Web Platform, Data Engineering")).toBeVisible();

  // Policies and vRO are reported, not imported.
  await page.getByRole("tab", { name: /Policies/ }).click();
  await expect(
    page.getByRole("region", { name: "Production needs a lead" }).getByText('Condition: environment == "prd"'),
  ).toBeVisible();
  await page.getByRole("tab", { name: /vRO/ }).click();
  const vro = page.getByRole("table", { name: "vRO workflows and actions" });
  await expect(
    vro.getByRole("row", { name: /Decommission application/ }).getByText("C · Custom workflow"),
  ).toBeVisible();

  // The import: the catalog, then the deployments its items allow.
  await page.getByRole("button", { name: "Import…" }).click();
  const dialog = page.getByRole("dialog", { name: "Import from vRA" });
  await expect(
    dialog.getByText("4 items to create and 0 new drafts, nothing published; shared with 5 more projects."),
  ).toBeVisible();
  await dialog.getByRole("checkbox", { name: /Deployments/ }).click();
  await dialog.getByRole("button", { name: "Import" }).click();
  await expect(dialog).toBeHidden();
  const imported = page.getByRole("region", { name: "Imported" });
  await expect(
    imported.getByText(
      "Items made, as drafts: RHEL 9 Server, Windows Server 2022, Three-tier application, Database server",
    ),
  ).toBeVisible();
  await page.getByRole("tab", { name: /Catalog items/ }).click();
  await expect(rhel.getByText("Unchanged")).toBeVisible();
  await page.getByRole("tab", { name: /Deployments/ }).click();
  await expect(
    page
      .getByRole("table", { name: "vRA deployments" })
      .getByRole("row", { name: /vra-gone/ })
      .getByText("Left in vRA"),
  ).toBeVisible();

  // Again: nothing left to do.
  await page.getByRole("button", { name: "Import…" }).click();
  await dialog.getByRole("button", { name: "Import" }).click();
  await expect(imported.getByText("Nothing to do: rvav has it all already.")).toBeVisible();
  expect(errors).toEqual([]);
});
