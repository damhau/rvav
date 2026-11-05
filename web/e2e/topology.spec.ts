import { expect, test } from "@playwright/test";

// An item's topology on the canvas (D1.2, ADR-0032): beside its YAML in
// catalog administration, following the text as it is typed.

const twoTiers = `form_schema: {type: object, properties: {web_nodes: {type: integer}}}
ui_schema: {layout: {sections: []}}
initial_values: {}
blueprint:
  kind: vm
  template: tpl-rhel9.4-2026.10
  bindings: {network: segments.app}
  segments: [{key: app, title: Application}]
  components:
    - {key: web, title: Web, count: inputs.web_nodes, groups: [web]}
    - {key: db, title: Database, count: 1, groups: [db]}
  security_groups:
    - {key: web, title: Web, rules: [{from: [any], services: [HTTPS]}]}
    - {key: db, title: Database, rules: [{from: [web], services: [PostgreSQL]}]}
`;

test("a three-tier item's topology beside its YAML, as it is edited", async ({ page }) => {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/admin/catalog-items");
  await page.getByRole("region", { name: "Items" }).getByRole("link", { name: "Three-tier application" }).click();
  await page.getByRole("button", { name: "New draft" }).click();
  await expect(page.getByText(/^Draft [\d.]+ made from the published version/)).toBeVisible();
  await page.getByRole("tab", { name: "Definition" }).click();

  // Its tiers, their segment, their groups and the rules between them.
  const topology = page.getByRole("region", { name: "Topology of Three-tier application" });
  const node = (name: RegExp) => topology.getByRole("button", { name });
  await expect(node(/^VMs Web\. web\. Count: inputs\.web_nodes\..*On separate hosts\..*In Web$/)).toBeVisible();
  await expect(node(/^1 VM Application\. app\./)).toBeVisible();
  await expect(node(/^1 VM Database\. db\..*Disks: system, 200 GB \(gold\)\..*In grp-backup-clients$/)).toBeVisible();
  await expect(node(/^Segment Application\. app\. Made for each deployment\./)).toBeVisible();
  await expect(
    node(/^Group Database\..*Allows PostgreSQL from Application\. Drops any service from Any/),
  ).toBeVisible();

  // Selected, all it shows under the graph.
  await node(/^Group Database\./).click();
  await expect(page.getByRole("region", { name: "Selected: Database" })).toContainText("Drops any service from Any");

  // Typed: drawn again.
  const editor = page.getByRole("textbox", { name: /^Definition of Three-tier application/ });
  await editor.click();
  await page.keyboard.press("Control+A");
  await page.keyboard.insertText(twoTiers);
  await expect(node(/^1 VM Database\. db\..*In Database$/)).toBeVisible();
  await expect(node(/^1 VM Application\./)).toHaveCount(0);

  // Broken: the last graph stays, and says why.
  await page.keyboard.insertText("blueprint: {}\n");
  await expect(
    page.getByText(/^The YAML doesn't parse at line \d+: the graph shows the definition as it last parsed\.$/),
  ).toBeVisible();
  await expect(node(/^1 VM Database\. db\..*In Database$/)).toBeVisible();

  // Undone: the stored definition again.
  await page.getByRole("button", { name: "Undo changes" }).click();
  await expect(page.getByText(/doesn't parse/)).toHaveCount(0);
  await expect(node(/^1 VM Application\. app\./)).toBeVisible();
  expect(errors).toEqual([]);
});
