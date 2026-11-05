import AxeBuilder from "@axe-core/playwright";
import { expect, type Locator, test } from "@playwright/test";

// A composite request's run on its graph (D1.4, ADR-0032) on the mock
// backend: the path D1.5's acceptance takes. The seeded PostgreSQL cluster
// that failed (REQ-10484) opens on its graph, the failed step selected with
// its error, its job's output and Retry; another node shows its steps.
// Retried, the run is followed live to the end, the graph staying where it
// is; its timeline is the list view. Axe finds nothing, in either theme.

// Where the graph and its nodes are drawn.
const placed = (run: Locator) =>
  run
    .locator(".react-flow__viewport, .react-flow__node")
    .evaluateAll((els) => els.map((e) => (e as HTMLElement).style.transform));

test("a composite request's run: failed, retried and followed on its graph", async ({ page }) => {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/requests");
  await page.getByRole("link", { name: /PostgreSQL 16 cluster · orders/ }).click();
  await expect(page).toHaveURL(/\/requests\/REQ-10484$/);
  const run = page.getByRole("application", { name: "Run of REQ-10484" });
  const node = (name: RegExp) => run.getByRole("button", { name });

  // Each step's status; the failed one selected, with its error, its job's output and Retry.
  await expect(node(/^Request\. Done\./)).toBeVisible();
  await expect(node(/^Item Database nodes\. Undone\./)).toBeVisible();
  await expect(node(/^AWX Backups\. Undone\./)).toBeVisible();
  await expect(node(/^Done\. Not started\./)).toBeVisible();
  await expect(node(/^Item The database's owner\. Failed\./)).toHaveAttribute("aria-pressed", "true");
  const owner = page.getByRole("region", { name: "Selected: The database's owner" });
  await expect(owner).toContainText("permission denied to create role");
  await expect(owner.getByRole("alert")).toContainText("could not be created: permission denied");
  // Rolled back already (rollback_on_failure): nothing left to roll back, a retry runs it all again.
  await expect(owner.getByRole("alert")).toContainText("It was rolled back: deleted VM web-dev-pgorders001");
  await expect(page.getByRole("button", { name: "Roll back" })).toHaveCount(0);
  await expect(page.getByRole("log", { name: "Output of The database's owner · pg-create-role" })).toContainText(
    "permission denied to create role orders_owner",
  );

  // Another node: its steps, its undo among them; the failure stays, under the graph.
  await node(/^AWX Backups/).click();
  const backups = page.getByRole("region", { name: "Selected: Backups" });
  await expect(backups).toContainText("Undo Backups · pgbackrest-stanza-delete");
  await expect(backups.getByRole("alert")).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Retry", exact: true })).toHaveCount(1);

  // Retried: followed live to the end, the graph not moved.
  const before = await placed(run);
  await page.getByRole("button", { name: "Retry", exact: true }).click();
  await expect(node(/^Done\. Done\./)).toBeVisible({ timeout: 15_000 });
  for (const name of [/^Item Database nodes\. Done\./, /^Item The database's owner\. Done\./, /^AWX Backups\. Done\./])
    await expect(node(name)).toBeVisible();
  await expect(page.getByRole("button", { name: "Retry", exact: true })).toHaveCount(0);
  expect(await placed(run)).toEqual(before);

  // The timeline is the list view: every step, in order.
  await page.getByRole("button", { name: "Timeline" }).click();
  await expect(run).toHaveCount(0);
  await expect(page.getByRole("region", { name: "Steps", exact: true })).toContainText(
    /Database nodes[\s\S]*The database's owner · pg-create-role[\s\S]*Backups · pgbackrest-stanza[\s\S]*Ready/,
  );
  expect(errors).toEqual([]);
});

for (const theme of ["light", "dark"] as const) {
  test(`axe: a failed composite request's run, ${theme} theme`, async ({ page }) => {
    await page.addInitScript((t) => localStorage.setItem("rvav.theme", t), theme);
    await page.goto("/requests/REQ-10484");
    await expect(page.getByRole("region", { name: "Selected: The database's owner" })).toBeVisible();
    await expect(page.getByRole("log", { name: /^Output of / })).toContainText("PLAY RECAP");
    const result = await new AxeBuilder({ page })
      .withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa"])
      .analyze();
    expect(result.violations.map((v) => `${v.id}: ${v.nodes.map((n) => n.target.join(" ")).join(", ")}`)).toEqual([]);
  });
}
