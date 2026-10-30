import { expect, test } from "@playwright/test";

// The catalog in Git against the mock backend (C1.4, ADR-0029): the
// repository as last read, the files that need someone, a read now, and a
// conflict with both sides, settled either way.

test("the catalog's repository: what needs someone, and a read now", async ({ page }) => {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/");
  await page.getByRole("link", { name: "Catalog items" }).click();
  const git = page.getByRole("region", { name: "Git repository" });
  await expect(git).toContainText("https://git.corp.example/infra/rvav-catalog.git");
  await expect(git).toContainText("main · catalog/");
  const attention = git.getByRole("list", { name: "Files that need you" });
  await expect(attention.getByRole("listitem").filter({ hasText: "Ubuntu 24.04 server" })).toContainText(
    "Conflict in Git",
  );
  await expect(attention.getByRole("listitem").filter({ hasText: "Windows Server 2025" })).toContainText(
    "the token was refused",
  );
  await expect(attention.getByRole("list", { name: "Problems of catalog/redis-cache.yaml" })).toContainText(
    'There is no category "caching"',
  );
  // The items say so too.
  const items = page.getByRole("region", { name: "Items" });
  await expect(items.getByRole("row", { name: /Ubuntu 24\.04 server/ })).toContainText("Conflict in Git");
  await expect(items.getByRole("row", { name: /RHEL 9 server/ })).not.toContainText("Git");

  // Read now: the failing commit goes through.
  await git.getByRole("button", { name: "Read the repository now" }).click();
  await expect(page.getByText("Repository read")).toBeVisible();
  await expect(attention.getByRole("listitem").filter({ hasText: "Windows Server 2025" })).toHaveCount(0);
  await expect(items.getByRole("row", { name: /Windows Server 2025/ })).not.toContainText("Commit failing");
  expect(errors).toEqual([]);
});

test("a conflict: both sides, settled for the repository's change", async ({ page }) => {
  await page.goto("/admin/catalog-items/ubuntu-server");
  await expect(page.getByRole("heading", { name: "Ubuntu 24.04 server" })).toBeVisible();
  await expect(page.getByText("Conflict in Git").first()).toBeVisible();
  await page.getByRole("tab", { name: "Git" }).click();
  const git = page.getByRole("region", { name: "Git" });
  await expect(git).toContainText("catalog/ubuntu-server.yaml");
  await expect(git).toContainText("Smaller default size — Thomas Nguyen");
  await expect(git).toContainText("Changed in the repository since rvav last read it.");
  const diff = git.getByLabel("Changes from rvav's file to the repository's");
  await expect(diff).toContainText("added:");
  await expect(diff).toContainText("removed:");
  await git.getByRole("button", { name: "Take the repository's" }).click();
  await expect(page.getByText("The repository's change is a draft")).toBeVisible();
  await expect(git).toContainText("Draft from Git");
  await expect(git).toContainText("review it in Versions");
  await expect(git.getByLabel("Changes from rvav's file to the repository's")).toBeVisible();
  await expect(git.getByRole("button", { name: "Keep rvav's" })).toHaveCount(0);
});

test("a conflict settled for rvav, and committed at the next read; a publish waits for its commit", async ({
  page,
}) => {
  await page.goto("/admin/catalog-items/ubuntu-server");
  await page.getByRole("tab", { name: "Git" }).click();
  const git = page.getByRole("region", { name: "Git" });
  await git.getByRole("button", { name: "Keep rvav's" }).click();
  await expect(page.getByText("rvav's file will be committed over the repository's change")).toBeVisible();
  await expect(git).toContainText("Waiting to be committed");
  await expect(git).toContainText("will be committed at the next read");

  // A new version published: it waits for its commit too. (Moving
  // through the app: the mock's state lives in the page.)
  await page.getByRole("link", { name: "Catalog items" }).first().click();
  await page.getByRole("region", { name: "Items" }).getByRole("link", { name: "RHEL 9 server" }).click();
  await page.getByRole("button", { name: "New draft" }).click();
  await expect(page.getByText(/Draft 3\.3 made/)).toBeVisible();
  await page.getByRole("button", { name: "Publish" }).click();
  await expect(page.getByText("3.3 published")).toBeVisible();
  await expect(page.getByText("Waiting to be committed").first()).toBeVisible();

  await page.getByRole("link", { name: "Catalog items" }).first().click();
  const repo = page.getByRole("region", { name: "Git repository" });
  await repo.getByRole("button", { name: "Read the repository now" }).click();
  await expect(page.getByText("Repository read")).toBeVisible();
  const items = page.getByRole("region", { name: "Items" });
  await expect(items.getByRole("row", { name: /RHEL 9 server/ })).not.toContainText("Waiting");
  await expect(items.getByRole("row", { name: /Ubuntu 24\.04 server/ })).not.toContainText("Git");
});
