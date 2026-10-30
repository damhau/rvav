import { expect, test } from "@playwright/test";

// The vRA importer (P2.5, P2.7) against the mock backend, which plans as the
// server does: an estate read after an earlier import (the RHEL item made,
// published and offered to Web Platform); the dry run; projects mapped or
// kept in vRA; the import, run again; one project's deployments adopted;
// the cutover.

test("see the dry run, map projects, import, and import again", async ({ page }) => {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/admin/vra-import");
  await expect(page.getByRole("heading", { name: "vRA import" })).toBeVisible();
  await expect(page.getByText("3 projects · 5 templates · 4 deployments · 7 vRO workflows")).toBeVisible();

  // The dry run: three items to make, the RHEL one there already.
  const templates = page.getByRole("table", { name: "Cloud templates" });
  const rhel = templates.getByRole("row", { name: /RHEL 9 Server/ });
  await expect(rhel.getByText("Unchanged")).toBeVisible();
  await expect(rhel.getByText("Not Data Services, Legacy Apps: not mapped")).toBeVisible();
  await rhel.getByRole("button", { name: "Notes on RHEL 9 Server: 1 to check" }).click();
  await expect(templates.getByText(/write an rvav placement rule/)).toBeVisible();
  await expect(templates.getByRole("row", { name: /Windows Server 2022/ }).getByText("Create")).toBeVisible();
  await expect(templates.getByRole("row", { name: /Work in progress/ }).getByText("Not imported")).toBeVisible();

  // Data Services is Data Engineering; Legacy Apps stays in vRA.
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
  await projects.getByRole("combobox", { name: "rvav project for Legacy Apps" }).click();
  await page.getByRole("option", { name: "Stays in vRA", exact: true }).click();
  await expect(projects.getByRole("combobox", { name: "rvav project for Legacy Apps" })).toHaveText("Stays in vRA");
  await page.getByRole("tab", { name: /Catalog items/ }).click();
  await expect(rhel.getByText("Adds Data Engineering")).toBeVisible();
  await expect(rhel.getByText(/not mapped/)).toBeHidden();

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
  await expect(vro.getByText("Subscription Old snapshot cleanup (disabled)")).toBeVisible();

  // The import: the catalog. Its items are drafts, so the database's
  // deployment can't be adopted yet, and the plan says why.
  await page.getByRole("button", { name: "Import…" }).click();
  const dialog = page.getByRole("dialog", { name: "Import from vRA" });
  await expect(
    dialog.getByText(
      "3 items to create and 0 new drafts, nothing published; 4 shares to add (an item offered to one more project).",
    ),
  ).toBeVisible();
  await dialog.getByRole("button", { name: "Import" }).click();
  await expect(dialog).toBeHidden();
  const imported = page.getByRole("region", { name: "Imported" });
  await expect(
    imported.getByText("Items made, as drafts: Windows Server 2022, Three-tier application, Database server"),
  ).toBeVisible();
  await expect(imported.getByText("4 shares added: items offered to more projects.")).toBeVisible();
  // The tab stays where it was.
  await expect(page.getByRole("tab", { name: /vRO/ })).toHaveAttribute("aria-selected", "true");
  await page.getByRole("tab", { name: /Deployments/ }).click();
  const deployments = page.getByRole("table", { name: "vRA deployments" });
  await expect(
    deployments.getByRole("row", { name: /vra-db-001/ }).getByText(/database-server isn't published/),
  ).toBeVisible();
  await expect(deployments.getByRole("row", { name: /VRA Web Pair/ }).getByText("vra-web-pair in rvav")).toBeVisible();

  // Again: nothing new.
  await page.getByRole("button", { name: "Import…" }).click();
  await dialog.getByRole("button", { name: "Import" }).click();
  await expect(imported.getByText(/Nothing new to import/)).toBeVisible();
  expect(errors).toEqual([]);
});

test("adopt one project's deployments, mark it moved, then not", async ({ page }) => {
  await page.goto("/admin/vra-import");
  await page.getByRole("tab", { name: /Cutover/ }).click();
  await expect(page.getByText("0 of 3 projects moved")).toBeVisible();
  const legacy = page.getByRole("region", { name: "Legacy Apps" });
  await expect(legacy.getByText("Map it to an rvav project, or create one.")).toBeVisible();
  await expect(legacy.getByRole("button", { name: "Mark as moved… (Legacy Apps)" })).toBeDisabled();

  // Web Platform's two deployments its published item allows.
  const web = page.getByRole("region", { name: "Web Platform" });
  await web.getByRole("button", { name: "Adopt 2 deployments (Web Platform)" }).click();
  await expect(page.getByText("Adopted 2 deployments of Web Platform")).toBeVisible();
  await expect(web.getByText("2 of 3 adopted. Still in vRA: vra-gone.")).toBeVisible();

  await web.getByRole("button", { name: "Mark as moved… (Web Platform)" }).click();
  const dialog = page.getByRole("dialog", { name: "Move Web Platform to rvav" });
  await expect(dialog.getByText("Not everything is ready")).toBeVisible();
  await dialog.getByRole("textbox", { name: "Note" }).fill("Cutover weekend");
  await dialog.getByRole("button", { name: "Mark as moved" }).click();
  await expect(dialog).toBeHidden();
  await expect(page.getByText("1 of 3 projects moved")).toBeVisible();
  await expect(web.getByText(/Moved .* by Marc Keller/)).toBeVisible();
  await expect(web.getByText("Cutover weekend")).toBeVisible();
  await expect(web.getByText(/vRA still offers it 3 items/)).toBeVisible();

  // Undone, once confirmed.
  await web.getByRole("button", { name: "Not moved… (Web Platform)" }).click();
  const undo = page.getByRole("dialog", { name: "Web Platform not moved" });
  await expect(undo.getByText(/deleting an adopted deployment in vRA deletes its VMs/)).toBeVisible();
  await undo.getByRole("button", { name: "Not moved" }).click();
  await expect(undo).toBeHidden();
  await expect(page.getByText("0 of 3 projects moved")).toBeVisible();
});
